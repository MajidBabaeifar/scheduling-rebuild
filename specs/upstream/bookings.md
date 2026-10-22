# Bookings

**Status:** Draft · **Prefix:** BKG

## Purpose

Create, confirm, reschedule and cancel a booking, and keep calendars and people in sync.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| BKG-1 | A booking has `uid` (opaque, unique, URL-safe), `event_type`, `organizer` (a user), `attendees [{name, email, time_zone, phone?, locale, no_show}]`, `start`, `end` (UTC), `location`, `responses`, `status`, `paid`, `from_reschedule?` (uid), `rescheduled` (bool), `cancel_reason?`, `cancelled_by?`, `reject_reason?`, `host_no_show`, `sequence` (starts 0), `ical_uid` (kept across reschedules, [CAL-11](calendars.md)). | create → uid of ≥ 16 URL-safe chars |
| BKG-2 | Statuses: `awaiting_payment`, `pending`, `accepted`, `rejected`, `cancelled`. Only these transitions: awaiting_payment → pending\|accepted\|cancelled; pending → accepted\|rejected\|cancelled; accepted → cancelled. | accepted → rejected → refused |
| BKG-3 | Booking form: system fields `name` (required), `email` (required), `phone` (hidden), `location`, `notes` (optional), `guests` (list of emails, hideable), `reschedule_reason` (reschedule only); owner-added fields of type text, textarea, number, email, phone, address, url, select, multiselect, checkbox, radio, boolean, each with `name` (key for responses and prefill, `[a-z][a-z0-9_]{0,39}`, unique), `label`, `required`, `hidden`, `options?`, `min_length?`, `max_length?`. System fields other than `name` and `email` can be hidden, none deleted; hidden fields are never required. | required custom field empty → rejected |
| BKG-4 | Field validation: emails are an addr-spec with a dotted domain; phones E.164-valid; email fields may have `allow_domains`/`block_domains`; guests de-duplicated and lower-cased; guests who are hosts of the event or the booker are dropped; each guest becomes an attendee named by their email. | guest list with a duplicate → stored once |
| BKG-5 | Location: the booker's pick from [EVT-14](event-types.md), required when several are configured (`location_required`); one configured → it; none configured → organizer's default video app ([CAL-5](calendars.md)), else a plain meeting link on the instance. | event without locations → video link created |
| BKG-6 | Create re-checks, in order: event exists; duration ([EVT-3](event-types.md)); start in the future, notice and window; limits; booker limit ([EVT-12](event-types.md)); at least one host free per [SLT](slots.md), at a start on the slot grid. Codes: `event_not_found`, `invalid_duration`, `in_past`, `too_soon`, `outside_window`, `limit_reached`, `booker_limit`, `slot_taken` (busy or held), `unavailable` (no hours, time off, off-grid). | two bookers race for 10:00 → one succeeds, other gets `slot_taken` |
| BKG-7 | At most one accepted booking per host per exact `[start, end)`; enforced atomically. Cancelling or rejecting frees it. | concurrent identical requests → one booking |
| BKG-8 | Same booker email, same event type, same start, while an earlier one is pending or awaiting payment → the earlier booking is returned, not a new one. | submit twice → same uid |
| BKG-9 | New status: paid event → `awaiting_payment` ([PAY](payments.md)); else confirmation required ([EVT-10](event-types.md)) → `pending`; else `accepted`. An organizer rescheduling their own booking → `accepted`. | free event, no confirmation → accepted |
| BKG-10 | On becoming `accepted`: create the event in the destination calendar and the video meeting ([CAL](calendars.md)), store their external ids, notify ([NTF](notifications.md)). Integration failures are logged and shown to the organizer; the booking stands. | calendar API down → booking accepted, organizer sees warning |
| BKG-11 | Confirm/reject a `pending` booking: by the organizer or a team admin, or via single-use links in the organizer's request email (opening shows the booking, acting needs a POST; using either link voids both). Reject takes an optional reason. Confirming a booking that isn't `pending` → error; confirming into a slot taken per [BKG-7](bookings.md) → `slot_taken`; confirming past a reached limit ([SLT-13](slots.md)) → `limit_reached`. | reuse a confirm link → refused |
| BKG-12 | Reschedule by booker (via `uid` link) or organizer: creates a new booking with `from_reschedule`, sets the old one `cancelled` with `rescheduled = true`, updates the same external calendar event (`sequence + 1`), carries over `paid`, attendees, responses, location and duration. | reschedule → old cancelled, new links back |
| BKG-13 | Reschedule refused when `allow_reschedule` is false, the booking is cancelled or rejected, it has started, or a non-organizer is inside `min_reschedule_notice`. | reschedule 1 h before start, notice 2 h → refused |
| BKG-14 | Organizer may "request reschedule": the booking becomes `cancelled` with a reason and the booker gets a link to pick a new time. | manual: booker email has rebook link |
| BKG-15 | Cancel by booker (via `uid` link, no login) or organizer, until the booking ends; refused when `allow_cancel` is false; reason required per [EVT-16](event-types.md). Deletes external calendar and video events. | cancel after end → refused |
| BKG-16 | Seats ([EVT-9](event-types.md)): one booking per slot holds up to `seats` attendees, each with a seat `uid` to cancel or reschedule their own seat; a full slot → `seats_full`; the same email twice → `already_booked`. Removing the last seat cancels the booking. | seats 2; third booker → seats_full |
| BKG-17 | Seated bookings show attendees to each other only if `show_attendees` (default false). | manual: attendee email lists only themself |
| BKG-18 | No-show: after the start of an accepted booking, the organizer can mark or unmark any attendee or the host as no-show; marking before start → refused. | mark 1 min before start → refused |
| BKG-19 | Recurring events (`{freq: weekly\|monthly, interval, count ≤ 24}`): one booking per occurrence sharing a `series_id`; every occurrence must be free; cancel one or all future. Seats and recurrence can't be combined. | weekly × 4 with week 3 busy → whole series refused |

## Out of scope

- Instant meetings, built-in video, recording, CRM routing, SMS, workflow automations.

## Decisions

- `awaiting_payment` is its own status (the source used pending + unpaid, and let a host confirm an unpaid booking).
- A recurring series is all-or-nothing at creation.
