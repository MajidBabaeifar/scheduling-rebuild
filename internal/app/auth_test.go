package app

import (
	"strings"
	"testing"
	"time"
)

func signup(e *env, email, username, pw, tz string) (*User, error) {
	return e.SignUp(ctx, SignUpInput{Email: email, Username: username, Name: "Ann", Password: pw, TimeZone: tz})
}

// AUTH-1: signup `Ann@X.com` → stored `ann@x.com`.
func TestAUTH_1(t *testing.T) {
	e := newEnv(t)
	u, err := signup(e, "Ann@X.com", "ann", goodPassword, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "ann@x.com" || u.Role != "user" || u.Locked || u.Locale != "en" {
		t.Fatalf("got %+v", u)
	}
	for _, bad := range []string{"an", "a+b", "ann_x", strings.Repeat("a", 41), "app"} {
		if _, err := signup(e, bad+"1@x.com", bad, goodPassword, "UTC"); Code(err) != ErrInvalid {
			t.Errorf("username %q: want invalid, got %v", bad, err)
		}
	}
}

// AUTH-2: signup disabled, no invite → refused; taken email or username → `taken`.
func TestAUTH_2(t *testing.T) {
	e := newEnv(t)
	e.user(t, "ann", "UTC")
	_, err := signup(e, "ANN@example.com", "other", goodPassword, "UTC")
	wantCode(t, err, ErrTaken)
	_, err = signup(e, "new@example.com", "ann", goodPassword, "UTC")
	wantCode(t, err, ErrTaken)
	e.SignupEnabled = false
	_, err = signup(e, "bob@example.com", "bob", goodPassword, "UTC")
	wantCode(t, err, ErrSignupDisabled)
}

// AUTH-3: `Password1` → refused (common). (F-4: it is also under 10 characters.)
func TestAUTH_3(t *testing.T) {
	for pw, ok := range map[string]bool{
		"Password1":              false,
		"password1234":           false, // common, long enough
		"QwertyUiop":             false,
		"aaaaaaaaaaaa":           false,
		strings.Repeat("x", 129): false,
		"ann-secret-thing":       false, // contains the username
		goodPassword:             true,
		"all lower no digits ok": true,
	} {
		err := CheckPassword(pw, "ann")
		if (err == nil) != ok {
			t.Errorf("%q: ok=%v err=%v", pw, ok, err)
		}
	}
	e := newEnv(t)
	_, err := signup(e, "a@x.com", "ann", "Password1", "UTC")
	wantCode(t, err, ErrWeakPassword)
}

// AUTH-4: manual: stored hash format — argon2id, never the password.
func TestAUTH_4(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	h, err := e.StoredPasswordHash(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(h, goodPassword) {
		t.Fatalf("hash %q", h)
	}
	if !verifyPassword(h, goodPassword) || verifyPassword(h, goodPassword+"x") {
		t.Fatal("verify")
	}
}

// AUTH-6: unknown email vs wrong password → same error; locked → `locked`.
func TestAUTH_6(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	_, errUnknown := e.SignIn(ctx, "nobody@example.com", goodPassword)
	_, errWrong := e.SignIn(ctx, "ann@example.com", "wrong password!")
	if Code(errUnknown) != ErrInvalidCredentials || errUnknown.Error() != errWrong.Error() {
		t.Fatalf("unknown=%v wrong=%v", errUnknown, errWrong)
	}
	if got, err := e.SignIn(ctx, "ANN@example.com ", goodPassword); err != nil || got.ID != u.ID {
		t.Fatalf("sign in: %v", err)
	}
	e.DB.Exec(`INSERT INTO users (email, username, name, time_zone, created_at) VALUES ('nopw@example.com', 'nopw', 'N', 'UTC', 0)`)
	_, err := e.SignIn(ctx, "nopw@example.com", "")
	wantCode(t, err, ErrInvalidCredentials)
	e.SetLocked(ctx, u.ID, true)
	_, err = e.SignIn(ctx, "ann@example.com", goodPassword)
	wantCode(t, err, ErrLocked)
	_, err = e.SignIn(ctx, "ann@example.com", "wrong password!") // F-5: no lock disclosure without the password
	wantCode(t, err, ErrInvalidCredentials)
}

// AUTH-13 (domain half; the cookie and 401 are in internal/web): revoke, idle timeout.
func TestAUTH_13_Sessions(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, "ann", "UTC")
	tok, _ := e.CreateSession(ctx, u.ID, "test")
	if _, _, err := e.SessionUser(ctx, tok); err != nil {
		t.Fatal(err)
	}
	e.clk.add(29 * 24 * time.Hour)
	if _, _, err := e.SessionUser(ctx, tok); err != nil {
		t.Fatal("29 days idle should still be valid")
	}
	e.clk.add(31 * 24 * time.Hour)
	if _, _, err := e.SessionUser(ctx, tok); err == nil {
		t.Fatal("31 days idle should expire")
	}
	tok2, _ := e.CreateSession(ctx, u.ID, "test")
	ss, _ := e.Sessions(ctx, u.ID)
	if len(ss) != 1 {
		t.Fatalf("sessions %d", len(ss))
	}
	if err := e.RevokeSession(ctx, u.ID, ss[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.SessionUser(ctx, tok2); err == nil {
		t.Fatal("revoked session still valid")
	}
}
