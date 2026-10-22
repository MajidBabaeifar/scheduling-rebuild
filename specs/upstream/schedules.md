# Schedules

**Status:** Draft · **Prefix:** AVL

## Purpose

When a person is available: weekly hours, date overrides, time off.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| AVL-1 | Every user has an IANA `time_zone` (required, chosen at signup) and `week_start` (`monday`\|`sunday`, default `monday`). | signup without zone → rejected |
| AVL-2 | A schedule has `name`, `time_zone?` (falls back to owner's), weekly rules `{days: [0..6] (0 = Sunday), start, end}` and date overrides `{date, start, end}`; times are wall-clock `HH:MM`; weekly rules need `end > start`, overrides `end ≥ start` ([AVL-7](schedules.md)). | create weekly rule with end ≤ start → rejected |
| AVL-3 | A new user gets one schedule, Mon–Fri 09:00–17:00, as their default. The owner can make any of their schedules the default; a user always has exactly one. | new user → one default schedule |
| AVL-4 | Deleting the default schedule makes the oldest remaining one default; every reference to a deleted schedule is repointed to the default; deleting the last schedule is refused. | delete only schedule → refused |
| AVL-5 | Schedule used for a host on an event: event's schedule, else the host's per-event schedule ([RR-2](team-events.md)), else the host's default. | event without schedule → host default used |
| AVL-6 | Rules expand per local date in the schedule's zone; overlapping or touching ranges on a date merge; `end` 23:59 means midnight. | 09–12 and 12–17 → one range 09–17 |
| AVL-7 | Any override on a date replaces all weekly rules for that date; several overrides on a date give several ranges; an override with `start == end` makes the date unavailable. | Mon rule 09–17, override Mon 13–14 → only 13–14 |
| AVL-8 | On DST change days, ranges keep their wall-clock times; their UTC instants shift. A time skipped by the change uses the offset before it (02:30 → 03:30); a repeated one the earlier instant. | Europe/Berlin, last Sun of March, 09–17 → 07:00–15:00 UTC |
| AVL-9 | Travel periods `{time_zone, start_date, end_date?}` read the default schedule's rules in the travel zone for those dates. | default schedule 09–17 Europe/Berlin, travel to Asia/Tokyo → 09–17 Tokyo time |
| AVL-10 | Time off `{start_date, end_date (inclusive, owner's zone), reason (free text), note?, note_public (default false), redirect_to_user?}` makes those dates unavailable for booking. | time off Tue → Tue has no bookable slots |
| AVL-11 | During time off with `redirect_to_user`, the public page shows the dates as away with a link to that user's page; `note` shown only if `note_public`. | manual: away dates show redirect |
| AVL-12 | Optional public holidays by country, each holiday can be switched off; an enabled holiday on a working day acts as time off. | country DE, Oct 3 on a Fri → no slots |

## Out of scope

- Restriction schedules (intersect with a second schedule).

## Decisions

- Explicit time zone at signup; the source defaulted to `Europe/London`.
- Monday week start by default (ISO 8601); used by weekly limits ([EVT-8](event-types.md)).
