# Rebuild

**Status:** Draft · **Prefix:** RB

## Purpose

Implement the individual-event core of `@spechub/scheduling@0.1.0` in Go + SQLite + htmx, from the spec alone.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| RB-1 | Implements, as written in `specs/upstream/`: `@spechub/auth:AUTH-1,2,3,4,6,13`; AVL-1..8, AVL-10; EVT-1..8, 10, 11, 13, 14, 16; SLT-1, 2, 4..9, 12..15, 17, 18; BKG-1..13, 15, 18; PUB-1, 2, 4, 5, 6. | TestSpecCoverage |
| RB-2 | Every in-scope requirement has a Go test `Test<PREFIX>_<n>` that runs its `Verified by` check (manual checks automated where possible). | TestSpecCoverage |
| RB-3 | Where the spec is ambiguous or silent, the choice made is listed in `FINDINGS.md` as `F-n` and the code cites it. | manual: FINDINGS.md |
| RB-4 | Emails are not sent: each is written to the log (`email to=… subject=…`) with its links. | TestBKG_11 |
| RB-5 | Calendar and video integrations are an interface; the default does nothing, and a failing fake drives BKG-10. | TestBKG_10 |
| RB-6 | One SQLite file; writes run in one connection, so booking re-checks and inserts are atomic. | TestBKG_7 |
| RB-7 | `go run ./cmd/scheduling` serves the app; `-addr`, `-db`, `-signup` (default on), `-base-url` flags. | manual: run, sign up, book |

## Out of scope

- Teams, round robin, dynamic groups, seats, recurring, payments, calendar/video providers, webhooks, real email.
- AUTH-5, 7..12, 14..16; AVL-9, 11, 12; EVT-9, 12, 15 (fields covered by BKG-3), 17..19 (EVT-19 needed by AVL-5, built); SLT-3, 10, 11, 16; BKG-14, 16, 17, 19; PUB-3, 7..9.

## Decisions

- Pure-Go SQLite driver (`modernc.org/sqlite`), no cgo.
- argon2id for passwords (AUTH-4).
- Server-rendered `html/template`; htmx only re-renders the time list on zone/date/duration change.
