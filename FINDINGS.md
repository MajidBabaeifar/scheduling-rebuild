# Findings

Spec gaps met while building `@spechub/scheduling@0.1.0` (+ `@spechub/auth`) from the spec alone.
Kind: **C** contradiction, **M** missing, **A** ambiguous, **T** wrong `Verified by`.
Code cites `F-n` where the assumption lives. ★ = proposed as a spec fix on SpecHub branch `fix/scheduling-rebuild-findings` (merged to SpecHub `main` at `01de4c7`, vendored in `specs/upstream/`). ✔ = resolved upstream in review.

| # | Req | Kind | Problem | Assumed here |
|---|---|---|---|---|
| F-1 ★ | AUTH-1, AUTH-2, AVL-1 | M | Sign-up inputs never listed; `username` and `name` are required by AUTH-1 but not said to be collected; no limits on `name`; `locale` has no default. | Sign-up takes email, username, name (1–100), password, time_zone, week_start; locale `en`. |
| F-2 ★ | AUTH-1, PUB-1 | M | `/{username}` shares the URL space with app routes (and `/{team-slug}`); no reserved names. | Reserved: app, api, booking, action, static, signup, login, logout, admin, settings. |
| F-3 ★ | PUB-1, AUTH-1 | M | Profile shows avatar and bio; no user field holds them. | `bio`, `avatar_url` on the user. |
| F-4 ★ | AUTH-3 | T, A | `Password1` is 9 chars, so the length rule refuses it; the common-list rule is never exercised. The list isn't named. | Embedded list of common 10+ char passwords, all-one-character, and passwords containing the username/email local part. Test also uses `password1234`. |
| F-5 ★ | AUTH-6 | A | Is `locked` returned before the password is checked? If so it reveals that the account exists. | `locked` only after a correct password. |
| F-6 ★ | AUTH-13 | M | Session token format/storage unspecified (API keys get a hash rule, sessions don't). | 256-bit random token; only SHA-256 stored; idle expiry slides on use. |
| F-7 ★ | AVL-2, AVL-7 | C | AVL-2 rejects `end ≤ start`; AVL-7 needs `start == end` overrides to block a date. | Weekly rules `end > start`; overrides `end ≥ start`. |
| F-8 ★ | AVL-3 | C, M | "The first schedule a user creates is their default" conflicts with the auto-created default; there's no way to change the default. | Auto-created one is default; explicit "make default" action. |
| F-9 ★ | AVL-4 | A | Which schedule becomes default; references to a deleted non-default schedule. | Oldest remaining becomes default; any event type pointing at the deleted schedule now points at the default. |
| F-10 ★ | AVL-10 | A | Time-off `start`/`end`: dates or instants, which zone, inclusive? `reason` free text or enum? | Inclusive local dates in the owner's zone; `reason` required free text. |
| F-11 ★ | EVT-8 | A | "≤ the next larger period's value" when that period is unset. | Each set value ≤ every larger set value. |
| F-12 ★ | AVL-8 | M | Wall times skipped or repeated by a DST change. | Skipped → offset before the gap (02:30 → 03:30); repeated → earlier instant. |
| F-13 ★ | BKG-3, PUB-4 | M | Booking fields have no key, but responses and `?field=` prefill need one. | `name` (`[a-z][a-z0-9_]{0,39}`, unique, not a system name). |
| F-14 ★ | BKG-3 | A | Can `name`/`email` be hidden? A hidden required field? | name/email can't be hidden or optional; hidden fields are never required (values still accepted, e.g. prefilled). |
| F-15 ★ | BKG-1, BKG-4 | M | Guests become attendees but have no name; is the booker's own email a guest? | Guest name = email; booker's email dropped from guests. |
| F-16 ★ | SLT-13, EVT-8 | A | Minute limit "reached" vs a booking that would push past it. | Offered only if booked + duration ≤ limit; booking limit: count < limit. |
| F-17 ★ | SLT-1, SLT-18 | A | `from`/`to`: dates or instants, inclusive? | RFC 3339 instants; slots starting in `[from, to)`. |
| F-18 ★ | SLT-15 | M | No account, so how is "a booker" identified? One hold per booker? Do holds block booking, carry buffers? | Anonymous cookie id; a new hold replaces the booker's previous one; holds block other bookers' bookings (`slot_taken`); no buffers. |
| F-19 ★ | EVT-11, SLT-8 | A | "Pending bookings of this type": the flag of the pending booking's type or of the type being queried? | The pending booking's own type. |
| F-20 ★ | EVT-7 | A | Business days undefined; does today count; how `rolling_available` counts. | Mon–Fri; days counted after today (fits both examples); `rolling_available`: today + N later dates with a free slot; fewer in 61 days → today + 61. |
| F-21 ★ | EVT-7, SLT-14 | C | EVT-7: range "in the owner's zone"; SLT-14: window "applied in the booker's zone". | Range dates in owner's zone; rolling windows in booker's zone. |
| F-22 ★ | BKG-6 | M | Only `slot_taken` is named; must a start lie on the slot grid (SLT-4)? | Yes. Codes: `event_not_found`, `invalid_duration`, `in_past`, `too_soon`, `outside_window`, `limit_reached`, `slot_taken` (busy/held), `unavailable` (no hours, time off, off-grid). Field errors (`field_required`, `field_invalid`, `location_required`) come after duration. |
| F-23 ★ | EVT-14, BKG-5 | C | EVT-14: with several locations "booker must choose"; BKG-5: "none picked → first configured". | Several → must choose (`location_required`); one → used; none → video. `phone_booker` requires the phone field. |
| F-24 ★ | BKG-18 | M | Host no-show has no field; which statuses; can it be undone? | `host_no_show` on booking; accepted only; toggle. |
| F-25 ★ | BKG-1, BKG-12 | M | BKG-12 sets `rescheduled = true`, not in BKG-1's fields. | Added `rescheduled`. |
| F-26 ★ | BKG-1, CAL-11 | M | `.ics` (PUB-5) needs a UID stable across reschedules; BKG-1 has none (CAL-11 implies one). | `ical_uid` (first booking's uid), copied on reschedule. |
| F-27 ★ | BKG-5, CAL-5 | M | No locations and no video app connected: what link? | Instance link `{base}/video/{uid}` (placeholder page). |
| F-28 ★ | BKG-7, BKG-11 | M | Confirming a pending booking whose exact slot is already accepted. | Refused `slot_taken`; overlapping (not exact) is the organizer's call. |
| F-29 ★ | EVT-16, BKG-13, BKG-15 | A | Do `allow_cancel` / `allow_reschedule` bind the organizer too? | Bookers only. |
| F-30 ★ | BKG-12 | M | What carries over on reschedule; may the booker edit answers? Old booking is cancelled even if the new one ends up pending/rejected. | Attendees, responses, location, duration carry over unchanged; old cancelled at once, as written. |
| F-31 ✔ | BKG-4 | A | "Emails valid" undefined (RFC 5322 accepts `a@b`). | addr-spec with a dotted domain. Resolved upstream: BKG-4 now says so. |
| F-32 ★ | AUTH-13, PUB | M | No CSRF rule for cookie-authenticated form posts. | SameSite=Lax plus an Origin check on POST. |
| F-33 ★ | PUB-2 | M | Zone "changeable unless locked": no event-type field locks the zone. | Not built. |
| F-34 ★ | SLT-8 | A | "Host is attendee" matched how? | Attendee email = host's account email. |
| F-35 ★ (with F-28) | BKG-11 | M | Confirm/reject link lifetime; does using one void the other; GET or POST? | No expiry; using either voids both; GET shows a page, POST acts (mail scanners prefetch links). |
| F-36 | AUTH-5, PUB-1 | M | Unverified users can't publish pages (AUTH-5), so PUB depends on AUTH-5, which the v0.1 scope left out. | Pages publish without verification in this build. |
| F-37 ★ | SLT-4, SLT-9 | A | "Range start" before or after busy time (and buffers) is removed; this moves slot alignment after a booking. | Free ranges are hours minus busy expanded by both sides' buffers (equivalent to SLT-9), so alignment restarts after each busy block. |
| F-38 ★ | BKG-13 | A | "In the past": started or ended? | Started. |
| F-39 ✔ | SLT-13 | A | Limits count only accepted bookings, so several pending ones can exceed a limit once confirmed. | Resolved upstream: BKG-11 refuses confirming past a reached limit (`limit_reached`). |
| F-40 ✔ | EVT-1 | A | `position` order direction; no upper bound on `length`. | Resolved upstream: `length` 1–1440, `position` ascending, ties by creation. |
| F-41 | AUTH-13 | A | "Next request 401" for an HTML app. | 401 status with the sign-in form. |
