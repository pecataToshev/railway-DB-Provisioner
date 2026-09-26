#!/bin/sh
set -e

# ci-entrypoint.sh — runs inside the CI Docker image.
#
# ci-setup does everything:
#   1. Ensures per-service *_POSTGRES_URL variables exist on the
#      db-provisioner Railway service (idempotent — only sets missing
#      or stale ones) via the Railway GraphQL API.
#   2. Deploys the db-provisioner service via `railway up` — uploads
#      local files (Dockerfile + services.txt) to Railway for building.
#
# Required env: RAILWAY_TOKEN, RAILWAY_SERVICE_NAME
# Optional env:
#   REPO_DIR      — path to the cloned repo (default: current directory).
#                   Set this when the CI provider mounts the repo somewhere
#                   other than the image's WORKDIR (e.g. GitLab: $CI_PROJECT_DIR).
#   SERVICES_FILE — path to the services file (default: services.txt, relative
#                   to REPO_DIR if set).

# cd to the repo directory so ci-setup finds the right files.
REPO_DIR="${REPO_DIR:-.}"
cd "$REPO_DIR"

echo "=== Working directory: $(pwd) ==="
echo "=== Ensuring database variables and deploying ==="
ci-setup

echo "=== Done ==="
