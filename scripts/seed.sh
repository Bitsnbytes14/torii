#!/usr/bin/env bash
# Seeds a few demo events straight through the booking service's own API
# (POST /events) rather than inserting rows directly, so seeding exercises
# the same code path a real client would use.
set -euo pipefail

BOOKING_URL="${BOOKING_URL:-http://localhost:8081}"

seed_event() {
  local name="$1" starts_at="$2" total_seats="$3"
  curl -sS -X POST "$BOOKING_URL/events" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"$name\", \"starts_at\": \"$starts_at\", \"total_seats\": $total_seats}" \
    | { command -v jq >/dev/null 2>&1 && jq . || cat; }
  echo
}

echo "Seeding demo events against $BOOKING_URL ..."
seed_event "Rooftop Jazz Night"       "$(date -u -d '+3 days'  +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+3d +%Y-%m-%dT%H:%M:%SZ)" 40
seed_event "Backend Engineering Meetup" "$(date -u -d '+7 days'  +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+7d +%Y-%m-%dT%H:%M:%SZ)" 25
seed_event "Sold-Out Demo Workshop"   "$(date -u -d '+1 day'   +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+1d +%Y-%m-%dT%H:%M:%SZ)" 2

echo "Done."
