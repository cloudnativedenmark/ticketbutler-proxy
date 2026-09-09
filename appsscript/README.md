# Apps Script client

These four files replace the ones in the Cloud Native Denmark spreadsheet automation
project. They read pre-computed numbers from ticketbutler-proxy instead of calling
the TicketButler orders endpoint, which no longer fits in the 60-second
`UrlFetchApp` limit.

| File               | What it is                                                                 |
| ------------------ | -------------------------------------------------------------------------- |
| `TbProxy.gs`       | New. Shared client: auth, retries, per-execution memo, stale warning.      |
| `TicketSales.gs`   | Rewritten. `readTicketbutlerOrders()` now reads `/v1/summary`.             |
| `Sponsors.gs`      | Only `processCommunitySponsors()` changed; it reads `/v1/sponsors`.        |
| `TicketButler.gs`  | Rewritten. `ticketButlerDiscount` still posts directly, but the token and event UUID come from Script Properties. |

`Utils.gs`, `Mail.gs`, `Contract.gs` and `Documents.gs` are unchanged and stay as
they are.

No token belongs in a `.gs` file. This repository is public, and the previous
`TicketButler.gs` had the TicketButler API token on line 2. Everything secret is a
Script Property.

## 1. Set the Script Properties

In the Apps Script editor: Project Settings, then Script Properties.

| Property        | Value                                                                     |
| --------------- | ------------------------------------------------------------------------- |
| `TBPROXY_URL`   | Base URL of the Cloud Run service, no trailing slash, for example `https://tbproxy-abc123-ew.a.run.app` |
| `TBPROXY_TOKEN` | One of the values in the service's `API_TOKENS`                           |
| `TB_TOKEN`      | TicketButler API token, used only for creating discount codes             |
| `TB_EVENT`      | TicketButler event UUID                                                   |

Set these before pasting the files. Each is checked at call time and the error names
the missing property, but a run that fails on configuration tells you less than one
that never started.

`API_TOKENS` on the service accepts several comma-separated tokens, so rotation is:
add the new token to the service, change `TBPROXY_TOKEN` here, then drop the old
token from the service.

## 2. Copy the files

Copy in this order, so that no file is present without the code it calls:

1. `TbProxy.gs` (new file in the project)
2. `TicketButler.gs` (replace)
3. `TicketSales.gs` (replace)
4. `Sponsors.gs` (replace)

Paste over the whole contents of each file. `fetchWithRetry`, `getCachedOrders`,
`cachedOrders` and `normalizeSize` disappear with the old `TicketSales.gs`; nothing
else in the project used them.

## 3. Verify

Select `readTicketbutlerOrders` in the editor and run it. Then open the execution
log. A healthy run looks like this:

```
tbproxy summary: 63 orders, 130 tickets, fetched_at 2026-09-09T10:00:00Z
```

and finishes in about a second rather than timing out near 60. Check that column F
and G of the REALIZED sheet and the two blocks on MERCH AND T-SHIRTS hold the same
numbers as before the switch.

Things the log may tell you instead:

- `Script Property TBPROXY_URL is not set` and similar: step 1 is incomplete.
- `tbproxy /v1/summary returned 401`: `TBPROXY_TOKEN` is not in the service's
  `API_TOKENS`. This is not retried, because retrying a bad token only delays the
  error.
- `WARNING: tbproxy snapshot is stale`: the sheets were updated from an old
  snapshot. The numbers are real but not current, and the Cloud Scheduler job that
  calls `/v1/refresh` needs looking at.
- `Failed to fetch /v1/summary from tbproxy after 3 attempts`: the service is down or
  unreachable.

`processCommunitySponsors()` sends email and creates discount codes, so it is not a
test you run casually. To check only the proxy part of it, run `tbSponsors_()` from
the editor and read the log.

## Rollback

Keep a copy of the pre-proxy `TicketSales.gs` and `Sponsors.gs` for one event cycle.
Rolling back means pasting those two files back over the new ones and clearing the
`TBPROXY_URL` and `TBPROXY_TOKEN` properties; nothing else in the project changes.

That old path only works while the TicketButler orders endpoint still answers inside
60 seconds, which it stopped doing at roughly 250 attendees. It is an escape hatch
for a problem with this service, not a supported way to run the automation.
