package web

import (
	"encoding/json"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"

	"scheduling-rebuild/internal/app"
)

// zones offered in pickers; the booker's detected zone is added when missing.
var zones = []string{
	"UTC", "Europe/London", "Europe/Dublin", "Europe/Lisbon", "Europe/Paris", "Europe/Berlin", "Europe/Amsterdam",
	"Europe/Madrid", "Europe/Rome", "Europe/Stockholm", "Europe/Warsaw", "Europe/Athens", "Europe/Helsinki",
	"Europe/Istanbul", "Europe/Moscow", "Asia/Tehran", "Asia/Dubai", "Asia/Karachi", "Asia/Kolkata", "Asia/Kathmandu",
	"Asia/Dhaka", "Asia/Bangkok", "Asia/Singapore", "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo", "Asia/Seoul",
	"Australia/Perth", "Australia/Adelaide", "Australia/Sydney", "Pacific/Auckland", "Africa/Lagos", "Africa/Cairo",
	"Africa/Johannesburg", "Africa/Nairobi", "America/Sao_Paulo", "America/Argentina/Buenos_Aires", "America/Bogota",
	"America/Mexico_City", "America/Halifax", "America/New_York", "America/Chicago", "America/Denver",
	"America/Phoenix", "America/Los_Angeles", "America/Anchorage", "Pacific/Honolulu",
}

func zonesWith(tz string) []string {
	for _, z := range zones {
		if z == tz {
			return zones
		}
	}
	out := append([]string{tz}, zones...)
	sort.Strings(out[1:])
	return out
}

func loc(tz string) *time.Location {
	l, err := app.LoadZone(tz)
	if err != nil {
		return time.UTC
	}
	return l
}

var funcs = template.FuncMap{
	"clock": func(t time.Time, tz string, h12 bool) string {
		if h12 {
			return t.In(loc(tz)).Format("3:04pm")
		}
		return t.In(loc(tz)).Format("15:04")
	},
	"day":      func(t time.Time, tz string) string { return t.In(loc(tz)).Format("Monday 2 January 2006") },
	"datetime": func(t time.Time, tz string) string { return t.In(loc(tz)).Format("Mon 2 Jan 2006 15:04") },
	"rfc3339":  func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"join":     strings.Join,
	"ints": func(xs []int) string {
		var s []string
		for _, x := range xs {
			s = append(s, strconv.Itoa(x))
		}
		return strings.Join(s, ",")
	},
	"opt": func(p *int) string {
		if p == nil {
			return ""
		}
		return strconv.Itoa(*p)
	},
	"optID": func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	},
	"jsonIndent": func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	},
	"locations": func(ls []app.Location) string {
		var lines []string
		for _, l := range ls {
			lines = append(lines, strings.TrimSpace(l.Kind+" "+l.Value))
		}
		return strings.Join(lines, "\n")
	},
	"rules": func(rs []app.Rule) string {
		var lines []string
		for _, r := range rs {
			var d []string
			for _, x := range r.Days {
				d = append(d, strconv.Itoa(x))
			}
			lines = append(lines, strings.Join(d, ",")+" "+r.Start+"-"+r.End)
		}
		return strings.Join(lines, "\n")
	},
	"overrides": func(os []app.Override) string {
		var lines []string
		for _, o := range os {
			lines = append(lines, o.Date+" "+o.Start+"-"+o.End)
		}
		return strings.Join(lines, "\n")
	},
	"locationText": func(l app.Location) string {
		switch l.Kind {
		case "in_person":
			return "In person: " + l.Value
		case "link", "video":
			return "Video/link: " + l.Value
		case "phone_host":
			return "Phone (call " + l.Value + ")"
		case "phone_booker":
			return "Phone (host calls " + l.Value + ")"
		}
		return l.Kind
	},
	"locationKind": func(k string) string {
		return map[string]string{"in_person": "In person", "link": "Link", "phone_host": "Phone (you call the host)", "phone_booker": "Phone (the host calls you)", "video": "Video call"}[k]
	},
	"has": func(xs []string, x string) bool {
		for _, y := range xs {
			if y == x {
				return true
			}
		}
		return false
	},
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	},
	"list":    func(xs ...string) []string { return xs },
	"minutes": func(a, b time.Time) int { return int(b.Sub(a) / time.Minute) },
	"past":    func(t, now time.Time) bool { return !t.After(now) },
}
