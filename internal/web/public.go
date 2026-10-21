package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"scheduling-rebuild/internal/app"
)

// PUB-1: profile with visible event types.
func (s *Server) publicProfile(w http.ResponseWriter, r *http.Request) {
	u, err := s.App.UserByUsername(r.Context(), r.PathValue("username"))
	if err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "There's no page here.")
		return
	}
	ets, err := s.App.EventTypes(r.Context(), u.ID, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "public_profile", map[string]any{"Owner": u, "EventTypes": ets})
}

func (s *Server) ownerEvent(w http.ResponseWriter, r *http.Request) (*app.User, *app.EventType, bool) {
	u, err := s.App.UserByUsername(r.Context(), r.PathValue("username"))
	if err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "There's no page here.")
		return nil, nil, false
	}
	et, err := s.App.EventTypeBySlug(r.Context(), u.ID, r.PathValue("slug"))
	if err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "There's no such event.")
		return nil, nil, false
	}
	return u, et, true
}

// pickerParams are the booking page's own query parameters; any other parameter is a field prefill (PUB-4).
var pickerParams = []string{"month", "date", "tz", "duration", "h12", "reschedule", "start"}

func prefill(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		if !slices.Contains(pickerParams, k) {
			out[k] = v
		}
	}
	return out
}

type dayCell struct {
	Date      string
	Day       int
	InMonth   bool
	Available bool
	Selected  bool
}

// PUB-2: month calendar, times for the chosen date in the booker's zone, 12/24 h, duration picker.
func (s *Server) publicEvent(w http.ResponseWriter, r *http.Request) {
	owner, et, ok := s.ownerEvent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	tz := q.Get("tz")
	if _, err := app.LoadZone(tz); err != nil {
		tz = owner.TimeZone
	}
	bloc := loc(tz)
	duration, _ := strconv.Atoi(q.Get("duration"))
	if !slices.Contains(et.AllowedDurations(), duration) {
		duration = et.Length
	}
	now := s.App.Now()
	month, err := time.ParseInLocation("2006-01", q.Get("month"), time.UTC)
	if err != nil {
		if d, err := time.Parse("2006-01-02", q.Get("date")); err == nil {
			month = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
		} else {
			l := now.In(bloc)
			month = time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	first := app.Date{Y: month.Year(), M: month.Month(), D: 1}
	nextMonth := month.AddDate(0, 1, 0)
	last := app.Date{Y: nextMonth.Year(), M: nextMonth.Month(), D: 1}
	reschedule := q.Get("reschedule")
	slots, err := s.App.Slots(r.Context(), app.SlotQuery{
		EventTypeID: et.ID, From: app.StartOfDay(first, bloc), To: app.StartOfDay(last, bloc), TimeZone: tz,
		Duration: duration, RescheduleUID: reschedule, BookerID: bookerID(w, r),
	})
	errText := ""
	if err != nil {
		errText = errorText(err)
	}
	date := q.Get("date")
	if _, ok := slots[date]; !ok {
		date = ""
		if ds := slots.Dates(); len(ds) > 0 {
			date = ds[0]
		}
	}
	// Calendar grid, weeks starting Monday.
	var weeks [][]dayCell
	start := first.AddDays(-((int(first.Weekday()) + 6) % 7))
	for d := start; d.Before(last) || (len(weeks) > 0 && len(weeks[len(weeks)-1]) < 7); d = d.AddDays(1) {
		if len(weeks) == 0 || len(weeks[len(weeks)-1]) == 7 {
			weeks = append(weeks, nil)
		}
		ds := d.String()
		weeks[len(weeks)-1] = append(weeks[len(weeks)-1], dayCell{Date: ds, Day: d.D, InMonth: d.M == first.M, Available: len(slots[ds]) > 0, Selected: ds == date})
	}
	keep := url.Values{"tz": {tz}, "duration": {strconv.Itoa(duration)}}
	if q.Get("h12") != "" {
		keep.Set("h12", "1")
	}
	if reschedule != "" {
		keep.Set("reschedule", reschedule)
	}
	for k, v := range prefill(q) {
		keep[k] = v
	}
	s.render(w, r, http.StatusOK, "public_event", map[string]any{
		"Owner": owner, "ET": et, "TZ": tz, "Zones": zonesWith(tz), "H12": q.Get("h12") != "", "Duration": duration,
		"Month": month.Format("January 2006"), "MonthKey": month.Format("2006-01"),
		"PrevMonth": month.AddDate(0, -1, 0).Format("2006-01"), "NextMonth": nextMonth.Format("2006-01"),
		"Weeks": weeks, "Date": date, "Times": slots[date], "Reschedule": reschedule,
		"Keep": template.URL(keep.Encode()), "Prefill": prefill(q), "Error": errText, "TZDetect": q.Get("tz") == "",
	})
}

// bookForm is PUB-4: choosing a time holds it (SLT-15) and shows the form, prefilled from the query.
func (s *Server) bookForm(w http.ResponseWriter, r *http.Request) {
	owner, et, ok := s.ownerEvent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	start, err := time.Parse(time.RFC3339, q.Get("start"))
	if err != nil {
		s.message(w, r, http.StatusBadRequest, "Pick a time", "No time was chosen.")
		return
	}
	duration, _ := strconv.Atoi(q.Get("duration"))
	if duration == 0 {
		duration = et.Length
	}
	if err := s.App.PlaceHold(r.Context(), et.ID, bookerID(w, r), start, duration); err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderBookForm(w, r, http.StatusOK, owner, et, start, duration, q.Get("tz"), q.Get("reschedule"), prefill(q), "")
}

func (s *Server) renderBookForm(w http.ResponseWriter, r *http.Request, status int, owner *app.User, et *app.EventType, start time.Time, duration int, tz, reschedule string, values url.Values, errText string) {
	if _, err := app.LoadZone(tz); err != nil {
		tz = owner.TimeZone
	}
	var fields []app.Field
	for _, f := range et.FormFields() {
		if reschedule != "" { // rescheduling keeps the original answers (F-30)
			if f.RescheduleOnly {
				fields = append(fields, f)
			}
			continue
		}
		if !f.RescheduleOnly && !f.Hidden {
			fields = append(fields, f)
		}
	}
	s.render(w, r, status, "book", map[string]any{
		"Owner": owner, "ET": et, "Start": start, "End": start.Add(time.Duration(duration) * time.Minute), "Duration": duration,
		"TZ": tz, "Reschedule": reschedule, "Fields": fields, "Values": values, "Error": errText,
		"Back": fmt.Sprintf("/%s/%s?tz=%s&duration=%d&date=%s&reschedule=%s", owner.Username, et.Slug, url.QueryEscape(tz), duration,
			start.In(loc(tz)).Format("2006-01-02"), url.QueryEscape(reschedule)),
	})
}

// book creates the booking, or moves one when `reschedule` is set (BKG-12; organizer if signed in as them).
func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	owner, et, ok := s.ownerEvent(w, r)
	if !ok {
		return
	}
	r.ParseForm()
	f := r.PostForm
	start, err := time.Parse(time.RFC3339, f.Get("start"))
	if err != nil {
		s.message(w, r, http.StatusBadRequest, "Pick a time", "No time was chosen.")
		return
	}
	duration, _ := strconv.Atoi(f.Get("duration"))
	tz, reschedule := f.Get("tz"), f.Get("reschedule")
	var b *app.Booking
	if reschedule != "" {
		var actor int64
		if u, _ := s.sessionUser(r); u != nil && u.ID == owner.ID {
			actor = u.ID
		}
		b, err = s.App.RescheduleBooking(r.Context(), app.RescheduleInput{
			UID: reschedule, Start: start, TimeZone: tz, Reason: f.Get("reschedule_reason"), BookerID: bookerID(w, r), ActorID: actor,
		})
	} else {
		b, err = s.App.CreateBooking(r.Context(), app.BookingInput{
			EventTypeID: et.ID, Start: start, Duration: duration, TimeZone: tz, Locale: acceptLanguage(r),
			Responses: f, BookerID: bookerID(w, r),
		})
	}
	if err != nil {
		status := http.StatusBadRequest
		if app.Code(err) == app.ErrSlotTaken {
			status = http.StatusConflict
		}
		s.renderBookForm(w, r, status, owner, et, start, duration, tz, reschedule, f, errorText(err))
		return
	}
	redirect(w, r, "/booking/"+b.UID)
}

func acceptLanguage(r *http.Request) string {
	l := r.Header.Get("Accept-Language")
	if l == "" {
		return "en"
	}
	l, _, _ = strings.Cut(l, ",")
	l, _, _ = strings.Cut(l, ";")
	return strings.TrimSpace(l)
}

// PUB-5, PUB-6: success / manage page.
func (s *Server) bookingPage(w http.ResponseWriter, r *http.Request) {
	s.renderBooking(w, r, http.StatusOK, "")
}

func (s *Server) renderBooking(w http.ResponseWriter, r *http.Request, status int, errText string) {
	ctx := r.Context()
	b, err := s.App.Booking(ctx, r.PathValue("uid"))
	if err != nil {
		s.message(w, r, http.StatusNotFound, "Not found", "No such booking.")
		return
	}
	et, err := s.App.EventType(ctx, b.EventTypeID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host, err := s.App.User(ctx, b.OrganizerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tz := r.URL.Query().Get("tz")
	if _, err := app.LoadZone(tz); err != nil {
		tz = b.Booker().TimeZone
	}
	now := s.App.Now()
	open := b.Status == app.StatusPending || b.Status == app.StatusAccepted
	var next *app.Booking
	if b.Rescheduled {
		next, _ = s.App.RescheduledTo(ctx, b.UID)
	}
	duration := int(b.End.Sub(b.Start) / time.Minute)
	s.render(w, r, status, "booking", map[string]any{
		"B": b, "ET": et, "Host": host, "TZ": tz, "Next": next, "Error": errText,
		"CanCancel":     open && now.Before(b.End) && et.AllowCancel,
		"CanReschedule": open && b.Start.After(now) && et.AllowReschedule,
		"RescheduleURL": fmt.Sprintf("/%s/%s?reschedule=%s&tz=%s&duration=%d", host.Username, et.Slug, b.UID, url.QueryEscape(tz), duration),
		"GoogleURL":     googleURL(et, host, b),
		"OutlookURL":    outlookURL(et, host, b),
		"ReasonNeeded":  et.CancelReason == "required_booker" || et.CancelReason == "required_both",
	})
}

func describe(et *app.EventType, host *app.User, b *app.Booking) (title, details, where string) {
	title = et.Title + " with " + host.Name
	details = "Manage: " + "/booking/" + b.UID
	where = b.Location.Value
	if where == "" {
		where = b.Location.Kind
	}
	return
}

func googleURL(et *app.EventType, host *app.User, b *app.Booking) string {
	title, details, where := describe(et, host, b)
	q := url.Values{"action": {"TEMPLATE"}, "text": {title}, "details": {details}, "location": {where},
		"dates": {b.Start.UTC().Format("20060102T150405Z") + "/" + b.End.UTC().Format("20060102T150405Z")}}
	return "https://calendar.google.com/calendar/render?" + q.Encode()
}

func outlookURL(et *app.EventType, host *app.User, b *app.Booking) string {
	title, details, where := describe(et, host, b)
	q := url.Values{"path": {"/calendar/action/compose"}, "rru": {"addevent"}, "subject": {title}, "body": {details}, "location": {where},
		"startdt": {b.Start.UTC().Format(time.RFC3339)}, "enddt": {b.End.UTC().Format(time.RFC3339)}}
	return "https://outlook.live.com/calendar/0/deeplink/compose?" + q.Encode()
}

func icsEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(s)
}

// bookingICS serves the .ics file (PUB-5); UID stable across reschedules, SEQUENCE = booking sequence.
func (s *Server) bookingICS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := s.App.Booking(ctx, r.PathValue("uid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	et, _ := s.App.EventType(ctx, b.EventTypeID)
	host, _ := s.App.User(ctx, b.OrganizerID)
	title, _, where := describe(et, host, b)
	status, method := "CONFIRMED", "PUBLISH"
	switch b.Status {
	case app.StatusPending:
		status = "TENTATIVE"
	case app.StatusCancelled, app.StatusRejected:
		status, method = "CANCELLED", "CANCEL"
	}
	const f = "20060102T150405Z"
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//scheduling-rebuild//EN", "METHOD:" + method, "BEGIN:VEVENT",
		"UID:" + b.ICalUID, "SEQUENCE:" + strconv.Itoa(b.Sequence), "DTSTAMP:" + s.App.Now().UTC().Format(f),
		"DTSTART:" + b.Start.UTC().Format(f), "DTEND:" + b.End.UTC().Format(f),
		"SUMMARY:" + icsEscape(title), "LOCATION:" + icsEscape(where), "STATUS:" + status,
		"ORGANIZER;CN=" + icsEscape(host.Name) + ":mailto:" + host.Email,
	}
	for _, a := range b.Attendees {
		lines = append(lines, "ATTENDEE;CN="+icsEscape(a.Name)+":mailto:"+a.Email)
	}
	lines = append(lines, "DESCRIPTION:"+icsEscape(s.App.BaseURL+"/booking/"+b.UID), "END:VEVENT", "END:VCALENDAR")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="booking.ics"`)
	w.Write([]byte(strings.Join(lines, "\r\n") + "\r\n"))
}

// bookingCancel is the booker's cancel from the manage page (PUB-6, BKG-15).
func (s *Server) bookingCancel(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if err := s.App.CancelBooking(r.Context(), r.PathValue("uid"), 0, r.PostForm.Get("reason")); err != nil {
		s.renderBooking(w, r, http.StatusBadRequest, errorText(err))
		return
	}
	redirect(w, r, "/booking/"+r.PathValue("uid"))
}

// actionPage shows a confirm/reject link's booking; acting needs a POST so link scanners can't trigger it.
func (s *Server) actionPage(w http.ResponseWriter, r *http.Request) {
	t, err := s.App.LookupActionToken(r.Context(), r.PathValue("token"))
	if err != nil || t.Used {
		s.message(w, r, http.StatusGone, "Link used", errorText(&app.Error{Code: app.ErrTokenUsed}))
		return
	}
	et, _ := s.App.EventType(r.Context(), t.Booking.EventTypeID)
	host, _ := s.App.User(r.Context(), t.Booking.OrganizerID)
	s.render(w, r, http.StatusOK, "action", map[string]any{"T": t, "ET": et, "Host": host, "Token": r.PathValue("token")})
}

func (s *Server) actionDo(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	t, err := s.App.UseActionToken(r.Context(), r.PathValue("token"), r.PostForm.Get("reason"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if t.Action == "confirm" {
		s.message(w, r, http.StatusOK, "Confirmed", "The booking is confirmed and the booker has been told.")
	} else {
		s.message(w, r, http.StatusOK, "Rejected", "The booking is rejected and the booker has been told.")
	}
}

// videoPage stands in for a video meeting: no video app integration in v0.1 (F-27).
func (s *Server) videoPage(w http.ResponseWriter, r *http.Request) {
	s.message(w, r, http.StatusOK, "Video meeting", "No video app is connected in this build; this link stands in for the meeting room.")
}

// apiSlots is SLT-1 as JSON: event_type, from, to (RFC 3339), time_zone, duration?, reschedule_uid?.
func (s *Server) apiSlots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, _ := strconv.ParseInt(q.Get("event_type"), 10, 64)
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": app.ErrInvalidRange})
		return
	}
	duration, _ := strconv.Atoi(q.Get("duration"))
	m, err := s.App.Slots(r.Context(), app.SlotQuery{
		EventTypeID: id, From: from, To: to, TimeZone: q.Get("time_zone"), Duration: duration,
		RescheduleUID: q.Get("reschedule_uid"), BookerID: bookerID(w, r),
	})
	if err != nil {
		status := http.StatusBadRequest
		if app.Code(err) == app.ErrEventNotFound {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": app.Code(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": m})
}
