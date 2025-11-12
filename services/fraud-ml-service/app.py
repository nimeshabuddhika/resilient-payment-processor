"""
fraud-ml-service (single-process)
- Flask + ONNX Runtime
- Prometheus metrics (default single-process registry)
- Ready/health endpoints
"""

import os
import time
import logging
from typing import Dict, Any, List

import numpy as np
from flask import Flask, request, jsonify, g
import onnxruntime as ort

# ---------- Prometheus ----------
from prometheus_client import (
    Counter,
    Histogram,
    Gauge,
    CONTENT_TYPE_LATEST,
    generate_latest,
    REGISTRY,  # default registry (single-process)
)

# -------- Config --------
MODEL_PATH = os.getenv("MODEL_PATH", "./train/fraud_model.onnx")
FRAUD_THRESHOLD = float(os.getenv("FRAUD_THRESHOLD", "0.5"))
FEATURE_ORDER: List[str] = ["amount", "transactionVelocity", "amountDeviation"]
PORT = int(os.getenv("PORT", "8082"))

# ONNX threading (optional; 0 = ORT chooses)
ORT_INTRA_OP = int(os.getenv("ORT_INTRA_OP", "0"))  # threads for ops
ORT_INTER_OP = int(os.getenv("ORT_INTER_OP", "0"))  # parallel ops

# -------- Logging --------
logger = logging.getLogger("fraud-ml-service")
handler = logging.StreamHandler()
# key=value for easy log scraping
handler.setFormatter(logging.Formatter("%(asctime)s level=%(levelname)s msg=%(message)s"))
logger.setLevel(logging.INFO)
logger.addHandler(handler)

app = Flask(__name__)

# -------- Prometheus metrics (single-process) --------
REQUESTS = Counter(
    "rpp_fraud_ml_http_requests_total",
    "HTTP requests to fraud-ml-service",
    ["method", "endpoint", "status_code"],
)

REQUEST_LATENCY = Histogram(
    "rpp_fraud_ml_http_request_duration_seconds",
    "HTTP request latency (seconds) for fraud-ml-service",
    ["method", "endpoint", "status_code"],
    buckets=(0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1.0, 1.5, 2.0, 3, 5, 10),
)

INFERENCE_LATENCY = Histogram(
    "rpp_fraud_ml_inference_duration_seconds",
    "ONNX inference latency (seconds)",
    ["model_version"],
    buckets=(0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1, 2),
)

MODEL_INFO = Gauge(
    "rpp_fraud_ml_model_info",
    "Model info (labels only, value is 1)",
    ["model_path", "providers", "input_name", "output_names", "onnx_version"],
)

PREDICTIONS = Counter(
    "rpp_fraud_ml_predictions_total",
    "Total predictions processed",
    ["result"],  # "fraud" | "not_fraud"
)

FAILURES = Counter(
    "rpp_fraud_ml_failed_total",
    "Prediction failures (by reason)",
    ["reason"],  # "bad_request" | "internal_error"
)

MODEL_LOADED = False

# -------- ONNX Session --------
try:
    sess_options = ort.SessionOptions()
    # Aggressive graph optimizations; safe for CPU
    sess_options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL

    if ORT_INTRA_OP > 0:
        sess_options.intra_op_num_threads = ORT_INTRA_OP
    if ORT_INTER_OP > 0:
        sess_options.inter_op_num_threads = ORT_INTER_OP

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

    MODEL_LOADED = True
    logger.info(
        "onnx_model_loaded model_path=%s input=%s outputs=%s onnx_version=%s",
        MODEL_PATH,
        input_name,
        output_names,
        onnx_version,
    )
except Exception:
    logger.exception("onnx_model_load_failed")
    raise

# -------- Helpers --------
def _extract_features(payload: Dict[str, Any]) -> np.ndarray:
    """
    Extract features in a stable order and validate types.
    Raises ValueError on invalid payload.
    """
    try:
        features = [float(payload[k]) for k in FEATURE_ORDER]
        return np.array([features], dtype=np.float32)
    except Exception:
        raise ValueError(f"invalid_payload: required keys {FEATURE_ORDER} as numbers")

# -------- Flask hooks for HTTP metrics --------
@app.before_request
def _before_request():
    g._start_time = time.perf_counter()

@app.after_request
def _after_request(resp):
    try:
        endpoint = request.path
        # Ignore internal endpoints to keep metrics clean
        if endpoint in ("/metrics", "/healthz", "/ready"):
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
    # Liveness: process is up
    return jsonify({"ok": True}), 200

@app.get("/ready")
def ready():
    # Readiness: model is loaded and session ready
    if MODEL_LOADED:
        return jsonify({"ready": True}), 200
    return jsonify({"ready": False}), 503

@app.post("/api/v1/fraud-ml/predict")
def predict():
    try:
        payload = request.get_json(force=True, silent=False) or {}
        x = _extract_features(payload)

        t0 = time.perf_counter()
        pred = session.run(output_names=None, input_feed={input_name: x})
        infer_dur = time.perf_counter() - t0

        # Inference latency by model version
        INFERENCE_LATENCY.labels(model_version=ort.__version__).observe(infer_dur)

        score = float(pred[0].item()) if hasattr(pred[0], "item") else float(pred[0][0][0])
        is_fraud = score >= FRAUD_THRESHOLD
        PREDICTIONS.labels(result="fraud" if is_fraud else "not_fraud").inc()

        logger.info(
            "prediction score=%.6f threshold=%.6f is_fraud=%s infer_ms=%.3f",
            score,
            FRAUD_THRESHOLD,
            is_fraud,
            infer_dur * 1000.0,
        )

        return jsonify({"score": score, "threshold": FRAUD_THRESHOLD, "isFraud": is_fraud}), 200

    except ValueError as ve:
        FAILURES.labels(reason="bad_request").inc()
        logger.warning("bad_request error=%s", str(ve))
        return jsonify({"error": str(ve)}), 400
    except Exception:
        FAILURES.labels(reason="internal_error").inc()
        logger.exception("prediction_failed")
        return jsonify({"error": "internal_server_error"}), 500

@app.get("/metrics")
def metrics():
    # Single-process metrics only
    data = generate_latest(REGISTRY)
    return (data, 200, {"Content-Type": CONTENT_TYPE_LATEST})

if __name__ == "__main__":
    # Dev server (for local). In container, use Gunicorn (see Dockerfile).
    app.run(host="0.0.0.0", port=PORT)
