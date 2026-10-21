package app

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	email TEXT NOT NULL UNIQUE,
	email_verified_at INTEGER,
	username TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	locale TEXT NOT NULL DEFAULT 'en',
	locked INTEGER NOT NULL DEFAULT 0,
	role TEXT NOT NULL DEFAULT 'user',
	password_hash TEXT,
	time_zone TEXT NOT NULL,
	week_start TEXT NOT NULL DEFAULT 'monday',
	bio TEXT NOT NULL DEFAULT '',
	avatar_url TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	user_agent TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS schedules (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	time_zone TEXT NOT NULL DEFAULT '',
	is_default INTEGER NOT NULL DEFAULT 0,
	rules TEXT NOT NULL DEFAULT '[]',
	overrides TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS time_off (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	start_date TEXT NOT NULL,
	end_date TEXT NOT NULL,
	reason TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	note_public INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS event_types (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	slug TEXT NOT NULL,
	data TEXT NOT NULL,
	UNIQUE (user_id, slug)
);
CREATE TABLE IF NOT EXISTS bookings (
	id INTEGER PRIMARY KEY,
	uid TEXT NOT NULL UNIQUE,
	event_type_id INTEGER NOT NULL REFERENCES event_types(id),
	organizer_id INTEGER NOT NULL REFERENCES users(id),
	start_at INTEGER NOT NULL,
	end_at INTEGER NOT NULL,
	location TEXT NOT NULL DEFAULT '{}',
	responses TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL,
	paid INTEGER NOT NULL DEFAULT 0,
	from_reschedule TEXT,
	rescheduled INTEGER NOT NULL DEFAULT 0,
	cancel_reason TEXT,
	cancelled_by TEXT,
	reject_reason TEXT,
	sequence INTEGER NOT NULL DEFAULT 0,
	ical_uid TEXT NOT NULL,
	host_no_show INTEGER NOT NULL DEFAULT 0,
	booker_email TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
-- BKG-7: at most one accepted booking per host per exact [start, end).
CREATE UNIQUE INDEX IF NOT EXISTS bookings_one_accepted
	ON bookings (organizer_id, start_at, end_at) WHERE status = 'accepted';
CREATE INDEX IF NOT EXISTS bookings_organizer_time ON bookings (organizer_id, start_at);
CREATE TABLE IF NOT EXISTS attendees (
	id INTEGER PRIMARY KEY,
	booking_id INTEGER NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	email TEXT NOT NULL,
	time_zone TEXT NOT NULL,
	phone TEXT NOT NULL DEFAULT '',
	locale TEXT NOT NULL DEFAULT 'en',
	no_show INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS attendees_email ON attendees (email);
CREATE TABLE IF NOT EXISTS holds (
	id INTEGER PRIMARY KEY,
	booker_id TEXT NOT NULL,
	host_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	event_type_id INTEGER NOT NULL,
	start_at INTEGER NOT NULL,
	end_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS action_tokens (
	token_hash TEXT PRIMARY KEY,
	booking_id INTEGER NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
	action TEXT NOT NULL,
	used_at INTEGER
);
CREATE TABLE IF NOT EXISTS booking_warnings (
	id INTEGER PRIMARY KEY,
	booking_id INTEGER NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
	message TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
`

// OpenDB opens (and migrates) the SQLite database at path; ":memory:" for tests.
// One connection only (RB-6): every write, including a booking's re-check and insert, is serialised.
func OpenDB(path string) (*sql.DB, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	for _, p := range []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000"} {
		if _, err := db.Exec(p); err != nil {
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}
