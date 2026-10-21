package app

import (
	"reflect"
	"strings"
	"testing"
)

func (e *env) tryEvent(u *User, mut func(*EventType)) error {
	et := NewEventType()
	et.Title, et.Slug = "Intro", "intro"
	mut(&et)
	_, err := e.CreateEventType(ctx, u.ID, et)
	return err
}

// EVT-1: create with length 0 → rejected.
func TestEVT_1(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	for name, mut := range map[string]func(*EventType){
		"length 0":  func(et *EventType) { et.Length = 0 },
		"no title":  func(et *EventType) { et.Title = " " },
		"long":      func(et *EventType) { et.Title = strings.Repeat("x", 201) },
		"bad slug":  func(et *EventType) { et.Slug = "Intro Call" },
		"long slug": func(et *EventType) { et.Slug = strings.Repeat("a", 65) },
	} {
		if err := e.tryEvent(u, mut); Code(err) != ErrInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	et := e.event(t, u, nil)
	if et.Hidden || et.UserID != u.ID || et.Length != 30 {
		t.Fatalf("%+v", et)
	}
}

// EVT-2: second `/intro` for same user → rejected; same slug for another user → ok.
func TestEVT_2(t *testing.T) {
	e := newEnv(t)
	ann, bob := e.user(t, "ann", "UTC"), e.user(t, "bob", "UTC")
	e.event(t, ann, nil)
	wantCode(t, e.tryEvent(ann, func(*EventType) {}), ErrTaken)
	e.event(t, bob, nil)
}

// EVT-3: durations [15,30]; book 45 min → rejected (slot queries too).
func TestEVT_3(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Length, et.Durations = 30, []int{15, 45} }), ErrInvalid) // must include length
	et := e.event(t, u, func(et *EventType) { et.Length, et.Durations = 30, []int{30, 15} })
	_, err := e.slotsOn(et, "UTC", "2026-11-02", func(q *SlotQuery) { q.Duration = 45 })
	wantCode(t, err, ErrInvalidDuration)
	_, err = e.CreateBooking(ctx, BookingInput{EventTypeID: et.ID, Start: at("2026-11-02T10:00:00Z"), Duration: 45, TimeZone: "UTC", Responses: form("name", "B", "email", "b@x.com")})
	wantCode(t, err, ErrInvalidDuration)
	if got := e.times(t, et, "UTC", "2026-11-02", func(q *SlotQuery) { q.Duration = 15 }); len(got) != 32 {
		t.Fatalf("15-min slots: %d", len(got))
	}
	b, err := e.CreateBooking(ctx, BookingInput{EventTypeID: et.ID, Start: at("2026-11-02T10:00:00Z"), Duration: 15, TimeZone: "UTC", Responses: form("name", "B", "email", "b@x.com")})
	if err != nil || b.End.Sub(b.Start).Minutes() != 15 {
		t.Fatalf("%v %v", b, err)
	}
	single := e.event(t, u, func(et *EventType) { et.Slug = "single" })
	_, err = e.slotsOn(single, "UTC", "2026-11-02", func(q *SlotQuery) { q.Duration = 45 })
	wantCode(t, err, ErrInvalidDuration)
}

// EVT-4: length 60, interval 30 → overlapping slots 09:00, 09:30.
func TestEVT_4(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.Length, et.SlotInterval = 60, 30 })
	got := e.times(t, et, "UTC", "2026-11-02")
	if len(got) < 2 || got[0] != "09:00" || got[1] != "09:30" || got[len(got)-1] != "16:00" {
		t.Fatalf("%v", got)
	}
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Slug, et.SlotInterval = "x", -1 }), ErrInvalid)
}

// EVT-5: now 10:00, notice 120 → first slot ≥ 12:00.
func TestEVT_5(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	if NewEventType().MinNotice != 120 {
		t.Fatal("default notice")
	}
	et := e.event(t, u, func(et *EventType) { et.MinNotice = 120 })
	e.clk.set("2026-11-02T10:00:00Z")
	if got := e.times(t, et, "UTC", "2026-11-02"); got[0] != "12:00" {
		t.Fatalf("%v", got)
	}
}

// EVT-6: buffers default 0, never negative; their effect is SLT-9.
func TestEVT_6(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	if et := NewEventType(); et.BufferBefore != 0 || et.BufferAfter != 0 {
		t.Fatal("defaults")
	}
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.BufferAfter = -5 }), ErrInvalid)
	et := e.event(t, u, func(et *EventType) { et.BufferBefore, et.BufferAfter = 10, 5 })
	got, _ := e.EventType(ctx, et.ID)
	if got.BufferBefore != 10 || got.BufferAfter != 5 {
		t.Fatalf("%+v", got)
	}
}

func lastDate(m SlotMap) string { ds := m.Dates(); return ds[len(ds)-1] }

// EVT-7: rolling 5 business days from Fri → last bookable day is next Fri. Also range and rolling_available.
func TestEVT_7(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	everyDay := Schedule{Name: "All week", Rules: []Rule{{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "09:00", End: "17:00"}}}
	sid, _ := e.CreateSchedule(ctx, u.ID, everyDay)
	e.clk.set("2026-10-23T06:00:00Z") // Friday
	query := func(et *EventType) SlotMap {
		m, err := e.Slots(ctx, SlotQuery{EventTypeID: et.ID, From: at("2026-10-23T00:00:00Z"), To: at("2026-11-23T00:00:00Z"), TimeZone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	rolling := e.event(t, u, func(et *EventType) {
		et.Slug, et.ScheduleID, et.Window = "rolling", &sid, Window{Kind: "rolling", Days: 5}
	})
	if got := lastDate(query(rolling)); got != "2026-10-30" {
		t.Fatalf("rolling business: last %s", got)
	}
	cal := e.event(t, u, func(et *EventType) {
		et.Slug, et.ScheduleID, et.Window = "cal", &sid, Window{Kind: "rolling", Days: 5, CalendarDays: true}
	})
	if got := lastDate(query(cal)); got != "2026-10-28" {
		t.Fatalf("rolling calendar: last %s", got)
	}
	rng := e.event(t, u, func(et *EventType) {
		et.Slug, et.ScheduleID, et.Window = "range", &sid, Window{Kind: "range", StartDate: "2026-10-26", EndDate: "2026-10-27"}
	})
	if got := query(rng).Dates(); !reflect.DeepEqual(got, []string{"2026-10-26", "2026-10-27"}) {
		t.Fatalf("range: %v", got)
	}
	// rolling_available counts only days with a free slot: time off Sat–Mon pushes the end out.
	e.AddTimeOff(ctx, u.ID, TimeOff{StartDate: "2026-10-24", EndDate: "2026-10-26", Reason: "away"})
	avail := e.event(t, u, func(et *EventType) {
		et.Slug, et.ScheduleID, et.Window = "avail", &sid, Window{Kind: "rolling_available", Days: 2}
	})
	if got := query(avail).Dates(); !reflect.DeepEqual(got, []string{"2026-10-23", "2026-10-27", "2026-10-28"}) {
		t.Fatalf("rolling_available: %v", got)
	}
	wantCode(t, e.tryEvent(u, func(et *EventType) {
		et.Slug, et.Window = "w", Window{Kind: "range", StartDate: "2026-10-27", EndDate: "2026-10-26"}
	}), ErrInvalid)
}

// EVT-8: save day 5, week 3 → rejected.
func TestEVT_8(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Limits.Bookings = PeriodLimits{Day: ptr(5), Week: ptr(3)} }), ErrInvalid)
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Limits.Minutes = PeriodLimits{Day: ptr(120), Year: ptr(60)} }), ErrInvalid) // F-11
	if err := e.tryEvent(u, func(et *EventType) { et.Limits.Bookings = PeriodLimits{Day: ptr(3), Week: ptr(5), Year: ptr(100)} }); err != nil {
		t.Fatal(err)
	}
}

// EVT-10: threshold 24 h; book 3 days ahead → accepted; book in 2 h → pending.
func TestEVT_10(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) {
		et.RequiresConfirmation, et.ConfirmWithin = true, &ConfirmWithin{Value: 24, Unit: "hours"}
	})
	e.clk.set("2026-11-02T08:00:00Z")
	if b := e.mustBook(t, et, "2026-11-05T10:00:00Z", "a@x.com"); b.Status != StatusAccepted {
		t.Fatalf("3 days ahead: %s", b.Status)
	}
	if b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "b@x.com"); b.Status != StatusPending {
		t.Fatalf("in 2 h: %s", b.Status)
	}
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Slug, et.ConfirmWithin = "x", &ConfirmWithin{Value: 1, Unit: "days"} }), ErrInvalid)
}

// EVT-11: pending 10:00, flag on → 10:00 not offered.
func TestEVT_11(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	on := e.event(t, u, func(et *EventType) {
		et.Slug, et.Length, et.RequiresConfirmation, et.PendingBlocksSlot = "on", 60, true, true
	})
	off := e.event(t, u, func(et *EventType) { et.Slug, et.Length, et.RequiresConfirmation = "off", 60, true })
	e.mustBook(t, off, "2026-11-02T11:00:00Z", "a@x.com")
	if got := e.times(t, on, "UTC", "2026-11-02"); !contains(got, "11:00") {
		t.Fatal("pending without flag must not block")
	}
	e.mustBook(t, on, "2026-11-02T10:00:00Z", "a@x.com")
	if got := e.times(t, off, "UTC", "2026-11-02"); contains(got, "10:00") {
		t.Fatalf("pending with flag must block: %v", got)
	}
}

// EVT-13: hidden event → not listed; direct URL → slots shown.
func TestEVT_13(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	e.event(t, u, func(et *EventType) { et.Slug, et.Position = "b", 2 })
	e.event(t, u, func(et *EventType) { et.Slug, et.Position = "a", 1 })
	h := e.event(t, u, func(et *EventType) { et.Slug, et.Hidden = "secret", true })
	pub, _ := e.EventTypes(ctx, u.ID, true)
	if len(pub) != 2 || pub[0].Slug != "a" || pub[1].Slug != "b" {
		t.Fatalf("public list: %v", pub)
	}
	got, err := e.EventTypeBySlug(ctx, u.ID, "secret")
	if err != nil || got.ID != h.ID {
		t.Fatal(err)
	}
	if len(e.times(t, h, "UTC", "2026-11-02")) == 0 {
		t.Fatal("hidden event must stay bookable")
	}
}

// EVT-14: two locations → booker must choose.
func TestEVT_14(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Locations = []Location{{Kind: "in_person"}} }), ErrInvalid)
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.Locations = []Location{{Kind: "carrier_pigeon"}} }), ErrInvalid)
	et := e.event(t, u, func(et *EventType) {
		et.Locations = []Location{{Kind: "in_person", Value: "1 Main St"}, {Kind: "phone_booker"}}
	})
	_, err := e.book(et, "2026-11-02T10:00:00Z", "a@x.com")
	wantCode(t, err, ErrLocation)
	_, err = e.book(et, "2026-11-02T10:00:00Z", "a@x.com", "location", "phone_booker")
	wantCode(t, err, ErrFieldRequired)
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com", "location", "phone_booker", "phone", "+4915112345678")
	if b.Location != (Location{Kind: "phone_booker", Value: "+4915112345678"}) {
		t.Fatalf("%+v", b.Location)
	}
	f := et.FormFields()
	if f[3].Name != "location" || f[3].Hidden {
		t.Fatal("location field must be shown with two locations")
	}
}

// EVT-16: allow_cancel false → booker cancel link refused. Defaults: allow both, reason required_host.
func TestEVT_16(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	if d := NewEventType(); !d.AllowCancel || !d.AllowReschedule || d.CancelReason != "required_host" {
		t.Fatalf("%+v", d)
	}
	wantCode(t, e.tryEvent(u, func(et *EventType) { et.CancelReason = "never" }), ErrInvalid)
	et := e.event(t, u, func(et *EventType) { et.AllowCancel, et.AllowReschedule = false, false })
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	wantCode(t, e.CancelBooking(ctx, b.UID, 0, "can't make it"), ErrNotAllowed)
	_, err := e.RescheduleBooking(ctx, RescheduleInput{UID: b.UID, Start: at("2026-11-02T11:00:00Z")})
	wantCode(t, err, ErrNotAllowed)
	if err := e.CancelBooking(ctx, b.UID, u.ID, "host away"); err != nil { // F-29
		t.Fatal(err)
	}
}
