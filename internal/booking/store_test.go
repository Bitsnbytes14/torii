package booking

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testStore connects to a real Postgres instance rather than mocking the
// database — the thing under test is a row-level lock, and a mock can't
// tell you whether the lock actually serializes concurrent transactions.
// Set TEST_DATABASE_URL (see docker-compose.yml for local credentials) to
// run this; it's skipped otherwise so `go test ./...` still works without
// Postgres running.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres-backed test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	store := NewStore(pool)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

// TestCreateBooking_NoOverbooking fires more concurrent booking requests
// than there are seats and asserts that seats_booked never exceeds
// total_seats and that exactly the right number of requests are rejected
// with ErrInsufficientSeats. This is the test that proves SELECT ... FOR
// UPDATE actually serializes the read-check-write instead of just reducing
// the odds of a race.
func TestCreateBooking_NoOverbooking(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	const totalSeats = 5
	const concurrentRequests = 20
	const seatsPerRequest = 1

	event, err := store.CreateEvent(ctx, "Concurrency Test Event", time.Now().Add(24*time.Hour), totalSeats)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		successes  int
		conflicts  int
		unexpected []error
	)

	wg.Add(concurrentRequests)
	for i := 0; i < concurrentRequests; i++ {
		go func(n int) {
			defer wg.Done()
			_, err := store.CreateBooking(ctx, event.ID, "holder", seatsPerRequest)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrInsufficientSeats):
				conflicts++
			default:
				unexpected = append(unexpected, err)
			}
		}(i)
	}
	wg.Wait()

	if len(unexpected) > 0 {
		t.Fatalf("got %d unexpected errors, first: %v", len(unexpected), unexpected[0])
	}
	if successes != totalSeats {
		t.Errorf("expected exactly %d successful bookings, got %d", totalSeats, successes)
	}
	if conflicts != concurrentRequests-totalSeats {
		t.Errorf("expected %d conflicts, got %d", concurrentRequests-totalSeats, conflicts)
	}

	final, err := store.GetEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if final.SeatsBooked != totalSeats {
		t.Errorf("seats_booked = %d, want %d (no overbooking)", final.SeatsBooked, totalSeats)
	}
	if final.SeatsBooked > final.TotalSeats {
		t.Fatalf("OVERBOOKED: seats_booked (%d) exceeds total_seats (%d)", final.SeatsBooked, final.TotalSeats)
	}
}

// TestMigrate_ConcurrentReplicas runs Migrate from many connections at once
// against an empty schema, the way several booking replicas do when they
// start together. CREATE TABLE IF NOT EXISTS alone is not safe under
// concurrency: two sessions can both see "missing" and one then fails on a
// catalog unique constraint.
func TestMigrate_ConcurrentReplicas(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres-backed test")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(admin.Close)

	schemaName := fmt.Sprintf("migrate_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaName+" CASCADE") })

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaName

	const replicas = 10
	var wg sync.WaitGroup
	errs := make(chan error, replicas)
	start := make(chan struct{})
	for range replicas {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		store := NewStore(pool)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.Migrate(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate failed: %v", err)
		}
	}
}
