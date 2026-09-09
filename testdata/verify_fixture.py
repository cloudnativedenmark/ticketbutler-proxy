#!/usr/bin/env python3
"""Check that orders.sample.json leaked no personal data and stayed faithful.

Two things must both hold for the fixture to be worth having, and they pull in
opposite directions:

1. No personal data from the real payload may appear in it. Checked by collecting
   every string the real payload holds under a personal-data key -- names, emails,
   phone numbers, employers, identifiers, discount codes -- and asserting none of
   them occurs anywhere in the fixture text.
2. Everything the aggregation reads must be unchanged, so the fixture produces the
   same numbers as the real payload. Checked by comparing a signature built from
   order states, VAT, prices, ticket types and T-shirt answers.

Run it after regenerating the fixture:
    python3 testdata/verify_fixture.py path/to/real/orders.json testdata/orders.sample.json
"""

import collections
import json
import sys

# Keys whose values identify a person, an organisation, or a purchase.
PII_KEYS = {
    "first_name", "last_name", "full_name", "name", "email", "phone",
    "company_name", "business_name", "order_id", "code",
    "answer_value", "answer_value_secondary", "ticket_download_absolute_url",
}


def collect_secrets(node, out, key=None):
    if isinstance(node, dict):
        for k, v in node.items():
            collect_secrets(v, out, k)
    elif isinstance(node, list):
        for v in node:
            collect_secrets(v, out, key)
    elif isinstance(node, str) and key is not None:
        value = node.strip()
        if len(value) < 4:
            return
        if key in PII_KEYS or "uuid" in key:
            out.add(value)
            if "@" in value:
                out.add(value.split("@", 1)[1])


def signature(doc):
    orders = doc["orders"]
    tickets = [t for o in orders for t in o.get("tickets") or []]
    return {
        "orders": len(orders),
        "tickets": len(tickets),
        "states": dict(collections.Counter(o["state"] for o in orders)),
        "vat": sorted({(o["vat_exempt"], o["vat_rate"]) for o in orders}),
        "types": sorted(collections.Counter(
            (t["ticket_type_pk"], t["ticket_type_name"]) for t in tickets).items()),
        "prices": sorted((str(t["price"]), str(t["price_total"])) for t in tickets),
        "tshirts": dict(collections.Counter(
            a["answered_choices"][0]["choice_heading"]
            for t in tickets if t.get("ticket_answer_collection")
            for a in t["ticket_answer_collection"]["answers"]
            if a["question_heading"].strip() == "T-Shirt Size" and a["answered_choices"])),
    }


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    real_path, fixture_path = sys.argv[1], sys.argv[2]
    real = json.load(open(real_path))
    fixture_text = open(fixture_path).read()
    fixture = json.loads(fixture_text)

    secrets = set()
    collect_secrets(real, secrets)
    leaked = sorted(s for s in secrets if s in fixture_text)

    print(f"checked {len(secrets)} personal-data values from {real_path}")
    ok = True
    if leaked:
        ok = False
        print(f"FAIL: {len(leaked)} value(s) leaked into the fixture:")
        for value in leaked[:20]:
            print("   ", repr(value))
    else:
        print("pass: no personal-data value from the real payload appears in the fixture")

    want, got = signature(real), signature(fixture)
    if want != got:
        ok = False
        print("FAIL: the fixture no longer matches the real payload's structure:")
        for key in want:
            if want[key] != got[key]:
                print(f"    {key}:\n      real:    {want[key]}\n      fixture: {got[key]}")
    else:
        print("pass: aggregation-relevant structure is identical")
        print(f"      {got['orders']} orders, {got['tickets']} tickets, "
              f"states {got['states']}, t-shirts {got['tshirts']}")

    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
