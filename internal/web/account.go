package web

import (
	"net/http"
	"strconv"
	"strings"

	"scheduling-rebuild/internal/app"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if u, _ := s.sessionUser(r); u != nil {
		redirect(w, r, "/app")
		return
	}
	redirect(w, r, "/login")
}

func (s *Server) signupPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "signup", map[string]any{"Zones": zones, "Form": map[string]string{}})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	in := app.SignUpInput{
		Email: r.PostForm.Get("email"), Username: r.PostForm.Get("username"), Name: r.PostForm.Get("name"),
		Password: r.PostForm.Get("password"), TimeZone: r.PostForm.Get("time_zone"), WeekStart: r.PostForm.Get("week_start"),
	}
	u, err := s.App.SignUp(r.Context(), in)
	if err != nil {
		form := map[string]string{"email": in.Email, "username": in.Username, "name": in.Name, "time_zone": in.TimeZone}
		s.render(w, r, http.StatusBadRequest, "signup", map[string]any{"Zones": zones, "Form": form, "Error": errorText(err)})
		return
	}
	s.startSession(w, r, u, "/app")
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *app.User, next string) {
	tok, err := s.App.CreateSession(r.Context(), u.ID, r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	setCookie(w, sessionCookie, tok, app.SessionIdle) // AUTH-13; the server enforces idle expiry
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/app"
	}
	redirect(w, r, next)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "login", map[string]any{"Next": r.URL.Query().Get("next")})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	u, err := s.App.SignIn(r.Context(), r.PostForm.Get("email"), r.PostForm.Get("password"))
	if err != nil {
		s.render(w, r, http.StatusUnauthorized, "login", map[string]any{"Error": errorText(err), "Email": r.PostForm.Get("email"), "Next": r.PostForm.Get("next")})
		return
	}
	s.startSession(w, r, u, r.PostForm.Get("next"))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.App.SignOut(r.Context(), c.Value)
	}
	setCookie(w, sessionCookie, "", -1)
	redirect(w, r, "/login")
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	_, current := s.sessionUser(r)
	list, err := s.App.Sessions(r.Context(), currentUser(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "sessions", map[string]any{"Sessions": list, "Current": current})
}

func (s *Server) sessionRevoke(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.App.RevokeSession(r.Context(), currentUser(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/app/sessions")
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "profile", map[string]any{"Zones": zones})
}

func (s *Server) profileUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.PostForm
	err := s.App.UpdateProfile(r.Context(), currentUser(r).ID, app.ProfileInput{
		Name: f.Get("name"), Bio: f.Get("bio"), AvatarURL: f.Get("avatar_url"), TimeZone: f.Get("time_zone"), WeekStart: f.Get("week_start"),
	})
	if err != nil {
		s.render(w, r, http.StatusBadRequest, "profile", map[string]any{"Zones": zones, "Error": errorText(err)})
		return
	}
	redirect(w, r, "/app/profile")
}
