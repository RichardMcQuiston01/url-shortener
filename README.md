# URL Shortener Service

- Author: [Richard McQuiston](mailto:richard.mcquiston@gmail.com)
- Website: https://richardmcquiston.com/

## Table of Contents

| [Overview](#overview) | [Getting Started](#getting-started) | [Configuration](#configuration) | [Deploy](#deploy-with-docker) | [Backups](#backups-and-restore) | [Development](#development) | [Buy Me a Coffee](#buy-me-a-coffee) | [License](#license) | [Copyright](#copyright) |

## Overview

A small, self-hosted URL shortener written in Go with SQLite. It gives short links such as
`https://go.richardmcquiston.com/hello` that redirect to long URLs (Chrome extension store pages,
VS Code Marketplace pages, project sites, and so on).

[Back to Top](#table-of-contents)

## Getting Started

### Prerequisites

- **To run locally:** Go 1.26 or newer.
- **To deploy:** Docker with the Compose plugin, a domain pointing at the server, and a reverse proxy that
  terminates HTTPS (this project is set up for an existing Caddy).

### Installation

```bash
git clone https://github.com/RichardMcQuiston01/url-shortener.git
cd url-shortener

cp .env.example .env        # then fill in BASE_URL, ADMIN_PASSWORD, API_KEY and SESSION_SECRET
set -a && . ./.env && set +a
export DATABASE_PATH=./data/links.db PORT=8080

go run ./cmd/server
```

The server refuses to start if a required setting is missing or too short, and the message names the
setting (never its value). The database file and its folder are created on first start.

### Usage

- **Redirects:** `GET /{slug}` answers `302` to the target. `GET /` goes to `HOME_REDIRECT_URL` if set.
- **Admin page:** open `/admin` and sign in with `ADMIN_PASSWORD`. The dashboard lists every link with
  click counts and has a form to add a link. Click a slug for its per-day stats and top referrers.
  Sessions last 12 hours; five failed sign-ins from one address in 15 minutes locks that address out.
- **JSON API:** send `Authorization: Bearer <API_KEY>` to `/api/*`.

  | Method and path                 | What it does                                                                                                       |
  | ------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
  | `POST /api/links`               | Create a link. `201` with the new link, or `200` with the existing one when the same URL is posted without a slug. |
  | `GET /api/links?limit=&offset=` | List links, newest first (default 50, maximum 200), with click counts.                                             |
  | `GET /api/links/{slug}`         | One link with its click counts.                                                                                    |

  Every error has the same shape, `{"error": "<code>", "message": "<what went wrong>"}`. The codes are
  `unauthorized`, `invalid_request`, `invalid_url`, `invalid_slug`, `slug_reserved`, `slug_taken`,
  `not_found`, `rate_limited` and `internal_error`.

  Slugs are 1 to 64 letters, digits, hyphens or underscores and are case-sensitive. These are reserved:
  `admin`, `api`, `healthz`, `robots.txt`, `favicon.ico`, `static`. Without a slug, a random 6-character one
  is generated.

### Examples

```bash
export API_KEY=...   # the value from your .env

# Create a link with a custom slug
curl -s -H "Authorization: Bearer $API_KEY" \
  -d '{"url": "https://example.com/some/long/path", "slug": "hello", "note": "demo"}' \
  http://localhost:8080/api/links
# {"id":"0b5f3c1e-...","slug":"hello","short_url":"https://go.example.com/hello",
#  "target_url":"https://example.com/some/long/path","note":"demo","created_at":"2026-10-21T12:00:00Z"}

# Let the server pick the slug
curl -s -H "Authorization: Bearer $API_KEY" -d '{"url": "https://example.com/other"}' http://localhost:8080/api/links

# List, or fetch one
curl -s -H "Authorization: Bearer $API_KEY" "http://localhost:8080/api/links?limit=10"
curl -s -H "Authorization: Bearer $API_KEY" http://localhost:8080/api/links/hello

# Follow it
curl -i http://localhost:8080/hello
```

What gets counted: one click per `GET` redirect. `HEAD` requests, unknown slugs and anything that looks
like a bot or link checker are not counted in the totals. Only the date, the referrer _host_ and a bot flag
are stored: no IP addresses and no user agents.

[Back to Top](#table-of-contents)

## Configuration

Settings are environment variables. In Docker they come from the `.env` file (`env_file`). `.env` is
git-ignored; `.env.example` shows the shape.

| Variable            | Required | Purpose                                                                                                                                                                           |
| ------------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `BASE_URL`          | yes      | Public base URL, for example `https://go.richardmcquiston.com` (no trailing slash). Used to build `short_url`. Only slugs are stored, so changing it never breaks existing links. |
| `ADMIN_PASSWORD`    | yes      | Password for the admin page. At least 16 characters.                                                                                                                              |
| `API_KEY`           | yes      | Bearer key for `/api/*`. At least 32 characters.                                                                                                                                  |
| `SESSION_SECRET`    | yes      | Signs admin session cookies. At least 32 characters. Changing it signs everyone out.                                                                                              |
| `DATABASE_PATH`     | no       | SQLite file. Default `/data/links.db`. Backups go in a `backups` folder next to it.                                                                                               |
| `PORT`              | no       | Listen port. Default `8080`.                                                                                                                                                      |
| `HOME_REDIRECT_URL` | no       | Where `GET /` redirects. Empty means a plain 404.                                                                                                                                 |
| `TRUST_PROXY`       | no       | `true` behind a reverse proxy, so the client address comes from `X-Forwarded-For`. Default `false`.                                                                               |
| `PROXY_NETWORK`     | no       | Docker Compose only: the name of the network the reverse proxy uses. Default `hired-hand-net`.                                                                                    |

Generate secrets with `openssl rand -base64 24` (password) and `openssl rand -base64 48` (the keys).
The admin cookie is `Secure`, so sign in over HTTPS or on `localhost`.

[Back to Top](#table-of-contents)

## Deploy with Docker

One container and one data folder. The container publishes no host port: the reverse proxy reaches it by name
(`shortlinks:8080`) over a shared Docker network.

1. **DNS.** Point an `A` record for the domain (for example `go.richardmcquiston.com`) at the server.
2. **Get the code** on the server and enter the folder:
   ```bash
   git clone https://github.com/RichardMcQuiston01/url-shortener.git && cd url-shortener
   ```
3. **Configure.**
   ```bash
   cp .env.example .env
   nano .env            # fill in the secrets; set TRUST_PROXY=true
   ```
4. **Find the proxy's network name.** Compose must join the network your reverse proxy already uses:
   ```bash
   docker network ls
   docker inspect <caddy-container-name> --format '{{json .NetworkSettings.Networks}}'
   ```
   Put that name in `.env` as `PROXY_NETWORK`.
5. **Create the data folder.** The container runs as a non-root user (uid 65532), which must own it:
   ```bash
   mkdir -p data && sudo chown 65532:65532 data
   ```
6. **Start it.**
   ```bash
   docker compose up -d --build
   docker compose ps          # STATUS should become "healthy"
   docker compose logs -f
   ```
7. **Route the domain to it.** Add a block to the Caddyfile that serves the other sites on this server, then
   reload Caddy:
   ```text
   go.richardmcquiston.com {
       reverse_proxy shortlinks:8080
   }
   ```
8. **Check.** `curl https://go.richardmcquiston.com/healthz` prints `ok`. Then sign in at `/admin`.

**Update:** `git pull && docker compose up -d --build`. The database lives in `./data`, so it survives.

**Change the domain** (for example to `mcqlinks.com`): set the new `BASE_URL`, add a Caddy block for it, and
`docker compose up -d`. Both domains can serve the same links while you switch over.

[Back to Top](#table-of-contents)

## Backups and restore

The app backs itself up; there is no cron job or extra container.

- **When:** every night at 03:00 UTC, and at startup if there is no backup or the newest is over 24 hours old.
- **Where:** `data/backups/links-YYYY-MM-DD.db` (a consistent copy made with SQLite's `VACUUM INTO`).
- **How many:** the newest 14. Older ones are deleted.
- **Failures** are written to the log (`docker compose logs`) and retried the next night.

These backups sit on the same disk as the database, so also copy them somewhere else now and then, for
example `rsync -a server:/path/to/url-shortener/data/backups/ ~/shortlinks-backups/`.

**Restore:**

```bash
docker compose down
mv data/links.db data/links.db.broken            # keep the old file until you are sure
rm -f data/links.db-wal data/links.db-shm        # stale write-ahead files must not be mixed in
cp data/backups/links-2026-10-21.db data/links.db  # pick the backup you want
sudo chown 65532:65532 data/links.db
docker compose up -d
docker compose ps                                  # wait for "healthy", then open /admin
```

Clicks recorded after the backup was taken are lost; the links themselves are restored. A backup is an ordinary
database file, so you can also open one with any SQLite tool to inspect it first.

[Back to Top](#table-of-contents)

## Development

```bash
gofmt -l .          # must print nothing
go vet ./...
go test -race ./...
```

CI runs the same checks and then builds the Docker image, starts it and waits for its health check.

Code layout: `cmd/server` (wiring and graceful shutdown), `internal/config`, `internal/store` (SQLite and
migrations in `internal/migrations`), `internal/links` (validation and link creation), `internal/redirect`
(public routes and click recording), `internal/httpapi` (JSON API), `internal/admin` (admin pages, with
templates and CSS in `web/`), `internal/backup`.

Work happens on `feature/*` branches with draft pull requests into `dev`. `main` is production; `dev` is
promoted to `main` only when the owner asks. See [ROADMAP.md](./ROADMAP.md) for the plan and
[CHANGELOG.md](./CHANGELOG.md) for what has shipped.

[Back to Top](#table-of-contents)

## Buy Me a Coffee

If this app, code, or repository has helped you or someone you know, please consider donating. I appreciate any help to offset the costs of development and/or AI Credits.

[**Donate via Stripe**](https://donate.stripe.com/00w5kD3Gj1Xo9v7gVOcs800), or scan:

[![Donate via Stripe](./donate.svg)](https://donate.stripe.com/00w5kD3Gj1Xo9v7gVOcs800)

[Back to Top](#table-of-contents)

## License

Apache 2

[Back to Top](#table-of-contents)

## Copyright

(c)2026 Richard McQuiston. All rights reserved.

[Back to Top](#table-of-contents)
