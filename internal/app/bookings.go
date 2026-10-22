package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Booking statuses (BKG-2). awaiting_payment exists for completeness; payments are out of scope.
const (
	StatusAwaitingPayment = "awaiting_payment"
	StatusPending         = "pending"
	StatusAccepted        = "accepted"
	StatusRejected        = "rejected"
	StatusCancelled       = "cancelled"
)

var transitions = map[string][]string{
	StatusAwaitingPayment: {StatusPending, StatusAccepted, StatusCancelled},
	StatusPending:         {StatusAccepted, StatusRejected, StatusCancelled},
	StatusAccepted:        {StatusCancelled},
}

// CanTransition is BKG-2.
func CanTransition(from, to string) bool { return slices.Contains(transitions[from], to) }

// Attendee is BKG-1.
type Attendee struct {
	Name     string
	Email    string
	TimeZone string
	Phone    string
	Locale   string
	NoShow   bool
}

// Booking is BKG-1 (+ `rescheduled`, `host_no_show`, `ical_uid`: F-25, F-26).
type Booking struct {
	ID             int64
	UID            string
	EventTypeID    int64
	OrganizerID    int64
	Attendees      []Attendee
	Start, End     time.Time
	Location       Location
	Responses      Responses
	Status         string
	Paid           bool
	FromReschedule string
	Rescheduled    bool
	CancelReason   string
	CancelledBy    string
	RejectReason   string
	Sequence       int
	ICalUID        string
	HostNoShow     bool
	BookerEmail    string
	CreatedAt      time.Time
	Warnings       []string
}

// Booker is the first attendee.
func (b *Booking) Booker() Attendee {
	if len(b.Attendees) == 0 {
		return Attendee{}
	}
	return b.Attendees[0]
}

// BookingInput is a new booking from the public form.
type BookingInput struct {
	EventTypeID int64
	Start       time.Time
	Duration    int // 0 = length
	TimeZone    string
	Locale      string
	Responses   url.Values
	BookerID    string
}

// CreateBooking is BKG-3..10.
func (a *App) CreateBooking(ctx context.Context, in BookingInput) (*Booking, error) {
	e, err := a.newEngine(ctx, a.DB, in.EventTypeID, in.TimeZone, in.Duration, "", in.BookerID) // event exists, duration
	if err != nil {
		return nil, err
	}
	et, host := e.et, e.host
	resp, err := et.ParseResponses(in.Responses, []string{host.Email}, false)
	if err != nil {
		return nil, err
	}
	loc, err := pickLocation(et, resp)
	if err != nil {
		return nil, err
	}
	locale := in.Locale
	if locale == "" {
		locale = "en"
	}
	atts := []Attendee{{Name: resp.Str("name"), Email: resp.Str("email"), TimeZone: in.TimeZone, Phone: resp.Str("phone"), Locale: locale}}
	for _, g := range resp.List("guests") {
		atts = append(atts, Attendee{Name: g, Email: g, TimeZone: in.TimeZone, Locale: locale}) // F-15
	}
	return a.book(ctx, bookArgs{
		et: et, host: host, start: in.Start, duration: int(e.duration / time.Minute), tz: in.TimeZone,
		resp: resp, atts: atts, loc: loc, bookerID: in.BookerID,
	})
}

// pickLocation is BKG-5 with EVT-14 (F-23): one configured → it; several → booker must pick;
// none → a plain video link (F-27).
func pickLocation(et *EventType, resp Responses) (Location, error) {
	var l Location
	switch pick := resp.Str("location"); {
	case len(et.Locations) == 0:
		return Location{Kind: "video"}, nil
	case pick != "":
		i := slices.IndexFunc(et.Locations, func(x Location) bool { return x.Kind == pick })
		if i < 0 {
			return l, errf(ErrFieldInvalid, "location %q", pick)
		}
		l = et.Locations[i]
	case len(et.Locations) == 1:
		l = et.Locations[0]
	default:
		return l, &Error{Code: ErrLocation, Detail: "pick one of the locations"}
	}
	if l.Kind == "phone_booker" {
		if resp.Str("phone") == "" {
			return l, errf(ErrFieldRequired, "phone")
		}
		l.Value = resp.Str("phone")
	}
	return l, nil
}

type bookArgs struct {
	et        *EventType
	host      *User
	start     time.Time
	duration  int
	tz        string
	resp      Responses
	atts      []Attendee
	loc       Location
	bookerID  string
	old       *Booking // reschedule
	actorID   int64    // organizer acting, 0 = booker
	resReason string
}

func (a *App) book(ctx context.Context, x bookArgs) (*Booking, error) {
	now := a.now()
	b := &Booking{
		UID: randomToken(16), EventTypeID: x.et.ID, OrganizerID: x.host.ID, Attendees: x.atts,
		Start: x.start.UTC(), End: x.start.UTC().Add(time.Duration(x.duration) * time.Minute),
		Location: x.loc, Responses: x.resp, BookerEmail: x.atts[0].Email, CreatedAt: now,
	}
	b.ICalUID = b.UID + "@scheduling"
	if b.Location.Kind == "video" && b.Location.Value == "" {
		b.Location.Value = a.BaseURL + "/video/" + b.UID
	}
	if x.old != nil {
		b.FromReschedule, b.Paid, b.Sequence, b.ICalUID = x.old.UID, x.old.Paid, x.old.Sequence+1, x.old.ICalUID
		if x.resReason != "" {
			b.Responses["reschedule_reason"] = x.resReason
		}
	}

	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if x.old == nil { // BKG-8
		var uid string
		err := tx.QueryRowContext(ctx, `SELECT uid FROM bookings WHERE booker_email = ? AND event_type_id = ? AND start_at = ? AND status IN ('pending', 'awaiting_payment') ORDER BY id LIMIT 1`,
			b.BookerEmail, x.et.ID, b.Start.Unix()).Scan(&uid)
		if err == nil {
			tx.Rollback()
			return a.Booking(ctx, uid)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	oldUID := ""
	if x.old != nil {
		oldUID = x.old.UID
	}
	e, err := a.newEngine(ctx, tx, x.et.ID, x.tz, x.duration, oldUID, x.bookerID)
	if err != nil {
		return nil, err
	}
	if err := e.check(b.Start); err != nil { // BKG-6
		return nil, err
	}

	// BKG-9 + EVT-10
	b.Status = StatusAccepted
	organizerReschedule := x.old != nil && x.actorID == x.host.ID
	if x.et.RequiresConfirmation && !organizerReschedule {
		if c := x.et.ConfirmWithin; c == nil || b.Start.Sub(now) < time.Duration(c.Minutes())*time.Minute {
			b.Status = StatusPending
		}
	}

	if err := insertBooking(ctx, tx, b); err != nil {
		if isUnique(err) { // BKG-7
			return nil, &Error{Code: ErrSlotTaken}
		}
		return nil, err
	}
	var tokens map[string]string
	if b.Status == StatusPending {
		if tokens, err = createActionTokens(ctx, tx, b.ID); err != nil {
			return nil, err
		}
	}
	if x.old != nil { // BKG-12
		by := x.old.BookerEmail
		if x.actorID != 0 {
			by = x.host.Email
		}
		if err := setStatus(ctx, tx, x.old, StatusCancelled); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bookings SET rescheduled = 1, cancelled_by = ?, cancel_reason = ? WHERE id = ?`, by, nullable(x.resReason), x.old.ID); err != nil {
			return nil, err
		}
	}
	if x.bookerID != "" {
		tx.ExecContext(ctx, `DELETE FROM holds WHERE booker_id = ?`, x.bookerID)
	}
	if err := tx.Commit(); err != nil {
		if isUnique(err) {
			return nil, &Error{Code: ErrSlotTaken}
		}
		return nil, err
	}

	switch {
	case x.old != nil && x.old.Status == StatusAccepted:
		a.Integrations.BookingCancelled(ctx, x.old)
		a.notifyRescheduled(x.et, x.host, x.old, b)
	case x.old != nil:
		a.notifyRescheduled(x.et, x.host, x.old, b)
	}
	if b.Status == StatusAccepted {
		a.afterAccepted(ctx, x.et, x.host, b, x.old == nil)
	} else {
		a.notifyRequested(x.et, x.host, b, tokens)
	}
	return a.Booking(ctx, b.UID)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func insertBooking(ctx context.Context, tx *sql.Tx, b *Booking) error {
	loc, _ := json.Marshal(b.Location)
	resp, _ := json.Marshal(b.Responses)
	res, err := tx.ExecContext(ctx, `INSERT INTO bookings (uid, event_type_id, organizer_id, start_at, end_at, location, responses, status, paid,
		from_reschedule, sequence, ical_uid, booker_email, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.UID, b.EventTypeID, b.OrganizerID, b.Start.Unix(), b.End.Unix(), string(loc), string(resp), b.Status, b.Paid,
		nullable(b.FromReschedule), b.Sequence, b.ICalUID, b.BookerEmail, b.CreatedAt.Unix())
	if err != nil {
		return err
	}
	b.ID, _ = res.LastInsertId()
	for _, at := range b.Attendees {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attendees (booking_id, name, email, time_zone, phone, locale) VALUES (?, ?, ?, ?, ?, ?)`,
			b.ID, at.Name, at.Email, at.TimeZone, at.Phone, at.Locale); err != nil {
			return err
		}
	}
	return nil
}

func setStatus(ctx context.Context, q queryer, b *Booking, to string) error {
	if !CanTransition(b.Status, to) {
		return errf(ErrBadTransition, "%s → %s", b.Status, to)
	}
	res, err := q.ExecContext(ctx, `UPDATE bookings SET status = ? WHERE id = ? AND status = ?`, to, b.ID, b.Status)
	if isUnique(err) {
		return &Error{Code: ErrSlotTaken}
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errf(ErrBadTransition, "status changed concurrently")
	}
	b.Status = to
	return nil
}

func createActionTokens(ctx context.Context, tx *sql.Tx, bookingID int64) (map[string]string, error) {
	out := map[string]string{}
	for _, act := range []string{"confirm", "reject"} {
		t := randomToken(24)
		if _, err := tx.ExecContext(ctx, `INSERT INTO action_tokens (token_hash, booking_id, action) VALUES (?, ?, ?)`, hashToken(t), bookingID, act); err != nil {
			return nil, err
		}
		out[act] = t
	}
	return out, nil
}

// afterAccepted is BKG-10: write to calendars (failure → warning, booking stands) and notify.
func (a *App) afterAccepted(ctx context.Context, et *EventType, host *User, b *Booking, notify bool) {
	if err := a.Integrations.BookingAccepted(ctx, b); err != nil {
		msg := "calendar/video integration failed: " + err.Error()
		a.Log.Warn("integration", "booking", b.UID, "err", err)
		a.DB.ExecContext(ctx, `INSERT INTO booking_warnings (booking_id, message, created_at) VALUES (?, ?, ?)`, b.ID, msg, a.now().Unix())
	}
	if notify {
		a.notifyAccepted(et, host, b)
	}
}

// Booking loads a booking by uid, with attendees and warnings.
func (a *App) Booking(ctx context.Context, uid string) (*Booking, error) {
	return loadBooking(ctx, a.DB, `uid = ?`, uid)
}

const bookingCols = `id, uid, event_type_id, organizer_id, start_at, end_at, location, responses, status, paid,
	coalesce(from_reschedule, ''), rescheduled, coalesce(cancel_reason, ''), coalesce(cancelled_by, ''), coalesce(reject_reason, ''),
	sequence, ical_uid, host_no_show, booker_email, created_at`

func scanBooking(row interface{ Scan(...any) error }) (*Booking, error) {
	var b Booking
	var s, e, c int64
	var loc, resp string
	err := row.Scan(&b.ID, &b.UID, &b.EventTypeID, &b.OrganizerID, &s, &e, &loc, &resp, &b.Status, &b.Paid,
		&b.FromReschedule, &b.Rescheduled, &b.CancelReason, &b.CancelledBy, &b.RejectReason,
		&b.Sequence, &b.ICalUID, &b.HostNoShow, &b.BookerEmail, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: ErrNotFound}
	}
	if err != nil {
		return nil, err
	}
	b.Start, b.End, b.CreatedAt = time.Unix(s, 0).UTC(), time.Unix(e, 0).UTC(), time.Unix(c, 0).UTC()
	json.Unmarshal([]byte(loc), &b.Location)
	json.Unmarshal([]byte(resp), &b.Responses)
	return &b, nil
}

func loadBooking(ctx context.Context, q queryer, where string, args ...any) (*Booking, error) {
	b, err := scanBooking(q.QueryRowContext(ctx, `SELECT `+bookingCols+` FROM bookings WHERE `+where, args...))
	if err != nil {
		return nil, err
	}
	return b, loadBookingExtras(ctx, q, b)
}

func loadBookingExtras(ctx context.Context, q queryer, b *Booking) error {
	rows, err := q.QueryContext(ctx, `SELECT name, email, time_zone, phone, locale, no_show FROM attendees WHERE booking_id = ? ORDER BY id`, b.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var at Attendee
		if err := rows.Scan(&at.Name, &at.Email, &at.TimeZone, &at.Phone, &at.Locale, &at.NoShow); err != nil {
			rows.Close()
			return err
		}
		b.Attendees = append(b.Attendees, at)
	}
	rows.Close()
	wrows, err := q.QueryContext(ctx, `SELECT message FROM booking_warnings WHERE booking_id = ? ORDER BY id`, b.ID)
	if err != nil {
		return err
	}
	defer wrows.Close()
	for wrows.Next() {
		var m string
		wrows.Scan(&m)
		b.Warnings = append(b.Warnings, m)
	}
	return wrows.Err()
}

// RescheduledTo returns the booking that replaced uid, if any.
func (a *App) RescheduledTo(ctx context.Context, uid string) (*Booking, error) {
	return loadBooking(ctx, a.DB, `from_reschedule = ?`, uid)
}

// OrganizerBookings lists an organizer's bookings, upcoming first then past.
func (a *App) OrganizerBookings(ctx context.Context, userID int64) ([]*Booking, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT `+bookingCols+` FROM bookings WHERE organizer_id = ? ORDER BY start_at`, userID)
	if err != nil {
		return nil, err
	}
	var out []*Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	rows.Close()
	for _, b := range out {
		if err := loadBookingExtras(ctx, a.DB, b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (a *App) organizerBooking(ctx context.Context, organizerID int64, uid string) (*Booking, error) {
	b, err := a.Booking(ctx, uid)
	if err != nil {
		return nil, err
	}
	if b.OrganizerID != organizerID {
		return nil, &Error{Code: ErrForbidden}
	}
	return b, nil
}

// ConfirmBooking accepts a pending booking (BKG-11).
func (a *App) ConfirmBooking(ctx context.Context, organizerID int64, uid string) error {
	b, err := a.organizerBooking(ctx, organizerID, uid)
	if err != nil {
		return err
	}
	return a.decide(ctx, b, "confirm", "")
}

// RejectBooking rejects a pending booking with an optional reason (BKG-11).
func (a *App) RejectBooking(ctx context.Context, organizerID int64, uid, reason string) error {
	b, err := a.organizerBooking(ctx, organizerID, uid)
	if err != nil {
		return err
	}
	return a.decide(ctx, b, "reject", reason)
}

// ActionToken describes a confirm/reject link.
type ActionToken struct {
	Action  string
	Booking *Booking
	Used    bool
}

// LookupActionToken reads a confirm/reject link without using it.
func (a *App) LookupActionToken(ctx context.Context, token string) (*ActionToken, error) {
	var id int64
	var act string
	var used sql.NullInt64
	err := a.DB.QueryRowContext(ctx, `SELECT booking_id, action, used_at FROM action_tokens WHERE token_hash = ?`, hashToken(token)).Scan(&id, &act, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: ErrTokenUsed}
	}
	if err != nil {
		return nil, err
	}
	b, err := loadBooking(ctx, a.DB, `id = ?`, id)
	if err != nil {
		return nil, err
	}
	return &ActionToken{Action: act, Booking: b, Used: used.Valid}, nil
}

// UseActionToken confirms or rejects via an emailed single-use link (BKG-11).
func (a *App) UseActionToken(ctx context.Context, token, reason string) (*ActionToken, error) {
	t, err := a.LookupActionToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if t.Used {
		return t, &Error{Code: ErrTokenUsed}
	}
	return t, a.decide(ctx, t.Booking, t.Action, reason)
}

func (a *App) decide(ctx context.Context, b *Booking, action, reason string) error {
	if b.Status != StatusPending {
		return &Error{Code: ErrNotPending}
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	to := StatusAccepted
	if action == "reject" {
		to = StatusRejected
	}
	if to == StatusAccepted { // BKG-11: confirming past a reached limit (SLT-13) → limit_reached
		e, err := a.newEngine(ctx, tx, b.EventTypeID, "UTC", 0, b.UID, "")
		if err != nil {
			return err
		}
		e.duration = b.End.Sub(b.Start)
		if ok, err := e.underLimits(b.Start); err != nil || !ok {
			if err != nil {
				return err
			}
			return &Error{Code: ErrLimitReached}
		}
	}
	if err := setStatus(ctx, tx, b, to); err != nil { // F-28: confirming onto an accepted exact slot → slot_taken
		return err
	}
	if to == StatusRejected {
		if _, err := tx.ExecContext(ctx, `UPDATE bookings SET reject_reason = ? WHERE id = ?`, nullable(reason), b.ID); err != nil {
			return err
		}
		b.RejectReason = reason
	}
	if _, err := tx.ExecContext(ctx, `UPDATE action_tokens SET used_at = ? WHERE booking_id = ? AND used_at IS NULL`, a.now().Unix(), b.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	et, err := a.EventType(ctx, b.EventTypeID)
	if err != nil {
		return err
	}
	host, err := a.User(ctx, b.OrganizerID)
	if err != nil {
		return err
	}
	if to == StatusAccepted {
		a.afterAccepted(ctx, et, host, b, true)
	} else {
		a.notifyRejected(et, host, b)
	}
	return nil
}

// CancelBooking is BKG-15. actorID is the organizer's user id, or 0 for the booker (uid link).
func (a *App) CancelBooking(ctx context.Context, uid string, actorID int64, reason string) error {
	b, err := a.Booking(ctx, uid)
	if err != nil {
		return err
	}
	if actorID != 0 && actorID != b.OrganizerID {
		return &Error{Code: ErrForbidden}
	}
	et, err := a.EventType(ctx, b.EventTypeID)
	if err != nil {
		return err
	}
	host, err := a.User(ctx, b.OrganizerID)
	if err != nil {
		return err
	}
	if b.Status == StatusCancelled || b.Status == StatusRejected {
		return &Error{Code: ErrBookingClosed}
	}
	if !a.now().Before(b.End) {
		return &Error{Code: ErrBookingEnded}
	}
	isHost := actorID != 0
	if !isHost && !et.AllowCancel { // F-29: the organizer may always cancel
		return &Error{Code: ErrNotAllowed}
	}
	reason = strings.TrimSpace(reason)
	need := et.CancelReason == "required_both" || (isHost && et.CancelReason == "required_host") || (!isHost && et.CancelReason == "required_booker")
	if need && reason == "" {
		return &Error{Code: ErrReasonRequired}
	}
	by := b.BookerEmail
	if isHost {
		by = host.Email
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	was := b.Status
	if err := setStatus(ctx, tx, b, StatusCancelled); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookings SET cancel_reason = ?, cancelled_by = ? WHERE id = ?`, nullable(reason), by, b.ID); err != nil {
		return err
	}
	tx.ExecContext(ctx, `UPDATE action_tokens SET used_at = ? WHERE booking_id = ? AND used_at IS NULL`, a.now().Unix(), b.ID)
	if err := tx.Commit(); err != nil {
		return err
	}
	b.CancelReason, b.CancelledBy = reason, by
	if was == StatusAccepted {
		if err := a.Integrations.BookingCancelled(ctx, b); err != nil {
			a.Log.Warn("integration", "booking", b.UID, "err", err)
		}
	}
	a.notifyCancelled(et, host, b)
	return nil
}

// RescheduleInput moves a booking to a new start (BKG-12).
type RescheduleInput struct {
	UID      string
	Start    time.Time
	TimeZone string // booker's zone for slot alignment; "" = the booker's stored zone
	Reason   string
	BookerID string
	ActorID  int64 // organizer's user id, 0 = booker
}

// RescheduleBooking is BKG-12, BKG-13. Attendees, responses, location and duration carry over (F-30).
func (a *App) RescheduleBooking(ctx context.Context, in RescheduleInput) (*Booking, error) {
	old, err := a.Booking(ctx, in.UID)
	if err != nil {
		return nil, err
	}
	if in.ActorID != 0 && in.ActorID != old.OrganizerID {
		return nil, &Error{Code: ErrForbidden}
	}
	et, err := a.EventType(ctx, old.EventTypeID)
	if err != nil {
		return nil, err
	}
	host, err := a.User(ctx, old.OrganizerID)
	if err != nil {
		return nil, err
	}
	isHost := in.ActorID != 0
	now := a.now()
	switch {
	case !isHost && !et.AllowReschedule: // F-29
		return nil, &Error{Code: ErrNotAllowed}
	case old.Status == StatusCancelled || old.Status == StatusRejected:
		return nil, &Error{Code: ErrBookingClosed}
	case !old.Start.After(now):
		return nil, &Error{Code: ErrInPast}
	case !isHost && now.Add(time.Duration(et.MinRescheduleNotice)*time.Minute).After(old.Start):
		return nil, &Error{Code: ErrTooLate}
	}
	tz := in.TimeZone
	if tz == "" {
		tz = old.Booker().TimeZone
	}
	resp := Responses{}
	for k, v := range old.Responses {
		resp[k] = v
	}
	return a.book(ctx, bookArgs{
		et: et, host: host, start: in.Start, duration: int(old.End.Sub(old.Start) / time.Minute), tz: tz,
		resp: resp, atts: old.Attendees, loc: old.Location, bookerID: in.BookerID,
		old: old, actorID: in.ActorID, resReason: strings.TrimSpace(in.Reason),
	})
}

// MarkNoShow is BKG-18: who is an attendee email or "host"; only after the start, only for
// accepted bookings (F-24).
func (a *App) MarkNoShow(ctx context.Context, organizerID int64, uid, who string, noShow bool) error {
	b, err := a.organizerBooking(ctx, organizerID, uid)
	if err != nil {
		return err
	}
	if a.now().Before(b.Start) {
		return &Error{Code: ErrTooEarly}
	}
	if b.Status != StatusAccepted {
		return errf(ErrBadTransition, "only accepted bookings")
	}
	if who == "host" {
		_, err = a.DB.ExecContext(ctx, `UPDATE bookings SET host_no_show = ? WHERE id = ?`, noShow, b.ID)
		return err
	}
	res, err := a.DB.ExecContext(ctx, `UPDATE attendees SET no_show = ? WHERE booking_id = ? AND email = ?`, noShow, b.ID, strings.ToLower(who))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &Error{Code: ErrNotFound}
	}
	return nil
}

// --- emails (RB-4: logged, not sent) ---

func (a *App) when(b *Booking, tz string) string {
	loc, err := LoadZone(tz)
	if err != nil {
		loc = time.UTC
	}
	return fmt.Sprintf("%s – %s (%s)", b.Start.In(loc).Format("Mon 2 Jan 2006 15:04"), b.End.In(loc).Format("15:04"), tz)
}

func (a *App) manageLink(b *Booking) string { return a.BaseURL + "/booking/" + b.UID }

func (a *App) attendeeEmails(b *Booking) []string {
	var out []string
	for _, at := range b.Attendees {
		out = append(out, at.Email)
	}
	return out
}

func (a *App) notifyAccepted(et *EventType, host *User, b *Booking) {
	a.Mail.Send(Email{To: []string{host.Email}, ReplyTo: b.BookerEmail, Subject: "New booking: " + et.Title,
		Body: fmt.Sprintf("%s with %s\n%s", a.when(b, host.TimeZone), b.Booker().Name, a.manageLink(b))})
	a.Mail.Send(Email{To: a.attendeeEmails(b), ReplyTo: host.Email, Subject: "Confirmed: " + et.Title,
		Body: fmt.Sprintf("%s with %s\nManage (reschedule, cancel): %s\nCalendar file: %s.ics", a.when(b, b.Booker().TimeZone), host.Name, a.manageLink(b), a.manageLink(b))})
}

func (a *App) notifyRequested(et *EventType, host *User, b *Booking, tokens map[string]string) {
	a.Mail.Send(Email{To: []string{host.Email}, ReplyTo: b.BookerEmail, Subject: "Booking request: " + et.Title,
		Body: fmt.Sprintf("%s with %s\nConfirm: %s/action/%s\nReject: %s/action/%s", a.when(b, host.TimeZone), b.Booker().Name,
			a.BaseURL, tokens["confirm"], a.BaseURL, tokens["reject"])})
	a.Mail.Send(Email{To: []string{b.BookerEmail}, ReplyTo: host.Email, Subject: "Request received: " + et.Title,
		Body: fmt.Sprintf("%s with %s, awaiting confirmation\n%s", a.when(b, b.Booker().TimeZone), host.Name, a.manageLink(b))})
}

func (a *App) notifyRejected(et *EventType, host *User, b *Booking) {
	a.Mail.Send(Email{To: a.attendeeEmails(b), ReplyTo: host.Email, Subject: "Declined: " + et.Title,
		Body: fmt.Sprintf("%s\nReason: %s", a.when(b, b.Booker().TimeZone), b.RejectReason)})
}

func (a *App) notifyCancelled(et *EventType, host *User, b *Booking) {
	body := fmt.Sprintf("%s\nCancelled by %s. Reason: %s", a.when(b, host.TimeZone), b.CancelledBy, b.CancelReason)
	a.Mail.Send(Email{To: []string{host.Email}, ReplyTo: b.BookerEmail, Subject: "Cancelled: " + et.Title, Body: body})
	a.Mail.Send(Email{To: a.attendeeEmails(b), ReplyTo: host.Email, Subject: "Cancelled: " + et.Title, Body: body})
}

func (a *App) notifyRescheduled(et *EventType, host *User, old, b *Booking) {
	body := fmt.Sprintf("Was: %s\nNow: %s\n%s", a.when(old, host.TimeZone), a.when(b, host.TimeZone), a.manageLink(b))
	a.Mail.Send(Email{To: []string{host.Email}, ReplyTo: b.BookerEmail, Subject: "Rescheduled: " + et.Title, Body: body})
	a.Mail.Send(Email{To: a.attendeeEmails(b), ReplyTo: host.Email, Subject: "Rescheduled: " + et.Title, Body: body})
}
