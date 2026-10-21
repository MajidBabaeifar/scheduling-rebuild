package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(s string)   { c.mu.Lock(); c.t = at(s); c.mu.Unlock() }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type recMailer struct {
	mu   sync.Mutex
	sent []Email
}

func (m *recMailer) Send(e Email) { m.mu.Lock(); m.sent = append(m.sent, e); m.mu.Unlock() }

func (m *recMailer) find(subjectPrefix string) *Email {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if strings.HasPrefix(m.sent[i].Subject, subjectPrefix) {
			return &m.sent[i]
		}
	}
	return nil
}

type env struct {
	*App
	clk  *clock
	mail *recMailer
}

// Default "now" is Sunday 2026-11-01 12:00 UTC; Monday 2026-11-02 is the usual test day.
func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, "http://test")
	clk := &clock{t: at("2026-11-01T12:00:00Z")}
	mail := &recMailer{}
	a.Now, a.Mail, a.Log = clk.now, mail, slog.New(slog.NewTextHandler(io.Discard, nil))
	return &env{App: a, clk: clk, mail: mail}
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

const goodPassword = "correct horse battery"

func (e *env) user(t *testing.T, username, tz string) *User {
	t.Helper()
	u, err := e.SignUp(ctx, SignUpInput{Email: username + "@example.com", Username: username, Name: strings.ToUpper(username[:1]) + username[1:], Password: goodPassword, TimeZone: tz})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// event creates an event type with no notice (unless set) for u.
func (e *env) event(t *testing.T, u *User, mut func(*EventType)) *EventType {
	t.Helper()
	et := NewEventType()
	et.Title, et.Slug, et.MinNotice = "Intro", "intro", 0
	if mut != nil {
		mut(&et)
	}
	out, err := e.CreateEventType(ctx, u.ID, et)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// times returns booker-local HH:MM starts on date (local midnight to midnight) for tz.
func (e *env) times(t *testing.T, et *EventType, tz, date string, mut ...func(*SlotQuery)) []string {
	t.Helper()
	m, err := e.slotsOn(et, tz, date, mut...)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := LoadZone(tz)
	var out []string
	for _, s := range m[date] {
		out = append(out, s.Start.In(loc).Format("15:04"))
	}
	return out
}

func (e *env) slotsOn(et *EventType, tz, date string, mut ...func(*SlotQuery)) (SlotMap, error) {
	loc, _ := LoadZone(tz)
	d, _ := ParseDate(date)
	q := SlotQuery{EventTypeID: et.ID, From: StartOfDay(d, loc), To: StartOfDay(d.AddDays(1), loc), TimeZone: tz}
	for _, f := range mut {
		f(&q)
	}
	return e.Slots(ctx, q)
}

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func (e *env) book(et *EventType, start, email string, extra ...string) (*Booking, error) {
	return e.CreateBooking(ctx, BookingInput{
		EventTypeID: et.ID, Start: at(start), TimeZone: "UTC",
		Responses: form(append([]string{"name", "Booker", "email", email}, extra...)...),
	})
}

func (e *env) mustBook(t *testing.T, et *EventType, start, email string, extra ...string) *Booking {
	t.Helper()
	b, err := e.book(et, start, email, extra...)
	if err != nil {
		t.Fatalf("book %s: %v", start, err)
	}
	return b
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if Code(err) != code {
		t.Fatalf("want %q, got %v", code, err)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }

var errBoom = errors.New("calendar API down")

type failingIntegrations struct{}

func (failingIntegrations) BookingAccepted(context.Context, *Booking) error  { return errBoom }
func (failingIntegrations) BookingCancelled(context.Context, *Booking) error { return errBoom }
