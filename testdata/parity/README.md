# Parity harness

The Go aggregation in `internal/aggregate` is a port of the Apps Script that used to
do this work in `TicketSales.gs`. The port's whole job was to move the computation
off Apps Script without changing a single published number, so "does it still agree
with the old script" is a question worth being able to answer mechanically rather
than by reading both and hoping.

`legacy.js` is that old aggregation, lifted from `TicketSales.gs` unchanged apart
from reading a file instead of calling `UrlFetchApp` and printing its result instead
of writing it to a sheet. `parity.py` runs both implementations over the same payload
and compares ticket group names, counts, income, t-shirt sizes, merchandise sizes and
the community sponsor list.

Income is compared exactly, not within a tolerance. Both implementations are doing
the same divisions on IEEE-754 doubles in the same order, so they agree to the last
bit; a tolerance would hide a real change in the arithmetic.

## Running it

Against the committed fixture, which needs nothing but Go, Node and Python:

    make parity

Against a real payload, which is what actually gates a release — the fixture's
anonymised company names collapse several sponsors onto one, so the sponsor list is
only meaningful on real data:

    make parity ORDERS=/path/to/real/orders.json

The real payload is attendee personal data and is gitignored. Delete it when done.
