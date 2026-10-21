package app

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Window is EVT-7.
type Window struct {
	Kind         string `json:"kind"` // unlimited | rolling | rolling_available | range
	Days         int    `json:"days,omitempty"`
	CalendarDays bool   `json:"calendar_days,omitempty"`
	StartDate    string `json:"start_date,omitempty"`
	EndDate      string `json:"end_date,omitempty"`
}

// PeriodLimits is one EVT-8 limit set; nil = not set.
type PeriodLimits struct {
	Day   *int `json:"day,omitempty"`
	Week  *int `json:"week,omitempty"`
	Month *int `json:"month,omitempty"`
	Year  *int `json:"year,omitempty"`
}

func (p PeriodLimits) list() []*int { return []*int{p.Day, p.Week, p.Month, p.Year} }

// Limits is EVT-8.
type Limits struct {
	Bookings PeriodLimits `json:"bookings"`
	Minutes  PeriodLimits `json:"minutes"`
}

// ConfirmWithin is EVT-10's threshold.
type ConfirmWithin struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"` // minutes | hours
}

// Minutes of the threshold.
func (c ConfirmWithin) Minutes() int {
	if c.Unit == "hours" {
		return c.Value * 60
	}
	return c.Value
}

// Location is EVT-14.
type Location struct {
	Kind  string `json:"kind"` // in_person | link | phone_host | phone_booker | video
	Value string `json:"value,omitempty"`
}

// EventType is EVT-1..8, 10, 11, 13, 14, 16, booking fields (BKG-3) and schedule (EVT-19).
type EventType struct {
	ID                   int64          `json:"-"`
	UserID               int64          `json:"-"`
	Title                string         `json:"title"`
	Slug                 string         `json:"slug"`
	Description          string         `json:"description,omitempty"`
	Length               int            `json:"length"`
	Durations            []int          `json:"durations,omitempty"`
	SlotInterval         int            `json:"slot_interval,omitempty"` // 0 = chosen duration
	MinNotice            int            `json:"min_notice"`
	BufferBefore         int            `json:"buffer_before"`
	BufferAfter          int            `json:"buffer_after"`
	Window               Window         `json:"window"`
	Limits               Limits         `json:"limits"`
	RequiresConfirmation bool           `json:"requires_confirmation"`
	ConfirmWithin        *ConfirmWithin `json:"confirm_within,omitempty"`
	PendingBlocksSlot    bool           `json:"pending_blocks_slot"`
	Hidden               bool           `json:"hidden"`
	Position             int            `json:"position"`
	Locations            []Location     `json:"locations,omitempty"`
	BookingFields        []Field        `json:"booking_fields,omitempty"`
	AllowCancel          bool           `json:"allow_cancel"`
	AllowReschedule      bool           `json:"allow_reschedule"`
	MinRescheduleNotice  int            `json:"min_reschedule_notice,omitempty"` // minutes, 0 = none
	CancelReason         string         `json:"cancel_reason"`
	ScheduleID           *int64         `json:"schedule_id,omitempty"`
}

// NewEventType returns an event type with the spec's defaults.
func NewEventType() EventType {
	return EventType{
		Length: 30, MinNotice: 120, Window: Window{Kind: "unlimited"},
		AllowCancel: true, AllowReschedule: true, CancelReason: "required_host",
	}
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// AllowedDurations is EVT-3: the list, or just the length.
func (et *EventType) AllowedDurations() []int {
	if len(et.Durations) == 0 {
		return []int{et.Length}
	}
	return et.Durations
}

// Validate is EVT-1..8, 10, 14, 16 and the booking fields of BKG-3.
func (et *EventType) Validate() error {
	et.Title = strings.TrimSpace(et.Title)
	if n := utf8.RuneCountInString(et.Title); n < 1 || n > 200 {
		return errf(ErrInvalid, "title: 1-200 characters")
	}
	if !slugRe.MatchString(et.Slug) {
		return errf(ErrInvalid, "slug: 1-64 of a-z, 0-9, -")
	}
	if et.Length < 1 {
		return errf(ErrInvalid, "length: at least 1 minute")
	}
	if len(et.Durations) > 0 {
		seen, hasLength := map[int]bool{}, false
		var ds []int
		for _, d := range et.Durations {
			if d < 1 {
				return errf(ErrInvalid, "durations: each at least 1")
			}
			if !seen[d] {
				seen[d] = true
				ds = append(ds, d)
			}
			hasLength = hasLength || d == et.Length
		}
		if !hasLength {
			return errf(ErrInvalid, "durations must include length")
		}
		sort.Ints(ds)
		et.Durations = ds
	}
	if et.SlotInterval < 0 {
		return errf(ErrInvalid, "slot_interval: at least 1")
	}
	if et.MinNotice < 0 || et.BufferBefore < 0 || et.BufferAfter < 0 || et.MinRescheduleNotice < 0 {
		return errf(ErrInvalid, "notice and buffers: at least 0")
	}
	switch et.Window.Kind {
	case "", "unlimited":
		et.Window = Window{Kind: "unlimited"}
	case "rolling", "rolling_available":
		if et.Window.Days < 1 {
			return errf(ErrInvalid, "window days: at least 1")
		}
	case "range":
		s, err1 := ParseDate(et.Window.StartDate)
		e, err2 := ParseDate(et.Window.EndDate)
		if err1 != nil || err2 != nil || e.Before(s) {
			return errf(ErrInvalid, "window range: start_date <= end_date")
		}
	default:
		return errf(ErrInvalid, "window kind %q", et.Window.Kind)
	}
	for _, pl := range []PeriodLimits{et.Limits.Bookings, et.Limits.Minutes} {
		vals := pl.list()
		for i, v := range vals {
			if v == nil {
				continue
			}
			if *v < 1 {
				return errf(ErrInvalid, "limits: at least 1")
			}
			for _, w := range vals[i+1:] { // F-11: every larger set period
				if w != nil && *v > *w {
					return errf(ErrInvalid, "limits: a period's limit can't exceed a longer period's")
				}
			}
		}
	}
	if c := et.ConfirmWithin; c != nil {
		if c.Value < 1 || (c.Unit != "minutes" && c.Unit != "hours") {
			return errf(ErrInvalid, "confirm_within: value >= 1, unit minutes|hours")
		}
	}
	for _, l := range et.Locations {
		switch l.Kind {
		case "in_person", "link", "phone_host":
			if strings.TrimSpace(l.Value) == "" {
				return errf(ErrInvalid, "location %s needs a value", l.Kind)
			}
		case "phone_booker", "video":
		default:
			return errf(ErrInvalid, "location kind %q", l.Kind)
		}
	}
	switch et.CancelReason {
	case "":
		et.CancelReason = "required_host"
	case "required_host", "required_booker", "required_both", "optional":
	default:
		return errf(ErrInvalid, "cancel_reason %q", et.CancelReason)
	}
	return validateFields(et.BookingFields)
}

func saveEventType(ctx context.Context, q queryer, et *EventType) error {
	data, _ := json.Marshal(et)
	_, err := q.ExecContext(ctx, `UPDATE event_types SET slug = ?, data = ? WHERE id = ?`, et.Slug, string(data), et.ID)
	return err
}

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "UNIQUE") }

// CreateEventType stores a new event type for userID (EVT-2: slug unique per owner).
func (a *App) CreateEventType(ctx context.Context, userID int64, et EventType) (*EventType, error) {
	if err := et.Validate(); err != nil {
		return nil, err
	}
	if err := a.checkScheduleOwner(ctx, userID, et.ScheduleID); err != nil {
		return nil, err
	}
	et.UserID = userID
	data, _ := json.Marshal(et)
	res, err := a.DB.ExecContext(ctx, `INSERT INTO event_types (user_id, slug, data) VALUES (?, ?, ?)`, userID, et.Slug, string(data))
	if isUnique(err) {
		return nil, errf(ErrTaken, "slug")
	}
	if err != nil {
		return nil, err
	}
	et.ID, _ = res.LastInsertId()
	return &et, nil
}

// UpdateEventType replaces an event type's settings.
func (a *App) UpdateEventType(ctx context.Context, userID int64, et EventType) error {
	if err := et.Validate(); err != nil {
		return err
	}
	if err := a.checkScheduleOwner(ctx, userID, et.ScheduleID); err != nil {
		return err
	}
	old, err := a.EventType(ctx, et.ID)
	if err != nil || old.UserID != userID {
		return &Error{Code: ErrNotFound}
	}
	et.UserID = userID
	err = saveEventType(ctx, a.DB, &et)
	if isUnique(err) {
		return errf(ErrTaken, "slug")
	}
	return err
}

func (a *App) checkScheduleOwner(ctx context.Context, userID int64, id *int64) error {
	if id == nil {
		return nil
	}
	if _, err := a.Schedule(ctx, userID, *id); err != nil {
		return errf(ErrInvalid, "schedule")
	}
	return nil
}

// DeleteEventType removes an event type that has no bookings.
func (a *App) DeleteEventType(ctx context.Context, userID, id int64) error {
	var n int
	a.DB.QueryRowContext(ctx, `SELECT count(*) FROM bookings WHERE event_type_id = ?`, id).Scan(&n)
	if n > 0 {
		return errf(ErrInvalid, "event type has bookings; hide it instead")
	}
	_, err := a.DB.ExecContext(ctx, `DELETE FROM event_types WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

func loadEventTypes(ctx context.Context, q queryer, where string, args ...any) ([]*EventType, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, user_id, data FROM event_types WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EventType
	for rows.Next() {
		var et EventType
		var data string
		if err := rows.Scan(&et.ID, &et.UserID, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &et); err != nil {
			return nil, err
		}
		out = append(out, &et)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out, rows.Err()
}

func eventType(ctx context.Context, q queryer, id int64) (*EventType, error) {
	ets, err := loadEventTypes(ctx, q, `id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(ets) == 0 {
		return nil, &Error{Code: ErrEventNotFound}
	}
	return ets[0], nil
}

// EventType loads an event type by id.
func (a *App) EventType(ctx context.Context, id int64) (*EventType, error) {
	return eventType(ctx, a.DB, id)
}

// EventTypeBySlug loads userID's event type by slug.
func (a *App) EventTypeBySlug(ctx context.Context, userID int64, slug string) (*EventType, error) {
	ets, err := loadEventTypes(ctx, a.DB, `user_id = ? AND slug = ?`, userID, slug)
	if err != nil {
		return nil, err
	}
	if len(ets) == 0 {
		return nil, &Error{Code: ErrEventNotFound}
	}
	return ets[0], nil
}

// EventTypes lists a user's event types in display order; public = without hidden ones (EVT-13).
func (a *App) EventTypes(ctx context.Context, userID int64, public bool) ([]*EventType, error) {
	ets, err := loadEventTypes(ctx, a.DB, `user_id = ?`, userID)
	if err != nil || !public {
		return ets, err
	}
	var out []*EventType
	for _, et := range ets {
		if !et.Hidden {
			out = append(out, et)
		}
	}
	return out, nil
}
