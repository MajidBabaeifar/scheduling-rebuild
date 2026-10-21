# Team events

**Status:** Draft · **Prefix:** RR

## Purpose

Event types owned by a team: everyone attends (collective) or one host is picked (round robin).

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| RR-1 | A team event type has `mode`: `collective` or `round_robin`, and hosts `[{user, fixed (bool), priority 0–4 (default 2), weight ≥ 0 (default 100), group?}]`; hosts must be accepted team members (`@spechub/teams`). `all_members` (bool) makes every member a host. | add non-member as host → refused |
| RR-2 | A host may set a per-event schedule overriding their default ([AVL-5](schedules.md)). | manual: see AVL-5 |
| RR-3 | Availability: collective → intersection of all hosts; round robin → intersection of fixed hosts and, for each group, the union of its non-fixed hosts. Fixed hosts with no common time → no slots. | RR hosts A 9–12, B 12–17 → slots 9–17 |
| RR-4 | Collective booking: all hosts attend; the first host is organizer; every host free or `hosts_unavailable`. | one collective host busy → no slot |
| RR-5 | Round-robin pick, per group: among non-fixed hosts free for the slot, keep the highest `priority`; then, if weights are on, the hosts furthest below their weighted share of this period's bookings; then the least recently booked. | A booked last, B not → B picked |
| RR-6 | Weighted share: `share = total_in_period × weight / sum_weights − own_in_period`; period is the current month (default) or day, counted by booking creation time; no-shows excluded. | weights 2:1, 30 bookings → 20:10 over time |
| RR-7 | A host added mid-period starts with the average bookings per host so far, so they aren't flooded. | new host on day 15 → not picked for every booking |
| RR-8 | Reschedule keeps the same host if they're free, else picks again. | host free at new time → same host |
| RR-9 | Seats on round-robin: later seats join the first seat's host. | manual: second seat same host |
| RR-10 | Recurring round-robin: the picked host must be free for all occurrences, else the next candidate is tried. | host busy in week 2 → next host |
| RR-11 | A team admin can reassign a booking to another free host; old host, new host and attendees are notified. | reassign → both hosts emailed |
| RR-12 | Team permissions (`@spechub/teams:TEAM-10` rows): event types read: member+; create, update, delete: admin+; team bookings read, reassign: admin+; webhooks read: member+, manage: admin+. A host always sees bookings they host. | member edits team event → 403 |

## Out of scope

- Managed event types, routing forms, CRM owner routing, attribute segments, lead thresholds.

## Decisions

- Priority before weights before recency, as the source does; simpler than combined scoring.
