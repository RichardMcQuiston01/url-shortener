# CLAUDE.md

This file guides Claude Code (claude.ai/code) when working in this repository.

## What this is

A small, self-hosted URL shortener written in Go with SQLite. It gives short links such as
`https://go.richardmcquiston.com/hello` that redirect to long URLs (Chrome extension store pages,
VS Code Marketplace pages, project sites, and so on). It replaces the idea of running YOURLS
(PHP + MySQL) with one container and one database file.

- **Production host:** `go.richardmcquiston.com`. `mcqlinks.com` may replace or join it later, so the
  public base URL is configuration (`BASE_URL`) and the database stores **slugs only**, never full
  short URLs.
- **Runs on:** a Hostinger VPS using Docker Compose, behind the Caddy reverse proxy that already
  serves other sites there (Caddy handles HTTPS).
- **Status:** built and on `main`: build order steps 1 to 6 (server, redirects, JSON API, click statistics,
  admin UI, Docker/compose/CI/backups/README) and the OpenAPI spec (`openapi.json`). Remaining: step 7, deploying
  to the VPS and verifying with real links (see README, "Deploy with Docker"). The "Build order" list below is kept
  as the record of how it was built.

### Goals

- Redirects are fast and never blocked by statistics.
- A small admin page to **view stats** and **add links**, protected by one admin password.
- A small JSON API to **add links and get the short URL back**, protected by one API key.
- Click statistics stored in the same SQLite database.
- One static binary, one container, one data volume.

### Non-goals (for now)

Editing or deleting links in the UI, user accounts, QR codes, link expiry, custom domains per link,
GeoIP, A/B targets, and any JavaScript framework. These can come later; do not add them unasked.

## Owner preferences (follow these)

- **Language:** Go (current stable release; set it in `go.mod`). Keep dependencies minimal: prefer the
  standard library (`net/http` with method patterns, `html/template`, `embed`, `log/slog`,
  `crypto/*`).
- **Code style:** format with `gofmt`; keep `go vet` clean. Use explicit types for declared variables,
  struct fields and function signatures. Do not fight `gofmt` or `vet` for this.
- Names are descriptive. Prefer clear names over short ones (`targetURL`, not `u`).
- **Functions return a value (or an error) that the caller checks.** Wrap errors with context
  (`fmt.Errorf("insert link %q: %w", slug, err)`). Do not ignore errors. Try to recover and continue
  where that is safe; otherwise exit gracefully with a clear message. No `panic` outside `main`.
- **Error messages are specific**, for example `slug "admin" is reserved`, never `invalid input`. API
  errors use the JSON shape below.
- **JSON for APIs**, never SOAP or form-encoded APIs.
- **Database:** table names are **singular** (`link`, not `links`); column names use `snake_case`;
  primary keys are **UUIDs**, not auto-increment; tables that are not append-only have `created_at`
  and `updated_at`.
- **Secrets** live only in a `.env` file on the server (and a local, git-ignored `.env` in dev). Never
  commit them, never print them in logs or output, and never ask the owner to paste them into chat.

## Git workflow

- Work on feature branches and open **draft pull requests into `dev`**. Do **not** open a PR into
  `main` unless the owner asks. The owner merges PRs themselves.
- `main` is production. Promotion is `dev` to `main`, by owner request only.
- Run `gofmt`, `go vet ./...` and `go test ./...` before pushing.

## Configuration (`.env`)

Read from environment variables. In production they come from a `.env` file on the VPS passed to
Docker Compose (`env_file`). Commit `.env.example` (placeholders only); `.env` is git-ignored.

| Variable | Required | Purpose |
| --- | --- | --- |
| `BASE_URL` | yes | Public base URL, e.g. `https://go.richardmcquiston.com` (no trailing slash). Used to build `short_url`. |
| `ADMIN_PASSWORD` | yes | Password for the admin UI. At least 16 characters. |
| `API_KEY` | yes | Bearer key for `/api/*`. At least 32 random characters. |
| `SESSION_SECRET` | yes | Random secret (32+ bytes) used to sign admin session cookies. |
| `DATABASE_PATH` | no | SQLite file path. Default `/data/links.db`. |
| `PORT` | no | Listen port. Default `8080`. |
| `HOME_REDIRECT_URL` | no | Where `GET /` redirects. If unset, `/` returns a plain 404 page. |
| `TRUST_PROXY` | no | `true` when behind Caddy, so the client IP is read from `X-Forwarded-For`. Default `false`. |

On startup, refuse to start with a clear error naming the missing or too-short variable (never the
value). Compare secrets with `crypto/subtle.ConstantTimeCompare` (hash both sides with SHA-256 first so
lengths match).

## Data model (SQLite)

Open with: WAL journal mode, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`. Use a pure-Go
driver (`modernc.org/sqlite`) so the build needs no cgo (`CGO_ENABLED=0`). Start with a single
connection (`db.SetMaxOpenConns(1)`), which is plenty at this scale.

Schema changes are embedded SQL migration files (`migrations/0001_init.sql`, ...) applied in order at
startup and recorded in a `schema_migration` table.

```sql
CREATE TABLE link (
  id          TEXT PRIMARY KEY,            -- UUID v4
  slug        TEXT NOT NULL UNIQUE,
  target_url  TEXT NOT NULL,
  note        TEXT,                        -- optional label, e.g. "Chrome ext: Hired Hand"
  created_at  TEXT NOT NULL,               -- RFC 3339, UTC
  updated_at  TEXT NOT NULL
);

CREATE TABLE click (
  id          TEXT PRIMARY KEY,            -- UUID v4
  link_id     TEXT NOT NULL REFERENCES link (id) ON DELETE CASCADE,
  clicked_at  TEXT NOT NULL,               -- RFC 3339, UTC
  referrer_host TEXT,                      -- host only, no path or query
  is_bot      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX click_link_id_clicked_at_idx ON click (link_id, clicked_at);
```

**Privacy:** do not store IP addresses or full user agents. Referrer is host only. Bot detection is a
simple user-agent substring check and sets `is_bot`; bot clicks are stored but excluded from the
headline counts.

## Routes

### Public

- `GET /{slug}`: look up the slug and answer **302** to `target_url`. Use 302 (not 301) so targets can be
  changed later without browsers caching them. Unknown slug: a short plain 404 page. Record the click
  asynchronously (see below). `HEAD` requests redirect but are not counted.
- `GET /healthz`: `200 ok`, used by Docker and monitoring. Does a trivial DB query.
- `GET /`: redirect to `HOME_REDIRECT_URL` if set, otherwise 404.
- `GET /robots.txt`: `Disallow: /admin` and `Disallow: /api`.

### Admin UI (server-rendered HTML, no JavaScript framework)

- `GET /admin/login`, `POST /admin/login`, `POST /admin/logout`.
- `GET /admin`: table of links (slug, short URL, target, note, created, total clicks, clicks last 7 and
  30 days) and an **add link** form (target URL, optional slug, optional note).
- `POST /admin/links`: create a link from the form, then redirect back with a success message that shows
  the new short URL.
- `GET /admin/links/{id}`: stats for one link: totals, a clicks-per-day table for the last 30 days, and
  top referrer hosts. A table is enough; no charting library.
- Auth: signed, `HttpOnly`, `Secure`, `SameSite=Lax` session cookie with an expiry (default 12 h). All
  state-changing forms include a CSRF token. Rate-limit login attempts (in memory, per client IP, e.g.
  5 failures per 15 minutes) and log failures without logging the password.
- Templates use `html/template` (auto-escaping) embedded with `embed`. Send `X-Robots-Tag: noindex` on
  all `/admin` pages. Keep the CSS small, accessible and readable.

### JSON API (`Authorization: Bearer <API_KEY>`)

- `POST /api/links`

  Request: `{"url": "https://...", "slug": "optional-slug", "note": "optional"}`

  Response `201`:
  ```json
  {
    "id": "0b5f3c1e-...",
    "slug": "hello",
    "short_url": "https://go.richardmcquiston.com/hello",
    "target_url": "https://example.com/some/long/path",
    "note": null,
    "created_at": "2026-10-20T12:00:00Z"
  }
  ```
  If `slug` is omitted, generate one (see Slugs). If the same `url` already exists and no `slug` was
  sent, return the existing link with `200` instead of creating a duplicate.

- `GET /api/links`: list links with total click counts. Optional `?limit=` and `?offset=`.
- `GET /api/links/{slug}`: one link with its stats summary.

Error body for every failure, with a matching HTTP status:

```json
{"error": "slug_taken", "message": "slug \"hello\" is already used by another link"}
```

Use stable machine-readable `error` codes: `unauthorized`, `invalid_url`, `invalid_slug`,
`slug_reserved`, `slug_taken`, `not_found`, `rate_limited`, `internal_error`. Never leak internals in
`message` for `internal_error`; log the detail server-side instead.

## Slugs and URLs

- Custom slug rules: 1 to 64 characters from `[A-Za-z0-9_-]`, case-sensitive.
- Reserved (rejected): `admin`, `api`, `healthz`, `robots.txt`, `favicon.ico`, `static`.
- Generated slugs: 6 characters from an alphabet without look-alikes (no `0 O 1 l I`), from
  `crypto/rand`. Retry on collision (bounded), then fail with `internal_error`.
- Target URL rules: parse with `net/url`; scheme must be `http` or `https`; host required; no userinfo
  (`user:pass@`); max 2048 characters; reject a target whose host equals the host of `BASE_URL` (no
  redirect loops).

## Click recording

Never slow or break a redirect because of statistics. The handler sends a small click record to a
buffered channel; one background goroutine batches inserts every second or every N records. If the
channel is full, drop the record and increment a logged counter. On shutdown, flush before exiting.

## HTTP server hardening

- `http.Server` with `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`; graceful shutdown
  on SIGTERM.
- Limit request bodies (`http.MaxBytesReader`, for example 16 KB).
- Security headers on HTML pages: `Content-Security-Policy` (no inline script), `X-Content-Type-Options`,
  `Referrer-Policy`, `X-Frame-Options`.
- Structured logging with `log/slog` (JSON in production). Log request method, path **without** query
  strings that may hold secrets, status and duration. Never log `Authorization` headers or cookies.

## Suggested layout

```text
cmd/server/main.go          # wiring: config, db, routes, graceful shutdown
internal/config/            # env loading and validation
internal/store/             # SQLite access: links, clicks, stats; migrations
internal/migrations/        # embedded .sql files
internal/links/             # slug generation, URL and slug validation
internal/httpapi/           # JSON API handlers and errors
internal/admin/             # admin handlers, templates, session and CSRF
internal/redirect/          # public redirect handler and click recorder
web/templates/, web/static/ # embedded HTML templates and small CSS
Dockerfile, docker-compose.yml, .env.example, Makefile, README.md
```

## Deploy (Hostinger VPS)

- **Dockerfile:** multi-stage; build with `CGO_ENABLED=0`; run as a non-root user from a minimal image;
  `HEALTHCHECK` against `/healthz`.
- **docker-compose.yml:** one service; `env_file: .env`; volume `./data:/data` for the SQLite file;
  `expose: 8080` (do not publish a host port). Join the shared external Docker network that Caddy
  already uses on this VPS (currently named `hired-hand-net`; confirm the name on the server before
  relying on it).
- **Caddy:** the existing Caddy instance is defined in the `html2lvgl-platform` repo's `Caddyfile`
  (owned by that repo). Add a block there, in a PR into that repo's `dev`:

  ```text
  go.richardmcquiston.com {
      reverse_proxy shortlinks:8080
  }
  ```

  Also point a DNS A record for `go.richardmcquiston.com` at the VPS. Set `TRUST_PROXY=true`.
- **Backups:** a nightly `VACUUM INTO '/data/backups/links-YYYY-MM-DD.db'` (or the `sqlite3 .backup`
  command), keeping the last 14. Document restore steps in `README.md`.
- **Changing domains later:** because only slugs are stored, switching to `mcqlinks.com` means changing
  `BASE_URL` and adding a Caddy block. Both hosts can serve the same links during a transition.

## Testing

- Table-driven tests for slug and URL validation, slug generation, config validation and the API error
  mapping.
- `httptest` tests for each route: redirect (302 and 404), admin login success and failure, CSRF
  rejection, API auth (missing, wrong and correct key), duplicate handling.
- Store tests use a temporary SQLite file (not mocks) so migrations and queries are really exercised.
- CI (GitHub Actions): `gofmt -l` must be empty, `go vet ./...`, `go test ./...`, and a Docker build.

## Build order

1. Repo scaffold, config loading and validation, SQLite open and migrations, `/healthz`.
2. `link` store, slug and URL validation, `GET /{slug}` redirect.
3. JSON API (`POST`, `GET` list, `GET` one) with API-key auth and the error shape.
4. Click recording and the stats queries.
5. Admin UI: login, dashboard with add form, per-link stats.
6. Dockerfile, compose file, `.env.example`, README with deploy and backup steps, CI.
7. Deploy to the VPS and verify with real links.

## Notes for Claude

- Ask before adding a dependency beyond the driver (and a UUID helper if you do not write one).
- Do not add features from the non-goals list unprompted.
- When something is ambiguous (for example the exact Docker network name on the VPS), say what you
  assumed and where it is configured, rather than guessing silently.
