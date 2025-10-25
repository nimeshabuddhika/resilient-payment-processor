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
	$(MAKE) up # Spinup dependencies
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

##@ Cleanup
.PHONY: clean-order-api
clean-order-api: ## Stop and remove order-api container and image
	@echo "Stopping and removing order-api"
	docker stop order-api || true
	docker rm order-api || true
	docker rmi order-api:latest || true

.PHONY: clean-payment-worker
clean-payment-worker: ## Stop and remove payment-worker containers and image
	@echo "Stopping and removing payment-worker"
	@ids=$$(docker ps -aq --filter name=payment-worker); \
	if [ -n "$$ids" ]; then \
		docker stop $$ids; \
		docker rm -f $$ids; \
	else \
		echo "No payment-worker containers to remove"; \
	fi
	docker rmi payment-worker:latest || true

.PHONY: clean-fraud-ml-service
clean-fraud-ml-service: ## Stop and remove fraud-ml-service container and image
	@echo "Stopping and deleting fraud-ml-service"
	docker stop fraud-ml-service || true
	docker rm -f fraud-ml-service || true
	docker rmi fraud-ml-service:latest || true

.PHONY: clean-services
clean-services: clean-order-api clean-payment-worker clean-fraud-ml-service## Clean all services (containers, images)

.PHONY: clean
clean: ## Cleaning project data
	@echo "Cleaning $(PROJECT_NAME) project"
	docker compose -f $(COMPOSE_FILE) --profile services -p $(PROJECT_NAME) down --remove-orphans -v
	$(MAKE) clean-services

##@ Seed
.PHONY: seed-usage
seed-help: ## Seed usage seeders
	@echo "Usage of user-and-accounts seeder"
	go run $(USER_SEED_FILE) -h
	@echo "Usage of order seeder"
	go run $(ORDER_SEED_FILE) -h

.PHONY: seed-users-and-accounts
seed-users-and-accounts: ## Seed Users and User accounts directly to the database
	go run $(USER_SEED_FILE) -noOfUsers 50000

.PHONY: seed-orders
seed-orders: ## Seed orders via order-api
	go run $(ORDER_SEED_FILE) -orderApiUrl http://localhost:8081 -noOfOrders 50000 -noOfOrdersPerAccount 1 -rps=500

.PHONY: seed
seed: seed-users-and-accounts seed-orders ## Seed users accounts and orders