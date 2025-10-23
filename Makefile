# Makefile for resilient-payment-processor
# This Makefile provides targets for documentation generation, Docker management, and cleanup.
# Run 'make help' for usage.

# Variables
PROJECT_NAME := resilient-payment-processor
COMPOSE_FILE := docker-compose.yml
RESTART_SERVICES := postgres order-api
USER_SEED_FILE := ./services/user-api/cmd/seed/user_account_seeder.go
ORDER_SEED_FILE := ./services/order-api/cmd/seed/order_seeder.go

# Help target to display available commands
.PHONY: help
help:
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Documentation
.PHONY: docs-order-api
docs-order-api: ## Generate Swagger docs for order-api
	swag init -g services/order-api/cmd/main.go --output ./docs/open-api/order-api

.PHONY: docs
docs: docs-order-api ## Generate all documentation

##@ Docker Management
.PHONY: up
up: ## Bring up all infrastructures services with Docker Compose
	docker compose -f $(COMPOSE_FILE) -p $(PROJECT_NAME) up -d

.PHONY: up-services
up-services: ## Bring up all infrastructures services and micro-services with Docker Compose
	docker compose -f $(COMPOSE_FILE) -p $(PROJECT_NAME) --profile services up -d

.PHONY: up-order-api
up-order-api: ## Bring up order-api service with Docker Compose
	docker compose -f $(COMPOSE_FILE) -p $(PROJECT_NAME) up -d order-api

.PHONY: up-payment-worker
up-payment-worker: ## Bring up payment-worker service with Docker Compose
	docker compose -f $(COMPOSE_FILE) -p $(PROJECT_NAME) up -d payment-worker

.PHONY: down
down: ## Bring down all services with Docker Compose
	docker compose -f $(COMPOSE_FILE) -p $(PROJECT_NAME) down

.PHONY: docker-restart
docker-restart: ## Restart services
	@echo "Restarting $(RESTART_SERVICES) payment-worker"
	docker restart $(RESTART_SERVICES)
	@ids=$$(docker ps -aq --filter name=payment-worker); \
		if [ -n "$$ids" ]; then \
			docker restart $$ids; \
		else \
			echo "No payment-worker containers to restart"; \
		fi

##@ Remove images
.PHONY: remove-order-api
remove-order-api: ## Remove order-api image
	@echo "removing order-api image"
	docker rmi order-api:latest || true

.PHONY: remove-payment-worker
remove-payment-worker: ## Remove payment-worker image
	@echo "removing payment-worker image"
	docker rmi payment-worker:latest || true

.PHONY: remove-fraud-ml-service
remove-fraud-ml-service: ## Remove fraud-ml-service image
	@echo "removing fraud-ml-service image"
	docker rmi fraud-ml-service:latest || true

.PHONY: remove-images
remove-images: remove-order-api remove-payment-worker remove-fraud-ml-service## Clean all images

##@ Cleanup
.PHONY: clean
clean: ## Cleaning project data
	@echo "Cleaning $(PROJECT_NAME) project"
	docker compose -f $(COMPOSE_FILE) --profile services -p $(PROJECT_NAME) down --remove-orphans -v
	$(MAKE) remove-images

##@ Seed
.PHONY: seed-usage
seed-help: ## Seed usage seeders
	@echo "Usage of user-and-accounts seeder"
	go run $(USER_SEED_FILE) -h
	@echo "Usage of order seeder"
	go run $(ORDER_SEED_FILE) -h

.PHONY: seed-users-and-accounts
seed-users-and-accounts: ## Seed Users and User accounts directly to the database
	go run $(USER_SEED_FILE) -noOfUsers 5000

.PHONY: seed-orders
seed-orders: ## Seed orders via order-api
	go run $(ORDER_SEED_FILE) -orderApiUrl http://localhost:8081 -noOfOrders 5000 -noOfOrdersPerAccount 2

.PHONY: seed
seed: seed-users-and-accounts seed-orders ## Seed users accounts and orders