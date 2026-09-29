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
- Deploy as a container (Docker Compose), natively as a hardened systemd
  service (`make install`), or as a proper `.deb` (`make deb`) — your
  choice.
- `Makefile` cross-compiles to any Go-supported OS/architecture with no
  extra toolchain (`make build GOOS=linux GOARCH=arm64`), and
  `make dist` cuts release archives for common self-hosting targets
  (amd64, arm64, armv7) in one shot.

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
| `DB_MAX_READ_CONNS` | `10` | Size of the concurrent-read connection pool (see "On concurrency" below) |
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

## Running as a systemd service (no Docker)

If you'd rather run the plain binary on a Linux box than a container,
`make install` builds it, installs it, and wires up a hardened systemd
unit in one go. Run this **on the target server**, not on your dev
machine:

```sh
git clone https://github.com/SecureCMD/cobiserver.git
cd cobiserver
sudo make install
```

That:

- builds `koserver` for the host's architecture and installs it to
  `/usr/local/bin/koserver`;
- creates a dedicated, unprivileged `koserver` system user/group;
- creates `/var/lib/koserver` (the default `DATA_DIR`, owned by that user)
  and `/etc/koserver/koserver.env` (from
  `packaging/systemd/koserver.env.example`, only if it doesn't already
  exist — re-running `make install` never clobbers your config);
- installs `packaging/systemd/koserver.service` to
  `/etc/systemd/system/` and runs `systemctl daemon-reload`.

Then:

```sh
sudo $EDITOR /etc/koserver/koserver.env   # BASE_URL, ALLOW_SIGNUP, SMTP_*, ...
sudo systemctl enable --now koserver
journalctl -u koserver -f
```

The unit (`packaging/systemd/koserver.service`) sandboxes the process
fairly aggressively (`ProtectSystem=strict`, no capabilities, private
`/tmp`, restricted syscalls, etc.) — it can only read/write
`/var/lib/koserver`. If you point `LISTEN_ADDR` at a privileged port
(<1024) instead of putting a reverse proxy in front, you'll need to grant
`CAP_NET_BIND_SERVICE`; see the comment in that file.

### Or: as a .deb package

For Debian/Ubuntu hosts, `debian/` is a standard debhelper (compat 13)
packaging directory — build it and install with `dpkg`/`apt` instead of
`make install`, and it does the same setup (dedicated system user via
`systemd-sysusers`, `/var/lib/koserver` via `systemd-tmpfiles`, the
systemd unit) declaratively, tracked by dpkg so `apt remove`/`purge`
clean up properly:

```sh
sudo apt install build-essential debhelper devscripts dpkg-dev golang-go git
make deb                       # → ../koserver_<version>_<arch>.deb
sudo apt install ../koserver_*.deb
```

Note: Debian 12 (bookworm)'s `golang-go` is 1.19, older than this
project's `go 1.23` requirement, and Go's automatic-toolchain-download
(`GOTOOLCHAIN=auto`) only exists from Go 1.21 onward — so a stock
bookworm `golang-go` can neither build this nor bootstrap a newer
toolchain itself. Install Go 1.23+ some other way first (backports,
Debian trixie, or the upstream tarball from go.dev/dl) and it works fine
either as `golang-go` or just a `go` on `$PATH`; `dpkg-buildpackage -d`
skips the apt-dependency version check if you've done the latter.

`apt remove koserver` keeps your config and data. `apt purge koserver`
also deletes `/etc/koserver/koserver.env` (it's a normal dpkg conffile)
but, deliberately, still leaves `/var/lib/koserver` (your database) and
the `koserver` system user alone — see `debian/README.Debian`.

After editing the unit file directly, `sudo make systemd-reload` reloads
and restarts it. `sudo make uninstall` removes the service and binary
(your data in `/var/lib/koserver` and config in `/etc/koserver` are left
alone — delete those yourself if you want a clean slate).

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
koserver version
```

Through Docker Compose, prefix with `docker compose exec koserver`; under
systemd it's just `koserver <command>` (it's on `$PATH`).

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

Requires Go 1.23+; no CGO, no external services needed (the SQLite driver
is pure Go), which also means cross-compiling for any target just works —
there's no C toolchain to cross-install.

Dependencies are vendored (`vendor/`, committed to the repo, kept in
sync with `go.mod`/`go.sum` via `go mod vendor`), and every build path
here (`make build`/`dist`, the Dockerfile, `debian/rules`) builds with
`-mod=vendor` — so none of them ever touch the network. Changed a
dependency? Run `go mod tidy && go mod vendor` and commit the result.

```sh
make help          # list every target
make build          # build for the host's OS/arch, into bin/koserver
make test           # go test ./...
make lint           # gofmt -l + go vet
```

### Cross-compiling for a specific architecture

```sh
make build GOOS=linux GOARCH=arm64          # e.g. a Raspberry Pi (64-bit) or AWS Graviton
make build GOOS=linux GOARCH=arm GOARM=7    # 32-bit Raspberry Pi
make build GOOS=linux GOARCH=amd64          # a regular x86_64 server
```

`GOOS`/`GOARCH` (and `GOARM` for 32-bit ARM) are any pair Go itself
supports — run `go tool dist list` for the full list.

### Building release archives for every supported platform at once

```sh
make dist    # or `make release`
```

Cross-compiles `linux/amd64`, `linux/arm64`, `linux/arm/v7` and
`darwin/arm64` into `dist/*.tar.gz`, plus `dist/SHA256SUMS`. Edit
`DIST_PLATFORMS` in the `Makefile` to add or drop targets.

### Docker

```sh
make docker-build              # single image for the host's architecture
make docker-buildx PUSH=1      # multi-arch (linux/amd64+linux/arm64) via buildx, pushed to $(IMAGE)
```

`docker-buildx` needs the `buildx` plugin (bundled with recent Docker
Desktop; on plain `docker` installs you may need `docker buildx install`
or the `docker-buildx-plugin` package) and, to push, that you're logged in
(`docker login`) and `IMAGE` set to a registry you can write to.

## On concurrency (SQLite)

Storage is SQLite in WAL mode, accessed through two separate connection
pools: a single connection for all writes, and a small pool (`DB_MAX_READ_CONNS`,
default 10) for reads. SQLite only ever allows one writer at a time
regardless of pooling, so funneling writes through one connection avoids
`SQLITE_BUSY` outright instead of retrying on lock contention, while WAL
lets reads run fully concurrently with that writer. Each sync request is a
single-row upsert or lookup, so this comfortably handles concurrent syncing
from hundreds of users on a single small VM.

This will not scale past a single process/host (no read replicas, no
multi-writer), and a SQLite file on a networked/clustered filesystem is
unsafe — keep `$DATA_DIR` on local disk. If you outgrow this (running
multiple replicas, or genuinely high write volume from many simultaneous
users), swapping `internal/store` for Postgres is the natural next step;
the package already isolates all SQL behind `database/sql`, so it's a
driver + query-syntax change, not a rewrite.

## Data & backups

Everything lives in one SQLite file (`$DATA_DIR/koserver.db`, plus its
`-wal`/`-shm` companions while the server is running). Back up the whole
`koserver-data` volume (or `$DATA_DIR`) — stop the container first, or use
`sqlite3 koserver.db ".backup backup.db"` for a live-safe copy.
