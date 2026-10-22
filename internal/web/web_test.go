package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"scheduling-rebuild/internal/app"
)

var ctx = context.Background()

type tenv struct {
	t   *testing.T
	a   *app.App
	ts  *httptest.Server
	c   *http.Client
	ann *app.User
	et  *app.EventType
}

// newTenv: TLS test server (Secure cookies), now = Sun 2026-11-01 12:00 UTC, user ann (UTC) with event "intro".
func newTenv(t *testing.T) *tenv {
	t.Helper()
	db, err := app.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(db, "")
	a.Now = func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }
	a.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	a.Mail = app.LogMailer{Log: a.Log}
	ts := httptest.NewTLSServer(New(a).Handler())
	t.Cleanup(func() { ts.Close(); db.Close() })
	a.BaseURL = ts.URL
	ann, err := a.SignUp(ctx, app.SignUpInput{Email: "ann@example.com", Username: "ann", Name: "Ann Host", Password: "correct horse battery", TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	et := app.NewEventType()
	et.Title, et.Slug, et.MinNotice = "Intro call", "intro", 0
	created, err := a.CreateEventType(ctx, ann.ID, et)
	if err != nil {
		t.Fatal(err)
	}
	e := &tenv{t: t, a: a, ts: ts, ann: ann, et: created}
	e.c = e.client()
	return e
}

func (e *tenv) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	c := *e.ts.Client() // a copy: the server's client is shared
	c.Jar = jar
	return &c
}

func (e *tenv) get(c *http.Client, path string, hdr ...string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (e *tenv) post(c *http.Client, path string, form url.Values) (*http.Response, string) {
	e.t.Helper()
	res, err := c.PostForm(e.ts.URL+path, form)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func must(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("missing %q in:\n%s", w, body)
		}
	}
}

func mustNot(t *testing.T, body string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(body, w) {
			t.Fatalf("unexpected %q", w)
		}
	}
}

// AUTH-13: HTTP-only, Secure, SameSite=Lax cookie; revoke session → next request 401.
func TestAUTH_13(t *testing.T) {
	e := newTenv(t)
	noRedirect := e.client()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, _ := e.post(noRedirect, "/login", url.Values{"email": {"ann@example.com"}, "password": {"correct horse battery"}})
	sc := res.Header.Get("Set-Cookie")
	for _, attr := range []string{"session=", "HttpOnly", "Secure", "SameSite=Lax", "Max-Age=2592000"} {
		if !strings.Contains(sc, attr) {
			t.Fatalf("cookie %q lacks %s", sc, attr)
		}
	}
	if code, _ := e.get(noRedirect, "/app"); code != 200 {
		t.Fatalf("signed in: %d", code)
	}
	other := e.client()
	e.post(other, "/login", url.Values{"email": {"ann@example.com"}, "password": {"correct horse battery"}})
	_, page := e.get(other, "/app/sessions")
	cards := strings.Split(page, `class="card row"`)[1:]
	if len(cards) != 2 || !strings.Contains(page, "this browser") {
		t.Fatalf("sessions listed: %d", len(cards))
	}
	idRe := regexp.MustCompile(`/app/sessions/(\d+)/revoke`)
	for _, c := range cards { // revoke the first browser's session from the second
		if !strings.Contains(c, "this browser") {
			e.post(other, "/app/sessions/"+idRe.FindStringSubmatch(c)[1]+"/revoke", nil)
		}
	}
	if code, _ := e.get(noRedirect, "/app"); code != http.StatusUnauthorized {
		t.Fatalf("revoked session: %d, want 401", code)
	}
	if code, _ := e.get(other, "/app"); code != 200 {
		t.Fatalf("other session: %d", code)
	}
}

// PUB-1: name, avatar, bio and visible event types (title, durations, description); hidden event not listed.
func TestPUB_1(t *testing.T) {
	e := newTenv(t)
	e.a.UpdateProfile(ctx, e.ann.ID, app.ProfileInput{Name: "Ann Host", Bio: "Coach", AvatarURL: "https://img.example/ann.png", TimeZone: "UTC", WeekStart: "monday"})
	h := app.NewEventType()
	h.Title, h.Slug, h.Hidden = "Secret chat", "secret", true
	e.a.CreateEventType(ctx, e.ann.ID, h)
	m := app.NewEventType()
	m.Title, m.Slug, m.Description, m.Length, m.Durations = "Deep dive", "deep", "Long form", 60, []int{30, 60}
	e.a.CreateEventType(ctx, e.ann.ID, m)
	code, body := e.get(e.c, "/ann")
	if code != 200 {
		t.Fatal(code)
	}
	must(t, body, "Ann Host", "Coach", "https://img.example/ann.png", "Intro call", "/ann/intro", "Deep dive", "30,60 min", "Long form")
	mustNot(t, body, "Secret chat")
	if code, _ := e.get(e.c, "/ann/secret"); code != 200 {
		t.Fatalf("hidden event by link: %d", code)
	}
	if code, _ := e.get(e.c, "/nobody"); code != 404 {
		t.Fatalf("unknown user: %d", code)
	}
}

// PUB-2: month calendar, times in the booker's zone; zone changed → times re-rendered; 12/24 h; duration picker.
func TestPUB_2(t *testing.T) {
	e := newTenv(t)
	_, berlin := e.get(e.c, "/ann/intro?tz=Europe/Berlin&date=2026-11-02")
	must(t, berlin, "November 2026", `aria-current="date" href="/ann/intro?date=2026-11-02`, ">10:00<", ">17:30<")
	mustNot(t, berlin, ">09:00<", "Duration</label>")
	code, tokyo := e.get(e.c, "/ann/intro?tz=Asia/Tokyo&date=2026-11-02", "HX-Request", "true")
	if code != 200 || strings.Contains(tokyo, "<html") {
		t.Fatal("htmx request must get the partial only")
	}
	must(t, tokyo, ">18:00<", `<option selected>Asia/Tokyo</option>`)
	_, h12 := e.get(e.c, "/ann/intro?tz=America/New_York&date=2026-11-02&h12=1")
	must(t, h12, ">4:00am<", ">11:30am<")
	m := app.NewEventType()
	m.Title, m.Slug, m.Length, m.Durations, m.MinNotice = "Flexible", "flex", 30, []int{30, 60}, 0
	e.a.CreateEventType(ctx, e.ann.ID, m)
	_, flex := e.get(e.c, "/ann/flex?tz=UTC&date=2026-11-02&duration=60")
	must(t, flex, "Duration</label>", `<option value="60" selected>60 min</option>`, ">16:00<")
	mustNot(t, flex, ">16:30<")
	_, detect := e.get(e.c, "/ann/intro")
	must(t, detect, "Intl.DateTimeFormat") // no tz given: the page detects the booker's zone
}

// PUB-4: choosing a time places the hold and shows the form; `?email=a@b.c` → email prefilled.
func TestPUB_4(t *testing.T) {
	e := newTenv(t)
	_, page := e.get(e.c, "/ann/intro?tz=UTC&date=2026-11-02&email=a@b.c")
	link := regexp.MustCompile(`href="(/ann/intro/book\?start=2026-11-02T10%3a00%3a00Z[^"]*|/ann/intro/book\?start=2026-11-02T10:00:00Z[^"]*)"`).FindStringSubmatch(page)
	if link == nil {
		t.Fatalf("no 10:00 link in %s", page)
	}
	href := strings.ReplaceAll(link[1], "&amp;", "&")
	code, form := e.get(e.c, href)
	if code != 200 {
		t.Fatal(code)
	}
	must(t, form, `name="email" value="a@b.c"`, `name="name" value=""`, "held for you for 5 minutes")
	mustNot(t, form, `name="phone"`) // hidden by default (BKG-3)
	other := e.client()
	_, theirs := e.get(other, "/ann/intro?tz=UTC&date=2026-11-02")
	mustNot(t, theirs, ">10:00<")
	_, mine := e.get(e.c, "/ann/intro?tz=UTC&date=2026-11-02")
	must(t, mine, ">10:00<")
}

func (e *tenv) bookVia(c *http.Client, start string, extra ...string) (*http.Response, string) {
	f := url.Values{"start": {start}, "tz": {"Europe/Berlin"}, "duration": {"30"}, "name": {"Bea Booker"}, "email": {"bea@example.com"}}
	for i := 0; i+1 < len(extra); i += 2 {
		f.Set(extra[i], extra[i+1])
	}
	return e.post(c, "/ann/intro/book", f)
}

// PUB-5: open uid → what, when (booker zone), where, who, add-to-calendar links; pending status shown.
func TestPUB_5(t *testing.T) {
	e := newTenv(t)
	res, body := e.bookVia(e.c, "2026-11-02T10:00:00Z")
	if !strings.HasPrefix(res.Request.URL.Path, "/booking/") {
		t.Fatalf("not redirected: %s", body)
	}
	uid := strings.TrimPrefix(res.Request.URL.Path, "/booking/")
	must(t, body, "This meeting is scheduled", "Intro call", "Monday 2 November 2026, 11:00–11:30", "Europe/Berlin",
		"Video/link: "+e.ts.URL+"/video/"+uid, "Ann Host", "Bea Booker", "calendar.google.com", "outlook.live.com", "/booking/"+uid+"/ics")
	code, ics := e.get(e.c, "/booking/"+uid+"/ics")
	if code != 200 {
		t.Fatal(code)
	}
	must(t, ics, "BEGIN:VCALENDAR", "UID:"+uid+"@scheduling", "SEQUENCE:0", "DTSTART:20261102T100000Z", "DTEND:20261102T103000Z", "STATUS:CONFIRMED")

	conf := app.NewEventType()
	conf.Title, conf.Slug, conf.RequiresConfirmation, conf.MinNotice = "Vetted", "vetted", true, 0
	e.a.CreateEventType(ctx, e.ann.ID, conf)
	res, body = e.post(e.c, "/ann/vetted/book", url.Values{"start": {"2026-11-02T12:00:00Z"}, "tz": {"UTC"}, "name": {"B"}, "email": {"b@example.com"}})
	must(t, body, "Request sent", "pending")
	if code, _ := e.get(e.c, "/booking/nope"); code != 404 {
		t.Fatal("unknown uid")
	}
	_, taken := e.bookVia(e.client(), "2026-11-02T10:00:00Z", "email", "c@example.com")
	must(t, taken, "That time was just taken")
}

// PUB-6: cancel from page → status cancelled; reschedule from the page links old and new.
func TestPUB_6(t *testing.T) {
	e := newTenv(t)
	res, body := e.bookVia(e.c, "2026-11-02T10:00:00Z")
	uid := strings.TrimPrefix(res.Request.URL.Path, "/booking/")
	must(t, body, "Reschedule</a>", "Cancel booking")
	rlink := regexp.MustCompile(`href="(/ann/intro\?reschedule=[^"]+)"`).FindStringSubmatch(body)
	if rlink == nil {
		t.Fatal("no reschedule link")
	}
	_, picker := e.get(e.c, strings.ReplaceAll(rlink[1], "&amp;", "&")+"&date=2026-11-02")
	must(t, picker, "Rescheduling booking "+uid, ">11:00<") // own slot offered (SLT-17)
	res, body = e.bookVia(e.c, "2026-11-03T10:00:00Z", "reschedule", uid, "reschedule_reason", "clash")
	newUID := strings.TrimPrefix(res.Request.URL.Path, "/booking/")
	if newUID == uid || !strings.Contains(body, "This meeting is scheduled") {
		t.Fatalf("reschedule: %s", body)
	}
	_, old := e.get(e.c, "/booking/"+uid)
	must(t, old, "This booking was rescheduled", "/booking/"+newUID)
	mustNot(t, old, "Cancel booking")

	res, body = e.post(e.c, "/booking/"+newUID+"/cancel", url.Values{"reason": {"sick"}})
	must(t, body, "This booking was cancelled", "Reason: sick", "status-cancelled")
	if b, _ := e.a.Booking(ctx, newUID); b.Status != app.StatusCancelled {
		t.Fatal(b.Status)
	}
}

// BKG-11 over HTTP: the emailed link shows a page; only POST acts; reuse is refused.
func TestBKG_11_Links(t *testing.T) {
	e := newTenv(t)
	conf := app.NewEventType()
	conf.Title, conf.Slug, conf.RequiresConfirmation, conf.MinNotice = "Vetted", "vetted", true, 0
	et, _ := e.a.CreateEventType(ctx, e.ann.ID, conf)
	var sent []app.Email
	e.a.Mail = mailFunc(func(m app.Email) { sent = append(sent, m) })
	b, err := e.a.CreateBooking(ctx, app.BookingInput{EventTypeID: et.ID, Start: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), TimeZone: "UTC",
		Responses: url.Values{"name": {"B"}, "email": {"b@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Confirm: \S+(/action/\S+)`).FindStringSubmatch(sent[0].Body)
	code, page := e.get(e.c, m[1])
	if code != 200 {
		t.Fatal(code)
	}
	must(t, page, "Confirm this booking?")
	if got, _ := e.a.Booking(ctx, b.UID); got.Status != app.StatusPending {
		t.Fatal("GET must not confirm")
	}
	_, done := e.post(e.c, m[1], nil)
	must(t, done, "Confirmed")
	_, again := e.post(e.c, m[1], nil)
	must(t, again, "already used")
}

type mailFunc func(app.Email)

func (f mailFunc) Send(m app.Email) { f(m) }

// Every owner page renders for a signed-in user.
func TestOwnerPages(t *testing.T) {
	e := newTenv(t)
	e.post(e.c, "/login", url.Values{"email": {"ann@example.com"}, "password": {"correct horse battery"}})
	e.bookVia(e.client(), "2026-11-02T10:00:00Z")
	for _, p := range []string{"/app", "/app/event-types/new", "/app/schedules", "/app/time-off", "/app/profile", "/app/sessions"} {
		if code, body := e.get(e.c, p); code != 200 {
			t.Fatalf("%s: %d %s", p, code, body)
		}
	}
	_, eventPage := e.get(e.c, "/app/event-types/1")
	must(t, eventPage, `value="Intro call"`)
	res, body := e.post(e.c, "/app/event-types/1", url.Values{"title": {"Intro call"}, "slug": {"intro"}, "length": {"45"}, "min_notice": {"0"},
		"window_kind": {"rolling"}, "window_days": {"10"}, "lb_day": {"3"}, "allow_cancel": {"on"}, "cancel_reason": {"optional"},
		"locations": {"in_person 1 Main St\nphone_booker"}, "booking_fields": {`[{"name":"company","type":"text","label":"Company"}]`}})
	if res.Request.URL.Path != "/app" {
		t.Fatalf("event update: %s", body)
	}
	et, _ := e.a.EventType(ctx, 1)
	if et.Length != 45 || et.Window.Days != 10 || *et.Limits.Bookings.Day != 3 || len(et.Locations) != 2 || et.Locations[0].Value != "1 Main St" || et.AllowReschedule || len(et.BookingFields) != 1 {
		t.Fatalf("%+v", et)
	}
	res, _ = e.post(e.c, "/app/schedules", url.Values{"name": {"Evenings"}, "time_zone": {"Europe/Berlin"}, "rules": {"1,2 18:00-20:00"}, "overrides": {"2026-12-24 off"}})
	ss, _ := e.a.Schedules(ctx, e.ann.ID)
	if len(ss) != 2 || ss[1].TimeZone != "Europe/Berlin" || len(ss[1].Overrides) != 1 || ss[1].Overrides[0].Start != "00:00" {
		t.Fatalf("%+v", ss[1])
	}
	_, body = e.post(e.c, "/app/bookings/x/confirm", nil)
	must(t, body, "Event types")
}
