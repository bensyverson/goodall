#!/usr/bin/env python3
"""Rewinds a check-drift baseline to before GPT-6, to show what the check would
have said then. Usage: rewind.py baseline.json > before-gpt6.json

It is an approximation: the pre-GPT-6 state was never captured, so this removes
what GPT-6 added rather than restoring what was there."""
import json
import sys

b = json.load(open(sys.argv[1]))
b["accepted"] = "2026-08-31 (rewound)"
for name in ("openai", "openrouter"):
    b["catalogs"][name] = [i for i in b["catalogs"][name] if "gpt-6" not in i]
for name in ("openai", "openai-deprecations"):
    b["changelogs"][name] = [
        s for s in b["changelogs"][name]
        if "September, 2026" not in s and "October, 2026" not in s
    ]
b["live"] = {
    row: state for row, state in b["live"].items()
    if "gpt-6" not in row and row != "loop/openai/gpt-5.6-terra/low"
}
json.dump(b, sys.stdout, indent=2)
