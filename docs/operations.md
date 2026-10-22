# Operations

## Configuration

Loaded by `internal/main/config.go` in increasing precedence: built-in
defaults, then the JSON file at `$CONFIG_FILE` (default `./config.json`; a
missing file is allowed), then environment variables. Unknown keys in the file
are an error, so typos fail at startup (key matching is case-insensitive).

| `config.json` key | Env override | Default | Used by |
|---|---|---|---|
| `server.port` | `PORT` | `8080` | server |
| `server.allowedOrigins` | `ALLOWED_ORIGINS` (comma-separated) | `["http://localhost:3000"]` | CORS |
| `database.url` | `DATABASE_URL` | local Postgres | server, migrate.sh |
| `database.maxOpenConns` / `maxIdleConns` / `connMaxLifetime` | — | 25 / 5 / `5m` | connection pool |
| `migrations.schemaDir` | `SCHEMA_DIR` | `db/migrations` (relative to the config file) | migrate.sh |
| `migrations.waitSeconds` | `DB_WAIT_SECONDS` | 60 | migrate.sh |
| `migrations.skip` | `SKIP_MIGRATIONS` (`1`/`true`) | false | migrate.sh |
| `auth.jwtSecret` | `JWT_SECRET` | dev placeholder when the key is absent | sessions (**required**: an empty value stops startup, because an empty key would make tokens forgeable) |
| `auth.jwtDuration` | — | `24h` | sessions |
| `auth.cookieSecure` | — | false (set true behind HTTPS) | cookies |
| `auth.postLoginRedirect` | `POST_LOGIN_REDIRECT` | `http://localhost:3000/` | OAuth callback |
| `invites.linkBaseUrl` | `INVITE_LINK_BASE_URL` | `http://localhost:3000/invite/` | invite links |
| `oauth.google.clientId` / `clientSecret` | `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | empty | OAuth (**required**: the server will not start without a provider) |
| `oauth.google.redirectUrl` | `OAUTH_REDIRECT_URL` | `http://localhost:8080/auth/google/callback` | OAuth |

### Secrets and the two config files

| File | In git | Contents |
|---|---|---|
| `config.example.json` | yes | The full structure with `jwtSecret`, `clientId` and `clientSecret` blank. Copied into the Docker image; CI copies it to `config.json` |
| `config.json` | no (git-ignored) | Your real settings and secrets. Mounted over the image's copy by Docker Compose |

Set up a checkout with `cp config.example.json config.json` and fill in the
secrets, or leave them blank there and supply `JWT_SECRET`,
`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` from the environment or `.env`
(also git-ignored; read by Docker Compose). When you add a key, add it to both
files: `TestExampleConfig` loads the template, and unknown keys are rejected.

## Migrations

- Files: `db/migrations/NNNN_description.sql`, applied in version order.
- Runner: `scripts/migrate.sh` (psql + jq). It waits for the database, takes
  advisory lock `4242001`, creates `schema_migrations` if needed, and applies
  every file whose version is not recorded, all in **one transaction**.
- Consequence: a migration must not use statements that cannot run in a
  transaction (`CREATE INDEX CONCURRENTLY`, `ALTER TYPE … ADD VALUE` on old
  Postgres versions).
- **Never edit an applied migration.** Add a new file.
- The test helper `applyMigrations` in `repostiory_test/store_test.go` mirrors
  the script.

```bash
# run by hand against any database
DATABASE_URL='postgres://…' sh scripts/migrate.sh
```

## Docker

- `Dockerfile`: multi-stage; Go build, then Alpine with `postgresql-client` and
  `jq`, non-root user, `CONFIG_FILE=/app/config.json`. The image contains
  `config.example.json` (as `/app/config.json`), never the real file, so it
  needs secrets from a mount or the environment to start.
- `scripts/docker-entrypoint.sh`: runs `migrate.sh`, then `exec`s the server so
  it is PID 1 and receives SIGTERM (10 s graceful shutdown).
- `docker-compose.yml`: Postgres 16 with a health check, and the app, which
  waits for it, mounts `config.json` read-only, and overrides `DATABASE_URL`
  (the host is `postgres` inside the compose network) and the secrets from
  `.env`.

```bash
docker compose up --build -d
docker compose logs -f app
docker compose down        # add -v to delete the database volume
```

## CI (`.github/workflows/ci.yml`)

1. **test**: Postgres service; copy `config.example.json` to `config.json`
   (its database URL matches the service); `migrate.sh` twice (must be a no-op
   the second time); `go vet ./...`; `go test ./...` with `TEST_DATABASE_URL`.
2. **docker**: build the image; boot it against a fresh Postgres with
   placeholder `JWT_SECRET` and Google credentials; require the migration and
   "Starting server" log lines, all tables present, and one
   `schema_migrations` row per migration file.

## Tests

| Command | Runs |
|---|---|
| `go test ./...` | unit tests (Postgres suites skip) |
| `TEST_DATABASE_URL=… go test ./...` | everything, including Postgres and API tests. **Empties all tables** in that database |
| `GOOGLE_OAUTH_LIVE_TEST=1 go test ./internal/oauth -run Live -v` | checks the configured Google credentials against Google (no sign-in needed) |
