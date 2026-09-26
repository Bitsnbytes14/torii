#!/usr/bin/env bash
# Seeds demo tenants through the control plane and demo events through the
# booking service's own API rather than writing to Redis/Postgres directly,
# so seeding exercises the same code paths a real client would use.
set -euo pipefail

BOOKING_URL="${BOOKING_URL:-http://localhost:8083}"
CONTROLPLANE_URL="${CONTROLPLANE_URL:-http://localhost:8082}"
CONTROLPLANE_ADMIN_TOKEN="${CONTROLPLANE_ADMIN_TOKEN:-dev-admin-token}"

pretty() { command -v jq >/dev/null 2>&1 && jq . || cat; }

seed_tenant() {
  local name="$1" limit="$2"
  curl -sS -X POST "$CONTROLPLANE_URL/tenants" \
    -H "Authorization: Bearer $CONTROLPLANE_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"$name\", \"rate_limit_per_minute\": $limit}" \
    | pretty
  echo
}

seed_event() {
  local name="$1" starts_at="$2" total_seats="$3"
  curl -sS -X POST "$BOOKING_URL/events" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"$name\", \"starts_at\": \"$starts_at\", \"total_seats\": $total_seats}" \
    | pretty
  echo
}

echo "Seeding demo tenants against $CONTROLPLANE_URL ..."
seed_tenant "Demo App"    100
# Deliberately tiny so a few refreshes of the frontend (or Bruno's rate-limit
# requests) hit 429 immediately.
seed_tenant "Tiny Tenant" 5

echo "Seeding demo events against $BOOKING_URL ..."
seed_event "Rooftop Jazz Night"       "$(date -u -d '+3 days'  +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+3d +%Y-%m-%dT%H:%M:%SZ)" 40
seed_event "Backend Engineering Meetup" "$(date -u -d '+7 days'  +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+7d +%Y-%m-%dT%H:%M:%SZ)" 25
seed_event "Sold-Out Demo Workshop"   "$(date -u -d '+1 day'   +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+1d +%Y-%m-%dT%H:%M:%SZ)" 2

echo "Done. Use an api_key above as the X-API-Key header (or paste it into the demo frontend)."
