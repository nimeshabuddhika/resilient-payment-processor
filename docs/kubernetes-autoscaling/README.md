# Kubernetes Deployment & Autoscaling (k3d Local Environment)

This document explains how to run the **Resilient Payment Processor** on a local
[k3d](https://k3d.io/) Kubernetes cluster and how its **autoscaling** behaves using a
combination of **HPA (Horizontal Pod Autoscaler)** and **KEDA**.

Local deployment entrypoint: `./deploy/local/k3d/Makefile`

---

## 1. High-Level Overview

The local environment is designed to look and behave like a small production
cluster:

- **Cluster**: k3d (single control-plane, multiple agent nodes)
- **Namespaces**:
  - `infra` – Kafka, Redis, Prometheus, Grafana, Loki, KEDA, kube-state-metrics
  - `db` – HA Postgres stack (primary + replicas, HAProxy, PgBouncer)
  - `apps` – application workloads:
    - `order-api` (Go HTTP API)
    - `payment-worker` (Go Kafka consumer)
    - `fraud-ml-service` (Python + ONNX fraud detection API)
- **Autoscaling strategy**:
  - **HPA (CPU-based)** for HTTP services:
    - `order-api`
    - `fraud-ml-service`
  - **KEDA (Kafka lag-based)** for the worker:
    - `payment-worker`

The goal is to demonstrate **realistic, production-style scaling behaviour**
rather than just “everything runs as a single replica”.

---

## 2. Prerequisites

You need the following installed locally:

- Docker (with Buildx enabled)
- k3d
- kubectl
- Helm

The Makefile assumes you are running commands from:

```text
./deploy/local/k3d
```
--- 

## 3. Cluster Lifecycle via Makefile

The Makefile at `./deploy/local/k3d/Makefile` orchestrates the entire local setup.

### 3.1 Bring everything up

This is the one-shot command that:

- Creates the k3d cluster
- Labels nodes
- Installs infra (Kafka, Redis, KEDA, observability stack)
- Installs Postgres
- Builds & pushes images
- Deploys all apps

```bash
cd deploy/local/k3d

# Full cluster + infra + db + apps
make up-all
```

When it completes successfully, you have a fully working local environment.

### 3.2 Tear everything down

```bash
# Delete the entire k3d cluster
make down
```
---
## 4. Node Layout & Namespaces

The cluster is split by responsibility using both namespaces and node labels.

### 4.1 Namespaces

The `init-nodes` target creates namespaces:

- `infra` – platform & observability
- `db` – Postgres
- `apps` – application services

```bash
make init-nodes
```

### 4.2 Node labels

The same target also labels nodes to keep workloads separated:

- `k3d-rpp-agent-0` → `rpp=infra`
<br> _Kafka, Kafka UI, Redis, Prometheus, Grafana, Loki, Promtail, KEDA, kube-state-metrics_
- `k3d-rpp-agent-1` → `rpp=db`
<br> _Postgres primary + replicas, HAProxy, PgBouncer_
- `k3d-rpp-agent-2`, `k3d-rpp-agent-3` → `rpp=apps`
<br> _order-api, payment-worker, fraud-ml-service_

This mirrors a typical production cluster with:

- infra nodes
- database nodes
- application nodes

and keeps noisy workloads away from the database and control-plane.

---
## 5. Local Endpoints

Once `make up-all` finishes, you can access:

- Grafana
<br> http://grafana.localtest.me/
- Kafka UI (Kafkaesque)
<br> http://kafka-ui.localtest.me/
- order-api health
<br> http://order-api.localtest.me/health
- fraud-ml-service health
<br> http://fraud-ml-service.localtest.me/healthz

The ingress/controller setup is configured so that `*.localtest.me` resolves to your local cluster (no extra /etc/hosts changes required on most setups).

--- 
## 6. Autoscaling Strategy

### 6.1 Why HPA for APIs and KEDA for workers?

Different workloads scale for different reasons:

- HTTP APIs (`order-api`, `fraud-ml-service`) are mostly CPU bound per request.
    - HPA (Horizontal Pod Autoscaler) using CPU utilization.
- Kafka consumers (`payment-worker`) care about backlog / lag.
    - Scaling strictly on CPU doesn’t tell you whether you are “keeping up” with
messages.
    - KEDA is used to scale based on Kafka lag, which is a much better
      signal for “are we falling behind?”.

This combination demonstrates a realistic, production-quality autoscaling approach for mixed workloads.

---
## 7. HPA: order-api & fraud-ml-service

### 7.1 Resource requests & limits
For HPA based on CPU utilization to work correctly, each service defines CPU requests (and optionally limits). HPA 
compares current usage to `resources.requests.cpu`.


Example starting points: 

`order-api`(Go HTTP API)

```yaml
orderApi:
  resources:
    requests:
      cpu: "100m" # 100 millicores = 0.1 vCPU.
      memory: "128Mi" # 128 MiB of memory capacity on the node for this pod
    limits:
      cpu: "500m" # Max CPU the container is allowed to use = 0.5 full vCPU.
      memory: "512Mi" #Max memory = 500 MiB of memory capacity on the node for this pod.
```
`fraud-ml-service` (Python + ONNX)

```yaml
fraudMlService:
  resources:
    requests:
      cpu: "200m"
      memory: "256Mi"
    limits:
      cpu: "1000m"
      memory: "1Gi"
```

### 7.2 HPA configuration (CPU-based)

Each service has a `HorizontalPodAutoscaler` defined (API version `autoscaling/v2`)
that targets CPU utilization.

Example for `fraud-ml-service`:

```yaml
kind: HorizontalPodAutoscaler
metadata:
  name: fraud-ml-service-hpa
  namespace: apps
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: fraud-ml-service
  minReplicas: 1
  maxReplicas: 4
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 60
```

A similar HPA object exists for order-api with its own scaling bounds and resource settings.

### 7.3 Inspecting HPA status

```bash
# All HPAs in the apps namespace
kubectl get hpa -n apps

# Detailed information
kubectl describe hpa fraud-ml-service-hpa -n apps
kubectl describe hpa order-api-hpa -n apps
```
And see current pod metrics (if metrics-server is installed):

```bash
kubectl top pods -n apps
```

---
## 8. KEDA: Kafka Lag Based Scaling for payment-worker

### 8.1 Why KEDA for payment-worker?

`payment-worker` is a **Kafka consumer** that processes orders in the background.

The key question for scaling is:

`“Are we keeping up with the Kafka topics, or is lag growing?”`

CPU usage alone does not reliably answer this. KEDA solves this by:

- Watching Kafka lag for a given consumer group and topic(s).
- Automatically creating/updating an HPA under the hood.
- Scaling the deployment up/down based on backlog, not just CPU.

### 8.2 Installing KEDA (infra namespace)

KEDA is installed as part of the infra setup:

```bash
# Installs KEDA and its Custom Resource Definitions (CRDs) into the 'infra' namespace
make install-keda
```

The `make init-infra` target also calls `install-keda` as part of provisioning Kafka, Redis, and observability:

```bash
make init-infra
```

You can verify KEDA with:

```bash
kubectl get pods -n infra | grep keda
kubectl get crd | grep keda
```

### 8.3 ScaledObject for payment-worker

A `ScaledObject` associates `payment-worker` with a Kafka-based trigger,
for example:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: payment-worker-kafka-scaler
  namespace: apps
spec:
  scaleTargetRef:
    kind: Deployment
    name: payment-worker

  pollingInterval: 30        # seconds between checks
  cooldownPeriod: 300        # seconds to wait before scaling down
  minReplicaCount: 1
  maxReplicaCount: 4        # tune relative to Kafka partition count

  triggers:
    - type: kafka
      metadata:
        bootstrapServers: kafka.infra.svc.cluster.local:29092 # Kafka bootstrap address
        consumerGroup: payment-order-workers
        topic: orders-placed
        lagThreshold: "1000"             # messages per replica
        activationLagThreshold: "10"     # don't wake from 0 until >= 10 messages are available
```

You can inspect the ScaledObject with:

```bash
kubectl get scaledobject -n apps
kubectl describe scaledobject payment-worker-kafka-scaler -n apps
```