# Roadmap

Multi-stage, multi-agent plan for building the URL shortener described in
[CLAUDE.md](./CLAUDE.md). The stages follow its "Build order". CLAUDE.md stays
the source of truth for behavior; this file only says who builds what, in what
order, and how each stage is accepted.

## Table of Contents

- [Operating Rules](#operating-rules)
- [Agent Roster](#agent-roster)
- [Stage Overview](#stage-overview)
- [Stage 0: Contracts](#stage-0-contracts)
- [Stage 1: Foundation](#stage-1-foundation)
- [Stage 2: Links and Redirect](#stage-2-links-and-redirect)
- [Stage 3: JSON API](#stage-3-json-api)
- [Stage 4: Click Recording and Stats](#stage-4-click-recording-and-stats)
- [Stage 5: Admin UI](#stage-5-admin-ui)
- [Stage 6: Packaging and CI](#stage-6-packaging-and-ci)
- [Stage 7: Deploy and Verify](#stage-7-deploy-and-verify)
- [Open Questions](#open-questions)
- [Later (Non-Goals)](#later-non-goals)

## Operating Rules

**Branching** (see `.claude/skills/git-workflow`)

- One `feature/<name>` branch per agent task, cut from `dev`.
- Draft PR into `dev` only. Merge commit, no squash or rebase. The owner merges.
- Never open a PR into `main` unless the owner asks.
- Conventional Commits, subject 50 characters or less, body explains why.

**Quality gate (every PR)**

- `gofmt -l .` prints nothing, `go vet ./...` and `go test ./...` pass.
- Explicit types, descriptive names, wrapped errors, no ignored errors, no
  `panic` outside `main`, specific error messages.
- Tables are singular, columns are `snake_case`, primary keys are UUIDs.
- No secrets in code, logs, tests, or chat.

**Parallelism**

- Agents in the same wave own disjoint directories, so they never edit the same
  file. Shared files (`go.mod`, `cmd/server/main.go`) are edited only by the
  Orchestrator or the stage's designated wiring agent.
- Stage 0 freezes the Go interfaces. Agents code against those interfaces and
  use in-memory fakes until the real implementation lands.
- If an agent needs an interface change, it stops and asks the Orchestrator
  rather than editing another agent's package.

**Dependencies**

- Allowed: standard library and `modernc.org/sqlite`. UUID v4 ids come from a
  small in-house `crypto/rand` helper, not a library. Anything else needs owner
  approval first.

## Agent Roster

| Agent        | Owns                                          | Role                                                                  |
| ------------ | --------------------------------------------- | --------------------------------------------------------------------- |
| Orchestrator | `go.mod`, `cmd/server/`, `ROADMAP.md`         | Sequences stages, freezes contracts, wires routes, resolves conflicts |
| Foundation   | `internal/config/`, `internal/migrations/`    | Config loading and validation, embedded migrations                    |
| Store        | `internal/store/`                             | SQLite open, migrations runner, link, click and stats queries         |
| Links        | `internal/links/`                             | Slug and URL validation, slug generation, reserved words              |
| Redirect     | `internal/redirect/`                          | Public routes, click recorder goroutine                               |
| API          | `internal/httpapi/`                           | JSON API, bearer auth, error shape                                    |
| Admin        | `internal/admin/`, `web/`                     | Sessions, CSRF, login limiter, templates, CSS                         |
| DevOps       | `Dockerfile`, compose, `Makefile`, `.github/` | Container, CI, backups                                                |
| QA           | `*_test.go` for any package (with the owner)  | Table-driven and `httptest` suites, store tests on temp SQLite files  |
| Reviewer     | read-only                                     | Security, style and CLAUDE.md compliance review before each PR        |
| Docs         | `README.md`, `CHANGELOG.md`, `.env.example`   | Deploy, backup, restore and usage docs                                |

Reviewer and QA run after each implementation agent, and never review their own
work.

## Stage Overview

| Stage | Name                      | Parallel agents                      | Gate                                  |
| ----- | ------------------------- | ------------------------------------ | ------------------------------------- |
| 0     | Contracts                 | Orchestrator                         | Interfaces compile, owner skims       |
| 1     | Foundation                | Foundation, Store, DevOps (skeleton) | `/healthz` works against real SQLite  |
| 2     | Links and Redirect        | Links, Store, Redirect               | Redirect 302 and 404 tests pass       |
| 3     | JSON API                  | API, QA                              | Auth and error-shape tests pass       |
| 4     | Click Recording and Stats | Redirect, Store, QA                  | Redirect latency unaffected by clicks |
| 5     | Admin UI                  | Admin, QA, Reviewer                  | Login, CSRF and add-link tests pass   |
| 6     | Packaging and CI          | DevOps, Docs                         | CI green, image builds and is healthy |
| 7     | Deploy and Verify         | Owner, DevOps, Docs                  | Real links redirect on the VPS        |

## Stage 0: Contracts

Goal: let later agents work in parallel without colliding.

| Task                                                                       | Agent        |
| -------------------------------------------------------------------------- | ------------ |
| `go mod init`, set current stable Go version, `.gitignore` check           | Orchestrator |
| Define `links.Validator`, `store.LinkStore`, `store.ClickStore` interfaces | Orchestrator |
| Define click record struct and API error code constants                    | Orchestrator |
| Define package layout from CLAUDE.md with `doc.go` stubs                   | Orchestrator |

Exit: `go build ./...` passes on empty stubs. Branch: `feature/contracts`.

## Stage 1: Foundation

Goal: a binary that boots, migrates, and answers `/healthz`.

| Task                                                                                           | Agent        |
| ---------------------------------------------------------------------------------------------- | ------------ |
| Config loader for every variable in CLAUDE.md; error names the variable, never value           | Foundation   |
| Validation: required vars, minimum lengths (16, 32, 32), `BASE_URL` no trailing slash          | Foundation   |
| `migrations/0001_init.sql` with `link`, `click`, index, `schema_migration`                     | Foundation   |
| Open DB with WAL, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`, one connection | Store        |
| Migration runner: ordered, transactional, recorded                                             | Store        |
| UUID v4 helper (`crypto/rand`, version and variant bits set) with format and uniqueness tests  | Store        |
| `GET /healthz` with trivial DB query, `http.Server` timeouts, SIGTERM shutdown                 | Orchestrator |
| Skeleton `Makefile` (`fmt`, `vet`, `test`, `run`)                                              | DevOps       |

Tests: config table tests (missing, too short, valid); store test applies
migrations to a temp file twice and proves idempotence.

Branches: `feature/config`, `feature/store-core`, `feature/healthz`.

## Stage 2: Links and Redirect

Goal: a stored slug redirects.

| Task                                                                                        | Agent    |
| ------------------------------------------------------------------------------------------- | -------- |
| Slug rules: 1 to 64 of `[A-Za-z0-9_-]`, case-sensitive, reserved list                       | Links    |
| Slug generator: 6 chars, no `0 O 1 l I`, `crypto/rand`, bounded retry                       | Links    |
| URL rules: http or https, host required, no userinfo, 2048 max, not `BASE_URL` host         | Links    |
| `link` store: create, get by slug, get by URL, list, UUID v4 ids, `created_at`/`updated_at` | Store    |
| `GET /{slug}` 302, plain 404, `HEAD` redirects, `GET /`, `GET /robots.txt`                  | Redirect |

Tests: validation tables with specific error messages (`slug "admin" is
reserved`); `httptest` for 302, 404, `HEAD`, `/` with and without
`HOME_REDIRECT_URL`; store tests on a real temp file.

Branches: `feature/links-validation`, `feature/link-store`,
`feature/redirect`.

## Stage 3: JSON API

Goal: add links and get the short URL back.

| Task                                                                               | Agent |
| ---------------------------------------------------------------------------------- | ----- |
| Bearer middleware using SHA-256 plus `ConstantTimeCompare`; never log the header   | API   |
| `POST /api/links`: 201 on create, 200 on duplicate URL without slug                | API   |
| `GET /api/links` (`limit`, `offset`) and `GET /api/links/{slug}`                   | API   |
| Error type mapping to `{"error","message"}` with the eight stable codes            | API   |
| `http.MaxBytesReader` at 16 KB; `internal_error` hides detail, logs it server-side | API   |
| Test suite: missing, wrong and correct key; every error code; duplicate handling   | QA    |

Branches: `feature/api-auth`, `feature/api-links`, `feature/api-tests`.

## Stage 4: Click Recording and Stats

Goal: statistics that can never slow or break a redirect.

| Task                                                                              | Agent    |
| --------------------------------------------------------------------------------- | -------- |
| Buffered channel, non-blocking send, drop plus logged counter when full           | Redirect |
| Single batching goroutine (every second or N records), flush on shutdown          | Redirect |
| Referrer host extraction, simple bot user-agent check, `HEAD` not counted         | Redirect |
| Stats queries: totals, last 7 and 30 days (bots excluded), per-day, top referrers | Store    |
| Wire counts into `GET /api/links` and `GET /api/links/{slug}`                     | API      |
| Tests: full channel drops without blocking, shutdown flushes, bot clicks excluded | QA       |

Gate: a redirect with a deliberately stalled DB writer still returns 302 within
the normal time. Branches: `feature/click-recorder`, `feature/stats-queries`.

## Stage 5: Admin UI

Goal: password-protected page to view stats and add links.

| Task                                                                               | Agent    |
| ---------------------------------------------------------------------------------- | -------- |
| Signed `HttpOnly`, `Secure`, `SameSite=Lax` session cookie, 12 h expiry            | Admin    |
| CSRF token on every state-changing form                                            | Admin    |
| In-memory login limiter (5 failures per 15 min per client IP); honor `TRUST_PROXY` | Admin    |
| `GET/POST /admin/login`, `POST /admin/logout`                                      | Admin    |
| `GET /admin` link table with add form; `POST /admin/links` success shows short URL | Admin    |
| `GET /admin/links/{id}` totals, 30-day table, top referrers (tables only)          | Admin    |
| Embedded `html/template`, small accessible CSS, `X-Robots-Tag: noindex`            | Admin    |
| Security headers (CSP without inline script, `nosniff`, referrer, frame options)   | Admin    |
| Tests: login success and failure, lockout, CSRF rejection, cookie flags            | QA       |
| Security review of auth, sessions, template escaping, log output                   | Reviewer |

Branches: `feature/admin-auth`, `feature/admin-dashboard`,
`feature/admin-link-stats`.

## Stage 6: Packaging and CI

Goal: reproducible build and deploy artifacts.

| Task                                                                                                | Agent  |
| --------------------------------------------------------------------------------------------------- | ------ |
| Multi-stage `Dockerfile`, `CGO_ENABLED=0`, non-root, minimal image, `HEALTHCHECK`                   | DevOps |
| `docker-compose.yml`: `env_file`, `./data:/data`, `expose: 8080`, proxy network via `PROXY_NETWORK` | DevOps |
| GitHub Actions: `gofmt -l` empty, `go vet`, `go test`, Docker build                                 | DevOps |
| In-app nightly `VACUUM INTO` backup to `/data/backups`, keep 14, plus restore test                  | Store  |
| `.env.example` (placeholders only), `README.md` (run, deploy, backup, restore), `CHANGELOG.md`      | Docs   |

The app ships as a single self-contained container. Compose attaches it to one
shared network so the existing Caddy container can reach it by service name
(`shortlinks:8080`). The network name comes from `PROXY_NETWORK` in `.env`
(default `hired-hand-net`, to be confirmed on the VPS with `docker network ls`
or `docker inspect <caddy container>`). No host port is published.

Branches: `feature/docker`, `feature/ci`, `feature/backup`, `feature/docs`.

## Stage 7: Deploy and Verify

Goal: live on `go.richardmcquiston.com`. Needs the owner for server access and
secrets; agents never see production secrets.

| Task                                                                    | Owner of task |
| ----------------------------------------------------------------------- | ------------- |
| Create `.env` on the VPS (`TRUST_PROXY=true`, real `BASE_URL`)          | Owner         |
| DNS A record for `go.richardmcquiston.com`                              | Owner         |
| Set `PROXY_NETWORK` to the network Caddy uses (`docker network ls`)     | Owner         |
| PR into `html2lvgl-platform` `dev` adding the Caddy block               | DevOps        |
| Promote `dev` to `main` (only when the owner asks)                      | Owner         |
| Smoke test: add link via API, redirect, admin stats, restart keeps data | Owner + QA    |
| Verify backup file appears and restores                                 | Owner         |

Exit: real Chrome extension and VS Code Marketplace links redirect correctly
and show clicks in the admin page.

## Open Questions

Resolved:

- UUIDs: in-house v4 generator (`crypto/rand`), no extra dependency.
- Sessions: signed stateless cookie.
- Backups: run inside the app, so there is no cron or sidecar to maintain.

Still to confirm on the VPS:

- The Docker network name Caddy uses (default `hired-hand-net`).

## Later (Non-Goals)

Not to be built unasked: editing or deleting links in the UI, user accounts, QR
codes, link expiry, custom domains per link, GeoIP, A/B targets, and any
JavaScript framework.
