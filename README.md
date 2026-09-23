# Torii

A distributed event/reservation booking platform, fronted by a self-built
API gateway and control plane, written in Go. Learning project — see
[CLAUDE.md](CLAUDE.md) for the full architecture, phase plan, and
conventions.

Currently at **Phase 0 (scaffolding) + Phase 1 (MVP)**: single gateway
instance, static routing to the booking service, basic JWT auth on writes,
and a Postgres-backed booking service with row-level locking to prevent
overbooking.

## Running locally

```sh
docker compose up -d postgres redis

go run ./cmd/booking       # :8081
go run ./cmd/gateway        # :8080, proxies /api/* to booking, serves web/
go run ./cmd/controlplane   # :8082, healthz stub only
```

Then open http://localhost:8080 for the demo frontend, or seed some events
first:

```sh
./scripts/seed.sh
```

Copy [.env.example](.env.example) to `.env` (or export the variables
manually) if you want to override defaults like `JWT_SECRET` or
`DATABASE_URL`.

## Tests

```sh
go test ./...
```

The booking service's concurrency test (`internal/booking/store_test.go`)
needs a real Postgres to run against — it's the test that proves the
`SELECT ... FOR UPDATE` locking actually prevents overbooking, so it isn't
mocked. Set `TEST_DATABASE_URL` (see `.env.example`) with Postgres running,
otherwise it's skipped.

## API testing

A [Bruno](https://www.usebruno.com/) collection covering the core flows
(create event, list events, get a dev token, book successfully, book into a
sold-out event, fetch a booking) lives in [bruno/torii-api](bruno/torii-api).
