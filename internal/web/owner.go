package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"scheduling-rebuild/internal/app"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ets, err := s.App.EventTypes(r.Context(), u.ID, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	bs, err := s.App.OrganizerBookings(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	titles := map[int64]string{}
	for _, et := range ets {
		titles[et.ID] = et.Title
	}
	now := s.App.Now()
	var upcoming, past []*app.Booking
	for _, b := range bs {
		if b.End.After(now) {
			upcoming = append(upcoming, b)
		} else {
			past = append([]*app.Booking{b}, past...)
		}
	}
	s.render(w, r, http.StatusOK, "dashboard", map[string]any{
		"EventTypes": ets, "Upcoming": upcoming, "Past": past, "Titles": titles, "Now": now,
		"Flash": r.URL.Query().Get("msg"),
	})
}

func (s *Server) eventTypeNew(w http.ResponseWriter, r *http.Request) {
	et := app.NewEventType()
	s.renderEventType(w, r, http.StatusOK, &et, "")
}

func (s *Server) renderEventType(w http.ResponseWriter, r *http.Request, status int, et *app.EventType, msg string) {
	scheds, _ := s.App.Schedules(r.Context(), currentUser(r).ID)
	var custom []app.Field
	for _, f := range et.BookingFields {
		custom = append(custom, f)
	}
	s.render(w, r, status, "event_type", map[string]any{"ET": et, "Schedules": scheds, "Fields": custom, "Error": msg})
}

func atoi(s string) (int, error) {
	if s = strings.TrimSpace(s); s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

func optInt(s string) (*int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return &n, err
}

// parseEventTypeForm reads the owner's edit form into et.
func parseEventTypeForm(f url.Values, et *app.EventType) error {
	bad := func(field string) error { return &app.Error{Code: app.ErrInvalid, Detail: field} }
	var err error
	et.Title, et.Slug, et.Description = f.Get("title"), strings.TrimSpace(f.Get("slug")), f.Get("description")
	if et.Length, err = atoi(f.Get("length")); err != nil {
		return bad("length")
	}
	et.Durations = nil
	for _, d := range strings.Split(f.Get("durations"), ",") {
		if d = strings.TrimSpace(d); d != "" {
			n, err := strconv.Atoi(d)
			if err != nil {
				return bad("durations")
			}
			et.Durations = append(et.Durations, n)
		}
	}
	for name, p := range map[string]*int{"slot_interval": &et.SlotInterval, "min_notice": &et.MinNotice, "buffer_before": &et.BufferBefore,
		"buffer_after": &et.BufferAfter, "position": &et.Position, "min_reschedule_notice": &et.MinRescheduleNotice} {
		if *p, err = atoi(f.Get(name)); err != nil {
			return bad(name)
		}
	}
	et.Window = app.Window{Kind: f.Get("window_kind"), CalendarDays: f.Get("window_calendar_days") != "", StartDate: f.Get("window_start"), EndDate: f.Get("window_end")}
	if et.Window.Days, err = atoi(f.Get("window_days")); err != nil {
		return bad("window_days")
	}
	for prefix, pl := range map[string]*app.PeriodLimits{"lb_": &et.Limits.Bookings, "lm_": &et.Limits.Minutes} {
		for name, p := range map[string]**int{"day": &pl.Day, "week": &pl.Week, "month": &pl.Month, "year": &pl.Year} {
			if *p, err = optInt(f.Get(prefix + name)); err != nil {
				return bad(prefix + name)
			}
		}
	}
	et.RequiresConfirmation = f.Get("requires_confirmation") != ""
	et.ConfirmWithin = nil
	if v := strings.TrimSpace(f.Get("confirm_within_value")); v != "" && et.RequiresConfirmation {
		n, err := strconv.Atoi(v)
		if err != nil {
			return bad("confirm_within")
		}
		et.ConfirmWithin = &app.ConfirmWithin{Value: n, Unit: f.Get("confirm_within_unit")}
	}
	et.PendingBlocksSlot = f.Get("pending_blocks_slot") != ""
	et.Hidden = f.Get("hidden") != ""
	et.AllowCancel = f.Get("allow_cancel") != ""
	et.AllowReschedule = f.Get("allow_reschedule") != ""
	et.CancelReason = f.Get("cancel_reason")
	et.Locations = nil
	for _, line := range strings.Split(f.Get("locations"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			kind, value, _ := strings.Cut(line, " ")
			et.Locations = append(et.Locations, app.Location{Kind: kind, Value: strings.TrimSpace(value)})
		}
	}
	et.BookingFields = nil
	if raw := strings.TrimSpace(f.Get("booking_fields")); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &et.BookingFields); err != nil {
			return bad("booking_fields: " + err.Error())
		}
	}
	et.ScheduleID = nil
	if id, _ := strconv.ParseInt(f.Get("schedule_id"), 10, 64); id > 0 {
		et.ScheduleID = &id
	}
	return nil
}

func (s *Server) eventTypeCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	et := app.NewEventType()
	if err := parseEventTypeForm(r.PostForm, &et); err != nil {
		s.renderEventType(w, r, http.StatusBadRequest, &et, errorText(err))
		return
	}
	if _, err := s.App.CreateEventType(r.Context(), currentUser(r).ID, et); err != nil {
		s.renderEventType(w, r, http.StatusBadRequest, &et, errorText(err))
		return
	}
	redirect(w, r, "/app")
}

func (s *Server) ownEventType(w http.ResponseWriter, r *http.Request) *app.EventType {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	et, err := s.App.EventType(r.Context(), id)
	if err != nil || et.UserID != currentUser(r).ID {
		s.message(w, r, http.StatusNotFound, "Not found", "No such event type.")
		return nil
	}
	return et
}

func (s *Server) eventTypeEdit(w http.ResponseWriter, r *http.Request) {
	if et := s.ownEventType(w, r); et != nil {
		s.renderEventType(w, r, http.StatusOK, et, "")
	}
}

func (s *Server) eventTypeUpdate(w http.ResponseWriter, r *http.Request) {
	et := s.ownEventType(w, r)
	if et == nil {
		return
	}
	r.ParseForm()
	if err := parseEventTypeForm(r.PostForm, et); err != nil {
		s.renderEventType(w, r, http.StatusBadRequest, et, errorText(err))
		return
	}
	if err := s.App.UpdateEventType(r.Context(), currentUser(r).ID, *et); err != nil {
		s.renderEventType(w, r, http.StatusBadRequest, et, errorText(err))
		return
	}
	redirect(w, r, "/app")
}

func (s *Server) eventTypeDelete(w http.ResponseWriter, r *http.Request) {
	et := s.ownEventType(w, r)
	if et == nil {
		return
	}
	if err := s.App.DeleteEventType(r.Context(), currentUser(r).ID, et.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/app")
}

// --- schedules ---

// parseRules reads lines like "1,2,3,4,5 09:00-17:00".
func parseRules(text string) ([]app.Rule, error) {
	var out []app.Rule
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		days, span, ok := strings.Cut(line, " ")
		start, end, ok2 := strings.Cut(strings.TrimSpace(span), "-")
		if !ok || !ok2 {
			return nil, &app.Error{Code: app.ErrInvalid, Detail: "rule " + line + ": want `1,2,3 09:00-17:00`"}
		}
		var r app.Rule
		for _, d := range strings.Split(days, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(d))
			if err != nil {
				return nil, &app.Error{Code: app.ErrInvalid, Detail: "rule " + line}
			}
			r.Days = append(r.Days, n)
		}
		r.Start, r.End = strings.TrimSpace(start), strings.TrimSpace(end)
		out = append(out, r)
	}
	return out, nil
}

// parseOverrides reads lines like "2026-11-02 13:00-14:00" or "2026-11-02 off".
func parseOverrides(text string) ([]app.Override, error) {
	var out []app.Override
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		date, span, _ := strings.Cut(line, " ")
		span = strings.TrimSpace(span)
		if span == "off" {
			out = append(out, app.Override{Date: date, Start: "00:00", End: "00:00"})
			continue
		}
		start, end, ok := strings.Cut(span, "-")
		if !ok {
			return nil, &app.Error{Code: app.ErrInvalid, Detail: "override " + line + ": want `2026-11-02 13:00-14:00` or `2026-11-02 off`"}
		}
		out = append(out, app.Override{Date: date, Start: strings.TrimSpace(start), End: strings.TrimSpace(end)})
	}
	return out, nil
}

func scheduleFromForm(f url.Values) (app.Schedule, error) {
	sc := app.Schedule{Name: f.Get("name"), TimeZone: f.Get("time_zone")}
	var err error
	if sc.Rules, err = parseRules(f.Get("rules")); err != nil {
		return sc, err
	}
	sc.Overrides, err = parseOverrides(f.Get("overrides"))
	return sc, err
}

func (s *Server) schedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.Schedules(r.Context(), currentUser(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "schedules", map[string]any{"Schedules": list, "Zones": zones, "Error": r.URL.Query().Get("err")})
}

func (s *Server) scheduleCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	sc, err := scheduleFromForm(r.PostForm)
	if err == nil {
		_, err = s.App.CreateSchedule(r.Context(), currentUser(r).ID, sc)
	}
	if err != nil {
		redirect(w, r, "/app/schedules?err="+url.QueryEscape(errorText(err)))
		return
	}
	redirect(w, r, "/app/schedules")
}

func (s *Server) scheduleEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sc, err := s.App.Schedule(r.Context(), currentUser(r).ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "schedule", map[string]any{"S": sc, "Zones": zones})
}

func (s *Server) scheduleUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	r.ParseForm()
	sc, err := scheduleFromForm(r.PostForm)
	sc.ID = id
	if err == nil {
		err = s.App.UpdateSchedule(r.Context(), currentUser(r).ID, sc)
	}
	if err != nil {
		s.render(w, r, http.StatusBadRequest, "schedule", map[string]any{"S": &sc, "Zones": zones, "Error": errorText(err)})
		return
	}
	redirect(w, r, "/app/schedules")
}

func (s *Server) scheduleDefault(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.App.SetDefaultSchedule(r.Context(), currentUser(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/app/schedules")
}

func (s *Server) scheduleDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.App.DeleteSchedule(r.Context(), currentUser(r).ID, id); err != nil {
		redirect(w, r, "/app/schedules?err="+url.QueryEscape(errorText(err)))
		return
	}
	redirect(w, r, "/app/schedules")
}

// --- time off ---

func (s *Server) timeOff(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.TimeOffs(r.Context(), currentUser(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "time_off", map[string]any{"List": list, "Error": r.URL.Query().Get("err")})
}

func (s *Server) timeOffCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.PostForm
	_, err := s.App.AddTimeOff(r.Context(), currentUser(r).ID, app.TimeOff{
		StartDate: f.Get("start_date"), EndDate: f.Get("end_date"), Reason: f.Get("reason"), Note: f.Get("note"), NotePublic: f.Get("note_public") != "",
	})
	if err != nil {
		redirect(w, r, "/app/time-off?err="+url.QueryEscape(errorText(err)))
		return
	}
	redirect(w, r, "/app/time-off")
}

func (s *Server) timeOffDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.App.DeleteTimeOff(r.Context(), currentUser(r).ID, id)
	redirect(w, r, "/app/time-off")
}

// bookingAction runs the organizer's confirm / reject / cancel / no-show actions.
func (s *Server) bookingAction(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	u, uid, ctx := currentUser(r), r.PathValue("uid"), r.Context()
	var err error
	switch r.PathValue("action") {
	case "confirm":
		err = s.App.ConfirmBooking(ctx, u.ID, uid)
	case "reject":
		err = s.App.RejectBooking(ctx, u.ID, uid, r.PostForm.Get("reason"))
	case "cancel":
		err = s.App.CancelBooking(ctx, uid, u.ID, r.PostForm.Get("reason"))
	case "no-show":
		err = s.App.MarkNoShow(ctx, u.ID, uid, r.PostForm.Get("who"), r.PostForm.Get("value") != "false")
	default:
		err = &app.Error{Code: app.ErrNotFound}
	}
	if err != nil {
		redirect(w, r, "/app?msg="+url.QueryEscape(errorText(err)))
		return
	}
	redirect(w, r, "/app")
}
