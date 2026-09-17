# Security

This service handles attendee personal data. The notes below are the things worth
knowing before changing it or operating it.

## What the endpoints expose

| Endpoint       | Content                                                                        |
| -------------- | ------------------------------------------------------------------------------ |
| `/v1/orders`   | The cached subset modelled by the service: attendee names, email addresses, employers, buyer billing addresses and phone numbers. |
| `/v1/sponsors` | Company name, contact name and email address per community sponsor.            |
| `/v1/summary`  | Aggregates only: counts and sums per ticket group, t-shirt size and merchandise item. No names, no addresses. |
| `/healthz`     | Nothing. This is why it is the only unauthenticated endpoint.                  |

`/v1/orders` and `/v1/sponsors` are sent with `Cache-Control: no-store`, so no proxy
or browser keeps a copy, and their bodies are never written to the logs. Request logs
record the path, status and duration. Prefer `/v1/summary` in any new caller that
only needs numbers.

## Authentication and token rotation

Every endpoint except `/healthz` requires a token from the comma-separated
`API_TOKENS`, presented either as `Authorization: Bearer <token>` or as
`X-Api-Key: <token>`. Both headers are checked and a match on either is accepted;
tokens are compared as SHA-256 digests in constant time. The service refuses to
start with `API_TOKENS` unset, because it would otherwise serve attendee data to
anyone who found the URL, and each token must be at least 16 characters.

Two headers rather than one because Cloud Scheduler authenticates to Cloud Run by
putting its OIDC token in `Authorization`. If that were the only header examined,
turning on Cloud Run IAM would make the service reject the very caller that triggers
its refreshes. With `X-Api-Key` available, both checks can apply at once, which is
stricter than either alone.

Several tokens are accepted at once so rotation has no window where nothing works:

1. Generate a new token and append it to `API_TOKENS`. Redeploy.
2. Update every caller: the `TBPROXY_TOKEN` Script Property in the Apps Script
   project, and the Cloud Scheduler job's `X-Api-Key` header (rerun
   `deploy/scheduler.sh`).
3. Remove the old token from `API_TOKENS`. Redeploy.

Rotate whenever someone with access to the token leaves the organizing team, and
after the event.

## Real data in the repository

`orders.json` and any `orders*.json` are gitignored. A real payload contains the
personal data of every attendee and must not be committed, even in a branch that is
never merged.

`testdata/orders.sample.json` is the one exception, and it is anonymized:
`testdata/anonymize.py` replaces names, email addresses, phone numbers, employers,
identifiers and discount codes with deterministic synthetic values, while leaving the
fields the aggregation reads untouched. `testdata/verify_fixture.py` checks that no
string from a personal-data field of the real payload survived into it. Run the
verifier whenever the fixture is regenerated.

If a real payload does get committed, treat it as a data breach: the fix is rewriting
history and rotating the TicketButler token, not a follow-up commit that deletes the
file.

## Reporting a vulnerability

Report privately through a GitHub security advisory on this repository: the Security
tab, then "Report a vulnerability". Please do not open a public issue for anything
that exposes attendee data or a credential.

This is run by conference volunteers, so a reply may take a few days. Include what
you found, how to reproduce it, and whether you believe attendee data was reachable.
