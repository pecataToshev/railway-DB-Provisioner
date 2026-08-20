#!/bin/sh
set -e

# ci-entrypoint.sh — runs inside the CI Docker image.
#
# 1. ci-setup: ensures per-service *_POSTGRES_URL variables exist on the
#    db-provisioner Railway service (idempotent — only sets missing ones).
# 2. railway up: builds and deploys the db-provisioner service, which reads
#    those variables and creates the actual databases/users.
#
# Required env: RAILWAY_TOKEN, RAILWAY_SERVICE_NAME
# Optional env: SERVICES_FILE (default: services.txt)

echo "=== Ensuring database variables ==="
ci-setup

echo "=== Deploying db-provisioner to Railway ==="
railway up --service "$RAILWAY_SERVICE_NAME"

echo "=== Done ==="
