# Changelog

All notable changes are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Redirects: `GET /{slug}` answers `302`, unknown slugs get a short plain 404, `GET /` redirects to
  `HOME_REDIRECT_URL` when set, `GET /robots.txt`, and `GET /healthz` (which checks the database).
- JSON API under `/api/links` (create, list, get one) behind a bearer API key, with a stable error shape and
  error codes. Posting a URL that is already shortened returns the existing link.
- Click statistics: clicks are queued without ever slowing a redirect and written in batches. Bots and `HEAD`
  requests are not counted. Only the referrer host and a bot flag are stored.
- Admin pages: sign-in with signed `HttpOnly`/`Secure` session cookies, CSRF protection and a login rate
  limit; a dashboard with click counts and an add-link form; per-link stats with a 30-day table and top
  referrers.
- Nightly in-app database backups (03:00 UTC, newest 14 kept) written to `data/backups`.
- Docker: a static, non-root image with a built-in health check; `docker-compose.yml` for the shared
  reverse-proxy network; `.env.example`.
- GitHub Actions: format, vet and test, plus a Docker build and smoke test.
- README with run, API, deploy, backup and restore instructions.
- `openapi.json`: an OpenAPI 3.1 description of the JSON API for generating TypeScript types, kept in sync with
  the code by a contract test.

### Fixed

- Removed a stray `pid` file that had been committed by accident, and ignored runtime files.
