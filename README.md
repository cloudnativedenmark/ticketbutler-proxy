# ticketbutler-proxy

A caching service between the Cloud Native Denmark Google Sheets automation and the
TicketButler API.

## The 60-second problem

The spreadsheet automation is a Google Apps Script project that calls
`GET /api/v3/events/{event}/orders/` and adds up ticket sales, t-shirt sizes and
merchandise. `UrlFetchApp` gives up after 60 seconds and that limit cannot be
raised. At around 250 attendees the orders response stopped arriving in time, so the
sheets stopped updating.

The endpoint has no pagination, no filtering and no field selection, so there is no
smaller request to make. The payload also repeats the entire event question schema
on every ticket, which is most of its size.

This service moves the slow call somewhere that is allowed to be slow. Cloud
Scheduler triggers a refresh every 30 minutes; the service fetches the orders with a
long timeout, aggregates them into the numbers the sheets need, and stores a
snapshot in Cloud Storage. Apps Script then reads pre-computed numbers in well under
a second.

### There is a second ceiling above the first

Measured against the live event on 2026-09-09, `/orders/` did not merely take longer
than 60 seconds. It returned **HTTP 524** on every attempt:

> Error 524: A timeout occurred. The origin web server did not return a complete
> response within the 120-second Proxy Read Timeout window.

TicketButler sits behind Cloudflare, which abandons the origin after 120 seconds. So
a patient client is necessary but not sufficient: past 120 seconds the answer is a
524 no matter how long this service is willing to wait, and no amount of retrying
changes that. Meanwhile `/api/v3/events/{event}/tickets/` answered in 0.34 seconds
with all 251 tickets — but it carries only names, emails and ticket type names, with
no prices, VAT, company names or question answers, so it cannot feed the sheets.

Two consequences shape this service:

- **It never makes the upstream problem worse.** A 524 is not retried within a
  refresh; the origin has just spent two minutes failing, and asking again
  immediately adds load for no plausible gain. The next scheduled refresh is the
  retry. Ordinary 5xx responses are retried, with a 30-second minimum backoff.
- **A failed refresh is not a failed read.** The previous snapshot keeps being
  served, flagged `stale` once it passes `STALE_AFTER`, so the sheets show the last
  good numbers with a warning rather than nothing.

If `/orders/` stays in this state, the fix is on TicketButler's side — a paginated
or filtered orders endpoint, or a raised origin timeout — and is worth raising with
support@ticketbutler.io. This service is what makes that outage survivable rather
than a fix for it.

## Architecture

```
  Refresh path (every 30 minutes; Cloudflare cuts a fetch off at 120s)

    Cloud Scheduler --POST /v1/refresh--> Cloud Run: tbproxy
                                                |
                              GET /api/v3/events/{uuid}/orders/
                                                |
                                                v
                                        TicketButler API
                                                |
                                aggregate, then store a snapshot
                                                |
                                                v
                                Cloud Storage: snapshot.json.gz

  Read path (sub-second, no upstream call)

    Apps Script --GET /v1/summary---> Cloud Run: tbproxy --read--> Cloud Storage
                --GET /v1/sponsors->

  Build path

    git push --> GitHub Actions --image--> GHCR --deploy--> Cloud Run
```

## Endpoints

Every endpoint except `/healthz` requires a token from `API_TOKENS`, presented
either as `Authorization: Bearer <token>` or as `X-Api-Key: <token>`. Both headers
are checked and a match on either is enough, which is what lets Cloud Scheduler send
its own OIDC token in `Authorization` while the service token travels in
`X-Api-Key`.

| Method | Path           | Body                                                     |
| ------ | -------------- | -------------------------------------------------------- |
| GET    | `/healthz`     | Liveness. Unauthenticated, no snapshot required.         |
| GET    | `/v1/summary`  | Pre-aggregated ticket, t-shirt and merchandise numbers.  |
| GET    | `/v1/sponsors` | Community sponsors, deduplicated by company name.        |
| GET    | `/v1/orders`   | The cached TicketButler payload, unmodified.             |
| POST   | `/v1/refresh`  | Fetch, aggregate and store. Called by Cloud Scheduler.   |

`GET /v1/summary`:

```json
{
  "schema_version": 1,
  "event_uuid": "11cc0a9f7f124ddcb11bc027e1a64f23",
  "fetched_at": "2026-09-09T10:00:00Z",
  "age_seconds": 142,
  "stale": false,
  "source_duration_ms": 78210,
  "counts": { "orders": 63, "tickets": 130 },
  "orders_by_state": { "PAID": 63 },
  "ticket_groups": {
    "Blind Bird": { "count": 84, "income_ex_vat": 100732.80 },
    "Sponsors and other free tickets": { "count": 7, "income_ex_vat": 0 },
    "Discounted": { "count": 0, "income_ex_vat": 0 }
  },
  "tshirt_sizes": { "small": 0, "medium": 26, "large": 36, "xl": 29, "2xl": 11, "3xl": 9 },
  "merch": {
    "Hoodie Black": { "small": 0, "medium": 0, "large": 0, "xl": 0, "2xl": 1, "3xl": 0 }
  }
}
```

`GET /v1/sponsors`:

```json
{
  "fetched_at": "2026-09-09T10:00:00Z",
  "age_seconds": 142,
  "stale": false,
  "sponsors": [
    {
      "company_name": "Example ApS",
      "email": "someone@example.com",
      "full_name": "Alfa Testperson",
      "ticket_type_pk": 183067,
      "ticket_type_name": "Community Sponsor"
    }
  ]
}
```

`stale` turns true once the snapshot is older than `STALE_AFTER`. The data is still
served, because an old number with a warning beats no number at all. Callers should
log the warning and carry on; a stale snapshot that stays stale means the refresh job
is failing.

## Configuration

All configuration is environment variables, read by `internal/config/config.go`.

| Variable                  | Default                                     | Meaning                                                                            |
| ------------------------- | ------------------------------------------- | ---------------------------------------------------------------------------------- |
| `PORT`                    | `8080`                                      | Listen port. Cloud Run sets this.                                                  |
| `TICKETBUTLER_BASE_URL`   | `https://cloudnativedenmark.ticketbutler.io`| Upstream host. Trailing slashes are trimmed.                                       |
| `TICKETBUTLER_EVENT_UUID` | none, required                              | Event to fetch orders for.                                                         |
| `TICKETBUTLER_TOKEN`      | none                                        | TicketButler API token. Required unless `TICKETBUTLER_FILE` is set.                |
| `TICKETBUTLER_FILE`       | none                                        | Read this JSON file instead of calling the API. For development and CI.            |
| `UPSTREAM_TIMEOUT`        | `10m`                                       | Limit on one upstream fetch attempt. Cloudflare cuts the origin off at 120s first. |
| `UPSTREAM_ATTEMPTS`       | `3`                                         | Attempts per refresh. A 524 stops early; the backoff is 30s and doubles.           |
| `API_TOKENS`              | none, required                              | Comma-separated accepted bearer tokens, minimum 16 characters each.                |
| `SNAPSHOT_BUCKET`         | none                                        | Cloud Storage bucket for the snapshot. Empty keeps the snapshot in memory only.    |
| `SNAPSHOT_OBJECT`         | `snapshot.json.gz`                          | Object name within the bucket.                                                     |
| `STALE_AFTER`             | `90m`                                       | Age at which responses report `stale: true`. Three 30-minute refresh cycles.       |
| `SPONSOR_TICKET_TYPE_PKS` | none                                        | Comma-separated ticket type ids counted as community sponsors. Currently `183067`. |
| `MERCH_NAME_PATTERNS`     | `hoodie`                                    | Comma-separated lower-case substrings that mark a ticket type as merchandise.      |

`SNAPSHOT_BUCKET` empty is fine locally and wrong on Cloud Run: the snapshot is lost
whenever the instance scales to zero, and the next read has nothing to serve.

## Local development

The committed fixture means development needs no TicketButler token and no network
access:

```sh
TICKETBUTLER_EVENT_UUID=11cc0a9f7f124ddcb11bc027e1a64f23 \
TICKETBUTLER_FILE=testdata/orders.sample.json \
SPONSOR_TICKET_TYPE_PKS=183067 \
API_TOKENS=local-development-token \
go run ./cmd/tbproxy
```

The snapshot starts empty, so fill it before reading:

```sh
curl -sX POST -H 'Authorization: Bearer local-development-token' \
  localhost:8080/v1/refresh
curl -s -H 'Authorization: Bearer local-development-token' \
  localhost:8080/v1/summary
```

`testdata/orders.sample.json` is anonymized but structurally faithful, so the numbers
it produces match the ones the real payload produces. That is what makes it usable in
tests.

## Regenerating the test fixture

`testdata/orders.sample.json` is derived from a real payload. Save the real one as
`orders.json`, which is gitignored, then:

```sh
python3 testdata/anonymize.py orders.json > testdata/orders.sample.json
python3 testdata/verify_fixture.py orders.json testdata/orders.sample.json
```

`verify_fixture.py` checks both directions: that no string from a personal-data field
of the real payload survived, and that order states, VAT, prices, ticket types and
t-shirt answers are unchanged. Do not commit a fixture that fails it.

## Deployment

See `deploy/README.md` for the Cloud Run service, the Cloud Scheduler job, the bucket
and the secrets.

## Apps Script setup

See `appsscript/README.md`. The `.gs` files in that directory replace the ones in the
old automation project.

## Behaviour preserved from the old script

The aggregation reproduces the previous Apps Script, including four decisions that
look like bugs until you know they were there before. They are kept so the numbers do
not change during the switchover.

**No order-state filter.** Every order counts, whatever its `state`, so a cancelled
or refunded order would still inflate the sheet. All current data is `PAID`. The
summary reports `orders_by_state` so this becomes visible instead of silent.

**Merchandise is detected by the substring `hoodie`.** A line item named
`"T-shirt Green - XL"` is therefore not merchandise: it lands in `ticket_groups`
under its full name including the size suffix, rather than in `merch`. Add patterns
via `MERCH_NAME_PATTERNS` when merchandise other than hoodies goes on sale.

**Unrecognised t-shirt answers are ignored.** The real data contains
`"Do not want a t-shirt"`, which matches no size and is dropped without a warning,
exactly as the old `normalizeSize` dropped it. The size counts are therefore lower
than the ticket count, by design.

**Refunds are ignored.** `ticket_refund` and `amount_of_refunded_tickets` are both in
the payload and neither is read.
