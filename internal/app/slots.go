package app

import (
	"context"
	"slices"
	"sort"
	"time"
)

// SlotQuery is SLT-1. From/To are instants; slots starting in [From, To) are returned (F-17).
type SlotQuery struct {
	EventTypeID   int64
	From, To      time.Time
	TimeZone      string // booker's IANA zone
	Duration      int    // 0 = event length
	RescheduleUID string
	BookerID      string // whose own hold is ignored (SLT-15, F-18)
}

// Slot is one offered start.
type Slot struct {
	Start time.Time `json:"start"`
}

// SlotMap maps booker-local YYYY-MM-DD to slots (SLT-1).
type SlotMap map[string][]Slot

// Dates returns the map's keys in order.
func (m SlotMap) Dates() []string {
	var ds []string
	for d := range m {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	return ds
}

// HoldDuration is SLT-15.
const HoldDuration = 5 * time.Minute

// MaxSlotRange is SLT-1's 62 days.
const MaxSlotRange = 62 * 24 * time.Hour

// Slots answers a slot query (SLT-1..18 in scope).
func (a *App) Slots(ctx context.Context, q SlotQuery) (SlotMap, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxSlotRange {
		return nil, errf(ErrInvalidRange, "to must be after from, at most 62 days")
	}
	e, err := a.newEngine(ctx, a.DB, q.EventTypeID, q.TimeZone, q.Duration, q.RescheduleUID, q.BookerID)
	if err != nil {
		return nil, err
	}
	starts, err := e.candidates(q.From, q.To, allChecks)
	if err != nil {
		return nil, err
	}
	out := SlotMap{}
	for _, s := range starts {
		d := DateOf(s, e.bookerLoc).String()
		out[d] = append(out[d], Slot{Start: s})
	}
	return out, nil
}

type checks struct{ busy, notice, window, limits bool }

var allChecks = checks{busy: true, notice: true, window: true, limits: true}

// engine computes slots for one event type, host, booker zone and duration.
type engine struct {
	ctx       context.Context
	q         queryer
	et        *EventType
	host      *User
	sched     *Schedule
	hostLoc   *time.Location
	schedLoc  *time.Location
	bookerLoc *time.Location
	duration  time.Duration
	interval  time.Duration
	now       time.Time
	exclude   string
	bookerID  string

	etCache     map[int64]*EventType
	limitCache  map[string]limitUse
	windowLast  *Date
	windowFound bool
}

type limitUse struct {
	count   int
	minutes int
}

func (a *App) newEngine(ctx context.Context, q queryer, etID int64, tz string, duration int, rescheduleUID, bookerID string) (*engine, error) {
	et, err := eventType(ctx, q, etID)
	if err != nil {
		return nil, err
	}
	host, err := scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, et.UserID))
	if err != nil {
		return nil, err
	}
	if duration == 0 {
		duration = et.Length
	}
	if !slices.Contains(et.AllowedDurations(), duration) { // EVT-3
		return nil, errf(ErrInvalidDuration, "%d minutes not offered", duration)
	}
	bookerLoc, err := LoadZone(tz)
	if err != nil {
		return nil, err
	}
	hostLoc, err := LoadZone(host.TimeZone)
	if err != nil {
		return nil, err
	}
	sched, err := scheduleFor(ctx, q, et, host.ID)
	if err != nil {
		return nil, err
	}
	schedLoc := hostLoc
	if sched.TimeZone != "" {
		if schedLoc, err = LoadZone(sched.TimeZone); err != nil {
			return nil, err
		}
	}
	interval := et.SlotInterval // EVT-4
	if interval == 0 {
		interval = duration
	}
	return &engine{
		ctx: ctx, q: q, et: et, host: host, sched: sched,
		hostLoc: hostLoc, schedLoc: schedLoc, bookerLoc: bookerLoc,
		duration: time.Duration(duration) * time.Minute, interval: time.Duration(interval) * time.Minute,
		now: a.now(), exclude: rescheduleUID, bookerID: bookerID,
		etCache: map[int64]*EventType{et.ID: et}, limitCache: map[string]limitUse{},
	}, nil
}

// alignStep is SLT-4: the first of 60, 30, 20, 15, 10, 5 dividing the interval, else 1.
func alignStep(interval time.Duration) time.Duration {
	m := int(interval / time.Minute)
	for _, s := range []int{60, 30, 20, 15, 10, 5} {
		if m%s == 0 {
			return time.Duration(s) * time.Minute
		}
	}
	return time.Minute
}

// alignUp rounds t up to a multiple of step past the hour in loc (SLT-4).
func alignUp(t time.Time, step time.Duration, loc *time.Location) time.Time {
	l := t.In(loc)
	hour := t.Add(-time.Duration(l.Minute())*time.Minute - time.Duration(l.Second())*time.Second - time.Duration(l.Nanosecond()))
	k := (t.Sub(hour) + step - 1) / step
	return hour.Add(k * step)
}

// candidates returns offered starts in [from, to), applying the given checks.
func (e *engine) candidates(from, to time.Time, c checks) ([]time.Time, error) {
	padFrom, padTo := from.Add(-48*time.Hour), to.Add(48*time.Hour)
	ranges := e.sched.Ranges(e.schedLoc, DateOf(padFrom, e.schedLoc), DateOf(padTo, e.schedLoc))

	busy, err := e.timeOff()
	if err != nil {
		return nil, err
	}
	if c.busy {
		b, err := e.busy(padFrom, padTo)
		if err != nil {
			return nil, err
		}
		busy = append(busy, b...)
	}
	free := subtractIntervals(ranges, busy) // SLT-2

	earliest := e.now
	if c.notice {
		earliest = e.now.Add(time.Duration(e.et.MinNotice) * time.Minute) // EVT-5
	}
	step := alignStep(e.interval)
	var out []time.Time
	seen := map[int64]bool{}
	for _, r := range free {
		s := r.Start
		if s.Before(earliest) {
			s = earliest
		}
		for t := alignUp(s, step, e.bookerLoc); !t.Add(e.duration).After(r.End); t = t.Add(e.interval) { // SLT-5, SLT-6
			if t.Before(from) || !t.Before(to) || seen[t.Unix()] { // SLT-7, SLT-18
				continue
			}
			if c.window {
				ok, err := e.inWindow(t)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			if c.limits {
				ok, err := e.underLimits(t)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			seen[t.Unix()] = true
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}

func (e *engine) timeOff() ([]Interval, error) {
	tos, err := timeOffs(e.ctx, e.q, e.host.ID)
	if err != nil {
		return nil, err
	}
	var out []Interval
	for _, t := range tos {
		out = append(out, t.Interval(e.hostLoc)) // AVL-10
	}
	return out, nil
}

func (e *engine) eventTypeOf(id int64) (*EventType, error) {
	if et, ok := e.etCache[id]; ok {
		return et, nil
	}
	et, err := eventType(e.ctx, e.q, id)
	if err != nil {
		return nil, err
	}
	e.etCache[id] = et
	return et, nil
}

// busy is SLT-8, SLT-9 and SLT-15: bookings expanded by both sides' buffers, and others' holds.
func (e *engine) busy(from, to time.Time) ([]Interval, error) {
	margin := 7 * 24 * time.Hour
	rows, err := e.q.QueryContext(e.ctx, `
		SELECT uid, event_type_id, organizer_id, start_at, end_at, status FROM bookings
		WHERE status IN ('accepted', 'pending') AND start_at < ? AND end_at > ?
		  AND (organizer_id = ? OR id IN (SELECT booking_id FROM attendees WHERE email = ?))`,
		to.Add(margin).Unix(), from.Add(-margin).Unix(), e.host.ID, e.host.Email)
	if err != nil {
		return nil, err
	}
	type row struct {
		uid         string
		etID, orgID int64
		s, en       int64
		status      string
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.uid, &r.etID, &r.orgID, &r.s, &r.en, &r.status); err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	B := time.Duration(e.et.BufferBefore) * time.Minute
	A := time.Duration(e.et.BufferAfter) * time.Minute
	var out []Interval
	for _, r := range rs {
		if r.uid == e.exclude { // SLT-17
			continue
		}
		xet, err := e.eventTypeOf(r.etID)
		if err != nil {
			return nil, err
		}
		if r.status == "pending" && (!xet.PendingBlocksSlot || r.orgID != e.host.ID) { // EVT-11, F-19
			continue
		}
		Bx := time.Duration(xet.BufferBefore) * time.Minute
		Ax := time.Duration(xet.BufferAfter) * time.Minute
		// SLT-9: [E.s−B, E.e+A] overlaps [X.s−Bx, X.e+Ax]  ⇔  E overlaps [X.s−Bx−A, X.e+Ax+B).
		out = append(out, Interval{time.Unix(r.s, 0).Add(-Bx - A), time.Unix(r.en, 0).Add(Ax + B)})
	}
	hrows, err := e.q.QueryContext(e.ctx, `SELECT start_at, end_at FROM holds WHERE host_id = ? AND expires_at > ? AND booker_id != ? AND start_at < ? AND end_at > ?`,
		e.host.ID, e.now.Unix(), e.bookerID, to.Unix(), from.Unix())
	if err != nil {
		return nil, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var s, en int64
		if err := hrows.Scan(&s, &en); err != nil {
			return nil, err
		}
		out = append(out, Interval{time.Unix(s, 0), time.Unix(en, 0)}) // F-18: holds carry no buffers
	}
	return out, hrows.Err()
}

// period returns the [start, end) of t's day/week/month/year in the owner's zone and week start (SLT-13).
func (e *engine) period(kind string, t time.Time) Interval {
	d := DateOf(t, e.hostLoc)
	var s, en Date
	switch kind {
	case "day":
		s, en = d, d.AddDays(1)
	case "week":
		ws := 1
		if e.host.WeekStart == "sunday" {
			ws = 0
		}
		s = d.AddDays(-((int(d.Weekday()) - ws + 7) % 7))
		en = s.AddDays(7)
	case "month":
		s = Date{d.Y, d.M, 1}
		n := s.t().AddDate(0, 1, 0)
		en = Date{n.Year(), n.Month(), 1}
	default:
		s, en = Date{d.Y, 1, 1}, Date{d.Y + 1, 1, 1}
	}
	return Interval{StartOfDay(s, e.hostLoc), StartOfDay(en, e.hostLoc)}
}

func (e *engine) use(kind string, t time.Time) (limitUse, error) {
	p := e.period(kind, t)
	key := kind + p.Start.String()
	if u, ok := e.limitCache[key]; ok {
		return u, nil
	}
	var u limitUse
	err := e.q.QueryRowContext(e.ctx, `SELECT count(*), coalesce(sum(end_at - start_at), 0) / 60 FROM bookings
		WHERE event_type_id = ? AND organizer_id = ? AND status = 'accepted' AND start_at >= ? AND start_at < ? AND uid != ?`,
		e.et.ID, e.host.ID, p.Start.Unix(), p.End.Unix(), e.exclude).Scan(&u.count, &u.minutes)
	e.limitCache[key] = u
	return u, err
}

// underLimits is SLT-13 / EVT-8. Booking limits: fewer than the limit already; minute limits:
// booked + this duration fits (F-16).
func (e *engine) underLimits(t time.Time) (bool, error) {
	kinds := []string{"day", "week", "month", "year"}
	bl, ml := e.et.Limits.Bookings.list(), e.et.Limits.Minutes.list()
	for i, k := range kinds {
		if bl[i] == nil && ml[i] == nil {
			continue
		}
		u, err := e.use(k, t)
		if err != nil {
			return false, err
		}
		if bl[i] != nil && u.count >= *bl[i] {
			return false, nil
		}
		if ml[i] != nil && u.minutes+int(e.duration/time.Minute) > *ml[i] {
			return false, nil
		}
	}
	return true, nil
}

// inWindow is EVT-7 / SLT-14. Rolling windows count days after today in the booker's zone
// (business days = Mon–Fri, F-20); range dates are in the owner's zone (F-21).
func (e *engine) inWindow(t time.Time) (bool, error) {
	w := e.et.Window
	switch w.Kind {
	case "rolling":
		today := DateOf(e.now, e.bookerLoc)
		last := today.AddDays(w.Days)
		if !w.CalendarDays {
			last = today
			for n := 0; n < w.Days; {
				last = last.AddDays(1)
				if wd := last.Weekday(); wd != time.Saturday && wd != time.Sunday {
					n++
				}
			}
		}
		return !DateOf(t, e.bookerLoc).After(last), nil
	case "rolling_available":
		if !e.windowFound {
			last, err := e.rollingAvailableLast(w.Days)
			if err != nil {
				return false, err
			}
			e.windowLast, e.windowFound = &last, true
		}
		return !DateOf(t, e.bookerLoc).After(*e.windowLast), nil
	case "range":
		s, _ := ParseDate(w.StartDate)
		en, _ := ParseDate(w.EndDate)
		d := DateOf(t, e.hostLoc)
		return !d.Before(s) && !d.After(en), nil
	}
	return true, nil
}

// rollingAvailableLast scans ≤ 61 days for the Nth date after today with a free slot (F-20).
func (e *engine) rollingAvailableLast(days int) (Date, error) {
	today := DateOf(e.now, e.bookerLoc)
	starts, err := e.candidates(e.now, e.now.Add(61*24*time.Hour), checks{busy: true, notice: true, limits: true})
	if err != nil {
		return Date{}, err
	}
	var dates []Date
	for _, s := range starts {
		d := DateOf(s, e.bookerLoc)
		if d.After(today) && (len(dates) == 0 || dates[len(dates)-1] != d) {
			dates = append(dates, d)
		}
	}
	if len(dates) >= days {
		return dates[days-1], nil
	}
	return today.AddDays(61), nil
}

// check is BKG-6's re-check of one start, in order, each failure with its own code.
func (e *engine) check(start time.Time) error {
	if !start.After(e.now) {
		return &Error{Code: ErrInPast}
	}
	if start.Before(e.now.Add(time.Duration(e.et.MinNotice) * time.Minute)) {
		return &Error{Code: ErrTooSoon}
	}
	if ok, err := e.inWindow(start); err != nil || !ok {
		if err != nil {
			return err
		}
		return &Error{Code: ErrOutsideWindow}
	}
	if ok, err := e.underLimits(start); err != nil || !ok {
		if err != nil {
			return err
		}
		return &Error{Code: ErrLimitReached}
	}
	end := start.Add(time.Second)
	free, err := e.candidates(start, end, checks{busy: true, notice: true})
	if err != nil {
		return err
	}
	if len(free) == 1 {
		return nil
	}
	open, err := e.candidates(start, end, checks{notice: true})
	if err != nil {
		return err
	}
	if len(open) == 1 {
		return &Error{Code: ErrSlotTaken}
	}
	return &Error{Code: ErrUnavailable} // outside hours, time off, or not on the slot grid (F-22)
}

// PlaceHold holds a slot for 5 minutes for bookerID, replacing their earlier hold (SLT-15, F-18).
func (a *App) PlaceHold(ctx context.Context, eventTypeID int64, bookerID string, start time.Time, duration int) error {
	if bookerID == "" {
		return errf(ErrInvalid, "booker id")
	}
	et, err := a.EventType(ctx, eventTypeID)
	if err != nil {
		return err
	}
	if duration == 0 {
		duration = et.Length
	}
	if !slices.Contains(et.AllowedDurations(), duration) {
		return &Error{Code: ErrInvalidDuration}
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := a.now()
	if _, err := tx.ExecContext(ctx, `DELETE FROM holds WHERE booker_id = ? OR expires_at <= ?`, bookerID, now.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO holds (booker_id, host_id, event_type_id, start_at, end_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		bookerID, et.UserID, et.ID, start.Unix(), start.Add(time.Duration(duration)*time.Minute).Unix(), now.Add(HoldDuration).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
