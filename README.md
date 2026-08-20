# railway-db-provisioner

Automated PostgreSQL database provisioning for Railway. Declare which services need a database, and this tool creates the roles, databases, and connection strings — idempotently, on every deploy.

## Goals

- **Zero-touch database provisioning**: add a service to `services.txt`, push, and its database + user + connection string are created automatically on Railway.
- **Idempotent**: safe to run repeatedly. Re-deploys sync passwords and reconcile grants — credential rotation is just a variable update + redeploy.
- **Secure by default**: each database is isolated (`CONNECT` revoked from `PUBLIC`, granted only to the service's own user). Maintenance databases (`postgres`, `template1`) are hardened on every run.
- **CI-agnostic**: works from GitHub Actions and GitLab CI alike — the CI logic ships as a Docker image, consuming pipelines just `docker run` it.
- **Single connection string per service**: each service gets one `<PREFIX>_POSTGRES_URL` variable (was three separate NAME/USER/PASS vars). The provisioner parses it; consuming services reference it directly.
- **No raw secrets in consuming services**: consuming services set `DATABASE_URL` to a Railway variable reference (e.g. `${{ "DB-Provisioner".QUIZZER_POSTGRES_URL }}`), never to a literal connection string.
- **Portable**: the codebase ships as two base Docker images. Consumers extend the provisioner image with their own `services.txt` and run the CI image in their pipeline.

## Architecture

Two binaries, two Docker images:

| Binary        | Image                                            | Where it runs                            | What it does                                                                                                                 |
| ------------- | ------------------------------------------------ | ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `provisioner` | `ghcr.io/pecatatoshev/railway-db-provisioner`    | Railway (one-shot service)               | Reads `*_POSTGRES_URL` env vars, creates roles + databases in Postgres, prints reference URLs                                |
| `ci-setup`    | `ghcr.io/pecatatoshev/railway-db-provisioner-ci` | CI pipeline (GitHub Actions / GitLab CI) | Ensures per-service `*_POSTGRES_URL` variables exist on the provisioner Railway service (idempotent), then triggers a deploy |

```
CI pipeline                                Railway project
┌─────────────────────────┐               ┌──────────────────────────────────┐
│ docker run              │               │ Postgres plugin                  │
│   railway-db-provisioner-ci             │   └── many databases             │
│                         │               │                                  │
│  ci-setup:              │──(1) set vars▶│ provisioner service              │
│    parse services.txt   │               │   env: POSTGRES_URL              │
│    ensure *_URL vars    │               │   env: QUIZZER_POSTGRES_URL      │
│    (generate passwords) │               │   env: AUTH_POSTGRES_URL         │
│                         │──(2) deploy──▶│   ...                            │
│  railway up:            │               │                                  │
│    deploy provisioner   │               │ provisioner binary runs:         │
│                         │               │   → create role + database       │
│                         │               │   → print reference URLs         │
└─────────────────────────┘               └──────────────────────────────────┘
```

1. **`ci-setup`** fetches the provisioner service's existing variables via Railway CLI, finds `POSTGRES_URL` to derive the host:port, and for each service in `services.txt` ensures `<PREFIX>_POSTGRES_URL` exists (generating a password and building the connection string if missing).
2. **`railway up`** deploys the provisioner service. The `provisioner` binary reads all `*_POSTGRES_URL` variables, connects to Postgres via `POSTGRES_URL` (superuser), and creates the corresponding roles + databases.

## How it works

1. **`services.txt`** — declare which services need a database, one entry per line in format `DBTYPE:SERVICE`.
2. On every CI run, `ci-setup` checks that every required `*_POSTGRES_URL` variable is present. If anything is missing it generates one (password + connection string) and sets it on the provisioner Railway service.
3. The `provisioner` binary then runs on Railway. On every run it first hardens the instance (idempotent; superusers are unaffected):

   ```sql
   REVOKE CONNECT ON DATABASE postgres FROM PUBLIC;
   REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;
   ```

4. For each service it creates the user, database, and grants the correct permissions. Each database is isolated: `CONNECT` is revoked from `PUBLIC` and granted only to the service's own user.

Extra role attributes (`CREATEROLE`, `SUPERUSER`, etc.) are deliberately **not** granted by this tool. Services that need them (e.g. Logto) get their base database from here, plus a documented one-time manual init.

The run is fully **idempotent**: safe to run repeatedly. Passwords are synced on each run, so credential rotation is as simple as updating the Railway variable and redeploying.

---

## Adding a service

In your consuming repo, create `services.txt` (based on [`services.example.txt`](services.example.txt)) and add the prefix:

```
POSTGRES:QUIZZER
POSTGRES:AUTH
```

Push to `main` — the CI pipeline will:

1. Generate `<PREFIX>_POSTGRES_URL` (e.g. `QUIZZER_POSTGRES_URL=postgresql://quizzer_user:pass@host:5432/quizzer_db`) if it doesn't already exist.
2. Deploy the provisioner, which creates the database and user.

Then set the `DATABASE_URL` in your consuming service to the Railway reference printed in the provisioner logs:

```text
${{ "DB-Provisioner".QUIZZER_POSTGRES_URL }}
```

---

## Railway setup

### 1. Shared Database Instances

Add the required database plugins to your Railway project:

- **PostgreSQL** — gives you the superuser `POSTGRES_URL`

### 2. Create the provisioner service

1. Create a new **empty service** in the same project, name it `DB-Provisioner` (or whatever you set as `RAILWAY_SERVICE_NAME`).
2. In your consuming repo, create a `Dockerfile` that extends the base image and adds your `services.txt`:

   ```dockerfile
   FROM ghcr.io/pecatatoshev/railway-db-provisioner:latest
   COPY services.txt /app/services.txt
   ```

3. Connect the service to your consuming GitHub repository (Railway GitHub App — no secrets needed on Railway's side).
4. Railway will build from your `Dockerfile` and run the provisioner.

### 3. Set environment variables on the service

See [`env.provisioner.example`](env.provisioner.example) for the full list. In the `DB-Provisioner` service **Variables** tab:

| Variable                                    | Value                                               |
| ------------------------------------------- | --------------------------------------------------- |
| `POSTGRES_URL`                              | `${{Postgres.POSTGRES_URL}}` (reference the plugin) |
| `QUIZZER_POSTGRES_URL`                      | _(auto-generated by ci-setup on first CI run)_      |
| _(repeat for each service in services.txt)_ |                                                     |

The `*_POSTGRES_URL` variables are created automatically by the CI pipeline. You only need to set `POSTGRES_URL` manually (referencing the Postgres plugin).

### 4. Service naming

The provisioner uses `RAILWAY_SERVICE_NAME` to generate Railway variable reference URLs. If not set, it defaults to `DB Provisioner`. Ensure this matches your actual Railway service name.

### 5. CI pipeline

In **your** consuming repo, add a workflow (e.g. `.github/workflows/provision-databases.yml`) that runs the CI Docker image against your `services.txt`. See [`env.ci.example`](env.ci.example) for the required environment variables.

```yaml
name: Provision Databases
on:
  push:
    branches: [main]
    paths: ["services.txt", ".github/workflows/provision-databases.yml"]
  workflow_dispatch:

jobs:
  provision:
    runs-on: ubuntu-latest
    env:
      RAILWAY_TOKEN: ${{ secrets.RAILWAY_TOKEN }}
      RAILWAY_SERVICE_NAME: DB-Provisioner
    steps:
      - uses: actions/checkout@v4
      - run: |
          docker run --rm \
            -e RAILWAY_TOKEN \
            -e RAILWAY_SERVICE_NAME \
            -v "$PWD:/repo" \
            -w /repo \
            ghcr.io/pecatatoshev/railway-db-provisioner-ci:latest
```

For **GitLab CI**, the equivalent step:

```yaml
provision-databases:
  image: ghcr.io/pecatatoshev/railway-db-provisioner-ci:latest
  variables:
    RAILWAY_TOKEN: $RAILWAY_TOKEN
    RAILWAY_SERVICE_NAME: DB-Provisioner
  script:
    - ci-entrypoint.sh
```

### 6. Enjoy

Once a prefix is in `services.txt` and the CI pipeline has run at least once, you can copy a safe `DATABASE_URL` reference from the provisioner's logs. On startup the Go service prints, for each service, a Railway-style reference like:

```text
${{ "DB-Provisioner".QUIZZER_POSTGRES_URL }}
```

Paste the appropriate one into your consuming service as its `DATABASE_URL` and it will always point at the right credentials without exposing raw secrets.

---

## Docker images

Two separate Dockerfiles, each self-contained:

```bash
# Provisioner base image (deployed to Railway)
docker build -f docker/Dockerfile -t railway-db-provisioner .

# CI image (used in CI pipelines)
docker build -f docker/Dockerfile.ci -t railway-db-provisioner-ci .
```

Published to GHCR on every `v*` tag push. Each release produces three tags:

| Tag      | Meaning                             |
| -------- | ----------------------------------- |
| `latest` | Most recent release                 |
| `<sha>`  | Commit SHA the image was built from |
| `vX.Y`   | The git tag (e.g. `v1.0`)           |

Pin to a specific version tag in production; use `latest` for experimentation.

The CI image bundles:

- `ci-setup` Go binary (var provisioning logic)
- Railway CLI (API calls)
- `ci-entrypoint.sh` (orchestrates: ci-setup → railway up)

---

## Project Structure

```
railway-DB-Provisioner/
├── .github/
│   ├── actions/write-buildinfo/  # Reusable composite action (injects build metadata)
│   └── workflows/
│       └── ci.yml                # build + vet (always); publish to GHCR (on tag/manual)
├── cmd/
│   ├── provisioner/
│   │   └── main.go              # Binary 1: runs on Railway, creates DBs
│   └── ci-setup/
│       └── main.go              # Binary 2: runs in CI, ensures vars exist
├── internal/
│   ├── buildinfo/               # Build metadata (injected by CI)
│   ├── config/                  # Service parsing, env var resolution
│   ├── provisioner/             # PostgreSQL provisioning logic
│   └── railway/                 # Railway CLI wrapper
├── docker/
│   ├── Dockerfile               # Provisioner base image (Railway)
│   └── Dockerfile.ci            # CI image (GitHub/GitLab pipelines)
├── services.example.txt         # Example service declarations (copy to services.txt)
├── env.provisioner.example      # Env vars for the provisioner service (Railway)
├── env.ci.example               # Env vars for the CI pipeline (GitHub Actions / GitLab CI)
├── ci-entrypoint.sh             # CI entrypoint (ci-setup + railway up)
├── .dockerignore
├── .gitignore
├── CONTRIBUTING.md
├── LICENSE
├── SECURITY.md
├── go.mod
└── README.md
```
