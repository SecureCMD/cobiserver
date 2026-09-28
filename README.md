# cobiserver — self-hosted KOReader sync server

A drop-in replacement for the official KOReader sync server
(`sync.koreader.rocks`), written in Go. It implements the exact wire
protocol the KOReader app speaks (`/users/create`, `/users/auth`,
`/syncs/progress`), so pointing the app at your own server is a matter of
changing the custom sync server URL in KOReader's settings — nothing else
about the app's behavior changes.

On top of that it adds what the stock protocol has no room for: a small web
UI so a user can register, change their password, or recover a forgotten
one without an admin having to touch the database.

## Features

- Full KOReader sync API compatibility: register, auth, push/pull reading
  progress, per-device tracking.
- Self-service account web UI at `/account`: change password, set a
  recovery email, view synced documents.
- Password recovery: emailed reset link (if SMTP is configured) or a
  server-side `gen-reset-link` command the admin can hand to the user.
- Admin panel at `/admin/users`: create/delete users, force a password
  reset, without shell access. The first account ever registered
  automatically becomes admin.
- Passwords are never stored in a form usable to log in directly: the
  server stores `bcrypt(MD5(password))`, matching what KOReader itself
  sends over the wire while still protecting against a leaked database.
- Built-in rate limiting on auth/login/register/forgot-password.
- Single static binary, pure-Go SQLite (no CGO), ships as a ~20MB Docker
  image, runs as a non-root user.
- Admin CLI baked into the same binary (`koserver create-user`, `set-password`,
  `list-users`, `delete-user`, `make-admin`, `gen-reset-link`).

## Quick start (Docker Compose)

```sh
docker compose up -d --build
```

This builds the image, starts the server on `http://localhost:8080`, and
persists data in the `koserver-data` named volume (SQLite database at
`/data/koserver.db`).

Then either:

- In the KOReader app: **Settings → Sync → tap the "…" server field → enter
  `http://<your-host>:8080`**, then tap **Register** and enter a
  username/password. `ALLOW_SIGNUP=true` (the default) lets the app create
  the account directly.
- Or from the web UI: open `http://<your-host>:8080/account/register`.

The very first account created (via either path) is automatically made an
admin, with access to `/admin/users`.

### Configuration

All configuration is via environment variables (see `docker-compose.yml`
and `.env.example`):

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Address/port the HTTP server binds to |
| `DATA_DIR` | `/data` | Directory for the SQLite database |
| `DB_PATH` | `$DATA_DIR/koserver.db` | Full DB file path override |
| `BASE_URL` | `http://localhost:8080` | Public URL, used to build reset links |
| `ALLOW_SIGNUP` | `true` | Allow new accounts via the app or `/account/register` |
| `COOKIE_SECURE` | `false` | Set `true` once served over HTTPS |
| `TRUST_PROXY_HEADERS` | `false` | Set `true` only behind a trusted reverse proxy (uses `X-Forwarded-For` for rate limiting) |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` / `SMTP_FROM` / `SMTP_STARTTLS` | unset | Optional, enables emailed password-reset links |

If you disable self-registration (`ALLOW_SIGNUP=false`), create the first
admin from the CLI instead (see below) before starting to rely on the web
UI.

### Running behind HTTPS

This server itself only speaks plain HTTP; put a reverse proxy (Caddy,
Traefik, nginx) in front of it for TLS, then set `COOKIE_SECURE=true` and,
if the proxy sets `X-Forwarded-For`, `TRUST_PROXY_HEADERS=true`. The
KOReader app itself is fine talking to a plain-HTTP server on your LAN if
you don't want to bother with TLS, but any web UI login without HTTPS
sends the password in the clear over the network.

## Recovering / changing a password

Three ways, from easiest to "admin has to help":

1. **Self-service, knows current password**: `/account/change-password`
   while logged in.
2. **Self-service, forgot password**: `/account/forgot-password`. If the
   account has a recovery email set and SMTP is configured, a reset link
   is emailed. Otherwise the server logs the reset link to stdout
   (`docker compose logs koserver`) for the admin to relay manually.
3. **Admin-initiated**: from `/admin/users`, click "Reset password" next
   to a user to generate a reset link, or from the CLI:

   ```sh
   docker compose exec koserver koserver gen-reset-link -username alice
   # or set a password directly, no link needed:
   docker compose exec koserver koserver set-password -username alice -password newpass123
   ```

Changing a password anywhere (web UI, admin panel, or CLI) immediately
takes effect for the KOReader app too — the app itself doesn't need
"resetting", just re-enter the new password in its sync settings.

## Admin CLI reference

The same binary is both the server and its own admin tool:

```sh
koserver serve                                  # run the HTTP server (default if no args)
koserver create-user -username U -password P [-email E] [-admin]
koserver set-password -username U -password P
koserver list-users
koserver delete-user -username U
koserver make-admin -username U
koserver revoke-admin -username U
koserver gen-reset-link -username U
```

Through Docker Compose, prefix with `docker compose exec koserver`.

## API compatibility

Implements the protocol from `plugins/kosync.koplugin/api.json` in the
KOReader source:

| Method | Path | Notes |
|---|---|---|
| `POST` | `/users/create` | `{username, password}` where `password` is the client's `MD5(password)`. Returns `201` or `402` if taken. |
| `GET` | `/users/auth` | Auth via `X-Auth-User` / `X-Auth-Key` headers. `200` or `401`. |
| `PUT` | `/syncs/progress` | Upserts reading position per document/user. |
| `GET` | `/syncs/progress/{document}` | Returns last known position, or an empty body if none yet. |
| `GET` | `/healthcheck` | Plain liveness probe (also used by the Docker `HEALTHCHECK`). |

## Building from source

Requires Go 1.23+; no CGO, no external services needed besides SQLite
which is pure Go here.

```sh
go build -o koserver ./cmd/koserver
```

Or just build the Docker image directly with `docker build -t koserver .`.

## Data & backups

Everything lives in one SQLite file (`$DATA_DIR/koserver.db`, plus its
`-wal`/`-shm` companions while the server is running). Back up the whole
`koserver-data` volume (or `$DATA_DIR`) — stop the container first, or use
`sqlite3 koserver.db ".backup backup.db"` for a live-safe copy.
