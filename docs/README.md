# Client Directory: documentation

The client directory is the Go service that knows **who** people are and
**where** documents live: peers (users), rooms, room membership and roles,
document references, invitations, bans and an audit log per room. It never
stores document content; that belongs to the collaboration server and the
editor clients (see [roadmap.md](roadmap.md)).

| Document | What it covers |
|---|---|
| [architecture.md](architecture.md) | Low-level design: layers, request lifecycle, transactions and locking, authorization, errors, audit events |
| [data-model.md](data-model.md) | Every model and table, constraints, relationships, the migration history |
| [api.md](api.md) | Every HTTP endpoint: who may call it, request and response, status codes |
| [flows.md](flows.md) | Sequence diagrams: sign-in, invites, public join, bans |
| [decisions.md](decisions.md) | Decision log: what was decided, why, and what is still open |
| [operations.md](operations.md) | Configuration, migrations, Docker, CI, running the tests |
| [roadmap.md](roadmap.md) | What is left to build, and known issues |

## Quick start

```bash
cp config.example.json config.json    # then fill in auth.jwtSecret and the Google credentials
docker compose up --build -d          # Postgres + the app; migrations run on startup
curl http://localhost:8080/healthz    # -> ok
```

The app reads `config.json`, which is git-ignored because it holds secrets;
[`config.example.json`](../config.example.json) is the committed template.
Environment variables override individual keys (see
[operations.md](operations.md#configuration)). A JWT secret and Google OAuth
credentials are required for the server to start.

```bash
go test ./...                                               # unit tests
TEST_DATABASE_URL='postgres://postgres:secretpassword@localhost:5432/dirtest?sslmode=disable' \
  go test ./...                                             # + Postgres and API tests (empties every table)
```

## Code map

```
internal/
  main/          entry point and config loading
  router/        route table and route groups
  middleware/    request IDs, logging, panic recovery, CORS, authentication
  handler/       HTTP <-> service translation (one file per resource)
  auth/          JWT session manager
  oauth/         Google OAuth adapter
  service/       business rules, repository interfaces, errors
  repository/    PostgreSQL implementations of the repository interfaces
  model/         the types the API returns
  service_test/  unit tests against an in-memory store
  repostiory_test/  Postgres integration and end-to-end API tests
db/migrations/   numbered SQL migrations
scripts/         migrate.sh, docker-entrypoint.sh
```
