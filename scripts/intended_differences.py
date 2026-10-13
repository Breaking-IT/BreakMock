# Copyright 2026 Breaking-IT. Apache-2.0.
"""Intended differences from the pinned WireMock reference (upstream bug fixes).

A registered case passes only when the reference returns its documented upstream
result and BreakMock returns its documented corrected result, both exactly. This
replaces equality for those cases alone; it never filters or normalizes others.
"""
import hashlib, json, pathlib

_CASES = {(c['suite'], c['name']): c for c in json.loads(
    (pathlib.Path(__file__).resolve().parent.parent / 'testdata/intended_differences.json').read_text())['cases']}


def _matches(expected, status, body):
    if status != expected['status']:
        return False
    if 'sha256' in expected:
        return hashlib.sha256(body).hexdigest() == expected['sha256']
    return body == expected['body'].encode()


def check(suite, name, reference, candidate):
    """Return None when unregistered, else (passed, issue). Arguments are (status, body bytes)."""
    case = _CASES.get((suite, name))
    if case is None:
        return None
    passed = _matches(case['reference'], *reference) and _matches(case['candidate'], *candidate)
    return passed, case['issue']
