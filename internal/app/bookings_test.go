package app

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// BKG-1: create → uid of ≥ 16 URL-safe chars; the booking carries BKG-1's fields.
func TestBKG_1(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, nil)
	b, err := e.CreateBooking(ctx, BookingInput{EventTypeID: et.ID, Start: at("2026-11-02T10:00:00Z"), TimeZone: "Europe/Paris", Locale: "fr",
		Responses: form("name", "Zoé", "email", "Zoe@X.com", "notes", "hi")})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`).MatchString(b.UID) {
		t.Fatalf("uid %q", b.UID)
	}
	at0 := b.Booker()
	if b.OrganizerID != u.ID || b.EventTypeID != et.ID || b.Sequence != 0 || b.Paid || b.Status != StatusAccepted ||
		at0.Email != "zoe@x.com" || at0.TimeZone != "Europe/Paris" || at0.Locale != "fr" || at0.NoShow ||
		b.End.Sub(b.Start) != 30*time.Minute || b.Responses.Str("notes") != "hi" {
		t.Fatalf("%+v", b)
	}
}

// BKG-2: accepted → rejected → refused; only the listed transitions exist.
func TestBKG_2(t *testing.T) {
	allowed := map[string]bool{
		"awaiting_payment>pending": true, "awaiting_payment>accepted": true, "awaiting_payment>cancelled": true,
		"pending>accepted": true, "pending>rejected": true, "pending>cancelled": true, "accepted>cancelled": true,
	}
	all := []string{StatusAwaitingPayment, StatusPending, StatusAccepted, StatusRejected, StatusCancelled}
	for _, f := range all {
		for _, to := range all {
			if CanTransition(f, to) != allowed[f+">"+to] {
				t.Errorf("%s → %s", f, to)
			}
		}
	}
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	b := e.mustBook(t, e.event(t, u, nil), "2026-11-02T10:00:00Z", "a@x.com")
	wantCode(t, e.RejectBooking(ctx, u.ID, b.UID, ""), ErrNotPending)
	wantCode(t, setStatus(ctx, e.DB, b, StatusRejected), ErrBadTransition)
}

// BKG-3: required custom field empty → rejected; system fields can be hidden, not deleted.
func TestBKG_3(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	mk := func(slug string, fs ...Field) error {
		return e.tryEvent(u, func(et *EventType) { et.Slug, et.BookingFields = slug, fs })
	}
	wantCode(t, mk("a", Field{Name: "email", System: true, Hidden: true}), ErrInvalid)
	wantCode(t, mk("b", Field{Name: "company", Type: "dropdown", Label: "Company"}), ErrInvalid)
	wantCode(t, mk("c", Field{Name: "size", Type: "select", Label: "Size"}), ErrInvalid) // options required
	wantCode(t, mk("d", Field{Name: "notes", Type: "text", Label: "Clash"}), ErrInvalid)
	et := e.event(t, u, func(et *EventType) {
		et.BookingFields = []Field{
			{Name: "notes", System: true, Hidden: true},
			{Name: "company", Type: "text", Label: "Company", Required: true, MinLength: ptr(2), MaxLength: ptr(20)},
			{Name: "size", Type: "select", Label: "Size", Options: []string{"S", "M"}},
			{Name: "topics", Type: "checkbox", Label: "Topics", Options: []string{"a", "b"}},
			{Name: "agree", Type: "boolean", Label: "Agree", Required: true},
			{Name: "seats", Type: "number", Label: "Seats"},
			{Name: "site", Type: "url", Label: "Site"},
			{Name: "secret", Type: "text", Label: "Hidden", Required: true, Hidden: true},
		}
	})
	names := []string{}
	for _, f := range et.FormFields() {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "name,email,phone,location,notes,guests,reschedule_reason,company,size,topics,agree,seats,site,secret" {
		t.Fatalf("form %v", names)
	}
	base := []string{"agree", "on"}
	_, err := e.book(et, "2026-11-02T10:00:00Z", "a@x.com", base...)
	wantCode(t, err, ErrFieldRequired) // company
	for _, bad := range [][]string{
		{"company", "x"}, {"company", "Acme", "size", "XL"}, {"company", "Acme", "topics", "c"},
		{"company", "Acme", "seats", "two"}, {"company", "Acme", "site", "ftp://x"},
	} {
		_, err := e.book(et, "2026-11-02T10:00:00Z", "a@x.com", append(base, bad...)...)
		wantCode(t, err, ErrFieldInvalid)
	}
	_, err = e.book(et, "2026-11-02T10:00:00Z", "a@x.com", "company", "Acme")
	wantCode(t, err, ErrFieldRequired) // agree
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com", "company", "Acme", "agree", "on", "topics", "a", "topics", "b", "seats", "3")
	if b.Responses.Str("company") != "Acme" || b.Responses["agree"] != true || len(b.Responses.List("topics")) != 2 || b.Responses["seats"] != 3.0 {
		t.Fatalf("%+v", b.Responses)
	}
}

// BKG-4: guest list with a duplicate → stored once; hosts dropped; emails, phones, domains validated.
func TestBKG_4(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) {
		et.BookingFields = []Field{
			{Name: "email", System: true, Required: true, BlockDomains: []string{"spam.com"}},
			{Name: "phone", System: true},
			{Name: "work", Type: "email", Label: "Work email", AllowDomains: []string{"acme.com"}},
		}
	})
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com", "guests", "G@x.com", "guests", "g@x.com", "guests", "ann@example.com", "guests", "a@x.com", "guests", "h@x.com")
	var emails []string
	for _, at := range b.Attendees {
		emails = append(emails, at.Email)
	}
	if strings.Join(emails, ",") != "a@x.com,g@x.com,h@x.com" {
		t.Fatalf("attendees %v", emails)
	}
	for _, bad := range [][]string{{"guests", "not-an-email"}, {"phone", "0151 1234"}, {"work", "bob@other.com"}} {
		_, err := e.book(et, "2026-11-02T11:00:00Z", "b@x.com", bad...)
		wantCode(t, err, ErrFieldInvalid)
	}
	_, err := e.book(et, "2026-11-02T11:00:00Z", "b@spam.com")
	wantCode(t, err, ErrFieldInvalid)
	_, err = e.book(et, "2026-11-02T11:00:00Z", "b@x", "x", "y")
	wantCode(t, err, ErrFieldInvalid)
	e.mustBook(t, et, "2026-11-02T11:00:00Z", "b@x.com", "phone", "+14155550123", "work", "bob@ACME.com")
}

// BKG-5: event without locations → video link created; one location → used.
func TestBKG_5(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	b := e.mustBook(t, e.event(t, u, nil), "2026-11-02T10:00:00Z", "a@x.com")
	if b.Location.Kind != "video" || b.Location.Value != "http://test/video/"+b.UID {
		t.Fatalf("%+v", b.Location)
	}
	one := e.event(t, u, func(et *EventType) { et.Slug, et.Locations = "one", []Location{{Kind: "link", Value: "https://meet.example/ann"}} })
	b = e.mustBook(t, one, "2026-11-02T11:00:00Z", "a@x.com")
	if b.Location.Value != "https://meet.example/ann" {
		t.Fatalf("%+v", b.Location)
	}
}

// BKG-6: two bookers race for 10:00 → one succeeds, other gets `slot_taken`; each failure has its own code, in order.
func TestBKG_6(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) {
		et.Length, et.MinNotice, et.Window, et.Limits.Bookings.Day = 60, 60, Window{Kind: "rolling", Days: 10, CalendarDays: true}, ptr(3)
	})
	e.clk.set("2026-11-02T08:00:00Z")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = e.book(et, "2026-11-02T10:00:00Z", []string{"a@x.com", "b@x.com"}[i])
		}(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) || Code(errs[0])+Code(errs[1]) != ErrSlotTaken {
		t.Fatalf("race: %v / %v", errs[0], errs[1])
	}

	_, err := e.CreateBooking(ctx, BookingInput{EventTypeID: 999, Start: at("2026-11-02T10:00:00Z"), TimeZone: "UTC"})
	wantCode(t, err, ErrEventNotFound)
	_, err = e.CreateBooking(ctx, BookingInput{EventTypeID: et.ID, Duration: 15, Start: at("2026-11-02T10:00:00Z"), TimeZone: "UTC"})
	wantCode(t, err, ErrInvalidDuration)
	for start, code := range map[string]string{
		"2026-11-02T07:00:00Z": ErrInPast,
		"2026-11-02T08:30:00Z": ErrTooSoon,
		"2026-11-20T10:00:00Z": ErrOutsideWindow,
		"2026-11-02T10:00:00Z": ErrSlotTaken,
		"2026-11-02T18:00:00Z": ErrUnavailable, // outside hours
		"2026-11-02T11:30:00Z": ErrUnavailable, // off the slot grid (F-22)
	} {
		_, err := e.book(et, start, "c@x.com")
		if Code(err) != code {
			t.Errorf("%s: want %s, got %v", start, code, err)
		}
	}
	e.mustBook(t, et, "2026-11-02T11:00:00Z", "c@x.com")
	e.mustBook(t, et, "2026-11-02T12:00:00Z", "d@x.com")
	_, err = e.book(et, "2026-11-02T13:00:00Z", "e@x.com")
	wantCode(t, err, ErrLimitReached)
}

// BKG-7: concurrent identical requests → one booking; enforced atomically, freed by cancel.
func TestBKG_7(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.book(et, "2026-11-02T10:00:00Z", "a@x.com"); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	var n int
	e.DB.QueryRow(`SELECT count(*) FROM bookings WHERE status = 'accepted'`).Scan(&n)
	if ok != 1 || n != 1 {
		t.Fatalf("ok=%d rows=%d", ok, n)
	}
	// The unique index holds even when slot checks are bypassed: two pending at one exact slot, confirm both.
	conf := e.event(t, u, func(et *EventType) { et.Slug, et.RequiresConfirmation = "conf", true })
	p1 := e.mustBook(t, conf, "2026-11-03T10:00:00Z", "p@x.com")
	p2 := e.mustBook(t, conf, "2026-11-03T10:00:00Z", "q@x.com")
	if err := e.ConfirmBooking(ctx, u.ID, p1.UID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.ConfirmBooking(ctx, u.ID, p2.UID), ErrSlotTaken) // F-28
	b, _ := e.Booking(ctx, p1.UID)
	if err := e.CancelBooking(ctx, b.UID, u.ID, "freeing"); err != nil {
		t.Fatal(err)
	}
	if err := e.ConfirmBooking(ctx, u.ID, p2.UID); err != nil {
		t.Fatal(err)
	}
}

// BKG-8: submit twice → same uid while the first is pending.
func TestBKG_8(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.RequiresConfirmation = true })
	a := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "A@x.com")
	if a.UID != b.UID {
		t.Fatalf("%s != %s", a.UID, b.UID)
	}
	var n int
	e.DB.QueryRow(`SELECT count(*) FROM bookings`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows %d", n)
	}
}

// BKG-9: free event, no confirmation → accepted; confirmation → pending; organizer reschedule → accepted.
func TestBKG_9(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	if b := e.mustBook(t, e.event(t, u, nil), "2026-11-02T10:00:00Z", "a@x.com"); b.Status != StatusAccepted {
		t.Fatal(b.Status)
	}
	conf := e.event(t, u, func(et *EventType) { et.Slug, et.RequiresConfirmation = "conf", true })
	b := e.mustBook(t, conf, "2026-11-02T11:00:00Z", "a@x.com")
	if b.Status != StatusPending {
		t.Fatal(b.Status)
	}
	nb, err := e.RescheduleBooking(ctx, RescheduleInput{UID: b.UID, Start: at("2026-11-02T13:00:00Z"), ActorID: u.ID})
	if err != nil || nb.Status != StatusAccepted {
		t.Fatalf("%v %v", nb, err)
	}
	nb2, err := e.RescheduleBooking(ctx, RescheduleInput{UID: nb.UID, Start: at("2026-11-02T14:00:00Z")})
	if err != nil || nb2.Status != StatusPending {
		t.Fatalf("booker reschedule: %v %v", nb2, err)
	}
}

// BKG-10: calendar API down → booking accepted, organizer sees warning.
func TestBKG_10(t *testing.T) {
	e := newEnv(t)
	e.Integrations = failingIntegrations{}
	u := e.user(t, "ann", "UTC")
	b := e.mustBook(t, e.event(t, u, nil), "2026-11-02T10:00:00Z", "a@x.com")
	if b.Status != StatusAccepted || len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "calendar API down") {
		t.Fatalf("%+v", b)
	}
	list, _ := e.OrganizerBookings(ctx, u.ID)
	if len(list) != 1 || len(list[0].Warnings) != 1 {
		t.Fatal("organizer list must show the warning")
	}
	if e.mail.find("Confirmed:") == nil {
		t.Fatal("still notified")
	}
}

var tokenRe = regexp.MustCompile(`/action/([A-Za-z0-9_-]+)`)

// BKG-11: reuse a confirm link → refused; confirm/reject by the organizer; non-pending → error. (RB-4: links in the logged email.)
func TestBKG_11(t *testing.T) {
	e := newEnv(t)
	ann, bob := e.user(t, "ann", "UTC"), e.user(t, "bob", "UTC")
	et := e.event(t, ann, func(et *EventType) { et.RequiresConfirmation = true })
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	req := e.mail.find("Booking request:")
	if req == nil || req.To[0] != "ann@example.com" || req.ReplyTo != "a@x.com" {
		t.Fatalf("request email %+v", req)
	}
	links := tokenRe.FindAllStringSubmatch(req.Body, -1)
	if len(links) != 2 {
		t.Fatalf("links in %q", req.Body)
	}
	confirm, reject := links[0][1], links[1][1]
	if tk, _ := e.LookupActionToken(ctx, confirm); tk.Action != "confirm" {
		t.Fatal("first link confirms")
	}
	if _, err := e.UseActionToken(ctx, confirm, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Booking(ctx, b.UID); got.Status != StatusAccepted {
		t.Fatal(got.Status)
	}
	_, err := e.UseActionToken(ctx, confirm, "")
	wantCode(t, err, ErrTokenUsed)
	_, err = e.UseActionToken(ctx, reject, "")
	wantCode(t, err, ErrTokenUsed)
	_, err = e.UseActionToken(ctx, "made-up", "")
	wantCode(t, err, ErrTokenUsed)
	wantCode(t, e.ConfirmBooking(ctx, ann.ID, b.UID), ErrNotPending)

	b2 := e.mustBook(t, et, "2026-11-02T11:00:00Z", "c@x.com")
	wantCode(t, e.RejectBooking(ctx, bob.ID, b2.UID, ""), ErrForbidden)
	if err := e.RejectBooking(ctx, ann.ID, b2.UID, "fully booked"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Booking(ctx, b2.UID)
	if got.Status != StatusRejected || got.RejectReason != "fully booked" {
		t.Fatalf("%+v", got)
	}
	if d := e.mail.find("Declined:"); d == nil || !strings.Contains(d.Body, "fully booked") {
		t.Fatal("declined email")
	}
}

// BKG-12: reschedule → old cancelled, new links back; sequence + 1, same calendar UID, paid carried.
func TestBKG_12(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, nil)
	old := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com", "guests", "g@x.com", "notes", "agenda")
	e.DB.Exec(`UPDATE bookings SET paid = 1 WHERE id = ?`, old.ID)
	nb, err := e.RescheduleBooking(ctx, RescheduleInput{UID: old.UID, Start: at("2026-11-03T10:00:00Z"), Reason: "clash"})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := e.Booking(ctx, old.UID)
	if o.Status != StatusCancelled || !o.Rescheduled || o.CancelledBy != "a@x.com" {
		t.Fatalf("old %+v", o)
	}
	if nb.FromReschedule != old.UID || nb.Sequence != 1 || nb.ICalUID != old.ICalUID || !nb.Paid || len(nb.Attendees) != 2 ||
		nb.Responses.Str("notes") != "agenda" || nb.Responses.Str("reschedule_reason") != "clash" || nb.End.Sub(nb.Start) != 30*time.Minute {
		t.Fatalf("new %+v", nb)
	}
	if r, _ := e.RescheduledTo(ctx, old.UID); r == nil || r.UID != nb.UID {
		t.Fatal("RescheduledTo")
	}
	if e.mail.find("Rescheduled:") == nil {
		t.Fatal("email")
	}
}

// BKG-13: reschedule 1 h before start, notice 2 h → refused (booker); also cancelled, past, disallowed.
func TestBKG_13(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.MinRescheduleNotice = 120 })
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	e.clk.set("2026-11-02T09:00:00Z")
	_, err := e.RescheduleBooking(ctx, RescheduleInput{UID: b.UID, Start: at("2026-11-02T14:00:00Z")})
	wantCode(t, err, ErrTooLate)
	nb, err := e.RescheduleBooking(ctx, RescheduleInput{UID: b.UID, Start: at("2026-11-02T14:00:00Z"), ActorID: u.ID})
	if err != nil {
		t.Fatal("organizer is not bound by the notice:", err)
	}
	_, err = e.RescheduleBooking(ctx, RescheduleInput{UID: b.UID, Start: at("2026-11-02T15:00:00Z"), ActorID: u.ID})
	wantCode(t, err, ErrBookingClosed)
	e.clk.set("2026-11-02T14:10:00Z")
	_, err = e.RescheduleBooking(ctx, RescheduleInput{UID: nb.UID, Start: at("2026-11-03T15:00:00Z"), ActorID: u.ID})
	wantCode(t, err, ErrInPast)
}

// BKG-15: cancel after end → refused; booker cancels via uid; reason rules per EVT-16.
func TestBKG_15(t *testing.T) {
	e := newEnv(t)
	ann, bob := e.user(t, "ann", "UTC"), e.user(t, "bob", "UTC")
	et := e.event(t, ann, nil) // cancel_reason required_host
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	wantCode(t, e.CancelBooking(ctx, b.UID, ann.ID, ""), ErrReasonRequired)
	wantCode(t, e.CancelBooking(ctx, b.UID, bob.ID, "x"), ErrForbidden)
	if err := e.CancelBooking(ctx, b.UID, 0, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Booking(ctx, b.UID)
	if got.Status != StatusCancelled || got.CancelledBy != "a@x.com" {
		t.Fatalf("%+v", got)
	}
	wantCode(t, e.CancelBooking(ctx, b.UID, 0, ""), ErrBookingClosed)
	if m := e.mail.find("Cancelled:"); m == nil {
		t.Fatal("email")
	}

	both := e.event(t, ann, func(et *EventType) { et.Slug, et.CancelReason = "both", "required_booker" })
	b2 := e.mustBook(t, both, "2026-11-02T11:00:00Z", "a@x.com")
	wantCode(t, e.CancelBooking(ctx, b2.UID, 0, " "), ErrReasonRequired)
	e.clk.set("2026-11-02T10:45:00Z") // during the meeting: still cancellable
	if err := e.CancelBooking(ctx, b2.UID, ann.ID, ""); err != nil {
		t.Fatal(err)
	}
	b3 := e.mustBook(t, both, "2026-11-02T12:00:00Z", "a@x.com")
	e.clk.set("2026-11-02T12:30:00Z")
	wantCode(t, e.CancelBooking(ctx, b3.UID, 0, "late"), ErrBookingEnded)
}

// BKG-18: mark 1 min before start → refused; after start the organizer marks an attendee or the host.
func TestBKG_18(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	b := e.mustBook(t, e.event(t, u, nil), "2026-11-02T10:00:00Z", "a@x.com")
	e.clk.set("2026-11-02T09:59:00Z")
	wantCode(t, e.MarkNoShow(ctx, u.ID, b.UID, "a@x.com", true), ErrTooEarly)
	e.clk.set("2026-11-02T10:05:00Z")
	if err := e.MarkNoShow(ctx, u.ID, b.UID, "A@x.com", true); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkNoShow(ctx, u.ID, b.UID, "host", true); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.MarkNoShow(ctx, u.ID, b.UID, "z@x.com", true), ErrNotFound)
	got, _ := e.Booking(ctx, b.UID)
	if !got.Attendees[0].NoShow || !got.HostNoShow {
		t.Fatalf("%+v", got)
	}
}
