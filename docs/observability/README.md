# Observability Overview

This document provides a high-level overview of the **Observability Stack** implemented in the *Resilient Payment Processor* project.  
It explains the purpose, architecture, and metrics integration strategy used to monitor and visualize the health and performance of each microservice.

## 1. Introduction

Observability is a core pillar of this system’s architecture, alongside **resilience** and **scalability**.  
It enables real-time visibility into system behavior, job throughput, and failure patterns across the distributed pipeline.

### Key Objectives
- Detect anomalies early (e.g., message lag, API latency spikes).
- Quantify throughput, success, and failure rates per service.
- Correlate metrics across the data pipeline: `order-api → Kafka → payment-worker → fraud-ml-service`.
- Facilitate root cause analysis through structured logs and dashboards.
---

## 2. Observability Stack

| Component                    | Purpose                                            | Location/Port           |
|------------------------------|----------------------------------------------------|-------------------------|
| **Prometheus**               | Time-series metrics collection from all services   | `http://localhost:9090` |
| **Grafana**                  | Dashboards and alert visualization                 | `http://localhost:3000` |
| **Loki / Promtail            | Centralized structured log aggregation             | `http://localhost:3100` |
| **Alert manager** *(future)* | Prometheus alert routing (Slack/email integration) | TBD                     |

Each microservice exposes Prometheus-compatible `/metrics` endpoints instrumented via:
- `prometheus/client_golang` (Go services)
- `prometheus_client` (Python fraud-ml-service)

---

## 3. Services with Observability

| Service            | Technology            | Dashboard File                                                         | Description                                                                                              |
|--------------------|-----------------------|------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------|
| `order-api`        | Go (Gin)              | [ORDER_API_OBSERVABILITY.md](ORDER_API_OBSERVABILITY.md)               | Monitors request rate, success/failure counts, and latency histograms for the `/api/v1/orders` endpoint. |
| `payment-worker`   | Go (Kafka Consumer)   | [PAYMENT_WORKER_OBSERVABILITY.md](PAYMENT_WORKER_OBSERVABILITY.md)     | Tracks Kafka message processing throughput, success/failure ratio, retry counts, and lag metrics.        |
| `fraud-ml-service` | Python (Flask + ONNX) | [FRAUD_ML_SERVICE_OBSERVABILITY.md](FRAUD_ML_SERVICE_OBSERVABILITY.md) | Exposes model inference latency, fraud prediction frequency, and system resource usage.                  |

---

## 4. Metric Categories

All metrics are grouped under four primary categories:

| Category          | Description                                     | Example Metric                                                         |
|-------------------|-------------------------------------------------|------------------------------------------------------------------------|
| **Throughput**    | How many requests/jobs are processed per second | `rate(order_api_requests_total[1m])`                                   |
| **Error Rates**   | Failures per endpoint/topic                     | `sum(rate(payment_worker_failed_total[5m]))`                           |
| **Latency**       | Request/processing duration histograms          | `histogram_quantile(0.95, rate(order_api_latency_seconds_bucket[5m]))` |
| **System Health** | Resource usage (CPU, memory) and Kafka lag      | `process_resident_memory_bytes`, `kafka_consumergroup_lag`             |

---

## 5. Grafana Dashboards

Each dashboard includes:
- **Panels** for RPS, latency, and error rate.
- **Legends** grouped by service/topic labels.
- **Thresholds** and color zones for alerting visibility.
- **Annotations** for major test runs (e.g., load tests, deployment changes).

You can access dashboards via: `http://localhost:3000`
```
User: admin
Pass: admin
```
---

## 6. Future Enhancements

* Aggregate metrics for `orders-placed` and `orders-retry` in `payment-worker`.
* Support multi-replica observability in `payment-worker` (per-replica and aggregated views).
* Enable HPA scaling in Kubernetes using CPU/memory and custom metrics.
* Add OpenTelemetry traces across order → payment → fraud flow.
* Integrate Alert manager for Slack and email notifications.
