# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]
### Added
- End-to-end payment flow: order-api accepts orders, publishes to Kafka, payment-worker processes with retries/rate limiting.
- Resilience: Exponential backoff, idempotency, Redis locks for accounts.
- Observability: Prometheus metrics, Grafana dashboards, Loki/Promtail logging.
- Data stores: Postgres (with encryption), Redis locking.
- Docker Compose for local setup (Kafka, Postgres, Redis, services).
- Swagger docs for order-api.
- Seeders for users/accounts/orders.
- Makefile for docs, Docker management, cleanup.

### Changed
- Updated architecture to use Kafka topics (orders-placed, retry, DLQ).
- Configured concurrent job limits via semaphores.

### Fixed
- Handled decryption errors and insufficient balance checks.