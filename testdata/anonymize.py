#!/usr/bin/env python3
"""Turn a real TicketButler orders payload into a committable test fixture.

The live response contains attendee names, email addresses, phone numbers,
employers and discount codes, so it can never be committed. This script replaces
every piece of personal data with deterministic fakes while leaving intact
everything the aggregation reads: order state, VAT rate and exemption, prices,
ticket type names and ids, and question and choice headings. Counts and totals
computed from the fixture therefore match the ones computed from the real payload,
which is what makes it usable as a golden-file test.

It walks the whole document rather than named paths, because the payload repeats
identifiers under several different key names -- a choice id appears as
``choice_uuid`` under an answer and as ``uuid`` under a question -- and missing one
of them reintroduces a value the fixture was supposed to have dropped.

It also drops the per-ticket ``questions`` array and the duplicated ``answers``
array from all but the first two tickets. The API repeats the entire event question
schema on every ticket, which is most of the payload size and none of its meaning;
keeping two preserves a test that the parser tolerates and ignores the fields.

Usage:
    python3 testdata/anonymize.py path/to/real/orders.json > testdata/orders.sample.json
"""

import hashlib
import json
import re
import sys

# The placeholder identities are deliberately not plausible Danish names. An
# earlier version of this script drew from real Danish name lists and reproduced an
# actual attendee's full name by coincidence, which put the very data the fixture
# exists to remove back into it. Synthetic words cannot collide, and
# verify_fixture.py enforces that by substring-matching the real payload.
FIRST = ["Alfa", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel",
         "India", "Juno", "Kilo", "Lima", "Mike", "November", "Oscar", "Papa",
         "Quebec", "Romeo", "Sierra", "Tango", "Uniform", "Victor", "Whiskey",
         "Xray", "Yankee", "Zulu"]
LAST = ["Testperson", "Prøveperson", "Eksempelsen", "Fiktivsen"]
COMPANY = ["Example Alfa ApS", "Example Bravo A/S", "Example Charlie GmbH",
           "Example Delta AB", "Example Echo Oy", "Example Foxtrot BV",
           "Example Golf AS", "Example Hotel Ltd", "Example India SARL",
           "Example Juno SpA", "Example Kilo ApS", "Example Lima A/S"]

# Free-text answers can contain anything the attendee typed, including dietary and
# health information, so they are blanked rather than replaced.
BLANKED_ANSWER_VARIATIONS = {"TEXT", "TEXTAREA", "PHONE"}


def pick(pool, *seed):
    """Deterministically choose from pool, so re-running gives the same fixture."""
    h = hashlib.sha256("|".join(str(s) for s in seed).encode()).digest()
    return pool[int.from_bytes(h[:4], "big") % len(pool)]


def fake_uuid(value):
    """Rewrite an identifier, keeping its formatting so parsers see the same shape.

    Derived from the original value, so a single logical id that appears under
    several key names is rewritten to one consistent fake id.
    """
    if not isinstance(value, str) or not value:
        return value
    h = hashlib.sha256(("uuid:" + value).encode()).hexdigest()[:32]
    if "-" in value:
        return f"{h[:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:32]}"
    return h


def person(seed):
    """Return a stable (first, last, email, company) identity for a seed."""
    first = pick(FIRST, "first", seed)
    last = pick(LAST, "last", seed)
    company = pick(COMPANY, "company", seed)
    domain = re.sub(r"[^a-z]", "", company.split()[1].lower()) + ".example"
    return first, last, f"{first.lower()}.{last.lower()}@{domain}", company


def fake_phone(seed):
    digits = int(hashlib.sha256(("phone:" + seed).encode()).hexdigest()[:8], 16) % 10**8
    return f"+45{digits:08d}"


def scrub(node, identity):
    """Rewrite personal data in place, everywhere in the tree below node.

    identity is the (first, last, email, company) the current subtree belongs to.
    """
    if isinstance(node, list):
        for item in node:
            scrub(item, identity)
        return
    if not isinstance(node, dict):
        return

    first, last, email, company = identity

    for key, value in list(node.items()):
        # Every identifier, under whichever name it appears.
        if "uuid" in key and isinstance(value, str):
            node[key] = fake_uuid(value)
            continue

        if key in ("first_name",):
            node[key] = first
        elif key in ("last_name",):
            node[key] = last
        elif key in ("full_name", "name"):
            node[key] = f"{first} {last}"
        elif key == "email":
            node[key] = email
        elif key == "phone":
            node[key] = fake_phone(email) if value else value
        elif key in ("company_name", "business_name"):
            node[key] = company if value else value
        elif key == "code":
            # Discount codes are derived from sponsor company names.
            node[key] = "CODE" + hashlib.sha256(str(value).encode()).hexdigest()[:8].upper()
        elif key == "ticket_download_absolute_url":
            node[key] = "https://example.invalid/tickets/" + fake_uuid(str(value))
        elif key == "answer_value" and value:
            variation = node.get("variation", "")
            heading = node.get("question_heading", "")
            if variation == "EMAIL" or "mail" in heading.lower():
                node[key] = email
            elif variation == "COMPANY":
                node[key] = company
            elif variation.startswith("NAME"):
                node[key] = f"{first} {last}"
            else:
                node[key] = ""
        elif key == "answer_value_secondary" and value:
            node[key] = ""
        else:
            scrub(value, identity)


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    data = json.load(open(sys.argv[1]))
    data["uuid"] = fake_uuid(data.get("uuid"))

    kept_verbose = 0
    for i, order in enumerate(data.get("orders", [])):
        buyer = person(f"order{i}")
        order["order_id"] = "mH" + hashlib.sha256(f"order{i}".encode()).hexdigest()[:8].upper()

        # The buyer identity covers the order itself: address, lines, discount.
        for key in ("uuid", "address", "order_lines", "discount", "payment_method"):
            if key in order:
                if key == "uuid":
                    order[key] = fake_uuid(order[key])
                else:
                    scrub(order[key], buyer)

        # Each ticket is a different attendee, so it gets its own identity.
        for j, ticket in enumerate(order.get("tickets") or []):
            attendee = person(f"order{i}-ticket{j}")
            ticket["id"] = 8000000 + i * 100 + j
            if kept_verbose >= 2:
                # Drop the repeated event question schema and the duplicated answers.
                ticket.pop("answers", None)
                ticket.pop("questions", None)
            else:
                kept_verbose += 1
            scrub(ticket, attendee)

    json.dump(data, sys.stdout, indent=1, ensure_ascii=False)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
