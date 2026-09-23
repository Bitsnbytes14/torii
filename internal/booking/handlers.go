package booking

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	store  *Store
	logger *slog.Logger
}

func NewHandler(store *Store, logger *slog.Logger) *Handler {
	return &Handler{store: store, logger: logger}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/healthz", h.healthz)
	r.Post("/events", h.createEvent)
	r.Get("/events", h.listEvents)
	r.Get("/events/{id}", h.getEvent)
	r.Post("/bookings", h.createBooking)
	r.Get("/bookings/{id}", h.getBooking)
	r.Get("/bookings", h.listBookingsByHolder)
	return r
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type createEventRequest struct {
	Name       string    `json:"name"`
	StartsAt   time.Time `json:"starts_at"`
	TotalSeats int       `json:"total_seats"`
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	var req createEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.TotalSeats <= 0 {
		writeError(w, http.StatusBadRequest, "total_seats must be greater than zero")
		return
	}
	if req.StartsAt.IsZero() {
		writeError(w, http.StatusBadRequest, "starts_at is required (RFC3339)")
		return
	}

	event, err := h.store.CreateEvent(r.Context(), req.Name, req.StartsAt, req.TotalSeats)
	if err != nil {
		h.logger.Error("create event failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create event")
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.store.ListEvents(r.Context())
	if err != nil {
		h.logger.Error("list events failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	if events == nil {
		events = []Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid event id")
		return
	}
	event, err := h.store.GetEvent(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		h.logger.Error("get event failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get event")
		return
	}
	writeJSON(w, http.StatusOK, event)
}

type createBookingRequest struct {
	EventID uuid.UUID `json:"event_id"`
	Holder  string    `json:"holder"`
	Seats   int       `json:"seats"`
}

func (h *Handler) createBooking(w http.ResponseWriter, r *http.Request) {
	var req createBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EventID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "event_id is required")
		return
	}
	if req.Holder == "" {
		writeError(w, http.StatusBadRequest, "holder is required")
		return
	}
	if req.Seats <= 0 {
		writeError(w, http.StatusBadRequest, "seats must be greater than zero")
		return
	}

	booking, err := h.store.CreateBooking(r.Context(), req.EventID, req.Holder, req.Seats)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "event not found")
	case errors.Is(err, ErrInsufficientSeats):
		writeError(w, http.StatusConflict, "not enough seats remaining for this event")
	case err != nil:
		h.logger.Error("create booking failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create booking")
	default:
		writeJSON(w, http.StatusCreated, booking)
	}
}

func (h *Handler) getBooking(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid booking id")
		return
	}
	booking, err := h.store.GetBooking(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.Error("get booking failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get booking")
		return
	}
	writeJSON(w, http.StatusOK, booking)
}

func (h *Handler) listBookingsByHolder(w http.ResponseWriter, r *http.Request) {
	holder := r.URL.Query().Get("holder")
	if holder == "" {
		writeError(w, http.StatusBadRequest, "holder query parameter is required")
		return
	}
	bookings, err := h.store.ListBookingsByHolder(r.Context(), holder)
	if err != nil {
		h.logger.Error("list bookings failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list bookings")
		return
	}
	if bookings == nil {
		bookings = []Booking{}
	}
	writeJSON(w, http.StatusOK, bookings)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
