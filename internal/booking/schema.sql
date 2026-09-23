CREATE TABLE IF NOT EXISTS events (
    id           UUID PRIMARY KEY,
    name         TEXT NOT NULL,
    starts_at    TIMESTAMPTZ NOT NULL,
    total_seats  INTEGER NOT NULL CHECK (total_seats > 0),
    seats_booked INTEGER NOT NULL DEFAULT 0 CHECK (seats_booked >= 0),
    CHECK (seats_booked <= total_seats)
);

CREATE TABLE IF NOT EXISTS bookings (
    id         UUID PRIMARY KEY,
    event_id   UUID NOT NULL REFERENCES events(id),
    holder     TEXT NOT NULL,
    seats      INTEGER NOT NULL CHECK (seats > 0),
    status     TEXT NOT NULL CHECK (status IN ('confirmed', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_bookings_holder ON bookings (holder);
CREATE INDEX IF NOT EXISTS idx_bookings_event_id ON bookings (event_id);
