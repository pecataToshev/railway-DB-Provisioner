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
# Optional env:
#   REPO_DIR      — path to the cloned repo (default: current directory).
#                   Set this when the CI provider mounts the repo somewhere
#                   other than the image's WORKDIR (e.g. GitLab: $CI_PROJECT_DIR).
#   SERVICES_FILE — path to the services file (default: services.txt, relative
#                   to REPO_DIR if set).

# cd to the repo directory so ci-setup and railway up find the right files.
REPO_DIR="${REPO_DIR:-.}"
cd "$REPO_DIR"

echo "=== Working directory: $(pwd) ==="
echo "=== Ensuring database variables ==="
ci-setup

echo "=== Deploying db-provisioner to Railway ==="
railway up --service "$RAILWAY_SERVICE_NAME"

echo "=== Done ==="
