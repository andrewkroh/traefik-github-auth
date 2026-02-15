<p align="center">
  <img src="gopher.png" alt="traefik-github-auth" width="300">
</p>

# traefik-github-auth

A [Traefik ForwardAuth](https://doc.traefik.io/traefik/middlewares/http/forwardauth/)
service that authenticates requests using GitHub Personal Access Tokens (PATs).
It verifies the token, checks organization membership, and forwards the
authenticated user's identity to your backend services via HTTP headers.

Use it to protect internal tools, dashboards, or APIs so that only members
of your GitHub organization can access them — no OAuth app registration,
no callback URLs, no session cookies. Users simply set a GitHub PAT as
a Bearer token.

## Features

- Validates GitHub fine-grained PATs against the GitHub API.
- Verifies the user belongs to a specific GitHub organization.
- Optionally rejects classic PATs (enabled by default), enforcing
  fine-grained PATs for better security scoping.
- Forwards user identity to upstream services via response headers:
  - `X-Auth-User-Login` — GitHub username
  - `X-Auth-User-Id` — GitHub user ID
  - `X-Auth-User-Org` — GitHub organization
  - `X-Auth-User-Teams` — Comma-separated team slugs within the org
- Caches validation results (default 5 minutes) to minimize GitHub API calls.
- Built-in OpenTelemetry support for traces and metrics.
- Health (`/healthz`) and readiness (`/ready`) endpoints.

## How it works

```
Client                  Traefik              traefik-github-auth         GitHub API
  │                       │                         │                       │
  ├─ Request ────────────►│                         │                       │
  │  Authorization:       │                         │                       │
  │  Bearer ghp_...       ├─ /validate ────────────►│                       │
  │                       │  (ForwardAuth)          ├─ GET /user ──────────►│
  │                       │                         │◄─ {login, id} ────────┤
  │                       │                         ├─ GET /orgs/ORG/       │
  │                       │                         │   members/USER ──────►│
  │                       │                         │◄─ 204 ────────────────┤
  │                       │                         ├─ GET /user/teams ────►│
  │                       │                         │◄─ [{slug}] ───────────┤
  │                       │◄─ 200 + headers ────────┤                       │
  │                       │   X-Auth-User-Login     │                       │
  │                       │   X-Auth-User-Id        │                       │
  │                       │   X-Auth-User-Org       │                       │
  │                       │   X-Auth-User-Teams     │                       │
  │◄─ Proxied request ────┤                         │                       │
  │   (with auth headers) │                         │                       │
```

## Installation

```bash
go install github.com/andrewkroh/traefik-github-auth/cmd/server@latest
```

Pre-built binaries and Docker images are published to
[GitHub Releases](https://github.com/andrewkroh/traefik-github-auth/releases)
and [GHCR](https://ghcr.io/andrewkroh/traefik-github-auth).

```bash
docker pull ghcr.io/andrewkroh/traefik-github-auth:latest
```

## Usage

```bash
traefik-github-auth -org <your-github-org>
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-org` | *(required)* | GitHub organization to validate membership against |
| `-listen` | `:8080` | HTTP listen address |
| `-cache-ttl` | `5m` | Duration to cache successful validation results |
| `-reject-classic-pats` | `true` | Reject classic PATs (only allow fine-grained PATs) |

### Traefik configuration

Configure Traefik to use the ForwardAuth middleware:

```yaml
# Dynamic configuration
http:
  middlewares:
    github-auth-headers:
      headers:
        customRequestHeaders:
          X-Auth-User-Login: ""
          X-Auth-User-Id: ""
          X-Auth-User-Org: ""
          X-Auth-User-Teams: ""

    github-auth:
      forwardAuth:
        address: "http://traefik-github-auth:8080/validate"
        authResponseHeaders:
          - "X-Auth-User-Login"
          - "X-Auth-User-Id"
          - "X-Auth-User-Org"
          - "X-Auth-User-Teams"

  routers:
    my-service:
      rule: "Host(`app.example.com`)"
      middlewares:
        - github-auth-headers # Important: Sanitizes headers before auth
        - github-auth
      service: my-service

  services:
    my-service:
      loadBalancer:
        servers:
          - url: "http://my-backend:8080"
```

### GitHub PAT requirements

Users authenticating against this service need a **fine-grained PAT** with the
**Resource owner** set to your organization and the following permissions:

| Permission              | Required for |
|-------------------------|--------------|
| **Members** (Read-only) | Org membership check and `X-Auth-User-Teams` header |

> **Note:** The Members permission is only required for the `X-Auth-User-Teams`
> header. Without it, authentication and org membership checks still work, but
> the teams list will be empty.

The token is sent as a Bearer token in the `Authorization` header:

```bash
curl -H "Authorization: Bearer github_pat_..." https://app.example.com/
```

### OpenTelemetry

The service exports traces and metrics via OTLP/HTTP when the standard
`OTEL_EXPORTER_OTLP_ENDPOINT` environment variable is set. The service
name is `traefik-github-auth`.

## License

Apache 2.0 — see [LICENSE](../LICENSE) for details.
