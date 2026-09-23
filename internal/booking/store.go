package booking

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound          = errors.New("not found")
	ErrInsufficientSeats = errors.New("not enough seats remaining")
)

// Store is the Postgres-backed persistence layer for events and bookings.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Migrate applies the schema. It's idempotent (CREATE TABLE IF NOT EXISTS),
// which is enough for this phase — a real migration tool comes later if the
// schema outgrows a single file.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	return err
}

func (s *Store) CreateEvent(ctx context.Context, name string, startsAt time.Time, totalSeats int) (Event, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx,
		`INSERT INTO events (id, name, starts_at, total_seats, seats_booked) VALUES ($1, $2, $3, $4, 0)`,
		id, name, startsAt, totalSeats,
	)
	if err != nil {
		return Event{}, err
	}
	return Event{ID: id, Name: name, StartsAt: startsAt, TotalSeats: totalSeats, AvailableSeats: totalSeats}, nil
}

func (s *Store) ListEvents(ctx context.Context) ([]Event, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, starts_at, total_seats, seats_booked FROM events ORDER BY starts_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Name, &e.StartsAt, &e.TotalSeats, &e.SeatsBooked); err != nil {
			return nil, err
		}
		e.AvailableSeats = e.TotalSeats - e.SeatsBooked
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) GetEvent(ctx context.Context, id uuid.UUID) (Event, error) {
	var e Event
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, starts_at, total_seats, seats_booked FROM events WHERE id = $1`, id,
	).Scan(&e.ID, &e.Name, &e.StartsAt, &e.TotalSeats, &e.SeatsBooked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	e.AvailableSeats = e.TotalSeats - e.SeatsBooked
	return e, nil
}

// CreateBooking is the concurrency-critical path: it locks the event row for
// the duration of the transaction (SELECT ... FOR UPDATE) so that two
// concurrent requests racing for the last seats can't both read the same
// "seats available" snapshot and both succeed. The loser blocks on the lock
// until the winner commits, then re-checks capacity against the now-updated
// row instead of the stale value it would've had under a plain SELECT.
func (s *Store) CreateBooking(ctx context.Context, eventID uuid.UUID, holder string, seats int) (Booking, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Booking{}, err
	}
	defer tx.Rollback(ctx) // no-op once committed

	var total, booked int
	err = tx.QueryRow(ctx,
		`SELECT total_seats, seats_booked FROM events WHERE id = $1 FOR UPDATE`, eventID,
	).Scan(&total, &booked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	if err != nil {
		return Booking{}, err
	}

	if total-booked < seats {
		return Booking{}, ErrInsufficientSeats
	}

	id := uuid.New()
	createdAt := time.Now().UTC()

	if _, err := tx.Exec(ctx,
		`UPDATE events SET seats_booked = seats_booked + $1 WHERE id = $2`, seats, eventID,
	); err != nil {
		return Booking{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO bookings (id, event_id, holder, seats, status, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, eventID, holder, seats, StatusConfirmed, createdAt,
	); err != nil {
		return Booking{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Booking{}, err
	}

	return Booking{ID: id, EventID: eventID, Holder: holder, Seats: seats, Status: StatusConfirmed, CreatedAt: createdAt}, nil
}

func (s *Store) GetBooking(ctx context.Context, id uuid.UUID) (Booking, error) {
	var b Booking
	err := s.pool.QueryRow(ctx,
		`SELECT id, event_id, holder, seats, status, created_at FROM bookings WHERE id = $1`, id,
	).Scan(&b.ID, &b.EventID, &b.Holder, &b.Seats, &b.Status, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	return b, err
}

func (s *Store) ListBookingsByHolder(ctx context.Context, holder string) ([]Booking, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, event_id, holder, seats, status, created_at FROM bookings WHERE holder = $1 ORDER BY created_at DESC`,
		holder,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bookings []Booking
	for rows.Next() {
		var b Booking
		if err := rows.Scan(&b.ID, &b.EventID, &b.Holder, &b.Seats, &b.Status, &b.CreatedAt); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}
