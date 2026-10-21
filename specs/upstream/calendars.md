# Calendars and video

**Status:** Draft · **Prefix:** CAL

## Purpose

Read busy times from people's calendars, write bookings into them, and create meeting links.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| CAL-1 | A connection `{provider, owner (user\|team), secret (encrypted at rest), status: ok\|invalid}` per connected account. Calendar adapters: `list_calendars`, `busy(from, to, calendars) → [{start, end}]`, `create_event`, `update_event`, `delete_event`. Video adapters: `create_meeting → {url, id, password?}`, `update_meeting`, `delete_meeting`. | adapter contract tests per provider |
| CAL-2 | Ship at least: Google Calendar, Microsoft 365/Outlook, CalDAV; video: Google Meet, Microsoft Teams, Zoom, plain link. | manual: each connects |
| CAL-3 | Conflict calendars: a user picks which of their calendars count as busy; per event type a different set may be chosen. A connection with none picked contributes no busy time. | unpicked calendar busy → slot still offered |
| CAL-4 | Destination calendar: one per user, optionally one per event type (overrides); none set → first connected calendar. | event-type destination set → event written there |
| CAL-5 | Each user may set a default video app; Google Meet requires Google as destination, else the default video app (or plain link) is used instead. | Meet + Outlook destination → fallback used |
| CAL-6 | OAuth tokens refresh before expiry; a refused refresh marks the connection `invalid`, notifies the owner, and stops using it. | revoked token → status invalid, owner emailed |
| CAL-7 | Busy query failure fails closed for that host ([SLT-11](slots.md)); other hosts unaffected. | one host's provider down → other hosts still offered |
| CAL-8 | Busy queries pad the range by 24 h each side, then clip, so time zone edges aren't missed. | event at 23:30 UTC−10 day before → seen |
| CAL-9 | Every external event or meeting created for a booking is recorded `{provider, external_id, calendar_id?, meeting_url?}` and used to update or delete it later. | cancel → external event deleted |
| CAL-10 | Video creation failure: organizer is told, the booking falls back to the plain link location if one exists, else keeps no link. | Zoom error → organizer email |
| CAL-11 | Events carry an iCalendar UID stable across reschedules, with `SEQUENCE` = booking `sequence`. | reschedule → same UID, sequence 1 |

## Out of scope

- Push sync channels, calendar caching, domain-wide delegation, CRM sync.

## Decisions

- One failing calendar blocks only its own host; the source blocked the whole query.
