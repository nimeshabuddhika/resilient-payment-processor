# AI/ML Fraud Detection

## Overview

This module adds a lightweight, offline-trained → online-served fraud detector to the payment pipeline.
We train a scikit-learn model from a JSON dataset, export the entire preprocessing+model pipeline to ONNX, and serve real-time predictions via a small Flask HTTP service (CPU-only, ONNX Runtime).

## Goals & non-goals

### Goals
- Keep hot-path inference simple and fast (single ONNX session, CPU).
- Bundle preprocessing with the model so the server accepts raw numeric features.
- Provide a stable API and a clear model versioning story.
- 

### Non-goals
- Perfect fraud detection (this is a starter baseline, not a production anti-fraud suite).
- Full feature store, complex identity graphs, or device fingerprinting.
- GPU acceleration (not needed for small tabular models).

## Model choice & preprocessing

- **Algorithm:** `RandomForestClassifier` (100 trees, `class_weight="balanced"`)
  Rationale: strong tabular baseline, robust to non-linearities and small feature sets; handles class imbalance reasonably well.
- **Train/test split:** 80/20 with random_state=42 for reproducibility.
- **Primary metric:** F1 score on the test split (balanced view for imbalanced fraud data).
  For production you’d track PR-AUC and cost-weighted metrics, but F1 is a pragmatic single number for iteration.

## Training pipeline

Script: `train_fraud_detector_onnx.py`

High-level steps:

1. Load ai_dataset.json, build a DataFrame, select `FEATURE_ORDER` and isFraud.
2. Split into train/test.
3. Fit a `Pipeline(StandardScaler → RandomForestClassifier)`.
4. Evaluate with F1 and print result.
5. Export the entire pipeline to ONNX (`fraud_model.onnx`) with ZipMap disabled so probabilities come back as a clean `(N,2)` array.

Artifacts

* `fraud_model.onnx` — the serialized inference graph.

## File layout

```filetree
/ai
├─ train_fraud_detector_onnx.py   # offline training → ONNX export
├─ app.py                         # Flask + ONNX Runtime inference server
├─ requirements.txt               # pinned runtime & training deps
└─ ai_dataset.json                # sample training data (not committed in prod)
```

