# ticketbutler-proxy

A caching service that sits between the Cloud Native Denmark Google Sheets
automation and the TicketButler API.

Google Apps Script cannot wait longer than 60 seconds for an HTTP response, and the
TicketButler orders endpoint stopped fitting in that window as the conference grew.
This service fetches the orders on a schedule with a timeout that can actually
succeed, pre-aggregates them into the numbers the spreadsheets need, and serves the
result in milliseconds.

Full documentation follows in `docs:` commits.
