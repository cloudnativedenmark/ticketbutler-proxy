# ticketbutler-proxy

A caching service between the Cloud Native Denmark Google Sheets automation and the
TicketButler API.

## Why this proxy exists

The spreadsheet automation is a Google Apps Script project that calls
`GET /api/v3/events/{event}/orders/` and adds up ticket sales, t-shirt sizes and
merchandise. `UrlFetchApp` has a fixed 60-second request limit. As the event grows,
the orders response can take longer than that limit and the sheets can no longer
update directly from the API.

The orders endpoint returns the event's orders in one response. Since the client
cannot request smaller pages or select individual fields, the complete response must
finish within the Apps Script time limit.

This service performs that work outside Apps Script. Cloud Scheduler triggers a
refresh on a configurable schedule. The service fetches the orders, calculates the
numbers used by the sheets, and stores a snapshot in Cloud Storage. Apps Script reads
the prepared snapshot through a small API.

### Handling long upstream requests

During testing with live event data, requests to `/orders/` sometimes returned HTTP
524 after exceeding an upstream timeout. Extending this service's client timeout
does not help when the request has already been ended elsewhere in the request path.

This leads to two reliability choices:

- **Origin timeout responses are not retried immediately.** The next scheduled
  refresh provides another attempt without repeating a request that has just timed
  out. Other 5xx responses use exponential backoff, starting at 30 seconds.
- **Reads remain available after an unsuccessful refresh.** The previous snapshot is
  still served and is marked `stale` once it exceeds `STALE_AFTER`.

The proxy provides a fast, stable read path for the spreadsheet while continuing to
use TicketButler as the source of record.

## Architecture

```
  Refresh path (schedule configured in Cloud Scheduler)

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
| GET    | `/healthz`     | Health and snapshot availability. Unauthenticated.       |
| GET    | `/v1/summary`  | Pre-aggregated ticket, t-shirt and merchandise numbers.  |
| GET    | `/v1/sponsors` | Community sponsors, deduplicated by company name.        |
| GET    | `/v1/orders`   | Cached orders containing the fields modelled by the service. |
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
log the warning and carry on. A snapshot that remains stale indicates that scheduled
refreshes have not completed successfully.

`GET /v1/orders` is not a byte-for-byte TicketButler response. It returns the event,
order, ticket, billing and answer fields modelled in `internal/ticketbutler`, wrapped
with the same freshness metadata as the other data endpoints. Unused upstream fields,
including the repeated question schema, are omitted to keep the snapshot smaller.

`GET /healthz` returns `200` when a snapshot is available or the service is waiting
for its first refresh. It returns `503` when the configured snapshot store cannot be
read or contains an invalid snapshot.

## Configuration

All configuration is environment variables, read by `internal/config/config.go`.

| Variable                  | Default                                     | Meaning                                                                            |
| ------------------------- | ------------------------------------------- | ---------------------------------------------------------------------------------- |
| `PORT`                    | `8080`                                      | Listen port. Cloud Run sets this.                                                  |
| `TICKETBUTLER_BASE_URL`   | `https://cloudnativedenmark.ticketbutler.io`| Upstream host. Trailing slashes are trimmed.                                       |
| `TICKETBUTLER_EVENT_UUID` | none, required                              | Event to fetch orders for.                                                         |
| `TICKETBUTLER_TOKEN`      | none                                        | TicketButler API token. Required unless `TICKETBUTLER_FILE` is set.                |
| `TICKETBUTLER_FILE`       | none                                        | Read this JSON file instead of calling the API. For development and CI.            |
| `UPSTREAM_TIMEOUT`        | `10m`                                       | Client-side limit for one upstream fetch attempt.                                  |
| `UPSTREAM_ATTEMPTS`       | `3`                                         | Attempts per refresh. A 524 stops early; the backoff is 30s and doubles.           |
| `API_TOKENS`              | none, required                              | Comma-separated accepted bearer tokens, minimum 16 characters each.                |
| `SNAPSHOT_BUCKET`         | none                                        | Cloud Storage bucket for the snapshot. Empty keeps the snapshot in memory only.    |
| `SNAPSHOT_OBJECT`         | `snapshot.json.gz`                          | Object name within the bucket.                                                     |
| `STALE_AFTER`             | `90m`                                       | Snapshot age at which responses report `stale: true`.                              |
| `SPONSOR_TICKET_TYPE_PKS` | none                                        | Comma-separated ticket type ids counted as community sponsors. Currently `183067`. |
| `MERCH_NAME_PATTERNS`     | `hoodie`                                    | Comma-separated lower-case substrings that mark a ticket type as merchandise.      |

For Cloud Run, configure `SNAPSHOT_BUCKET` so snapshots survive instance shutdowns and
deployments. Leaving it empty uses in-memory storage, which is useful for local work.

### Refresh schedule

Cloud Scheduler controls how often `POST /v1/refresh` runs. The deployment script
defaults to every 30 minutes and accepts any cron expression through `SCHEDULE`:

```sh
# Refresh every 15 minutes.
SCHEDULE='*/15 * * * *' ./deploy/scheduler.sh
```

`STALE_AFTER` does not control the refresh schedule. It only sets the age at which a
stored snapshot is marked stale. Set it high enough to allow for the chosen schedule
and occasional missed or unsuccessful refreshes.

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

The aggregation retains four behaviors from the previous Apps Script so the published
numbers do not change during the switchover.

**No order-state filter.** Every order counts, whatever its `state`. The summary
reports `orders_by_state` so callers can see which states contributed to the totals.

**Merchandise is detected by the substring `hoodie`.** A line item named
`"T-shirt Green - XL"` is therefore not merchandise: it lands in `ticket_groups`
under its full name including the size suffix, rather than in `merch`. Add patterns
via `MERCH_NAME_PATTERNS` when merchandise other than hoodies goes on sale.

**Unrecognised t-shirt answers are ignored.** An answer such as
`"Do not want a t-shirt"` does not match a size and is not included in the size
totals. The size counts can therefore be lower than the ticket count.

**Refunds are ignored.** `ticket_refund` and `amount_of_refunded_tickets` are both in
the payload and neither is read.

## License

Licensed under the [Apache License 2.0](LICENSE).
