# Public pages

**Status:** Draft · **Prefix:** PUB

## Purpose

What bookers see: profile, booking flow, confirmation, and managing a booking. No sign-in needed.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| PUB-1 | `/{username}` and `/{team-slug}` (one namespace; route names such as `app`, `api`, `booking`, `login` are reserved and refused as usernames or slugs): name, avatar, bio, and visible event types (title, durations, description); private teams hide members. | hidden event not listed |
| PUB-2 | `/{owner}/{event-slug}`: month calendar with available dates, time list for the chosen date in the booker's zone (detected, changeable unless [EVT-20](event-types.md) locks it), 12/24 h toggle, duration picker when several. | zone changed → times re-rendered |
| PUB-3 | `/{user1}+{user2}/{duration}` (dynamic group): collective booking across users who allow it; any user not allowing it → 404. | user disallows → 404 |
| PUB-4 | Choosing a time places the hold ([SLT-15](slots.md)) and shows the form ([BKG-3](bookings.md)); fields can be prefilled from query parameters with the field name. | `?email=a@b.c` → email prefilled |
| PUB-5 | Success page `/booking/{uid}`: what, when (booker zone), where, who; add-to-calendar links (Google, Outlook, `.ics`); status shown for pending and awaiting payment. | open uid → details shown |
| PUB-6 | The same page offers reschedule and cancel per [BKG-12](bookings.md)..[BKG-15](bookings.md); for seats, by seat uid. | cancel from page → status cancelled |
| PUB-7 | Embeds: inline, pop-up button and floating button snippets for any event page; embedded pages post `booking_successful` to the parent window. | manual: embed on a test page |
| PUB-8 | Pages are server-rendered, usable without third-party cookies, WCAG 2.2 AA, and work at 360 px width. | manual: axe scan, phone width |
| PUB-9 | Users can hide the product branding and set a brand colour and light/dark theme for their pages. | manual |

## Out of scope

- Custom domains, SEO settings, routing forms.
