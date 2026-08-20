# Contributing

Thanks for your interest in improving `railway-DB-Provisioner`!

## Development

```bash
# Build both binaries
go build ./...
go vet ./...

# Smoke-test (expected to fail — no env vars set)
go build -o /tmp/provisioner ./cmd/provisioner
SERVICES_FILE=services.example.txt /tmp/provisioner

go build -o /tmp/ci-setup ./cmd/ci-setup
/tmp/ci-setup   # fails: RAILWAY_TOKEN not set
```

## Submitting changes

1. Fork the repo and create a branch from `main`.
2. Make your change. Keep commits focused — one logical change per PR.
3. Ensure `go build ./...` and `go vet ./...` pass locally.
4. Open a PR. The CI workflow runs build + vet automatically.
5. Don't change the Go code logic unless the PR description explains _why_ —
   this tool runs against production databases, so behavior changes need
   clear justification.

## What not to change

- **`services.txt`** — this repo ships only `services.example.txt`. Real
  service lists are consumer-specific and live in the consumer's repo.
- **`railway.json`** — consumers create their own.
- **The provisioner's SQL behavior** (role/database creation, grants,
  `REVOKE CONNECT`) — it's deliberately idempotent and security-hardened.
  Changes here affect every consumer's databases.

## Releasing

Maintainers only:

1. Tag a release: `git tag v1.0 && git push --tags`.
2. The CI workflow builds, vets, and (on tag) publishes both Docker images
   to GHCR. Each image gets three tags: `latest`, the commit SHA, and the
   git tag name (e.g. `v1.0`).
3. Create a GitHub Release from the tag with release notes.
