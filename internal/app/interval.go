package app

import (
	"sort"
	"time"
)

// Interval is a half-open UTC range [Start, End).
type Interval struct{ Start, End time.Time }

// Overlaps is SLT-12: touching is not overlap.
func (a Interval) Overlaps(b Interval) bool { return a.Start.Before(b.End) && a.End.After(b.Start) }

// mergeIntervals sorts and merges overlapping or touching intervals (AVL-6).
func mergeIntervals(in []Interval) []Interval {
	if len(in) == 0 {
		return nil
	}
	s := append([]Interval(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i].Start.Before(s[j].Start) })
	out := []Interval{s[0]}
	for _, iv := range s[1:] {
		last := &out[len(out)-1]
		if !iv.Start.After(last.End) {
			if iv.End.After(last.End) {
				last.End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// subtractIntervals returns free minus every busy interval.
func subtractIntervals(free, busy []Interval) []Interval {
	busy = mergeIntervals(busy)
	var out []Interval
	for _, f := range free {
		cur := []Interval{f}
		for _, b := range busy {
			var next []Interval
			for _, c := range cur {
				if !c.Overlaps(b) {
					next = append(next, c)
					continue
				}
				if c.Start.Before(b.Start) {
					next = append(next, Interval{c.Start, b.Start})
				}
				if c.End.After(b.End) {
					next = append(next, Interval{b.End, c.End})
				}
			}
			cur = next
		}
		out = append(out, cur...)
	}
	return out
}

// Date is a civil date.
type Date struct {
	Y int
	M time.Month
	D int
}

// ParseDate reads YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, errf(ErrInvalid, "date %q", s)
	}
	return Date{t.Year(), t.Month(), t.Day()}, nil
}

// DateOf is t's civil date in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	l := t.In(loc)
	return Date{l.Year(), l.Month(), l.Day()}
}

func (d Date) String() string { return d.t().Format("2006-01-02") }
func (d Date) t() time.Time   { return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, time.UTC) }

// AddDays moves d by n days.
func (d Date) AddDays(n int) Date {
	t := d.t().AddDate(0, 0, n)
	return Date{t.Year(), t.Month(), t.Day()}
}

// Weekday of d.
func (d Date) Weekday() time.Weekday { return d.t().Weekday() }

// Before reports d < e.
func (d Date) Before(e Date) bool { return d.t().Before(e.t()) }

// After reports d > e.
func (d Date) After(e Date) bool { return d.t().After(e.t()) }

// WallTime is the instant of local wall-clock time h:m on d in loc (AVL-8). A time skipped by a
// DST gap uses the offset before the gap (02:30 → 03:30 CEST); a repeated time uses the earlier
// instant (F-12).
func WallTime(d Date, h, m int, loc *time.Location) time.Time {
	u := time.Date(d.Y, d.M, d.D, h, m, 0, 0, time.UTC)
	_, offBefore := u.Add(-24 * time.Hour).In(loc).Zone()
	_, offAfter := u.Add(24 * time.Hour).In(loc).Zone()
	var valid []time.Time
	for _, off := range []int{offBefore, offAfter} {
		c := u.Add(-time.Duration(off) * time.Second)
		l := c.In(loc)
		if l.Year() == d.Y && l.Month() == d.M && l.Day() == d.D && l.Hour() == h && l.Minute() == m {
			valid = append(valid, c)
		}
	}
	switch {
	case len(valid) == 0:
		return u.Add(-time.Duration(offBefore) * time.Second)
	case len(valid) == 2 && valid[1].Before(valid[0]):
		return valid[1]
	default:
		return valid[0]
	}
}

// StartOfDay is local midnight of d in loc.
func StartOfDay(d Date, loc *time.Location) time.Time { return WallTime(d, 0, 0, loc) }
