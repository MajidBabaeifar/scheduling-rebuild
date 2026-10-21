# Payments

**Status:** Draft · **Prefix:** PAY

## Purpose

Charge for a booking, refund fairly, and optionally hold a card for no-show fees.

## Requirements

| ID | Requirement | Verified by |
|---|---|---|
| PAY-1 | An event type may have `price {amount (integer minor units, > 0), currency (ISO 4217)}`, `payment_mode`: `on_booking` (default) or `hold` (save card, charge later), and a connected payment provider. A price without a provider → can't publish. | price set, no provider → save refused |
| PAY-2 | Booking a paid event creates it `awaiting_payment` with a payment `{uid, amount, currency, provider_ref, status: pending\|succeeded\|failed\|refunded}` and returns a checkout URL. | book paid event → response has checkout URL |
| PAY-3 | `awaiting_payment` bookings block the slot until `payment_timeout` (default 30 min); unpaid at timeout → `cancelled` with reason `payment_timeout`, slot freed. | no payment in 30 min → booking cancelled, slot offered again |
| PAY-4 | 15 minutes after creation, still unpaid → reminder email to the booker with the checkout link. | manual: email at +15 min |
| PAY-5 | Provider confirms payment (signed webhook, idempotent by `provider_ref`) → `paid = true`; status → `pending` if confirmation is required, else `accepted`. | duplicate provider webhook → one transition |
| PAY-6 | `refund_policy`: `never`, `always`, or `before {days, calendar_days: bool}`. Applied when the booking is cancelled or rejected; rejection always refunds. | policy 2 days, cancel 1 day before → no refund |
| PAY-7 | A failed refund notifies the organizer; the booking stays cancelled. | provider refund error → organizer email |
| PAY-8 | Rescheduling a paid booking needs no new payment. | reschedule paid booking → no checkout |
| PAY-9 | `hold` mode: the organizer can charge the no-show fee after marking the booker no-show ([BKG-18](bookings.md)); optional auto-charge when the booker cancels within `{value, unit}` of the start. | auto-charge 24 h; cancel 2 h before → charged |
| PAY-10 | Seated paid events take one payment per seat; cancelling a seat refunds that seat per policy. | cancel one seat → one refund |

## Out of scope

- Coupons, taxes, invoices, multiple providers per event.

## Decisions

- Payment timeout (30 min) is new: the source had none, so unpaid bookings could block a slot forever.
