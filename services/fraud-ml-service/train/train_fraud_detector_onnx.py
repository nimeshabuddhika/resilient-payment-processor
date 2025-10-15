# train_fraud_detector_onnx.py
import json
import numpy as np
import pandas as pd
from sklearn.ensemble import RandomForestClassifier
from sklearn.metrics import f1_score
from sklearn.model_selection import train_test_split
from sklearn.preprocessing import StandardScaler
from sklearn.pipeline import Pipeline

from skl2onnx import convert_sklearn
from skl2onnx.common.data_types import FloatTensorType

DATA_PATH = "ai_dataset.json"
MODEL_PATH = "fraud_model.onnx"
FEATURE_ORDER = ["amount", "transactionVelocity", "amountDeviation"]

def main():
    # Load dataset
    with open(DATA_PATH, "r") as f:
        data = json.load(f)

    print("no_of_records:", data.get("count"))
    print("dataset_created_at:", data.get("createdAt"))

    df = pd.DataFrame(data["orders"])

    # Features & label
    X = df[FEATURE_ORDER].astype(np.float32)
    y = df["isFraud"].astype(int)

    # Train/Val split
    X_train, X_test, y_train, y_test = train_test_split(
        X.values, y.values, test_size=0.2, random_state=42
    )

    # Simple pipeline: scaler + RF
    pipe = Pipeline(steps=[
        ("scaler", StandardScaler()),
        ("rf", RandomForestClassifier(
            n_estimators=100, class_weight="balanced", random_state=42
        ))
    ])

    pipe.fit(X_train, y_train)

    # Eval
    y_pred = pipe.predict(X_test)
    f1 = f1_score(y_test, y_pred)
    print(f"f1_score: {f1:.4f}")

    # Export ONNX (disable ZipMap for straightforward probability array)
    initial_type = [("input", FloatTensorType([None, len(FEATURE_ORDER)]))]
    onnx_model = convert_sklearn(
        pipe,
        initial_types=initial_type,
        target_opset=12,
        options={id(pipe.named_steps["rf"]): {"zipmap": False}},
    )

    with open(MODEL_PATH, "wb") as f:
        f.write(onnx_model.SerializeToString())

    print(f"model_exported_to: {MODEL_PATH}")
    print(f"feature_order: {FEATURE_ORDER}")

if __name__ == "__main__":
    main()
