package booking

import (
	"time"

	"github.com/google/uuid"
)

// Event is a bookable event with a fixed seat capacity.
type Event struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	StartsAt       time.Time `json:"starts_at"`
	TotalSeats     int       `json:"total_seats"`
	SeatsBooked    int       `json:"seats_booked"`
	AvailableSeats int       `json:"available_seats"`
}

// Booking is a reservation of seats against an event.
type Booking struct {
	ID        uuid.UUID `json:"id"`
	EventID   uuid.UUID `json:"event_id"`
	Holder    string    `json:"holder"`
	Seats     int       `json:"seats"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	StatusConfirmed = "confirmed"
	StatusCancelled = "cancelled"
)
