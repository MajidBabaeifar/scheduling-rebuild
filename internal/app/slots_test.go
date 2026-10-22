package app

import (
	"reflect"
	"testing"
	"time"
)

// SLT-1: to < from → rejected; > 62 days → rejected; result keyed by booker-local date, starts in UTC.
func TestSLT_1(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, nil)
	q := SlotQuery{EventTypeID: et.ID, From: at("2026-11-03T00:00:00Z"), To: at("2026-11-02T00:00:00Z"), TimeZone: "UTC"}
	_, err := e.Slots(ctx, q)
	wantCode(t, err, ErrInvalidRange)
	q.From, q.To = at("2026-11-02T00:00:00Z"), at("2027-01-04T00:00:00Z")
	_, err = e.Slots(ctx, q)
	wantCode(t, err, ErrInvalidRange)
	q.To, q.TimeZone = at("2026-11-03T00:00:00Z"), "Nowhere"
	_, err = e.Slots(ctx, q)
	wantCode(t, err, ErrInvalidTimeZone)
	q.TimeZone = "Asia/Tokyo"
	m, err := e.Slots(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// 09:00–17:00 UTC Monday is 18:00 Mon – 02:00 Tue in Tokyo.
	if got := m.Dates(); !reflect.DeepEqual(got, []string{"2026-11-02", "2026-11-03"}) {
		t.Fatalf("dates %v", got)
	}
	if s := m["2026-11-02"][0].Start; s.Location() != time.UTC || s.Format(time.RFC3339) != "2026-11-02T09:00:00Z" {
		t.Fatalf("start %v", s)
	}
}

// SLT-2: busy 10–11 in 09–17 → 10:00 not offered.
func TestSLT_2(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.Length = 60 })
	e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	want := []string{"09:00", "11:00", "12:00", "13:00", "14:00", "15:00", "16:00"}
	if got := e.times(t, et, "UTC", "2026-11-02"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

// SLT-4: free 09:05–12:00, 60 min → 10:00, 11:00; alignment is to the hour in the booker's zone.
func TestSLT_4(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	sid, _ := e.CreateSchedule(ctx, u.ID, Schedule{Name: "odd", Overrides: []Override{{Date: "2026-11-02", Start: "09:05", End: "12:00"}}})
	et := e.event(t, u, func(et *EventType) { et.Length, et.ScheduleID = 60, &sid })
	if got := e.times(t, et, "UTC", "2026-11-02"); !reflect.DeepEqual(got, []string{"10:00", "11:00"}) {
		t.Fatalf("%v", got)
	}
	// Booker in India (UTC+5:30): their whole hours are :30 UTC.
	if got := e.times(t, et, "Asia/Kolkata", "2026-11-02"); !reflect.DeepEqual(got, []string{"15:00", "16:00"}) {
		t.Fatalf("Kolkata %v", got)
	}
	for iv, step := range map[int]int{60: 60, 90: 30, 45: 15, 40: 20, 25: 5, 7: 1, 120: 60} {
		if got := alignStep(time.Duration(iv) * time.Minute); got != time.Duration(step)*time.Minute {
			t.Errorf("interval %d: step %v", iv, got)
		}
	}
}

// SLT-5: free 09:00–10:00, 45 min, interval 30 → 09:00 only.
func TestSLT_5(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	sid, _ := e.CreateSchedule(ctx, u.ID, Schedule{Name: "short", Rules: []Rule{{Days: []int{1}, Start: "09:00", End: "10:00"}}})
	et := e.event(t, u, func(et *EventType) { et.Length, et.SlotInterval, et.ScheduleID = 45, 30, &sid })
	if got := e.times(t, et, "UTC", "2026-11-02"); !reflect.DeepEqual(got, []string{"09:00"}) {
		t.Fatalf("%v", got)
	}
}

// SLT-6: 60 min, interval 30, free 09–11 → 09:00, 09:30, 10:00.
func TestSLT_6(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	sid, _ := e.CreateSchedule(ctx, u.ID, Schedule{Name: "short", Rules: []Rule{{Days: []int{1}, Start: "09:00", End: "11:00"}}})
	et := e.event(t, u, func(et *EventType) { et.Length, et.SlotInterval, et.ScheduleID = 60, 30, &sid })
	if got := e.times(t, et, "UTC", "2026-11-02"); !reflect.DeepEqual(got, []string{"09:00", "09:30", "10:00"}) {
		t.Fatalf("%v", got)
	}
}

// SLT-7: a start offered by two ranges appears once (one host: overlapping rules; across a DST fall-back hour).
func TestSLT_7(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	sid, _ := e.CreateSchedule(ctx, u.ID, Schedule{Name: "dup", Rules: []Rule{{Days: []int{1}, Start: "09:00", End: "11:00"}, {Days: []int{1}, Start: "10:00", End: "12:00"}}})
	et := e.event(t, u, func(et *EventType) { et.Length, et.ScheduleID = 60, &sid })
	if got := e.times(t, et, "UTC", "2026-11-02"); !reflect.DeepEqual(got, []string{"09:00", "10:00", "11:00"}) {
		t.Fatalf("%v", got)
	}
}

// SLT-8: cancelled 10:00 → 10:00 offered; host as attendee of another booking → busy; rejected never busy.
func TestSLT_8(t *testing.T) {
	e := newEnv(t)
	ann, bob := e.user(t, "ann", "UTC"), e.user(t, "bob", "UTC")
	et := e.event(t, ann, func(et *EventType) { et.Length = 60 })
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	if err := e.CancelBooking(ctx, b.UID, 0, ""); err != nil {
		t.Fatal(err)
	}
	if !contains(e.times(t, et, "UTC", "2026-11-02"), "10:00") {
		t.Fatal("cancelled must not block")
	}
	bobs := e.event(t, bob, func(et *EventType) { et.Length = 60 })
	e.mustBook(t, bobs, "2026-11-02T13:00:00Z", "ann@example.com") // ann attends bob's meeting
	if contains(e.times(t, et, "UTC", "2026-11-02"), "13:00") {
		t.Fatal("host attending another booking must be busy")
	}
	conf := e.event(t, ann, func(et *EventType) {
		et.Slug, et.Length, et.RequiresConfirmation, et.PendingBlocksSlot = "conf", 60, true, true
	})
	p := e.mustBook(t, conf, "2026-11-02T15:00:00Z", "c@x.com")
	if contains(e.times(t, et, "UTC", "2026-11-02"), "15:00") {
		t.Fatal("pending with flag blocks")
	}
	if err := e.RejectBooking(ctx, ann.ID, p.UID, ""); err != nil {
		t.Fatal(err)
	}
	if !contains(e.times(t, et, "UTC", "2026-11-02"), "15:00") {
		t.Fatal("rejected must not block")
	}
}

// SLT-9: X 10:00–10:30 with A=0, E B=15 → 10:30 blocked, 10:45 ok. Buffers never shrink working hours.
func TestSLT_9(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	x := e.event(t, u, func(et *EventType) { et.Slug = "x" })
	ev := e.event(t, u, func(et *EventType) { et.Slug, et.Length, et.BufferBefore = "e", 15, 15 })
	e.mustBook(t, x, "2026-11-02T10:00:00Z", "a@x.com")
	got := e.times(t, ev, "UTC", "2026-11-02")
	if contains(got, "10:30") || !contains(got, "10:45") || !contains(got, "09:00") || !contains(got, "09:30") {
		t.Fatalf("%v", got)
	}
	if !contains(got, "09:45") { // E 09:45–10:00 ends where X starts; neither has a buffer on that side
		t.Fatalf("09:45 should be offered: %v", got)
	}
	// X's own after-buffer protects it from E without buffers.
	y := e.event(t, u, func(et *EventType) { et.Slug, et.BufferAfter = "y", 20 })
	plain := e.event(t, u, func(et *EventType) { et.Slug, et.Length = "plain", 15 })
	e.mustBook(t, y, "2026-11-02T14:00:00Z", "b@x.com")
	got = e.times(t, plain, "UTC", "2026-11-02")
	if contains(got, "14:30") || contains(got, "14:45") || !contains(got, "15:00") {
		t.Fatalf("after-buffer: %v", got)
	}
}

// SLT-12: booking 10–11, slot 11:00 → offered.
func TestSLT_12(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.Length = 60 })
	e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	got := e.times(t, et, "UTC", "2026-11-02")
	if !contains(got, "11:00") || !contains(got, "09:00") {
		t.Fatalf("%v", got)
	}
	if !(Interval{at("2026-11-02T10:00:00Z"), at("2026-11-02T11:00:00Z")}).Overlaps(Interval{at("2026-11-02T10:59:00Z"), at("2026-11-02T11:30:00Z")}) {
		t.Fatal("overlap")
	}
}

// SLT-13: day limit 2, two bookings Tue → no Tue slots. Minute limits count per host; weeks use the owner's week start.
func TestSLT_13(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.Limits.Bookings.Day = ptr(2) })
	e.mustBook(t, et, "2026-11-03T09:00:00Z", "a@x.com")
	e.mustBook(t, et, "2026-11-03T10:00:00Z", "b@x.com")
	if got := e.times(t, et, "UTC", "2026-11-03"); len(got) != 0 {
		t.Fatalf("Tue %v", got)
	}
	if len(e.times(t, et, "UTC", "2026-11-04")) == 0 {
		t.Fatal("Wed must stay open")
	}
	_, err := e.book(et, "2026-11-03T12:00:00Z", "c@x.com")
	wantCode(t, err, ErrLimitReached)

	// 90 minutes a week: 60 booked Mon → only 30-min fits; a 60-min duration doesn't.
	mins := e.event(t, u, func(et *EventType) {
		et.Slug, et.Length, et.Durations, et.Limits.Minutes.Week = "mins", 30, []int{30, 60}, ptr(90)
	})
	e.CreateBooking(ctx, BookingInput{EventTypeID: mins.ID, Start: at("2026-11-02T13:00:00Z"), Duration: 60, TimeZone: "UTC", Responses: form("name", "A", "email", "a@x.com")})
	if len(e.times(t, mins, "UTC", "2026-11-05")) == 0 {
		t.Fatal("30 min still fits")
	}
	if got := e.times(t, mins, "UTC", "2026-11-05", func(q *SlotQuery) { q.Duration = 60 }); len(got) != 0 {
		t.Fatalf("60 min must not fit: %v", got)
	}
	// Next week (Mon 9 Nov, owner's monday week start) is free again.
	if len(e.times(t, mins, "UTC", "2026-11-09", func(q *SlotQuery) { q.Duration = 60 })) == 0 {
		t.Fatal("next week free")
	}
}

// SLT-14: rolling 3 calendar days from Mon → no Fri slots; "today" is the booker's.
func TestSLT_14(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.Window = Window{Kind: "rolling", Days: 3, CalendarDays: true} })
	e.clk.set("2026-11-02T07:00:00Z") // Monday
	if len(e.times(t, et, "UTC", "2026-11-05")) == 0 {
		t.Fatal("Thu must be open")
	}
	if got := e.times(t, et, "UTC", "2026-11-06"); len(got) != 0 {
		t.Fatalf("Fri %v", got)
	}
	// For a booker in Tokyo it is already Monday evening; 09:00 UTC Fri is 18:00 Fri Tokyo, past their Thu.
	if got := e.times(t, et, "Asia/Tokyo", "2026-11-06"); len(got) != 0 {
		t.Fatalf("Tokyo Fri %v", got)
	}
	_, err := e.book(et, "2026-11-06T10:00:00Z", "a@x.com")
	wantCode(t, err, ErrOutsideWindow)
}

// SLT-15: A holds 10:00; B queries → no 10:00; A's own hold ignored; expires after 5 minutes.
func TestSLT_15(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, nil)
	if err := e.PlaceHold(ctx, et.ID, "booker-a", at("2026-11-02T10:00:00Z"), 0); err != nil {
		t.Fatal(err)
	}
	asB := func(q *SlotQuery) { q.BookerID = "booker-b" }
	asA := func(q *SlotQuery) { q.BookerID = "booker-a" }
	if contains(e.times(t, et, "UTC", "2026-11-02", asB), "10:00") {
		t.Fatal("B must not see A's held slot")
	}
	if !contains(e.times(t, et, "UTC", "2026-11-02", asA), "10:00") {
		t.Fatal("A sees own hold")
	}
	_, err := e.CreateBooking(ctx, BookingInput{EventTypeID: et.ID, Start: at("2026-11-02T10:00:00Z"), TimeZone: "UTC", BookerID: "booker-b", Responses: form("name", "B", "email", "b@x.com")})
	wantCode(t, err, ErrSlotTaken)
	e.clk.add(5 * time.Minute)
	if !contains(e.times(t, et, "UTC", "2026-11-02", asB), "10:00") {
		t.Fatal("hold must expire after 5 minutes")
	}
}

// SLT-17: reschedule 10:00 → 10:00 offered to that booker; the moved booking doesn't count towards limits.
func TestSLT_17(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, func(et *EventType) { et.Length, et.Limits.Bookings.Day = 60, ptr(1) })
	b := e.mustBook(t, et, "2026-11-02T10:00:00Z", "a@x.com")
	if got := e.times(t, et, "UTC", "2026-11-02"); len(got) != 0 {
		t.Fatalf("limit reached: %v", got)
	}
	got := e.times(t, et, "UTC", "2026-11-02", func(q *SlotQuery) { q.RescheduleUID = b.UID })
	if !contains(got, "10:00") || !contains(got, "14:00") {
		t.Fatalf("reschedule view: %v", got)
	}
	nb, err := e.RescheduleBooking(ctx, RescheduleInput{UID: b.UID, Start: at("2026-11-02T14:00:00Z")})
	if err != nil || nb.Status != StatusAccepted {
		t.Fatal(err)
	}
}

// SLT-18: query one day → one date key, though availability spans dates in the booker's zone.
func TestSLT_18(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	et := e.event(t, u, nil)
	m, err := e.slotsOn(et, "Asia/Tokyo", "2026-11-03")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Dates(); !reflect.DeepEqual(got, []string{"2026-11-03"}) {
		t.Fatalf("%v", got)
	}
	// [from, to): a slot starting at `to` is left out; dates without slots have no key.
	m, err = e.Slots(ctx, SlotQuery{EventTypeID: et.ID, From: at("2026-10-31T00:00:00Z"), To: at("2026-11-02T10:00:00Z"), TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Dates(); !reflect.DeepEqual(got, []string{"2026-11-02"}) || len(m["2026-11-02"]) != 2 {
		t.Fatalf("%v %v", got, m["2026-11-02"])
	}
}
