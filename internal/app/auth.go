package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// User is AUTH-1 plus the profile fields PUB-1 needs (F-3) and AVL-1's zone and week start.
type User struct {
	ID        int64
	Email     string
	Username  string
	Name      string
	Locale    string
	Locked    bool
	Role      string
	TimeZone  string
	WeekStart string
	Bio       string
	AvatarURL string
}

// SignUpInput is what the sign-up form collects (F-1).
type SignUpInput struct {
	Email, Username, Name, Password, TimeZone, WeekStart, Locale string
}

var usernameRe = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

// reservedUsernames collide with app routes under `/{username}` (F-2).
var reservedUsernames = map[string]bool{
	"app": true, "api": true, "booking": true, "action": true, "static": true,
	"signup": true, "login": true, "logout": true, "admin": true, "settings": true,
}

//go:embed common_passwords.txt
var commonPasswordsTxt string

var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(commonPasswordsTxt, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			m[strings.ToLower(l)] = true
		}
	}
	return m
}()

// CheckPassword applies AUTH-3: 10..128 characters, not common, no composition rules (F-4).
func CheckPassword(pw string, context ...string) error {
	n := utf8.RuneCountInString(pw)
	if n < 10 {
		return errf(ErrWeakPassword, "at least 10 characters")
	}
	if n > 128 {
		return errf(ErrWeakPassword, "at most 128 characters")
	}
	low := strings.ToLower(pw)
	if commonPasswords[low] {
		return errf(ErrWeakPassword, "too common")
	}
	if strings.Count(low, low[:1]) == len(low) {
		return errf(ErrWeakPassword, "too common")
	}
	for _, c := range context {
		if c != "" && strings.Contains(low, strings.ToLower(c)) {
			return errf(ErrWeakPassword, "contains your email or username")
		}
	}
	return nil
}

// AUTH-4: argon2id, OWASP minimum parameters (19 MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash equalises sign-in timing for unknown emails (AUTH-6).
var dummyHash = hashPassword("dummy-password-for-timing")

// NormalizeEmail lower-cases and validates a bare address.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return "", errf(ErrFieldInvalid, "email")
	}
	// F-31: "valid" also means a dotted domain (RFC 5322 alone accepts `a@b`).
	if d := s[strings.LastIndex(s, "@")+1:]; !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return "", errf(ErrFieldInvalid, "email")
	}
	return s, nil
}

// SignUp creates a user and their default schedule (AUTH-1..4, AVL-1, AVL-3).
func (a *App) SignUp(ctx context.Context, in SignUpInput) (*User, error) {
	if !a.SignupEnabled {
		return nil, &Error{Code: ErrSignupDisabled}
	}
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return nil, errf(ErrInvalid, "email")
	}
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRe.MatchString(username) || reservedUsernames[username] {
		return nil, errf(ErrInvalid, "username: 3-40 of a-z, 0-9, -")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return nil, errf(ErrInvalid, "name: 1-100 characters")
	}
	if _, err := LoadZone(in.TimeZone); err != nil {
		return nil, errf(ErrInvalid, "time_zone: required IANA zone")
	}
	weekStart := in.WeekStart
	if weekStart == "" {
		weekStart = "monday"
	}
	if weekStart != "monday" && weekStart != "sunday" {
		return nil, errf(ErrInvalid, "week_start")
	}
	locale := in.Locale
	if locale == "" {
		locale = "en"
	}
	if err := CheckPassword(in.Password, username, strings.Split(email, "@")[0]); err != nil {
		return nil, err
	}
	hash := hashPassword(in.Password)

	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE email = ? OR username = ?`, email, username).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, &Error{Code: ErrTaken}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users (email, username, name, locale, password_hash, time_zone, week_start, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, email, username, name, locale, hash, in.TimeZone, weekStart, a.now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := insertSchedule(ctx, tx, id, DefaultSchedule(), true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a.User(ctx, id)
}

const userCols = `id, email, username, name, locale, locked, role, time_zone, week_start, bio, avatar_url`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Username, &u.Name, &u.Locale, &u.Locked, &u.Role, &u.TimeZone, &u.WeekStart, &u.Bio, &u.AvatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: ErrNotFound}
	}
	return &u, err
}

// User loads a user by id.
func (a *App) User(ctx context.Context, id int64) (*User, error) {
	return scanUser(a.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// UserByUsername loads a user by username.
func (a *App) UserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(a.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, strings.ToLower(username)))
}

// StoredPasswordHash returns the encoded hash (AUTH-4 check).
func (a *App) StoredPasswordHash(ctx context.Context, userID int64) (string, error) {
	var h sql.NullString
	err := a.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&h)
	return h.String, err
}

// ProfileInput is what a user may change about themselves.
type ProfileInput struct{ Name, Bio, AvatarURL, TimeZone, WeekStart string }

// UpdateProfile changes name, bio, avatar, zone and week start.
func (a *App) UpdateProfile(ctx context.Context, userID int64, in ProfileInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errf(ErrInvalid, "name")
	}
	if _, err := LoadZone(in.TimeZone); err != nil {
		return err
	}
	if in.WeekStart != "monday" && in.WeekStart != "sunday" {
		return errf(ErrInvalid, "week_start")
	}
	_, err := a.DB.ExecContext(ctx, `UPDATE users SET name = ?, bio = ?, avatar_url = ?, time_zone = ?, week_start = ? WHERE id = ?`,
		strings.TrimSpace(in.Name), in.Bio, in.AvatarURL, in.TimeZone, in.WeekStart, userID)
	return err
}

// SetLocked locks or unlocks a user (instance admin action; no UI in v0.1).
func (a *App) SetLocked(ctx context.Context, userID int64, locked bool) error {
	_, err := a.DB.ExecContext(ctx, `UPDATE users SET locked = ? WHERE id = ?`, locked, userID)
	return err
}

// SignIn checks email + password (AUTH-6). Unknown email, wrong password and password-less
// accounts give the same error; `locked` is told only to someone who knows the password (F-5).
func (a *App) SignIn(ctx context.Context, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var id int64
	var hash sql.NullString
	err := a.DB.QueryRowContext(ctx, `SELECT id, password_hash FROM users WHERE email = ?`, email).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !hash.Valid) {
		verifyPassword(dummyHash, password)
		return nil, &Error{Code: ErrInvalidCredentials}
	}
	if err != nil {
		return nil, err
	}
	if !verifyPassword(hash.String, password) {
		return nil, &Error{Code: ErrInvalidCredentials}
	}
	u, err := a.User(ctx, id)
	if err != nil {
		return nil, err
	}
	if u.Locked {
		return nil, &Error{Code: ErrLocked}
	}
	return u, nil
}

// SessionIdle is AUTH-13's idle timeout.
const SessionIdle = 30 * 24 * time.Hour

// Session is a signed-in browser.
type Session struct {
	ID         int64
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// CreateSession returns a new opaque session token; only its SHA-256 is stored (F-6).
func (a *App) CreateSession(ctx context.Context, userID int64, userAgent string) (string, error) {
	token := randomToken(32)
	now := a.now().Unix()
	_, err := a.DB.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, user_agent, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), userID, userAgent, now, now)
	return token, err
}

// SessionUser resolves a token, enforcing the idle timeout and sliding it forward.
func (a *App) SessionUser(ctx context.Context, token string) (*User, int64, error) {
	var sid, uid, last int64
	err := a.DB.QueryRowContext(ctx, `SELECT id, user_id, last_seen_at FROM sessions WHERE token_hash = ?`, hashToken(token)).Scan(&sid, &uid, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, &Error{Code: ErrNotFound}
	}
	if err != nil {
		return nil, 0, err
	}
	now := a.now()
	if now.Sub(time.Unix(last, 0)) > SessionIdle {
		a.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sid)
		return nil, 0, &Error{Code: ErrNotFound}
	}
	u, err := a.User(ctx, uid)
	if err != nil {
		return nil, 0, err
	}
	if u.Locked {
		return nil, 0, &Error{Code: ErrLocked}
	}
	if now.Unix()-last > 60 {
		a.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now.Unix(), sid)
	}
	return u, sid, nil
}

// Sessions lists a user's sessions, newest first.
func (a *App) Sessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, user_agent, created_at, last_seen_at FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var c, l int64
		if err := rows.Scan(&s.ID, &s.UserAgent, &c, &l); err != nil {
			return nil, err
		}
		s.CreatedAt, s.LastSeenAt = time.Unix(c, 0).UTC(), time.Unix(l, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// RevokeSession deletes one of the user's own sessions.
func (a *App) RevokeSession(ctx context.Context, userID, sessionID int64) error {
	res, err := a.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &Error{Code: ErrNotFound}
	}
	return nil
}

// SignOut deletes the session behind token.
func (a *App) SignOut(ctx context.Context, token string) error {
	_, err := a.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}
