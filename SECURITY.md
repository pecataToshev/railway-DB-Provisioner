# Security Policy

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please report them privately:

1. Open a **private security advisory** via GitHub:
   *Security* tab → *Report a vulnerability*.
2. Or email the maintainer: **pecata.toshev@gmail.com** with the subject
   `[security] railway-DB-Provisioner`.

Include:
- A description of the vulnerability and its impact.
- Steps to reproduce (proof of concept if possible).
- Affected versions / image tags.

You will receive a response within 72 hours. If the vulnerability is
confirmed, a fix and advisory will be published as soon as possible.

## Scope

This tool provisions PostgreSQL roles and databases on Railway. Issues
that affect the security of provisioned databases (e.g. privilege
escalation, SQL injection in role/database creation, leaked credentials
in logs) are in scope.

## Out of scope

- Vulnerabilities in Railway's platform or the Railway CLI.
- Vulnerabilities in consumer-defined `services.txt` content.
- Issues that require already having superuser access to the target
  Postgres instance.
