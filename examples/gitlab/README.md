# Example: GitLab CI consumer repo

This is a complete example of a consuming repository that uses
`railway-DB-Provisioner` with GitLab CI.

## Files

| File             | Purpose                                                 |
| ---------------- | ------------------------------------------------------- |
| `services.txt`   | Declare which services need a database                  |
| `Dockerfile`     | Extends the provisioner base image, adds `services.txt` |
| `railway.json`   | Railway build + deploy config (one-shot, never restart) |
| `.gitlab-ci.yml` | CI pipeline that runs the CI image                      |

## Setup

1. **Fork or copy this folder** into your own GitLab repo.
2. **Edit `services.txt`** — add your service prefixes.
3. **Set the GitLab CI/CD variable:**
   - Go to Settings → CI/CD → Variables → Add variable
   - Key: `RAILWAY_TOKEN`
   - Value: your Railway API token (https://railway.com/account/tokens)
   - Check **Mask variable** so it doesn't leak in logs
   - Check **Protected** if your `main` branch is protected
4. **Create the provisioner service on Railway:**
   - Create an empty service named `DB-Provisioner`
   - Connect it to your GitLab repo (or deploy via the CI pipeline)
   - Set `POSTGRES_URL` to `${{Postgres.POSTGRES_URL}}` (reference the Postgres plugin)
   - See [`env.provisioner.example`](https://github.com/pecataToshev/railway-DB-Provisioner/blob/main/env.provisioner.example) for all env vars
5. **Push to `main`** — the pipeline runs, generates missing `*_POSTGRES_URL` vars,
   and deploys the provisioner.

## What happens on each run

1. `ci-setup` reads `services.txt`, fetches existing variables from the Railway
   service, and generates `*_POSTGRES_URL` for any missing services.
2. `railway up` deploys the provisioner, which creates the PostgreSQL roles +
   databases.
3. Existing variables are left untouched (idempotent).
