# ResilientJobGo

ResilientJobGo is an open-source demonstration of a scalable, event-driven job processing system
built with Go microservices and Kafka. It showcases best practices in distributed systems, including horizontal scaling,
resilience patterns (retries, rate limiting, circuit breakers), and observability. This project mirrors real-world high-volume pipelines for tasks
like data processing or image resizing, emphasizing reliability,and maintainability in architecting production-ready systems.

## Project Background

* Situation: Needed a scalable system to handle 100k+ concurrent jobs without failures in a horizontally scalable manner.
* Task: Architect event-driven microservices with production features for job submission and processing.
* Action: Built Go services with Kafka for messaging, added resilience patterns, deployed via Helm on Kubernetes, and tested for load.
* Result: System handles 5k jobs/min locally, zero critical issues in tests, ready for enterprise scaling.


## Key Features
* API Service (Go with Gin): Accepts job submissions via JSON payloads, authenticates with Basic Auth (short-term; planned OAuth2/OIDC with Keycloak for RBAC).
* Worker Service (Go): Scalable consumers from Kafka topics, with retries, rate limiting (golang.org/x/time/rate), and circuit breakers.
* Event-Driven Messaging: Kafka topics for job queues and dead-letter queues for failures.
* Scalability & Resilience: Horizontal scaling with Kubernetes replicas, resource locking via Redis.
* Observability: Prometheus metrics (job throughput, error rates), Grafana dashboards, structured logging with Zap.
* Security: Tenant isolation with IDs in Kafka partitions; certificate management (self-signed for dev).
* AI Tie-In: Uses Torch (via Go bindings) for job classification to prioritize high-impact tasks.
* Deployment: Kubernetes via Helm Charts, including Ingress for API exposure.
* Testing: Unit/integration tests with Go Test + Testify, Testcontainers for Kafka/Redis, JMeter load tests.
* CI/CD: GitHub Actions for build, test, deploy.
* Maintainability: Common library in `/libs`.

# Installation

1. Clone the repository:

```
git clone https://github.com/nimeshabuddhika/resilient-job-go.git
cd resilient-job-go
```

2. Install dependencies:

```
    go mod tidy
```

3. Set up local environment (requires Docker, Minikube/Kubernetes, Kafka via Helm):

```
minikube start
helm install kafka confluentinc/cp-helm-charts --name my-kafka
```

4. Build services:

```
go build -o api ./api/cmd/main.go
go build -o worker ./worker/cmd/main.go
```

## Usage
1. Run API service:
```
 ./api
```

* Submit jobs: `POST /jobs` with JSON payload

```json

```

2. Run Worker service (scales with multiple instances):
```
./worker
```

3. Monitor with Prometheus/Grafana:
* Access dashboards at `localhost:3000` after setup.

4. Deploy to Kubernetes:

```
helm install resilient-job ./charts/resilientjobgo
```

## Contributing

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community guidelines. See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution. Use issue templates in [.github/ISSUE_TEMPLATE](.github/ISSUE_TEMPLATE) 
for bugs/features. Pull requests via [PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md).

## Changelog
See [CHANGELOG.md](CHANGELOG.md) for releases following semantic versioning.

## Security
Report vulnerabilities via [SECURITY.md](SECURITY.md).

## License
See [LICENSE.md](LICENSE.md).