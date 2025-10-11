.PHONY: docs-order-api docs

docs-order-api:
	swag init -g services/order-api/cmd/main.go --output ./docs/open-api/order-api # Generate order-api swagger


.PHONY: docs run

docs: docs-order-api
	@echo "All docs generated"
