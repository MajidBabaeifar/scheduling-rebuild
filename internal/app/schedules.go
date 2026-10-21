package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Rule is a weekly rule (AVL-2): days 0..6 (0 = Sunday), wall-clock HH:MM.
type Rule struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Override replaces the weekly rules on one date (AVL-7); Start == End marks the date unavailable.
type Override struct {
	Date  string `json:"date"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Schedule is AVL-2.
type Schedule struct {
	ID        int64
	UserID    int64
	Name      string
	TimeZone  string // "" = owner's zone
	IsDefault bool
	Rules     []Rule
	Overrides []Override
}

// DefaultSchedule is AVL-3's Mon–Fri 09:00–17:00.
func DefaultSchedule() Schedule {
	return Schedule{Name: "Working hours", Rules: []Rule{{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}}}
}

func parseHM(s string) (int, int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, errf(ErrInvalid, "time %q: want HH:MM", s)
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || m < 0 || h > 23 || m > 59 {
		return 0, 0, errf(ErrInvalid, "time %q: want HH:MM", s)
	}
	return h, m, nil
}

// Validate is AVL-2: weekly rules need end > start; overrides allow start == end (F-7).
func (s *Schedule) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len(s.Name) > 100 {
		return errf(ErrInvalid, "name: 1-100 characters")
	}
	if s.TimeZone != "" {
		if _, err := LoadZone(s.TimeZone); err != nil {
			return err
		}
	}
	for _, r := range s.Rules {
		if len(r.Days) == 0 {
			return errf(ErrInvalid, "rule needs at least one day")
		}
		for _, d := range r.Days {
			if d < 0 || d > 6 {
				return errf(ErrInvalid, "day %d: want 0..6", d)
			}
		}
		if err := checkRange(r.Start, r.End, false); err != nil {
			return err
		}
	}
	for _, o := range s.Overrides {
		if _, err := ParseDate(o.Date); err != nil {
			return err
		}
		if err := checkRange(o.Start, o.End, true); err != nil {
			return err
		}
	}
	return nil
}

func checkRange(start, end string, allowEmpty bool) error {
	sh, sm, err := parseHM(start)
	if err != nil {
		return err
	}
	eh, em, err := parseHM(end)
	if err != nil {
		return err
	}
	a, b := sh*60+sm, eh*60+em
	if b < a || (b == a && !allowEmpty) {
		return errf(ErrInvalid, "end %s must be after start %s", end, start)
	}
	return nil
}

func insertSchedule(ctx context.Context, q queryer, userID int64, s Schedule, isDefault bool) error {
	rules, _ := json.Marshal(nonNil(s.Rules))
	ovs, _ := json.Marshal(nonNil(s.Overrides))
	_, err := q.ExecContext(ctx, `INSERT INTO schedules (user_id, name, time_zone, is_default, rules, overrides) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, s.Name, s.TimeZone, isDefault, string(rules), string(ovs))
	return err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// CreateSchedule adds a schedule; it becomes default only if the user has none (AVL-3, F-8).
func (a *App) CreateSchedule(ctx context.Context, userID int64, s Schedule) (int64, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schedules WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, err
	}
	if err := insertSchedule(ctx, tx, userID, s, n == 0); err != nil {
		return 0, err
	}
	var id int64
	tx.QueryRowContext(ctx, `SELECT last_insert_rowid()`).Scan(&id)
	return id, tx.Commit()
}

// UpdateSchedule replaces name, zone, rules and overrides.
func (a *App) UpdateSchedule(ctx context.Context, userID int64, s Schedule) error {
	if err := s.Validate(); err != nil {
		return err
	}
	rules, _ := json.Marshal(nonNil(s.Rules))
	ovs, _ := json.Marshal(nonNil(s.Overrides))
	res, err := a.DB.ExecContext(ctx, `UPDATE schedules SET name = ?, time_zone = ?, rules = ?, overrides = ? WHERE id = ? AND user_id = ?`,
		s.Name, s.TimeZone, string(rules), string(ovs), s.ID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &Error{Code: ErrNotFound}
	}
	return nil
}

// SetDefaultSchedule makes id the user's one default (AVL-3, F-8).
func (a *App) SetDefaultSchedule(ctx context.Context, userID, id int64) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE schedules SET is_default = (id = ?) WHERE user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	var n int
	tx.QueryRowContext(ctx, `SELECT count(*) FROM schedules WHERE user_id = ? AND is_default = 1`, userID).Scan(&n)
	if c, _ := res.RowsAffected(); c == 0 || n != 1 {
		return &Error{Code: ErrNotFound}
	}
	return tx.Commit()
}

// DeleteSchedule is AVL-4: the last one is refused; deleting the default promotes the oldest
// remaining schedule; event types that pointed at the deleted one now point at the default (F-9).
func (a *App) DeleteSchedule(ctx context.Context, userID, id int64) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var isDefault bool
	err = tx.QueryRowContext(ctx, `SELECT is_default FROM schedules WHERE id = ? AND user_id = ?`, id, userID).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: ErrNotFound}
	}
	if err != nil {
		return err
	}
	var n int
	tx.QueryRowContext(ctx, `SELECT count(*) FROM schedules WHERE user_id = ?`, userID).Scan(&n)
	if n <= 1 {
		return &Error{Code: ErrLastSchedule}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id); err != nil {
		return err
	}
	if isDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE schedules SET is_default = 1 WHERE id = (SELECT min(id) FROM schedules WHERE user_id = ?)`, userID); err != nil {
			return err
		}
	}
	var def int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM schedules WHERE user_id = ? AND is_default = 1`, userID).Scan(&def); err != nil {
		return err
	}
	ets, err := loadEventTypes(ctx, tx, `user_id = ?`, userID)
	if err != nil {
		return err
	}
	for _, et := range ets {
		if et.ScheduleID != nil && *et.ScheduleID == id {
			et.ScheduleID = &def
			if err := saveEventType(ctx, tx, et); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

const scheduleCols = `id, user_id, name, time_zone, is_default, rules, overrides`

func scanSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	var s Schedule
	var rules, ovs string
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &s.TimeZone, &s.IsDefault, &rules, &ovs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: ErrNotFound}
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(rules), &s.Rules)
	json.Unmarshal([]byte(ovs), &s.Overrides)
	return &s, nil
}

// Schedules lists a user's schedules, default first.
func (a *App) Schedules(ctx context.Context, userID int64) ([]*Schedule, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE user_id = ? ORDER BY is_default DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Schedule loads one of the user's schedules.
func (a *App) Schedule(ctx context.Context, userID, id int64) (*Schedule, error) {
	return scanSchedule(a.DB.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = ? AND user_id = ?`, id, userID))
}

// scheduleFor is AVL-5 for an individual event: the event's schedule, else the host's default.
func scheduleFor(ctx context.Context, q queryer, et *EventType, hostID int64) (*Schedule, error) {
	if et.ScheduleID != nil {
		s, err := scanSchedule(q.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = ? AND user_id = ?`, *et.ScheduleID, hostID))
		if err == nil {
			return s, nil
		}
		if Code(err) != ErrNotFound {
			return nil, err
		}
	}
	return scanSchedule(q.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE user_id = ? AND is_default = 1`, hostID))
}

// Ranges expands the schedule to UTC intervals for local dates [from, to] in loc (AVL-6..8).
func (s *Schedule) Ranges(loc *time.Location, from, to Date) []Interval {
	byDate := map[string][]Override{}
	for _, o := range s.Overrides {
		byDate[o.Date] = append(byDate[o.Date], o)
	}
	var out []Interval
	add := func(d Date, start, end string) {
		sh, sm, err1 := parseHM(start)
		eh, em, err2 := parseHM(end)
		if err1 != nil || err2 != nil || eh*60+em <= sh*60+sm {
			return
		}
		a := WallTime(d, sh, sm, loc)
		var b time.Time
		if end == "23:59" { // AVL-6: 23:59 means midnight
			b = StartOfDay(d.AddDays(1), loc)
		} else {
			b = WallTime(d, eh, em, loc)
		}
		if b.After(a) {
			out = append(out, Interval{a, b})
		}
	}
	for d := from; !d.After(to); d = d.AddDays(1) {
		if ovs, ok := byDate[d.String()]; ok {
			for _, o := range ovs {
				add(d, o.Start, o.End)
			}
			continue
		}
		wd := int(d.Weekday())
		for _, r := range s.Rules {
			for _, day := range r.Days {
				if day == wd {
					add(d, r.Start, r.End)
				}
			}
		}
	}
	return mergeIntervals(out)
}

// TimeOff is AVL-10: inclusive local dates in the owner's zone (F-10).
type TimeOff struct {
	ID         int64
	UserID     int64
	StartDate  string
	EndDate    string
	Reason     string
	Note       string
	NotePublic bool
}

// AddTimeOff records time off.
func (a *App) AddTimeOff(ctx context.Context, userID int64, t TimeOff) (int64, error) {
	s, err := ParseDate(t.StartDate)
	if err != nil {
		return 0, err
	}
	e, err := ParseDate(t.EndDate)
	if err != nil {
		return 0, err
	}
	if e.Before(s) {
		return 0, errf(ErrInvalid, "end before start")
	}
	if strings.TrimSpace(t.Reason) == "" {
		return 0, errf(ErrInvalid, "reason required")
	}
	res, err := a.DB.ExecContext(ctx, `INSERT INTO time_off (user_id, start_date, end_date, reason, note, note_public) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, t.StartDate, t.EndDate, strings.TrimSpace(t.Reason), t.Note, t.NotePublic)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteTimeOff removes one of the user's time-off entries.
func (a *App) DeleteTimeOff(ctx context.Context, userID, id int64) error {
	_, err := a.DB.ExecContext(ctx, `DELETE FROM time_off WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// TimeOffs lists a user's time off.
func (a *App) TimeOffs(ctx context.Context, userID int64) ([]TimeOff, error) {
	return timeOffs(ctx, a.DB, userID)
}

func timeOffs(ctx context.Context, q queryer, userID int64) ([]TimeOff, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, user_id, start_date, end_date, reason, note, note_public FROM time_off WHERE user_id = ? ORDER BY start_date`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimeOff
	for rows.Next() {
		var t TimeOff
		if err := rows.Scan(&t.ID, &t.UserID, &t.StartDate, &t.EndDate, &t.Reason, &t.Note, &t.NotePublic); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Interval of a time-off entry in loc.
func (t TimeOff) Interval(loc *time.Location) Interval {
	s, _ := ParseDate(t.StartDate)
	e, _ := ParseDate(t.EndDate)
	return Interval{StartOfDay(s, loc), StartOfDay(e.AddDays(1), loc)}
}
