SHELL := /bin/bash

# ---- config ----
CLUSTER        ?= rpp
K3D_CONFIG     ?= k3d-rpp.yaml

NAMESPACE      ?= rpp
RELEASE        ?= rpp
CHART_PATH     ?= ../../../charts/resilient-payment-processor
VALUES_FILE    ?= ../../envs/local-k3d.values.yaml

REGISTRY       ?= localhost:5001
TAG            ?= dev
PLATFORM       ?= linux/amd64,linux/arm64
BUILDER        ?= rpp-builder

# repo root relative to this file (deploy/local/k3d)
ROOT           ?= ../../../

# image coordinates
ORDER_API_IMG      ?= $(REGISTRY)/order-api:$(TAG)
PAYMENT_WORKER_IMG ?= $(REGISTRY)/payment-worker:$(TAG)
FRAUD_ML_SERVICE_IMG ?= $(REGISTRY)/fraud-ml-service:$(TAG)

ORDER_API_DOCKERFILE      ?= $(ROOT)Dockerfile.order-api
PAYMENT_WORKER_DOCKERFILE ?= $(ROOT)Dockerfile.payment-worker
FRAUD_ML_SERVICE_DOCKERFILE ?= $(ROOT)Dockerfile.fraud-ml-service

# Help target to display available commands
.PHONY: help
help:
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
##@ Cluster Lifecycle Management for k3d
.PHONY: up
up: ## Create k3d cluster
	k3d cluster create --config $(K3D_CONFIG)

.PHONY: down
down: ## Bring down k3d cluster
	k3d cluster delete $(CLUSTER) || true

.PHONY: k3d-status
status: ## Status of k3d
	kubectl cluster-info
	kubectl get nodes -o wide --show-labels

##@ Docker Image Management
.PHONY: builder
builder: ## Create builder for multi-arch friendly. Default to arm64 for Apple Silicon
	@if ! docker buildx inspect $(BUILDER) >/dev/null 2>&1; then \
    	  docker buildx create --name $(BUILDER) --use; \
	else \
	  docker buildx use $(BUILDER); \
	fi

.PHONY clean-images:
clean-images: ## Clean all service images on local
	docker image rm $(ORDER_API_IMG) || true
	docker image rm $(PAYMENT_WORKER_IMG) || true
	docker image rm $(FRAUD_ML_SERVICE_IMG) || true

.PHONY : images
images: builder ## Build and push services images to registry
	@echo "Build $(ORDER_API_IMG) and push to registry"
	docker buildx build --platform $(PLATFORM) -t $(ORDER_API_IMG) -f $(ORDER_API_DOCKERFILE) $(ROOT) --load
	docker push $(ORDER_API_IMG)

	@echo "Build $(PAYMENT_WORKER_IMG) and push to registry"
	docker buildx build --platform $(PLATFORM) -t $(PAYMENT_WORKER_IMG) -f $(PAYMENT_WORKER_DOCKERFILE) $(ROOT) --load
	docker push $(PAYMENT_WORKER_IMG)

	@echo "Build $(FRAUD_ML_SERVICE_IMG) and push to registry"
	docker buildx build --platform $(PLATFORM) -t $(FRAUD_ML_SERVICE_IMG) -f $(FRAUD_ML_SERVICE_DOCKERFILE) $(ROOT) --load
	docker push $(FRAUD_ML_SERVICE_IMG)

##@ Infrastructure Management

.PHONY : init-infra
infra-init: ## Create fundamental infrastructures
	kubectl create ns infra

	@echo "Assigning labels"
	kubectl label node k3d-rpp-agent-0 rpp=infra --overwrite #kafka, kafka-ui, prometheus, grafana, loki, promtail
	kubectl label node k3d-rpp-agent-1 rpp=db --overwrite # postgres-primary, postgres-replica-[1..2], haproxy-read, pgbouncer-*
	kubectl label node k3d-rpp-agent-2 rpp=apps --overwrite #order-api, payment-worker, fraud-ml-service, redis

.PHONY : init-kafka
init-kafka: ## Provision kafka
	@echo "Install Kafka"
	helm upgrade --install infra-kafka $(ROOT)/charts/infra-kafka -n infra \
      -f $(ROOT)/deploy/envs/local-k3d.values.yaml

    # Wait and check the external NodePort
	kubectl -n infra get svc

.PHONY : uninstall-kafka
uninstall-kafka: ## Uninstall kafka
	helm list -n infra
	helm uninstall infra-kafka -n infra