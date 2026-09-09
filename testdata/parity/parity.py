"""Compare the Go aggregation with the original Apps Script aggregation.

Usage: parity.py <legacy.json> <go.json> <label>
Exits non-zero on any disagreement. See README.md in this directory.
"""

import json, sys
legacy = json.load(open(sys.argv[1]))
go = json.load(open(sys.argv[2]))
label = sys.argv[3]
fail = []

# ticket groups
lg = {k: (v["count"], v["income"]) for k, v in legacy["ticketCounts"].items()}
gg = {k: (v["count"], v["income_ex_vat"]) for k, v in go["summary"]["ticket_groups"].items()}
if set(lg) != set(gg):
    fail.append(f"group names differ:\n  only legacy: {sorted(set(lg)-set(gg))}\n  only go:     {sorted(set(gg)-set(lg))}")
for k in sorted(set(lg) & set(gg)):
    lc, li = lg[k]; gc, gi = gg[k]
    if lc != gc:
        fail.append(f"count mismatch for {k!r}: legacy {lc} vs go {gc}")
    # Both are IEEE-754 doubles doing the same divisions in the same order, so this
    # should be exact; compare exactly and report the delta if it is not.
    if li != gi:
        fail.append(f"income mismatch for {k!r}: legacy {li!r} vs go {gi!r} (delta {li-gi!r})")

# t-shirts
if legacy["tshirtSizes"] != go["summary"]["tshirt_sizes"]:
    fail.append(f"tshirt mismatch:\n  legacy {legacy['tshirtSizes']}\n  go     {go['summary']['tshirt_sizes']}")

# merch
if legacy["merchCounts"] != go["summary"]["merch"]:
    fail.append(f"merch mismatch:\n  legacy {legacy['merchCounts']}\n  go     {go['summary']['merch']}")

# sponsors
gs = [s["company_name"] for s in go["sponsors"]]
if legacy["sponsors"] != gs:
    fail.append(f"sponsor mismatch:\n  legacy {legacy['sponsors']}\n  go     {gs}")

print(f"=== parity: {label}")
if fail:
    for f in fail: print("  FAIL", f)
    sys.exit(1)
tot = sum(v["count"] for v in legacy["ticketCounts"].values())
inc = sum(v["income"] for v in legacy["ticketCounts"].values())
print(f"  pass: {len(lg)} ticket groups, {tot} tickets, income ex VAT {inc:.2f}")
print(f"  pass: t-shirts {legacy['tshirtSizes']}")
print(f"  pass: merch {legacy['merchCounts']}")
print(f"  pass: {len(gs)} sponsors {gs}")
