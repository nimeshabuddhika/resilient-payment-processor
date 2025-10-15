# server.py
import os
import logging
from typing import Dict, Any

import numpy as np
from flask import Flask, request, jsonify
import onnxruntime as ort

# -------- Config --------
MODEL_PATH = os.getenv("MODEL_PATH", "./train/fraud_model.onnx")
FRAUD_THRESHOLD = float(os.getenv("FRAUD_THRESHOLD", "0.5"))
FEATURE_ORDER = ["amount", "transactionVelocity", "amountDeviation"]
PORT = int(os.getenv("PORT", "8082"))

# -------- Logging --------
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("fraud_ml_server")

# -------- ONNX Session --------
try:
    sess_options = ort.SessionOptions()
    # Keep defaults light; tune if needed
    session = ort.InferenceSession(
        MODEL_PATH,
        sess_options=sess_options,
        providers=["CPUExecutionProvider"],
    )
    input_name = session.get_inputs()[0].name
    output_names = [o.name for o in session.get_outputs()]
    logger.info("onnx_model_loaded", extra={
        "model_path": MODEL_PATH,
        "input_name": input_name,
        "output_names": output_names
    })
except Exception as e:
    logger.exception("onnx_model_load_failed")
    raise

# -------- Helpers --------
def _validate_and_vectorize(payload: Dict[str, Any]) -> np.ndarray:
    missing = [k for k in FEATURE_ORDER if k not in payload]
    if missing:
        raise ValueError(f"missing_fields: {missing}")

    try:
        vals = [
            float(payload["amount"]),
            float(payload["transactionVelocity"]),
            float(payload["amountDeviation"]),
        ]
    except Exception:
        raise ValueError("invalid_field_types: expected numeric values")

    # shape (1, 3), float32
    return np.array([vals], dtype=np.float32)

def _extract_probability(outputs) -> float:
    """
    We disabled ZipMap in export, so probabilities should be an array shape (N, C).
    For binary classification, we take column index 1 as P(class=1).
    Fallbacks included for robustness.
    """
    # Typical skl2onnx RF classifier order: [labels, probabilities]
    labels = None
    probas = None

    if isinstance(outputs, list):
        if len(outputs) == 2:
            labels, probas = outputs[0], outputs[1]
        elif len(outputs) == 1:
            probas = outputs[0]
        else:
            probas = outputs[-1]
    else:
        probas = outputs

    # If ZipMap slipped through (dict), handle it
    if isinstance(probas, list) and len(probas) > 0 and isinstance(probas[0], dict):
        # Keys may be 0/1 or '0'/'1'
        return float(probas[0].get(1, probas[0].get("1", 0.0)))

    # If ndarray with shape (1,2)
    if hasattr(probas, "shape") and len(probas.shape) == 2 and probas.shape[1] >= 2:
        return float(probas[0, 1])

    # Last resort: if only label available, map label->prob
    if labels is not None and len(labels) > 0:
        return 1.0 if int(labels[0]) == 1 else 0.0

    return 0.0

# -------- Flask App --------
app = Flask(__name__)

@app.route("/api/v1/fraud-ml/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "model_path": MODEL_PATH}), 200

@app.route("/api/v1/fraud-ml/predict", methods=["POST"])
def predict():
    try:
        payload = request.get_json(force=True, silent=False)
        if not isinstance(payload, dict):
            return jsonify({"error": "invalid_json_body"}), 400

        x = _validate_and_vectorize(payload)

        outputs = session.run(None, {input_name: x})
        p_fraud = _extract_probability(outputs)
        is_fraud = p_fraud >= FRAUD_THRESHOLD

        resp = {
            "fraudProbability": round(p_fraud, 6),
            "isFraud": bool(is_fraud),
            "threshold": FRAUD_THRESHOLD,
        }

        logger.info("prediction_completed", extra={
            "transaction_velocity": payload.get("transactionVelocity"),
            "amount_deviation": payload.get("amountDeviation"),
            "fraud_probability": resp["fraudProbability"],
            "is_fraud": resp["isFraud"],
        })
        return jsonify(resp), 200

    except ValueError as ve:
        logger.warning("bad_request", extra={"error": str(ve)})
        return jsonify({"error": str(ve)}), 400
    except Exception as e:
        logger.exception("prediction_failed")
        return jsonify({"error": "internal_server_error"}), 500

if __name__ == "__main__":
    # Dev server (for local). For prod use gunicorn or uvicorn workers.
    app.run(host="0.0.0.0", port=PORT)
