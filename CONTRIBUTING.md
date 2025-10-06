# Contributing to Resilient Payment Processor

Thank you for considering contributing! We follow best practices to maintain a scalable, reliable codebase. Please read
this guide before submitting changes.

## Code of Conduct

This project adheres to the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you agree to
uphold this code.

## How to Contribute

1. **Report Issues**: Use the [issue templates](.github/ISSUE_TEMPLATE) for bugs or features.
2. **Submit Pull Requests**: Follow the [pull request template](.github/PULL_REQUEST_TEMPLATE.md). Ensure your PR
   addresses an open issue or includes a clear description.
3. **Security Reports**: See [SECURITY.md](SECURITY.md) for vulnerabilities.

## Git Workflow (Git Flow)

We use the Git Flow branching model for structured development:

- **main**: Production-ready code. Only merge release or hotfix branches here.
- **develop**: Integration branch for features. Base new work here.
- **feature/**: For new features (e.g., `feature/add-job-dispatcher`). Branch from develop, merge back via PR.
- **release/**: For preparing releases (e.g., `release/v1.1.0`). Branch from develop, merge to main and develop after
  testing.
- **hotfix/**: For urgent fixes (e.g., `hotfix/fix-kafka-connection`). Branch from main, merge to main and develop.

### Steps for Features:

1. `git checkout -b feature/my-feature develop`
2. Commit changes (use conventional commits: e.g., `feat: add retry logic`).
3. Push: `git push origin feature/my-feature`
4. Open PR to develop, add tests, ensure 80% coverage.
5. After review, merge and delete branch.

### Commit Guidelines

- Use semantic commits: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.
- Keep messages concise: `<type>: <short description>`.

### Testing & Quality

- Add unit/integration tests.
- Run `go test ./...` locally.
- CI/CD via GitHub Actions enforces builds and coverage.

### Setup

Fork the repo, clone your fork, and add upstream:
`git remote add upstream git@github.com:nimeshabuddhika/resilient-job-go.git`.

For questions, open an issue.