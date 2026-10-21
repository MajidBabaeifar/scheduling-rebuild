// Package web serves the scheduling app as server-rendered HTML (htmx for partial refreshes).
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scheduling-rebuild/internal/app"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server is the HTTP front end.
type Server struct {
	App   *app.App
	pages map[string]*template.Template
}

const (
	sessionCookie = "session"
	bookerCookie  = "booker"
)

// New parses templates and returns a server.
func New(a *app.App) *Server {
	s := &Server{App: a, pages: map[string]*template.Template{}}
	names, _ := templateFS.ReadDir("templates")
	for _, n := range names {
		name := n.Name()
		if name == "layout.html" || strings.HasPrefix(name, "_") {
			continue
		}
		t := template.New("layout.html").Funcs(funcs)
		t = template.Must(t.ParseFS(templateFS, "templates/layout.html", "templates/_*.html", "templates/"+name))
		s.pages[strings.TrimSuffix(name, ".html")] = t
	}
	return s
}

// Handler routes every page.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.home)
	m.HandleFunc("GET /signup", s.signupPage)
	m.HandleFunc("POST /signup", s.signup)
	m.HandleFunc("GET /login", s.loginPage)
	m.HandleFunc("POST /login", s.login)
	m.HandleFunc("POST /logout", s.logout)

	m.HandleFunc("GET /app", s.auth(s.dashboard))
	m.HandleFunc("GET /app/event-types/new", s.auth(s.eventTypeNew))
	m.HandleFunc("POST /app/event-types", s.auth(s.eventTypeCreate))
	m.HandleFunc("GET /app/event-types/{id}", s.auth(s.eventTypeEdit))
	m.HandleFunc("POST /app/event-types/{id}", s.auth(s.eventTypeUpdate))
	m.HandleFunc("POST /app/event-types/{id}/delete", s.auth(s.eventTypeDelete))
	m.HandleFunc("GET /app/schedules", s.auth(s.schedules))
	m.HandleFunc("POST /app/schedules", s.auth(s.scheduleCreate))
	m.HandleFunc("GET /app/schedules/{id}", s.auth(s.scheduleEdit))
	m.HandleFunc("POST /app/schedules/{id}", s.auth(s.scheduleUpdate))
	m.HandleFunc("POST /app/schedules/{id}/default", s.auth(s.scheduleDefault))
	m.HandleFunc("POST /app/schedules/{id}/delete", s.auth(s.scheduleDelete))
	m.HandleFunc("GET /app/time-off", s.auth(s.timeOff))
	m.HandleFunc("POST /app/time-off", s.auth(s.timeOffCreate))
	m.HandleFunc("POST /app/time-off/{id}/delete", s.auth(s.timeOffDelete))
	m.HandleFunc("POST /app/bookings/{uid}/{action}", s.auth(s.bookingAction))
	m.HandleFunc("GET /app/sessions", s.auth(s.sessions))
	m.HandleFunc("POST /app/sessions/{id}/revoke", s.auth(s.sessionRevoke))
	m.HandleFunc("GET /app/profile", s.auth(s.profile))
	m.HandleFunc("POST /app/profile", s.auth(s.profileUpdate))

	m.HandleFunc("GET /api/slots", s.apiSlots)
	m.HandleFunc("GET /booking/{uid}", s.bookingPage)
	m.HandleFunc("GET /booking/{uid}/ics", s.bookingICS)
	m.HandleFunc("POST /booking/{uid}/cancel", s.bookingCancel)
	m.HandleFunc("GET /action/{token}", s.actionPage)
	m.HandleFunc("POST /action/{token}", s.actionDo)
	m.HandleFunc("GET /video/{uid}", s.videoPage)
	m.HandleFunc("/", s.public)
	return s.sameOrigin(m)
}

// public routes /{username}, /{username}/{slug} and /{username}/{slug}/book. They're dispatched by hand
// because ServeMux can't rank /{username}/{slug}/book against /app/event-types/{id}; reserved
// usernames (F-2) keep the two apart.
func (s *Server) public(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	get, post := r.Method == http.MethodGet || r.Method == http.MethodHead, r.Method == http.MethodPost
	if len(parts) >= 1 {
		r.SetPathValue("username", parts[0])
	}
	if len(parts) >= 2 {
		r.SetPathValue("slug", parts[1])
	}
	switch {
	case len(parts) == 1 && get:
		s.publicProfile(w, r)
	case len(parts) == 2 && get:
		s.publicEvent(w, r)
	case len(parts) == 3 && parts[2] == "book" && get:
		s.bookForm(w, r)
	case len(parts) == 3 && parts[2] == "book" && post:
		s.book(w, r)
	default:
		s.message(w, r, http.StatusNotFound, "Not found", "There's no page here.")
	}
}

// sameOrigin refuses cross-site form posts (F-32: CSRF isn't in the spec; SameSite=Lax + Origin check).
func (s *Server) sameOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
			}
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

type ctxKey int

const userKey ctxKey = 0

func currentUser(r *http.Request) *app.User {
	u, _ := r.Context().Value(userKey).(*app.User)
	return u
}

// sessionUser resolves the session cookie, if any.
func (s *Server) sessionUser(r *http.Request) (*app.User, int64) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, 0
	}
	u, sid, err := s.App.SessionUser(r.Context(), c.Value)
	if err != nil {
		return nil, 0
	}
	return u, sid
}

// auth requires a session; without one the response is 401 with the sign-in form (AUTH-13).
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _ := s.sessionUser(r)
		if u == nil {
			s.render(w, r, http.StatusUnauthorized, "login", map[string]any{"Error": "Please sign in.", "Next": r.URL.Path})
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

func setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(maxAge / time.Second),
	})
}

// bookerID returns the anonymous booker id used for holds (SLT-15, F-18), setting it if new.
func bookerID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(bookerCookie); err == nil && c.Value != "" {
		return c.Value
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	setCookie(w, bookerCookie, id, 30*24*time.Hour)
	return id
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "no template "+page, http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["User"]; !ok {
		data["User"] = currentUser(r)
	}
	var buf bytes.Buffer
	name := "layout.html"
	if r.Header.Get("HX-Request") == "true" && t.Lookup("partial") != nil {
		name = "partial"
	}
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("render", "page", page, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) message(w http.ResponseWriter, r *http.Request, status int, title, text string) {
	s.render(w, r, status, "message", map[string]any{"Title": title, "Text": text})
}

// fail maps domain errors to HTTP statuses and a message page.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	switch app.Code(err) {
	case "":
		slog.Error("request", "path", r.URL.Path, "err", err)
		s.message(w, r, http.StatusInternalServerError, "Something went wrong", "Please try again.")
		return
	case app.ErrNotFound, app.ErrEventNotFound:
		status = http.StatusNotFound
	case app.ErrForbidden:
		status = http.StatusForbidden
	case app.ErrSlotTaken, app.ErrTaken, app.ErrNotPending, app.ErrBookingClosed, app.ErrTokenUsed:
		status = http.StatusConflict
	}
	s.message(w, r, status, "Can't do that", errorText(err))
}

var errorTexts = map[string]string{
	app.ErrSlotTaken:          "That time was just taken. Please pick another.",
	app.ErrUnavailable:        "That time isn't available.",
	app.ErrTooSoon:            "That time is too soon to book.",
	app.ErrInPast:             "That time has passed.",
	app.ErrOutsideWindow:      "That date can't be booked yet.",
	app.ErrLimitReached:       "No more bookings are possible in that period.",
	app.ErrInvalidCredentials: "Email or password is wrong.",
	app.ErrLocked:             "This account is locked.",
	app.ErrTaken:              "That email or username is already taken.",
	app.ErrSignupDisabled:     "Sign-up is turned off.",
	app.ErrTokenUsed:          "This link was already used or is invalid.",
	app.ErrNotPending:         "This booking is no longer waiting for confirmation.",
	app.ErrNotAllowed:         "The organizer doesn't allow this.",
	app.ErrTooLate:            "It's too close to the start to reschedule.",
	app.ErrBookingClosed:      "This booking is already cancelled or rejected.",
	app.ErrBookingEnded:       "This booking has already ended.",
	app.ErrReasonRequired:     "Please give a reason.",
	app.ErrTooEarly:           "The meeting hasn't started yet.",
	app.ErrLocation:           "Please choose a location.",
	app.ErrLastSchedule:       "You need at least one schedule.",
}

func errorText(err error) string {
	var e *app.Error
	if !errors.As(err, &e) {
		return "Something went wrong."
	}
	if t, ok := errorTexts[e.Code]; ok {
		return t
	}
	if e.Detail != "" {
		return strings.ReplaceAll(e.Code, "_", " ") + ": " + e.Detail
	}
	return strings.ReplaceAll(e.Code, "_", " ")
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
