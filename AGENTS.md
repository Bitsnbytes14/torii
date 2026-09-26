# AGENTS.md

Briefing for any coding agent (or human) working in this repo. If you're
picking this up cold, read this whole file before writing code.

## What this project is

Torii is a distributed event/reservation booking platform, fronted by a
self-built API gateway and control plane, written in Go. It's a **learning
project** — the goal is to genuinely understand distributed systems and
cloud-native infra, not to ship features fast. Correctness and understanding
come before velocity.

It's being built as a portfolio piece targeting backend/DevOps engineering
internships, so favor patterns and tools that are standard in real infra
teams (structured logging, explicit error handling, tests, IaC) over
shortcuts that only work locally.

## Architecture

Three services, one repo, talking to each other over HTTP (not in-process
calls — they run as separate processes, even locally):

- **gateway** (`cmd/gateway`) — the single entry point for clients. Handles
  routing to backend services, per-tenant rate limiting, JWT/OIDC auth +
  RBAC, and emits OpenTelemetry traces.
- **booking** (`cmd/booking`) — the domain service. Owns reservations:
  creating/cancelling bookings, preventing double-booking under concurrent
  load, waitlists.
- **controlplane** (`cmd/controlplane`) — a small admin API that changes
  gateway config (routes, tenants, rate limits) without redeploying the
  gateway. Writes config to Redis; the gateway watches and hot-reloads.

Shared infra: Postgres (bookings), Redis (rate-limit counters, distributed
locks, control-plane config), OpenTelemetry (tracing, added in Phase 5).

**Where it runs**: self-managed k3s on a single EC2 node, not managed EKS.
EKS's control plane alone bills ~$73/month before any nodes, which rules it
out for a project budgeted at $2/month; k3s gives the same Kubernetes APIs.

**Demo frontend** (`web/`) — a single static `index.html`, no framework, no
build step. Served by the gateway at `/` (API routes stay under `/api/`).
Lists events, books seats against one, and shows a clear sold-out state —
it's the quickest way to see the booking service's locking behavior work
live rather than only through curl/Bruno.

## Directory structure

```
cmd/
  gateway/        main.go — gateway binary entrypoint
  booking/        main.go — booking service entrypoint
  controlplane/   main.go — control plane entrypoint

internal/
  gateway/        routing, JWT/tenant auth, rate limiting, hot-reload cache
  booking/        reservation domain logic, Postgres store, HTTP handlers
  controlplane/   admin API handlers (tenants, routes, rate limits)
  tenant/         shared tenant store
  redistest/      Redis test helpers (miniredis-backed by default)
  startup/        shared "wait for dependency" startup helper

web/
  index.html      static demo frontend, served by the gateway at /

deploy/
  terraform/      EC2 + k3s provisioning, budget alert, GitHub OIDC for CD
  k8s/            manifests: namespace, Postgres/Redis, services, HPAs
  scripts/        rollout.sh — deploy helper run over SSH

docs/
  DEPLOYMENT.md   step-by-step AWS deployment guide

bruno/            Bruno API collection for manual testing against the gateway
e2e/              cross-service tests (e.g. rate limiting across replicas)
scripts/          seed.sh — seeds demo tenants/events for local runs

docker-compose.yml   local dev stack: 2 gateway replicas, booking,
                     controlplane, Postgres, Redis
Dockerfile           multi-stage build, selects binary via SERVICE build-arg
.github/workflows/   ci.yml (test/lint on push+PR), deploy.yml (manual, SSH)
```

## Setup and running locally

Prerequisites: Docker with Compose, and Go 1.26+ if running services or
tests outside Docker.

```sh
docker compose up -d --build
./scripts/seed.sh
```

This starts two gateway replicas (`:8080` and `:8081`), the booking
service, the control plane, Postgres, and Redis. The seed script creates
demo events and two tenants, printing each tenant's `api_key`. Open
http://localhost:8080 and paste an `api_key` into the Tenant field — every
`/api/*` request needs one (401 without a key, 429 over the tenant's rate
limit).

To run a service directly (outside Docker), copy `.env.example` to `.env`
(or export the variables manually) and run its `main.go`, e.g.:

```sh
go run ./cmd/booking      # needs DATABASE_URL, PORT
go run ./cmd/gateway      # needs BOOKING_SERVICE_URL, JWT_SECRET, REDIS_URL, WEB_DIR
go run ./cmd/controlplane # needs CONTROLPLANE_ADMIN_TOKEN, REDIS_URL
```

Postgres and Redis still need to be running somewhere reachable (e.g.
`docker compose up -d postgres redis`) when running services this way.

## Build, test, and lint commands

```sh
go build ./...          # compile everything
go vet ./...             # static checks
gofmt -l .               # list files that need formatting (CI fails on any output)
go test ./...            # unit tests; Postgres/Redis-backed tests skip
                         # gracefully if TEST_DATABASE_URL / TEST_REDIS_URL aren't set
go test -race -count=1 ./...   # race detector, run separately in CI so a
                                # data race is never buried in an ordinary failure
```

To run the Postgres-backed tests (e.g. `internal/booking`'s concurrency /
locking tests) instead of having them skip, set `TEST_DATABASE_URL` — the
default in `.env.example` matches the Postgres container in
`docker-compose.yml`:

```sh
export TEST_DATABASE_URL=postgres://torii:torii@localhost:5432/torii?sslmode=disable
go test ./internal/booking/...
```

`TEST_REDIS_URL` is optional and runs the Redis-backed tests against a real
Redis instance instead of the in-process `miniredis` fake.

This is exactly what `.github/workflows/ci.yml` runs on every push and PR
(plus a `terraform fmt/validate/test` job against a mocked AWS provider, and
a GHCR image push on `main`).

## Deployment

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full guide to
provisioning the EC2/k3s node with Terraform and deploying with the
Kubernetes manifests in `deploy/k8s/`. Deploys are manual-only, triggered
via `.github/workflows/deploy.yml` over SSH — never automatic on push.

## Current phase — read this before writing code

Phases 1–3 are **complete**. We're in **Phase 4: cloud-native**, designed
to run at (near) zero cost. Scope for this phase only:
- Terraform (`deploy/terraform/`): one t3.micro in the default VPC,
  running k3s, plus a $2/month budget alert; local state
- Kubernetes manifests (`deploy/k8s/`): Postgres/Redis on hostPath PVCs,
  booking and gateway at 2 replicas with CPU-based HPAs, Traefik Ingress,
  and Secrets created by hand with `kubectl create secret` (never committed)
- CI (`.github/workflows/ci.yml`): fmt/vet/tests/race, push SHA-tagged images
  to GHCR; Deploy (`deploy.yml`) is manual-only, over SSH
- Control-plane admin token (`CONTROLPLANE_ADMIN_TOKEN`)

Full build order, so you know what's in scope later and what isn't yet:
1. **Scaffolding** (done) — structure, local dev environment, health checks
2. **MVP** (done) — single gateway instance, static routing, basic JWT auth, booking
   service with Postgres + row-level locking
3. **Distributed** (done) — multiple gateway replicas, rate-limit state moved to
   Redis, control plane hot-reload
4. **Cloud-native** (in progress) — Terraform, k3s, and CI are built; CD
   (`deploy.yml`) and the actual first cloud deploy are not yet done
5. **Observability** (not started) — OTel tracing end-to-end, Prometheus/Grafana
6. **Stretch** (not started) — chaos testing, canary deploys, load-test numbers

If asked to implement something, check which phase it belongs to first.
Don't quietly pull in a later phase's complexity (e.g. don't add
multi-tenancy while still on Phase 1 auth) — ask if it's unclear rather
than assuming ahead.

## Tech stack

- Go (latest stable)
- Router: `chi`
- Postgres via `pgx`
- Redis via `go-redis`
- OIDC via `coreos/go-oidc` (Phase 2+)
- OpenTelemetry Go SDK (Phase 5)
- Docker Compose for local dev

## Conventions

- **Git**: never run `git commit` or `git push` on your own initiative —
  only do so when explicitly told to in that turn. Default assumption
  between explicit asks is still: stage and wait. One logical change per
  commit, clear commit messages, no AI co-author trailers.
- **Comments**: explain *why* the code is the way it is, not what it does.
  1–3 lines. No meta-comments referencing chat history, past versions, or
  issue numbers.
- **Tests**: write them as each piece is built, not as a separate pass at
  the end — this is a learning project, so the tests are part of the point.
- **Explaining decisions**: when implementing something non-trivial (locking
  strategy, rate-limit algorithm, auth flow), briefly explain the tradeoff
  when reporting the change back — this project exists for its author to
  learn from, not just to produce a working artifact, so don't just hand
  over code with no reasoning.

## Module path

`github.com/Bitsnbytes14/torii` — working name, easy to rename later if
it collides with something real (same thing happened with RoomSync →
RoomFit, and EventGate → Torii).
