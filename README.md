# Resilient Payment Processor

A scalable, event-driven microservices system demonstrating resilient distributed job processing (e.g., high-volume payment tasks). Built with Go, Kafka, Postgres, Redis, and observability tools. Mirrors real-world greenfield projects handling 10k+ concurrent jobs with zero critical issues.

## Architecture
Aligned with event-driven best practices: decoupling via Kafka for scalability, idempotency for reliability, observability for maintainability. Follows Go conventions (lowercase packages, kebab-case folders) and domain-driven design (bounded contexts for users, orders, payments).

### High-Level Components
- **Microservices** (Go + Gin for APIs, confluent-kafka-go):
    - `order-api`: CRUD for orders. Validates balance (sync DB query), publishes to Kafka. REST: POST /orders.
    - `payment-worker`: Stateless Kafka consumer. Processes jobs with resilience. Scales horizontally.
    - `fraud-ml-service`: AI-based fraud detection. Integrates with payment-worker for real-time analysis and smart decisions making.
- **Event Bus**: Kafka (topics: orders-placed partitioned by userID; retry/DLQ for failures).
- **Data Stores**:
    - Postgres (pgx driver): Shared DB with encryption (AES-GCM app-level).
    - Redis: Distributed locks (e.g., for account holds), caching.
- **Resilience** (/pkg): Retries (exponential backoff), rate limiting (golang.org/x/time/rate), circuit breakers, idempotency.
- **Observability**: Prometheus metrics (job throughput/errors), Grafana dashboards, Loki/Promtail logs.
- **Security**: AES encryption at rest. (Future: Keycloak for OAuth2/OIDC/RBAC.)

## 📚 Documentation

Everything you need to understand, run, and extend the system lives in `./docs`. Start here:

### At a glance
| Audience      | Start with                                                                                                | Why                                                                                  |
|---------------|-----------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------|
| New readers   | [/docs/observability/README.md](docs/observability/README.md)                                             | High-level overview of metrics, dashboards, and how to observe the system end-to-end |
| Backend devs  | [/docs/observability/ORDER_API_OBSERVABILITY.md](docs/observability/ORDER_API_OBSERVABILITY.md)           | Order API Prometheus metrics + Grafana queries                                       |
| Worker devs   | [/docs/observability/PAYMENT_WORKER_OBSERVABILITY.md](docs/observability/PAYMENT_WORKER_OBSERVABILITY.md) | Payment Worker Prometheus metrics + Grafana queries                                  |
| ML/Platform   | [/docs/fraud-ml-service/README.md](docs/fraud-ml-service/README.md)                                       | Fraud ML service: model, ONNX, inference, API                                        |
| Infra/DB      | [/docs/postgres/README.md](docs/postgres/README.md)                                                       | HA Postgres (Primary + Read Replicas) with PgBouncer + HAProxy                       |
| API consumers | [/docs/open-api/order-api](docs/open-api/order-api)                                                       | Swagger/OpenAPI for `order-api`                                                      |

```filetree
./docs
├─ fraud-ml-service
│ └─ README.md # Fraud ML service (training → ONNX → inference API)
├─ observability
│ ├─ README.md # Observability overview (Prometheus + Grafana)
│ ├─ ORDER_API_OBSERVABILITY.md # Order API dashboards & queries
│ ├─ PAYMENT_WORKER_OBSERVABILITY.md # Payment Worker dashboards & queries
│ └─ FRAUD_ML_SERVICE_OBSERVABILITY.md # Fraud ML dashboards & queries
├─ open-api
│ └─ order-api # Swagger/OpenAPI for Order API
└─ postgres
    └─ README.md # HA Postgres (Primary + Replicas) + PgBouncer + HAProxy
```

## Setup and Usage
### Prerequisites
- Go 1.23+
- Docker + Compose
- Git

### Local Development
1. Clone: `git clone https://github.com/nimeshabuddhika/resilient-payment-processor.git && cd resilient-payment-processor`
2. Build docs: `make docs`
3. Start infra: `make up`
4. Start services: `make up-services`
5. Seed data: `make seed-users-and-accounts && make seed-orders`
6. Access:
    - order-api: http://localhost:8081/swagger/index.html (POST /orders)
    - Grafana: http://localhost:3000 (admin/admin)
    - Kafka UI: http://localhost:8180
    - Prometheus: http://localhost:9090

Test end-to-end: Submit order via Swagger; monitor processing in payment-worker logs/Grafana.

### Testing
- Unit/Integration: `go test ./...` (uses Testify, Testcontainers for Kafka/Redis).
- Load: JMeter scripts in /tests/load (future expansion).

### Deployment
- Local: Docker Compose (above).
- Kubernetes: Helm charts in /charts (apply via Minikube: `helm install rpp ./charts/resilient-payment-processor`). (future expansion).

## Future Plans
- **Security**: Integrate Keycloak for authN/authZ, OAuth2/OIDC, RBAC, certificate management.
- **Optimization**: Tune concurrent processing (semaphores to autoscaling), high-availability DB (read replicas, failover).
- **Testing/Deployment**: Expand JMeter for 10k+ loads; Minikube with HPA autoscaling; CI/CD via GitHub Actions (build/test/deploy).
- **Other**: Multi-tenancy (tenantID partitions), more metrics (e.g., latency histograms).

## Contributing

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community guidelines. See [CONTRIBUTING.md](CONTRIBUTING.md) for
contribution. Use issue templates in [.github/ISSUE_TEMPLATE](.github/ISSUE_TEMPLATE)
for bugs/features. Pull requests via [PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md).

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for releases following semantic versioning.

## Security

Report vulnerabilities via [SECURITY.md](SECURITY.md).
## License
MIT - see [LICENSE.md](LICENSE.md).