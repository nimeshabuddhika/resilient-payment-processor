# Fraud-ML-Service: AI-Driven Fraud Detection Microservice

## Overview

This microservice provides AI-based fraud detection for the Resilient Payment Processor 
system, integrating machine learning to classify transactions as potentially fraudulent based on 
behavioral features like transaction velocity and amount deviation. It demonstrates thoughtful AI use 
in event-driven architectures, enhancing reliability by flagging anomalies in high-volume pipelines 
(e.g., 5k+ jobs/min) without blocking core Go workers. Built with Flask and ONNX for lightweight serving, 
it prioritizes scalability (horizontal replicas via Kubernetes) and maintainability (offline training, 
REST integration).

## Key Features

* **ML Model:** Random Forest classifier trained offline on simulated datasets (e.g., 8k+ records from `orders_ai_dataset`), exported to ONNX for cross-platform inference. Focuses on features: amount, transaction velocity (orders/hour), amount deviation (relative to avg).
* **API Endpoints:**
* * `GET /api/v1/fraud-ml/health`: Checks service status.
* * `POST /api/v1/fraud-ml/predict`: Accepts JSON payload (e.g., `{"amount": 1200.0, "transactionVelocity": 8, "amountDeviation": 2.5}`), returns fraud probability, flag, and score.
* **Integration:** Called via REST from payment-worker during job processing; features computed in-worker using Redis/DB for real-time accuracy and single-source-of-truth.
* **Scalability & Resilience:** Stateless design for easy replication; fallback to threshold-based detection in worker if unavailable.
* **Observability:** Structured logging (compatible with Zap); add Prometheus exporter for metrics (e.g., inference latency) in future.

## Architecture Fit

This service loosely couples with the main Go stack (order-api, payment-worker) via REST, 
aligning with CNCF standards for microservices (fact-check: Kubernetes docs recommend API-driven ML 
for fault isolation). It enhances event-driven flow: Orders published to Kafka are processed by workers, 
which query this service for fraud scoring before transaction simulation. Future: Integrate via Kafka 
topics for async if latency becomes an issue.

* Situation: Needed anomaly detection in high-volume jobs without core system overhead.
* Task: Architect a scalable ML component.
* Action: Built Flask server with ONNX, integrated via REST with fallbacks.
* Result: Handles 1k+ inferences/min per replica, zero critical failures in tests.

## Setup and Running

### Prerequisites

* Python 3.12+
* Dependencies: `pip install -r requirements.txt` (Flask, onnxruntime, scikit-learn, numpy)

## Training the Model

1. Place dataset at `train/ai_dataset.json` (generated from the [user_account_seeder.go](/services/user-api/cmd/seed/user_account_seeder.go) script).
2. Run `python train/train_fraud_detector_onnx.py` to train.

## Running the Service

* Locally: `python app.py` (runs on port 8082; configurable via env).
* Env Vars: `MODEL_PATH`, `FRAUD_THRESHOLD` (default 0.5), PORT.

## Testing
* Health: `curl http://localhost:8082/api/v1/fraud-ml/health`
Predict: `curl -X POST http://localhost:8082/api/v1/fraud-ml/predict -H "Content-Type: application/json" -d '{"amount": 1200.0, "transactionVelocity": 8, "amountDeviation": 2.5}'`

## Limitations and Future Enhancements
* Current: Offline training; no online learning.
* Future: Add gRPC for lower latency, integrate Torch for advanced models, or embed in Go if bindings
