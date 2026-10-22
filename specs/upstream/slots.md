# Slots

**Status:** Draft · **Prefix:** SLT

## Purpose

Which start times a booker is offered. The same rules re-check every booking ([BKG-6](bookings.md)).

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| SLT-1 | Query: `event`, `from`, `to` (ISO 8601 instants, to > from, ≤ 62 days apart; slots starting in `[from, to)`), booker `time_zone`, `duration?`, `reschedule_uid?`. Result: map booker-local `YYYY-MM-DD` → list of `{start (ISO 8601 UTC), seats_taken?}`. | to < from → rejected |
| SLT-2 | Per host: free = schedule ranges ([AVL](schedules.md)) − time off − busy times (SLT-8..11). | busy 10–11 in 09–17 → 10:00 not offered |
| SLT-3 | Hosts combine per [RR-3](team-events.md); individual events have one host. | manual: see RR-3 |
| SLT-4 | Slots start at `max(range start, now + min_notice)` per free range (schedule ranges minus busy time widened per SLT-9), rounded up to the alignment step: the first of 60, 30, 20, 15, 10, 5 that divides the interval, else 1; alignment is to the hour in the booker's zone. | free 09:05–12:00, 60 min → 10:00, 11:00 |
| SLT-5 | A slot is offered only if `start + duration ≤ range end`; next start = start + interval. | free 09:00–10:00, 45 min, interval 30 → 09:00 only |
| SLT-6 | Interval < duration gives overlapping slots. | 60 min, interval 30, free 09–11 → 09:00, 09:30, 10:00 |
| SLT-7 | A start offered by two ranges appears once. | manual: two hosts, same start → one slot |
| SLT-8 | Accepted bookings where the host is organiser or attendee (by account email) are busy; `awaiting_payment` bookings are busy until their payment timeout ([PAY-3](payments.md)); pending bookings are busy only per [EVT-11](event-types.md), for their host; cancelled and rejected never. | cancelled 10:00 → 10:00 offered |
| SLT-9 | Buffers: new event E (before B, after A) conflicts with booking X (before Bx, after Ax) iff `[E.start − B, E.end + A]` overlaps `[X.start − Bx, X.end + Ax]`. Buffers never shrink working hours. | X 10:00–10:30 with A=0, E B=15 → 10:30 blocked, 10:45 ok |
| SLT-10 | Busy time from connected calendars ([CAL-3](calendars.md)) is busy like a booking with no buffers of its own. | external 14–15 → 14:00 not offered |
| SLT-11 | If a host's connected calendar can't be read, that host has no availability for the query (fail closed). | calendar API error → host offers nothing |
| SLT-12 | Overlap means `a.start < b.end && a.end > b.start`; touching is not overlap. | booking 10–11, slot 11:00 → offered |
| SLT-13 | A limit reached ([EVT-8](event-types.md)) makes its whole period busy; periods are in the event owner's zone and week start; a booking counts in the period of its start; a slot is offered only while bookings < the booking limit and booked minutes + its duration ≤ the minute limit; booking limits count accepted bookings of the event type across all hosts; minute limits count per host. | day limit 2, two bookings Tue → no Tue slots |
| SLT-14 | Rolling windows ([EVT-7](event-types.md)) count dates in the booker's zone using each slot's own UTC offset; `range` dates are the owner's zone; notice is an absolute duration; for rolling windows all dates after the limit are dropped. | rolling 3 calendar days from Mon → no Fri slots |
| SLT-15 | Holds: when a booker (an anonymous browser id) selects a slot, it is held for 5 minutes, replacing their earlier hold; slots overlapping another booker's unexpired hold are neither offered nor bookable (`slot_taken`); a booker's own hold is ignored; holds have no buffers. | A holds 10:00; B queries → no 10:00 |
| SLT-16 | Seated events: a slot at time T with an existing booking stays offered until `seats_taken = seats`; other slots overlapping that booking are busy. | seats 3, 1 taken → T offered with seats_taken 1 |
| SLT-17 | When rescheduling, the booking being moved is not busy and doesn't count towards limits. | reschedule 10:00 → 10:00 offered to that booker |
| SLT-18 | Only slots starting in `[from, to)` are returned; date keys without slots are omitted. | manual: query one day → one date key |

## Out of scope

- Slot caching, slot troubleshooter, "only first slot" display mode.

## Decisions

- Seats: a seated booking blocks overlapping slots with other start times (the source counted bookings, not seats, and left them free).
- Pending bookings block only their own host (the source blocked the slot for every host).
- UTC offsets per slot date, not the current offset.
