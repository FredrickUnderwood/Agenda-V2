# Security Policy

## Supported versions

agenda-v2 is pre-1.0 and under active development. Security fixes land on the
default branch (`master`); please run the latest commit.

| Version | Supported |
|---|---|
| `master` (latest) | ✅ |
| older commits / tags | ❌ |

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately through GitHub's **[Private Vulnerability Reporting](https://github.com/FredrickUnderwood/Agenda-V2/security/advisories/new)**
(the repository's *Security → Advisories → Report a vulnerability*). This keeps the
report confidential until a fix is available and lets us coordinate a disclosure.

When reporting, please include:

- affected component (`control-plane` / `gateway` / `node` / `sdk` / `web`) and
  version/commit,
- a description of the issue and its impact,
- reproduction steps or a proof of concept, and
- any suggested remediation, if you have one.

## What to expect

- **Acknowledgement**: we aim to acknowledge a report within a few days.
- **Assessment**: we will confirm the issue, determine severity, and keep you
  updated on remediation progress.
- **Fix & disclosure**: once a fix is ready we will release it and, with your
  consent, credit you in the advisory. Please give us a reasonable window to
  remediate before any public disclosure.

## Scope notes

agenda-v2 is infrastructure you self-host, so its security also depends on how you
deploy it. A few things that are **your responsibility as the operator** rather
than platform vulnerabilities:

- Keeping the generated secrets in `deploy/quickstart/.env` and your
  `config/agenda-v2.yaml` out of version control (they are git-ignored by default).
- Restricting network access to the control plane, the `agenda-node` management
  port, and the database to trusted networks.
- Setting `security.master_key` and `auth.jwt_secret` to strong random values and
  rotating the bootstrap admin credentials after first login.

Reports about missing hardening in these operator-controlled areas are welcome as
regular issues or discussions rather than private security reports.
