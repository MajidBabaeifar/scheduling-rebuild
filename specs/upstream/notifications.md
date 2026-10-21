# Notifications

**Status:** Draft · **Prefix:** NTF

## Purpose

Who hears about what. Emails here; webhook delivery from `@spechub/webhooks`.
Host side = organizer + other hosts; attendee side = booker + guests.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| NTF-1 | Emails are sent from the instance's address with `Reply-To` = organizer (host-side mails: booker). | manual: headers of a scheduled email |
| NTF-2 | accepted → host: scheduled; attendees: scheduled, with `.ics` attachment and manage links (reschedule, cancel). | book → both sides emailed, `.ics` valid |
| NTF-3 | pending → host: request with confirm/reject links ([BKG-11](bookings.md)); booker: request received. Host reminders at 48 h, 24 h, 3 h after creation while still pending and before start. | pending 50 h → 2 reminders sent |
| NTF-4 | rejected → attendees: declined (with reason). cancelled → both: cancelled (with reason). rescheduled → both: rescheduled (old and new time). reschedule requested → booker: rebook link. | cancel → both sides emailed |
| NTF-5 | Seats: joining → organizer and the new attendee; cancelling a seat → organizer and that attendee only. | manual |
| NTF-6 | Payments: reminder ([PAY-4](payments.md)), refund failed → organizer, no-show fee charged → booker. | manual |
| NTF-7 | Emails use the recipient's locale and time zone; the event type can turn off host-side or attendee-side emails. | attendee in Asia/Tokyo → times in JST |
| NTF-8 | Webhook events: `booking.created`, `booking.requested`, `booking.rescheduled`, `booking.cancelled`, `booking.rejected`, `booking.payment_initiated`, `booking.paid`, `booking.no_show_updated`, `meeting.started`, `meeting.ended`, `time_off.created`. | each transition → one event |
| NTF-9 | Booking event payload: `{uid, status, event_type {id, slug, title}, start, end, organizer {name, email, time_zone}, attendees, location, responses, from_reschedule?, cancel_reason?, reject_reason?}`. | payload matches schema |
| NTF-10 | `meeting.started`/`meeting.ended` fire at start/end of accepted bookings; cancelled or rescheduled bookings cancel their pending ones. | cancel before start → no meeting.started |
| NTF-11 | Webhook subscriptions may be scoped to a user, a team or one event type. | event-type webhook → only that event's bookings |

## Out of scope

- SMS, workflow builder, location-change and guest-added emails.
