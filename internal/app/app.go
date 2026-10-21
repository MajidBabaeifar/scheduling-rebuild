// Package app holds the scheduling domain: auth, schedules, event types, slots and bookings.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones without relying on the host
)

// App wires the domain to its storage and side effects.
type App struct {
	DB            *sql.DB
	Now           func() time.Time
	Mail          Mailer
	Integrations  Integrations
	Log           *slog.Logger
	BaseURL       string // absolute URL used in emails, no trailing slash
	SignupEnabled bool   // AUTH-2
}

// New returns an App with defaults: real clock, logged emails, no integrations.
func New(db *sql.DB, baseURL string) *App {
	log := slog.Default()
	return &App{
		DB:            db,
		Now:           time.Now,
		Mail:          LogMailer{Log: log},
		Integrations:  NoIntegrations{},
		Log:           log,
		BaseURL:       strings.TrimRight(baseURL, "/"),
		SignupEnabled: true,
	}
}

func (a *App) now() time.Time { return a.Now().UTC().Truncate(time.Second) }

// Error is a domain error with a stable code (e.g. `slot_taken`) and optional detail.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func errf(code, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Code returns the domain error code of err, or "" if it isn't one.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Error codes.
const (
	ErrInvalid            = "invalid"
	ErrTaken              = "taken"
	ErrSignupDisabled     = "signup_disabled"
	ErrWeakPassword       = "weak_password"
	ErrInvalidCredentials = "invalid_credentials"
	ErrLocked             = "locked"
	ErrNotFound           = "not_found"
	ErrLastSchedule       = "last_schedule"

	// BKG-6, in check order.
	ErrEventNotFound   = "event_not_found"
	ErrInvalidDuration = "invalid_duration"
	ErrInPast          = "in_past"
	ErrTooSoon         = "too_soon"
	ErrOutsideWindow   = "outside_window"
	ErrLimitReached    = "limit_reached"
	ErrUnavailable     = "unavailable"
	ErrSlotTaken       = "slot_taken"

	ErrInvalidRange    = "invalid_range"
	ErrInvalidTimeZone = "invalid_time_zone"
	ErrFieldRequired   = "field_required"
	ErrFieldInvalid    = "field_invalid"
	ErrLocation        = "location_required"
	ErrBadTransition   = "invalid_transition"
	ErrNotPending      = "not_pending"
	ErrTokenUsed       = "token_invalid"
	ErrNotAllowed      = "not_allowed"
	ErrBookingClosed   = "booking_closed"
	ErrTooLate         = "too_late"
	ErrBookingEnded    = "booking_ended"
	ErrReasonRequired  = "reason_required"
	ErrTooEarly        = "too_early"
	ErrForbidden       = "forbidden"
)

// Email is a message that would be sent; RB-4 logs it instead.
type Email struct {
	To      []string
	ReplyTo string
	Subject string
	Body    string
}

// Mailer delivers emails.
type Mailer interface{ Send(Email) }

// LogMailer writes emails to the log (RB-4).
type LogMailer struct{ Log *slog.Logger }

func (m LogMailer) Send(e Email) {
	m.Log.Info("email", "to", strings.Join(e.To, ","), "reply_to", e.ReplyTo, "subject", e.Subject, "body", e.Body)
}

// Integrations writes bookings to external calendars and video apps (BKG-10, RB-5).
type Integrations interface {
	BookingAccepted(ctx context.Context, b *Booking) error
	BookingCancelled(ctx context.Context, b *Booking) error
}

// NoIntegrations is the default: nothing connected, nothing to write.
type NoIntegrations struct{}

func (NoIntegrations) BookingAccepted(context.Context, *Booking) error  { return nil }
func (NoIntegrations) BookingCancelled(context.Context, *Booking) error { return nil }

// randomToken returns n random bytes as unpadded base64url.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// LoadZone parses an IANA zone name; "" and "Local" are refused.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errf(ErrInvalidTimeZone, "%q", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, errf(ErrInvalidTimeZone, "%q", name)
	}
	return loc, nil
}
