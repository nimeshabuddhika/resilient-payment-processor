# server.py
import os
import time
import logging
from typing import Dict, Any

import numpy as np
from flask import Flask, request, jsonify, g
import onnxruntime as ort

# ---------- Prometheus ----------
from prometheus_client import (
    Counter, Histogram, Gauge, CollectorRegistry, CONTENT_TYPE_LATEST,
    generate_latest, multiprocess, REGISTRY
)

# -------- Config --------
MODEL_PATH = os.getenv("MODEL_PATH", "./train/fraud_model.onnx")
FRAUD_THRESHOLD = float(os.getenv("FRAUD_THRESHOLD", "0.5"))
FEATURE_ORDER = ["amount", "transactionVelocity", "amountDeviation"]
PORT = int(os.getenv("PORT", "8082"))

# -------- Logging --------
logger = logging.getLogger("fraud-ml-service")
handler = logging.StreamHandler()
handler.setFormatter(logging.Formatter('%(asctime)s %(levelname)s %(message)s'))
logger.setLevel(logging.INFO)
logger.addHandler(handler)

app = Flask(__name__)

# -------- Prometheus registry (supports Gunicorn multiprocess) --------
# If PROMETHEUS_MULTIPROC_DIR is set, use a custom registry.
if "PROMETHEUS_MULTIPROC_DIR" in os.environ:
    registry = CollectorRegistry()
    multiprocess.MultiProcessCollector(registry)
else:
    registry = REGISTRY  # default global registry

REQUESTS = Counter(
    "rpp_fraud_ml_http_requests_total",
    "HTTP requests to fraud-ml-service",
    ["method", "endpoint", "status_code"],
    registry=registry,
)

REQUEST_LATENCY = Histogram(
    "rpp_fraud_ml_http_request_duration_seconds",
    "HTTP request latency (seconds) for fraud-ml-service",
    ["method", "endpoint", "status_code"],
    # millisecond to multi-second buckets
    buckets=(0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1.0, 1.5, 2.0, 3, 5, 10),
    registry=registry,
)

INFERENCE_LATENCY = Histogram(
    "rpp_fraud_ml_inference_duration_seconds",
    "ONNX inference latency (seconds)",
    ["model_version"],
    buckets=(0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1, 2),
    registry=registry,
)

MODEL_INFO = Gauge(
    "rpp_fraud_ml_model_info",
    "Model info (labels only, value is 1)",
    ["model_path", "providers", "input_name", "output_names", "onnx_version"],
    registry=registry,
)

PREDICTIONS = Counter(
    "rpp_fraud_ml_predictions_total",
    "Total predictions processed",
    ["result"],  # result = "fraud" | "not_fraud"
    registry=registry,
)

FAILURES = Counter(
    "rpp_fraud_ml_failed_total",
    "Prediction failures (by reason)",
    ["reason"],  # reason = "bad_request" | "internal_error"
    registry=registry,
)

# -------- ONNX Session --------
try:
    sess_options = ort.SessionOptions()
    session = ort.InferenceSession(
        MODEL_PATH,
        sess_options=sess_options,
        providers=["CPUExecutionProvider"],
    )
    input_name = session.get_inputs()[0].name
    output_names = [o.name for o in session.get_outputs()]
    onnx_version = ort.__version__

    MODEL_INFO.labels(
        model_path=MODEL_PATH,
        providers="CPUExecutionProvider",
        input_name=input_name,
        output_names=",".join(output_names),
        onnx_version=onnx_version,
    ).set(1)

    logger.info("onnx_model_loaded: path=%s input=%s outputs=%s", MODEL_PATH, input_name, output_names)
except Exception as e:
    logger.exception("onnx_model_load_failed")
    raise

# -------- Helpers --------
def _extract_features(payload: Dict[str, Any]) -> np.ndarray:
    try:
        features = [float(payload[k]) for k in FEATURE_ORDER]
        return np.array([features], dtype=np.float32)
    except Exception as e:
        raise ValueError(f"invalid_payload: required keys {FEATURE_ORDER} as numbers")

# -------- Flask hooks for HTTP metrics --------
@app.before_request
def _before_request():
    g._start_time = time.perf_counter()

@app.after_request
def _after_request(resp):
    try:
        endpoint = request.path
        # --- Ignore internal endpoints ---
        if endpoint in ("/metrics", "/healthz"):
            return resp

        dur = time.perf_counter() - getattr(g, "_start_time", time.perf_counter())
        method = request.method
        status = str(resp.status_code)

        REQUESTS.labels(method=method, endpoint=endpoint, status_code=status).inc()
        REQUEST_LATENCY.labels(method=method, endpoint=endpoint, status_code=status).observe(dur)
    except Exception:
        logger.exception("metrics_after_request_error")
    return resp

# -------- Routes --------
@app.get("/healthz")
def healthz():
    return jsonify({"ok": True}), 200

@app.post("/api/v1/fraud-ml/predict")
def predict():
    try:
        payload = request.get_json(force=True, silent=False) or {}
        x = _extract_features(payload)

        t0 = time.perf_counter()
        pred = session.run(output_names=None, input_feed={input_name: x})
        infer_dur = time.perf_counter() - t0

        # Inference latency by model version (use onnxruntime version as proxy)
        INFERENCE_LATENCY.labels(model_version=ort.__version__).observe(infer_dur)

        score = float(pred[0].item()) if hasattr(pred[0], "item") else float(pred[0][0][0])
        is_fraud = score >= FRAUD_THRESHOLD
        PREDICTIONS.labels(result="fraud" if is_fraud else "not_fraud").inc()

        resp = {
            "score": score,
            "threshold": FRAUD_THRESHOLD,
            "isFraud": is_fraud,
        }
        logger.info("prediction", extra={"score": score, "threshold": FRAUD_THRESHOLD, "is_fraud": is_fraud})
        return jsonify(resp), 200

    except ValueError as ve:
        FAILURES.labels(reason="bad_request").inc()
        logger.warning("bad_request %s", str(ve))
        return jsonify({"error": str(ve)}), 400
    except Exception:
        FAILURES.labels(reason="internal_error").inc()
        logger.exception("prediction_failed")
        return jsonify({"error": "internal_server_error"}), 500

@app.get("/metrics")
def metrics():
    # Expose metrics compatible with single or multiprocess mode
    if "PROMETHEUS_MULTIPROC_DIR" in os.environ:
        # multiprocess registry already wired
        data = generate_latest(registry)
    else:
        data = generate_latest(registry)
    return (data, 200, {"Content-Type": CONTENT_TYPE_LATEST})

if __name__ == "__main__":
    # Dev server (for local). For prod prefer Gunicorn with multiple workers
    app.run(host="0.0.0.0", port=PORT)
