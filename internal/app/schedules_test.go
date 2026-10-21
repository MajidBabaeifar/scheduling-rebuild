package app

import (
	"reflect"
	"testing"
	"time"
)

// AVL-1: signup without zone → rejected; week start defaults to monday.
func TestAVL_1(t *testing.T) {
	e := newEnv(t)
	for _, tz := range []string{"", "Local", "Mars/Olympus"} {
		_, err := signup(e, "a@x.com", "ann", goodPassword, tz)
		wantCode(t, err, ErrInvalid)
	}
	u := e.user(t, "ann", "Europe/Berlin")
	if u.TimeZone != "Europe/Berlin" || u.WeekStart != "monday" {
		t.Fatalf("%+v", u)
	}
}

// AVL-2: create schedule with end ≤ start → rejected.
func TestAVL_2(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	for _, r := range []Rule{{Days: []int{1}, Start: "17:00", End: "09:00"}, {Days: []int{1}, Start: "09:00", End: "09:00"}, {Days: []int{7}, Start: "09:00", End: "10:00"}, {Days: []int{1}, Start: "9:00", End: "10:00"}} {
		_, err := e.CreateSchedule(ctx, u.ID, Schedule{Name: "x", Rules: []Rule{r}})
		wantCode(t, err, ErrInvalid)
	}
	_, err := e.CreateSchedule(ctx, u.ID, Schedule{Name: "x", Overrides: []Override{{Date: "2026-11-02", Start: "10:00", End: "09:00"}}})
	wantCode(t, err, ErrInvalid)
	_, err = e.CreateSchedule(ctx, u.ID, Schedule{Name: "x", TimeZone: "Nowhere/City"})
	wantCode(t, err, ErrInvalidTimeZone)
	// F-7: an override with start == end is allowed (AVL-7 marks the date unavailable).
	if _, err := e.CreateSchedule(ctx, u.ID, Schedule{Name: "x", TimeZone: "Asia/Tokyo", Rules: []Rule{{Days: []int{0, 6}, Start: "10:00", End: "14:00"}},
		Overrides: []Override{{Date: "2026-11-02", Start: "00:00", End: "00:00"}}}); err != nil {
		t.Fatal(err)
	}
}

// AVL-3: new user → one default schedule (Mon–Fri 09:00–17:00); always exactly one default.
func TestAVL_3(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	ss, _ := e.Schedules(ctx, u.ID)
	if len(ss) != 1 || !ss[0].IsDefault || !reflect.DeepEqual(ss[0].Rules, DefaultSchedule().Rules) {
		t.Fatalf("%+v", ss)
	}
	id, err := e.CreateSchedule(ctx, u.ID, Schedule{Name: "Evenings", Rules: []Rule{{Days: []int{1}, Start: "18:00", End: "20:00"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetDefaultSchedule(ctx, u.ID, id); err != nil {
		t.Fatal(err)
	}
	ss, _ = e.Schedules(ctx, u.ID)
	defaults := 0
	for _, s := range ss {
		if s.IsDefault {
			defaults++
			if s.ID != id {
				t.Fatal("wrong default")
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("defaults=%d", defaults)
	}
}

// AVL-4: delete only schedule → refused; deleting the default promotes another and repoints references.
func TestAVL_4(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	ss, _ := e.Schedules(ctx, u.ID)
	first := ss[0].ID
	wantCode(t, e.DeleteSchedule(ctx, u.ID, first), ErrLastSchedule)
	second, _ := e.CreateSchedule(ctx, u.ID, Schedule{Name: "B", Rules: []Rule{{Days: []int{1}, Start: "18:00", End: "20:00"}}})
	et := e.event(t, u, func(et *EventType) { et.ScheduleID = &first })
	if err := e.DeleteSchedule(ctx, u.ID, first); err != nil {
		t.Fatal(err)
	}
	ss, _ = e.Schedules(ctx, u.ID)
	if len(ss) != 1 || ss[0].ID != second || !ss[0].IsDefault {
		t.Fatalf("%+v", ss)
	}
	got, _ := e.EventType(ctx, et.ID)
	if got.ScheduleID == nil || *got.ScheduleID != second {
		t.Fatalf("event schedule %v, want %d", got.ScheduleID, second)
	}
}

// AVL-5: event without schedule → host default used; with one → that one.
func TestAVL_5(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	evening, _ := e.CreateSchedule(ctx, u.ID, Schedule{Name: "Evenings", Rules: []Rule{{Days: []int{1}, Start: "18:00", End: "19:00"}}})
	plain := e.event(t, u, func(et *EventType) { et.Length = 60 })
	if got := e.times(t, plain, "UTC", "2026-11-02"); len(got) != 8 || got[0] != "09:00" {
		t.Fatalf("default schedule: %v", got)
	}
	ev := e.event(t, u, func(et *EventType) { et.Slug, et.Length, et.ScheduleID = "evening", 60, &evening })
	if got := e.times(t, ev, "UTC", "2026-11-02"); !reflect.DeepEqual(got, []string{"18:00"}) {
		t.Fatalf("event schedule: %v", got)
	}
}

func hm(ivs []Interval, loc *time.Location) [][2]string {
	var out [][2]string
	for _, iv := range ivs {
		out = append(out, [2]string{iv.Start.In(loc).Format("01-02 15:04"), iv.End.In(loc).Format("01-02 15:04")})
	}
	return out
}

// AVL-6: 09–12 and 12–17 → one range 09–17; 23:59 means midnight.
func TestAVL_6(t *testing.T) {
	s := Schedule{Rules: []Rule{{Days: []int{1}, Start: "09:00", End: "12:00"}, {Days: []int{1}, Start: "12:00", End: "17:00"}, {Days: []int{2}, Start: "20:00", End: "23:59"}, {Days: []int{3}, Start: "00:00", End: "02:00"}}}
	d, _ := ParseDate("2026-11-02")
	got := hm(s.Ranges(time.UTC, d, d.AddDays(2)), time.UTC)
	want := [][2]string{{"11-02 09:00", "11-02 17:00"}, {"11-03 20:00", "11-04 02:00"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// AVL-7: Mon rule 09–17, override Mon 13–14 → only 13–14; several overrides → several ranges; start == end → none.
func TestAVL_7(t *testing.T) {
	s := Schedule{Rules: []Rule{{Days: []int{1, 2, 3}, Start: "09:00", End: "17:00"}}, Overrides: []Override{
		{Date: "2026-11-02", Start: "13:00", End: "14:00"},
		{Date: "2026-11-03", Start: "08:00", End: "09:00"}, {Date: "2026-11-03", Start: "15:00", End: "16:00"},
		{Date: "2026-11-04", Start: "00:00", End: "00:00"},
	}}
	d, _ := ParseDate("2026-11-02")
	got := hm(s.Ranges(time.UTC, d, d.AddDays(2)), time.UTC)
	want := [][2]string{{"11-02 13:00", "11-02 14:00"}, {"11-03 08:00", "11-03 09:00"}, {"11-03 15:00", "11-03 16:00"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// AVL-8: Europe/Berlin, last Sun of March, 09–17 → 07:00–15:00 UTC.
func TestAVL_8(t *testing.T) {
	berlin, _ := LoadZone("Europe/Berlin")
	s := Schedule{Rules: []Rule{{Days: []int{0, 6}, Start: "09:00", End: "17:00"}}}
	d, _ := ParseDate("2027-03-27") // Sat (CET) and Sun (CEST)
	got := hm(s.Ranges(berlin, d, d.AddDays(1)), time.UTC)
	want := [][2]string{{"03-27 08:00", "03-27 16:00"}, {"03-28 07:00", "03-28 15:00"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// F-12: a wall time skipped by the gap uses the offset before it; a repeated one the earlier instant.
	if got := WallTime(Date{2027, 3, 28}, 2, 30, berlin).Format(time.RFC3339); got != "2027-03-28T01:30:00Z" {
		t.Errorf("gap: %s", got)
	}
	if got := WallTime(Date{2026, 10, 25}, 2, 30, berlin).Format(time.RFC3339); got != "2026-10-25T00:30:00Z" {
		t.Errorf("repeat: %s", got)
	}
}

// AVL-10: time off Tue → Tue has no bookable slots.
func TestAVL_10(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "America/New_York")
	et := e.event(t, u, nil)
	if _, err := e.AddTimeOff(ctx, u.ID, TimeOff{StartDate: "2026-11-03", EndDate: "2026-11-03", Reason: "vacation"}); err != nil {
		t.Fatal(err)
	}
	if got := e.times(t, et, "America/New_York", "2026-11-03"); len(got) != 0 {
		t.Fatalf("Tue: %v", got)
	}
	if got := e.times(t, et, "America/New_York", "2026-11-04"); len(got) == 0 {
		t.Fatal("Wed should have slots")
	}
	_, err := e.AddTimeOff(ctx, u.ID, TimeOff{StartDate: "2026-11-05", EndDate: "2026-11-04", Reason: "x"})
	wantCode(t, err, ErrInvalid)
}
