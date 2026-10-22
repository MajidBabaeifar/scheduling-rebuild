# Event types

**Status:** Draft · **Prefix:** EVT

## Purpose

A bookable meeting template: how long it is, when it can be booked, and what the booker must give.
All durations are whole minutes.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| EVT-1 | An event type is owned by exactly one user or one team (`@spechub/teams`) and has `title` (1–200 chars), `slug` (`[a-z0-9-]`, 1–64), `description?`, `length` (1–1440), `hidden` (default false), `position` (display order, ascending, ties by creation). | create with length 0 → rejected |
| EVT-2 | `slug` is unique per owner. | second event `/intro` for same user → rejected; same slug for another user → ok |
| EVT-3 | `durations?`: list of allowed lengths that includes `length`; the booker picks one. A slot query or booking with a duration not in the list (or ≠ `length` when no list) is rejected. | durations [15,30]; book 45 min → rejected |
| EVT-4 | `slot_interval?` (≥ 1): step between slot starts; default = the chosen duration. | length 60, interval 30 → overlapping slots 09:00, 09:30 ([SLT-6](slots.md)) |
| EVT-5 | `min_notice` (≥ 0, default 120): no slot starts before now + `min_notice`. | now 10:00, notice 120 → first slot ≥ 12:00 |
| EVT-6 | `buffer_before`, `buffer_after` (≥ 0, default 0): protected time around each booking of this type ([SLT-9](slots.md)). | manual: see SLT-9 |
| EVT-7 | `window`: `unlimited` (default); `rolling {days, calendar_days: bool}` (last bookable date = today + `days` calendar days, or + `days` Mon–Fri days); `rolling_available {days}` (last bookable date = the `days`-th date after today with ≥ 1 free slot; fewer than `days` such dates within 61 days → today + 61); `range {start_date, end_date}` inclusive in the owner's zone. | rolling 5 business days from Fri → last bookable day is next Fri |
| EVT-8 | `limits.bookings {day?, week?, month?, year?}` and `limits.minutes {…}` (total booked minutes); each set value must be ≤ every set value of a longer period. | save day 5, week 3 → rejected |
| EVT-9 | `seats?` (≥ 1): several bookers share one slot ([BKG-16](bookings.md)). Null = one booking per slot. | manual: see BKG-16 |
| EVT-10 | `requires_confirmation` (default false), with optional `confirm_within {value, unit: minutes\|hours}`: confirmation needed only when the booking starts within that time of now; otherwise auto-accepted. | threshold 24 h; book 3 days ahead → accepted; book in 2 h → pending |
| EVT-11 | `pending_blocks_slot` (default false): pending bookings of this type block their host's time, for every event type. | pending 10:00, flag on → 10:00 not offered |
| EVT-12 | `max_active_per_booker?`: a booker (by email) with that many future accepted bookings of this type is refused, and shown their next booking to reschedule. | max 1; same email books twice → second refused |
| EVT-13 | `hidden` events are left out of the public profile but stay bookable by direct link. | hidden event → not listed; direct URL → slots shown |
| EVT-14 | `locations`: ordered list of `{kind: in_person\|link\|phone_host\|phone_booker\|video, value?}`; the booker picks one when there are several ([BKG-5](bookings.md)). | two locations → booker must choose |
| EVT-15 | `booking_fields`: the booking form ([BKG-3](bookings.md)). | manual: see BKG-3 |
| EVT-16 | `allow_cancel`, `allow_reschedule` (default true; restrict the booker, never the organizer); `min_reschedule_notice?`; `cancel_reason`: `required_host` (default), `required_booker`, `required_both`, `optional`. | allow_cancel false → booker cancel link refused |
| EVT-17 | `private_links`: secret URLs `{token, expires_at?, max_uses (default 1), uses}` that open a hidden event; a link past expiry or `max_uses` is refused. | link max 1; second booking via it → refused |
| EVT-18 | `success_redirect_url?`: after booking, the booker is sent there with booking details as query parameters. | manual: book → redirected with `uid` |
| EVT-19 | `schedule?`: the schedule used for availability ([AVL-5](schedules.md)). | manual: see AVL-5 |
| EVT-20 | `locked_time_zone?` (IANA): the booking page shows times in this zone and no zone picker ([PUB-2](public-pages.md)). | locked Europe/Berlin → times in Berlin, no picker |

## Out of scope

- Offset start times, "optimised" slot alignment, auto-translation, instant meetings, managed event types pushed to members, recording, attribute-based routing.

## Decisions

- Duration is validated on slot queries as well as on booking (the source validated only on booking).
- Limits use the owner's week start, not a fixed Sunday.
