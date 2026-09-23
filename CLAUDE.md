# CLAUDE.md

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

**Demo frontend** (`web/`) — a single static `index.html`, no framework, no
build step. Served by the gateway at `/` (API routes stay under `/api/`).
Lists events, books seats against one, and shows a clear sold-out state —
it's the quickest way to see the booking service's locking behavior work
live rather than only through curl/Bruno.

## Current phase — read this before writing code

We're at **Phase 0: scaffolding**. Scope for this phase only:
- Repo structure, `go.mod`, Docker Compose for local Postgres + Redis
- Each service gets a minimal `main.go` with a `/healthz` endpoint
- No business logic, no auth, no rate limiting yet

Full build order, so you know what's in scope later and what isn't yet:
1. **Scaffolding** (this phase) — structure, local dev environment, health checks
2. **MVP** — single gateway instance, static routing, basic JWT auth, booking
   service with Postgres + row-level locking
3. **Distributed** — multiple gateway replicas, rate-limit state moved to
   Redis, control plane hot-reload
4. **Cloud-native** — Terraform, EKS, CI/CD, autoscaling
5. **Observability** — OTel tracing end-to-end, Prometheus/Grafana
6. **Stretch** — chaos testing, canary deploys, load-test numbers

If asked to implement something, check which phase it belongs to first.
Don't quietly pull in a later phase's complexity (e.g. don't add
multi-tenancy while we're still on Phase 1 auth) — ask if it's unclear
rather than assuming ahead.

## Tech stack

- Go (latest stable)
- Router: `chi`
- Postgres via `pgx`
- Redis via `go-redis`
- OIDC via `coreos/go-oidc` (Phase 2+)
- OpenTelemetry Go SDK (Phase 5)
- Docker Compose for local dev

## Conventions

- **Git**: never run `git commit` or `git push` — stage changes and let
  Ahmad review and commit himself. One logical change per commit, clear
  commit messages, no AI co-author trailers.
- **Comments**: explain *why* the code is the way it is, not what it does.
  1–3 lines. No meta-comments referencing chat history, past versions, or
  issue numbers.
- **Tests**: write them as each piece is built, not as a separate pass at
  the end — this is a learning project, so the tests are part of the point.
- **Explaining decisions**: when implementing something non-trivial (locking
  strategy, rate-limit algorithm, auth flow), briefly explain the tradeoff
  in the summary back to chat. Ahmad is using this project to learn, not
  just to get a working artifact — don't just dump code with no reasoning.

## Module path

`github.com/Bitsnbytes14/torii` — working name, easy to rename later if
it collides with something real (same thing happened with RoomSync →
RoomFit, and EventGate → Torii).
