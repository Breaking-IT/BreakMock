#!/usr/bin/env python3
# Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
"""Bounded HTTP-journal concurrency regression for disposable WireMock instances.

Start EVERY server with --max-request-journal-entries matching --capacity and
journaling enabled. This script resets mappings, scenarios, and journals.

Concurrent completion/retention order is intentionally NOT compared between
servers. Each server independently checks UUID uniqueness, complete under-cap
capture, capacity, and retained-request membership. Sequential follow-up checks
verify eviction order and compare admin JSON bytes, preserving whitespace and
member order after narrowly documented dynamic scalar substitutions.

Examples:
  python3 scripts/journal_concurrency_differential.py --capacity 64 \
    --reference http://127.0.0.1:18601 \
    --candidate go=http://127.0.0.1:18602 \
    --candidate rust=http://127.0.0.1:18603 --scenario-check

No servers or child processes are started. Stdlib only; oracle.identify checks
both the pinned Java version and each candidate's independent language header.
"""
import argparse
import concurrent.futures
import datetime
import hashlib
import http.client
import json
import os
import pathlib
import threading
import time
import urllib.parse
import uuid

from oracle import identify

MAPPING_ID = "41000000-0000-4000-8000-000000000001"
SCENARIO_IDS = ["41000000-0000-4000-8000-000000000002",
                "41000000-0000-4000-8000-000000000003"]
MOCK_BODY = b'fixed UUID 11111111-1111-4111-8111-111111111111; date 2020-01-02T03:04:05Z'
DYNAMIC_RULES = [
    "Serve-event id: generated UUID replaced with <generated-event-id>.",
    "Logged request loggedDate/loggedDateString: replaced with 0/<logged-time>.",
    "Logged request port: actual listening port replaced with 0.",
    "Serve-event timing processTime/responseSendTime/serveTime/totalTime: replaced with 0.",
    "Logged response headers Date scalar values: replaced with <response-date>.",
    "No sorting, object reserialization, whitespace normalization, response-body rewriting, "
    "fixed-mapping-ID replacement, or concurrent event-order comparison.",
]


def call(base, method, path, body=None, timeout=15):
    """Send identical HTTP header order and Host to every implementation."""
    target = urllib.parse.urlsplit(base)
    connection_class = http.client.HTTPSConnection if target.scheme == "https" else http.client.HTTPConnection
    connection = connection_class(target.hostname, target.port, timeout=timeout)
    data = None if body is None else json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode()
    try:
        connection.putrequest(method, path, skip_host=True, skip_accept_encoding=True)
        connection.putheader("Host", "journal.example")
        connection.putheader("Accept-Encoding", "identity")
        if data is not None:
            connection.putheader("Content-Type", "application/json")
            connection.putheader("Content-Length", str(len(data)))
        connection.endheaders(data)
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


def require_call(base, method, path, body=None, expected=200):
    status, raw = call(base, method, path, body)
    if status != expected:
        raise RuntimeError(f"{method} {path}: HTTP {status}, expected {expected}; {raw[:500]!r}")
    return raw


def clear_journal(base):
    require_call(base, "DELETE", "/__admin/requests")


def install(base):
    require_call(base, "POST", "/__admin/reset")
    require_call(base, "POST", "/__admin/mappings", {
        "id": MAPPING_ID,
        "request": {"method": "GET", "urlPath": "/journal-concurrency"},
        "response": {"status": 200, "body": MOCK_BODY.decode(),
                     "headers": {"Content-Type": "text/plain"}},
    }, expected=201)


def snapshot(base, count, timeout=5):
    """Allow response-send completion to finalize journal entries; never retry load."""
    deadline = time.monotonic() + timeout
    while True:
        raw = require_call(base, "GET", "/__admin/requests")
        document = json.loads(raw)
        events = document.get("requests", [])
        finalized = all("response" in event for event in events)
        if len(events) >= count and finalized or time.monotonic() >= deadline:
            return document, raw
        time.sleep(.01)


def token(event):
    query = urllib.parse.urlsplit(event.get("request", {}).get("url", "")).query
    values = urllib.parse.parse_qs(query).get("token", [])
    return values[0] if len(values) == 1 else None


def load(base, tokens, workers):
    """One synchronization point per worker; no retries hide loss or duplicates."""
    count = min(workers, len(tokens))
    barrier = threading.Barrier(count)

    def worker(index):
        barrier.wait(timeout=10)
        results = []
        for value in tokens[index::count]:
            try:
                status, raw = call(base, "GET", "/journal-concurrency?token=" + urllib.parse.quote(value))
                results.append({"token": value, "passed": status == 200 and raw == MOCK_BODY,
                                **({} if status == 200 and raw == MOCK_BODY else
                                   {"status": status, "body": raw[:500].decode(errors="replace")})})
            except Exception as error:
                results.append({"token": value, "passed": False, "error": str(error)})
        return results

    with concurrent.futures.ThreadPoolExecutor(max_workers=count) as executor:
        return [item for result in executor.map(worker, range(count)) for item in result]


def invariant_record(name, passed, details):
    return {"name": name, "comparison": "per_server_invariant", "passed": bool(passed), **details}


def inspect_journal(base, expected, capacity, exact_membership):
    count = min(len(expected), capacity)
    document, _ = snapshot(base, count)
    events = document.get("requests", [])
    ids = [event.get("id") for event in events]
    actual = [token(event) for event in events]
    valid_ids = all(isinstance(value, str) and valid_uuid(value) for value in ids)
    count_raw = require_call(base, "POST", "/__admin/requests/count", {"urlPath": "/journal-concurrency"})
    count_result = json.loads(count_raw).get("count")
    membership = set(actual) == set(expected) if exact_membership else set(actual).issubset(set(expected))
    checks = {
        "entry_count": len(events) == count,
        "meta_total": document.get("meta", {}).get("total") == count,
        "count_endpoint": count_result == count,
        "valid_unique_ids": valid_ids and len(set(ids)) == len(ids),
        "unique_request_tokens": len(set(actual)) == len(actual),
        "request_membership": membership,
        "all_matched": all(event.get("wasMatched") is True for event in events),
    }
    return all(checks.values()), {"checks": checks, "expected_count": count,
                                "actual_count": len(events), "count_endpoint": count_result,
                                "tokens": actual, "ids": ids}


def valid_uuid(value):
    try:
        return str(uuid.UUID(value)) == value.lower()
    except (ValueError, AttributeError):
        return False


def normalized_admin_bytes(raw):
    """Patch only approved scalar spans in the ORIGINAL bytes; never dump objects."""
    source = raw.decode("utf-8")
    document = json.loads(source)
    decoder = json.JSONDecoder()
    edits = []
    paths = []

    def skip(index):
        while index < len(source) and source[index].isspace():
            index += 1
        return index

    def replacement(path, value):
        # This harness normalizes the GET /requests envelope only. Every other
        # endpoint remains byte-exact unless it has that same real envelope.
        if len(path) < 3 or path[0] != "requests" or not isinstance(path[1], int):
            return value
        rest = path[2:]
        if rest == ("id",) and isinstance(value, str) and valid_uuid(value):
            return "<generated-event-id>"
        if rest == ("request", "loggedDate") or rest == ("request", "port"):
            return 0
        if rest == ("request", "loggedDateString"):
            return "<logged-time>"
        if len(rest) == 2 and rest[0] == "timing" and rest[1] in (
                "processTime", "responseSendTime", "serveTime", "totalTime"):
            return 0
        if len(rest) >= 3 and rest[:2] == ("response", "headers") and str(rest[2]).lower() == "date":
            return "<response-date>"
        return value

    def walk(index, value, path):
        index = skip(index)
        if isinstance(value, dict):
            index = skip(index + 1)
            while source[index] != "}":
                key, end = decoder.raw_decode(source, index)
                index = skip(end)
                if source[index] != ":":
                    raise ValueError("Malformed JSON object")
                index = skip(walk(index + 1, value[key], path + (key,)))
                if source[index] == ",":
                    index = skip(index + 1)
            return index + 1
        if isinstance(value, list):
            index = skip(index + 1)
            for position, child in enumerate(value):
                index = skip(walk(index, child, path + (position,)))
                if source[index] == ",":
                    index = skip(index + 1)
            if source[index] != "]":
                raise ValueError("Malformed JSON array")
            return index + 1
        _, end = decoder.raw_decode(source, index)
        normalized = replacement(path, value)
        if normalized != value:
            edits.append((index, end, json.dumps(normalized, ensure_ascii=False)))
            paths.append("/" + "/".join(str(part) for part in path))
        return end

    walk(0, document, ())
    for start, end, value in reversed(edits):
        source = source[:start] + value + source[end:]
    return source.encode("utf-8"), paths


def byte_record(name, reference, candidate, normalize=False):
    rs, rb = reference
    cs, cb = candidate
    rnorm, rpaths = normalized_admin_bytes(rb) if normalize else (rb, [])
    cnorm, cpaths = normalized_admin_bytes(cb) if normalize else (cb, [])
    passed = rs == cs and rnorm == cnorm
    return {
        "name": name, "comparison": "admin_bytes_after_dynamic_scalar_substitution" if normalize else "exact_status_and_body_bytes",
        "passed": passed, "reference_status": rs, "candidate_status": cs,
        "reference_sha256": hashlib.sha256(rnorm).hexdigest(),
        "candidate_sha256": hashlib.sha256(cnorm).hexdigest(),
        "reference_normalized_paths": rpaths, "candidate_normalized_paths": cpaths,
        **({} if passed else {"reference_body": rnorm[:20000].decode(errors="replace"),
                              "candidate_body": cnorm[:20000].decode(errors="replace"),
                              "reference_body_bytes": len(rnorm), "candidate_body_bytes": len(cnorm)}),
    }


def sequential_followup(base, capacity):
    clear_journal(base)
    count = capacity + 3
    responses = []
    for index in range(count):
        responses.append(call(base, "GET", f"/journal-concurrency?token=tail-{index:06d}"))
    document, _ = snapshot(base, capacity)
    expected = [f"tail-{index:06d}" for index in reversed(range(count - capacity, count))]
    actual = [token(event) for event in document.get("requests", [])]
    checks = {"newest_first_and_oldest_evicted": actual == expected,
              "all_responses_exact": all(status == 200 and raw == MOCK_BODY for status, raw in responses)}
    record = invariant_record("sequential retention order", all(checks.values()),
                              {"checks": checks, "expected_tokens": expected, "actual_tokens": actual})
    # A small deterministic fixture keeps byte-diff output useful and excludes
    # prior concurrent order completely rather than sorting it away.
    clear_journal(base)
    for index in range(3):
        require_call(base, "GET", f"/journal-concurrency?token=deterministic-{index}")
    _, raw = snapshot(base, 3)
    snapshots = {
        "deterministic journal": (200, raw),
        "deterministic count": call(base, "POST", "/__admin/requests/count", {"urlPath": "/journal-concurrency"}),
        "deterministic unmatched count": call(base, "POST", "/__admin/requests/count", {"urlPath": "/absent"}),
    }
    return record, snapshots


def scenario_check(base, workers):
    """Check completed transition visibility; observe, do not invent, atomicity.

    Java's scenario matching and onStubServed state update are separate operations.
    Concurrent 'Started' winner counts are recorded, never compared or assumed 1.
    Once the concurrent batch is complete, every later response must be 'done'.
    """
    for identifier, required, response, new in zip(SCENARIO_IDS, ("Started", "Done"), ("started", "done"), ("Done", None)):
        mapping = {"id": identifier, "scenarioName": "journal-concurrency-state",
                   "requiredScenarioState": required,
                   "request": {"urlPath": "/journal-scenario"}, "response": {"body": response}}
        if new is not None:
            mapping["newScenarioState"] = new
        require_call(base, "POST", "/__admin/mappings", mapping, expected=201)
    require_call(base, "POST", "/__admin/scenarios/reset")
    barrier = threading.Barrier(workers)

    def worker(_):
        barrier.wait(timeout=10)
        return call(base, "GET", "/journal-scenario")

    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
        responses = list(executor.map(worker, range(workers)))
    followup = [call(base, "GET", "/journal-scenario") for _ in range(3)]
    winners = sum(status == 200 and body == b"started" for status, body in responses)
    passed = winners >= 1 and all(status == 200 and body in (b"started", b"done") for status, body in responses) and all(value == (200, b"done") for value in followup)
    return invariant_record("scenario completed-transition visibility", passed, {
        "concurrent_started_responses": winners, "concurrent_done_responses": len(responses) - winners,
        "single_winner_observed": winners == 1,
        "single_winner_is_not_a_parity_assertion": True,
        "followup_bodies": [body.decode(errors="replace") for _, body in followup],
    })


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--reference", required=True)
    parser.add_argument("--candidate", action="append", required=True, help="Repeat KIND=URL for go/rust, or URL with --candidate-kind")
    parser.add_argument("--candidate-kind", choices=("go", "rust"), default=os.environ.get("BREAKMOCK_CANDIDATE_KIND", "go"))
    parser.add_argument("--capacity", type=int, default=64, help="Must match EVERY server's --max-request-journal-entries")
    parser.add_argument("--workers", type=int, default=16)
    parser.add_argument("--iterations", type=int, default=3)
    parser.add_argument("--scenario-check", action="store_true")
    parser.add_argument("--output", default="testdata/reports/journal-concurrency.json")
    args = parser.parse_args()
    if args.capacity < 4 or args.workers < 2 or args.iterations < 1:
        parser.error("capacity >= 4, workers >= 2, and iterations >= 1 are required")
    targets = [("java", args.reference)]
    for item in args.candidate:
        kind, url = item.split("=", 1) if "=" in item else (args.candidate_kind, item)
        if kind not in ("go", "rust"):
            parser.error("candidate kind must be go or rust")
        targets.append((kind, url))
    if len({url.rstrip("/") for _, url in targets}) != len(targets):
        parser.error("reference and candidates must be distinct instances")
    report = {"reference_version": "4.0.0-beta.40", "timestamp_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "capacity": args.capacity, "workers": args.workers, "iterations": args.iterations,
              "dynamic_scalar_normalization": DYNAMIC_RULES, "servers": [], "comparisons": []}
    deterministic = []
    for index, (kind, base) in enumerate(targets):
        server = {"kind": kind, "base": base, "invariants": []}
        report["servers"].append(server)
        try:
            # oracle's historical 'go' alias follows the candidate environment.
            # Explicit multi-language targets must bypass that alias substitution.
            old_kind = os.environ.get("BREAKMOCK_CANDIDATE_KIND")
            try:
                os.environ["BREAKMOCK_CANDIDATE_KIND"] = kind
                server["identity"] = identify(base, kind)
            finally:
                if old_kind is None:
                    os.environ.pop("BREAKMOCK_CANDIDATE_KIND", None)
                else:
                    os.environ["BREAKMOCK_CANDIDATE_KIND"] = old_kind
            install(base)
            for iteration in range(args.iterations):
                for phase, count, complete in (("under-cap", max(2, args.capacity // 2), True), ("over-cap", args.capacity * 3, False)):
                    clear_journal(base)
                    tokens = [f"{iteration}-{phase}-{n:06d}" for n in range(count)]
                    responses = load(base, tokens, args.workers)
                    ok, details = inspect_journal(base, tokens, args.capacity, complete)
                    details["mock_response_failures"] = [value for value in responses if not value["passed"]]
                    details["mock_response_exact_checks"] = len(responses)
                    server["invariants"].append(invariant_record(f"iteration {iteration} {phase}", ok and not details["mock_response_failures"], details))
            record, snapshots = sequential_followup(base, args.capacity)
            server["invariants"].append(record)
            deterministic.append((index, snapshots))
            if args.scenario_check:
                server["invariants"].append(scenario_check(base, args.workers))
        except Exception as error:
            server["invariants"].append(invariant_record("setup or execution error", False, {"error": str(error)}))
        for record in server["invariants"]:
            print(("PASS " if record["passed"] else "FAIL ") + kind + " " + record["name"], flush=True)
    reference = next((snapshots for index, snapshots in deterministic if index == 0), None)
    if reference is not None:
        for index, candidate in deterministic:
            if index == 0:
                continue
            for name in reference:
                try:
                    record = byte_record(name, reference[name], candidate[name], normalize=name == "deterministic journal")
                except Exception as error:
                    record = {"name": name, "comparison": "admin_comparison_error", "passed": False, "error": str(error)}
                record["candidate"] = targets[index][0]
                report["comparisons"].append(record)
                print(("PASS " if record["passed"] else "FAIL ") + targets[index][0] + " " + name, flush=True)
    all_invariants = [record for server in report["servers"] for record in server["invariants"]]
    report["invariants_passed"] = sum(record["passed"] for record in all_invariants)
    report["invariants_failed"] = sum(not record["passed"] for record in all_invariants)
    report["exact_admin_byte_comparisons_passed"] = sum(record["passed"] and record["comparison"] == "exact_status_and_body_bytes" for record in report["comparisons"])
    report["normalized_admin_byte_comparisons_passed"] = sum(record["passed"] and record["comparison"] == "admin_bytes_after_dynamic_scalar_substitution" for record in report["comparisons"])
    report["byte_comparisons_failed"] = sum(not record["passed"] for record in report["comparisons"])
    report["passed"] = report["invariants_failed"] == 0 and report["byte_comparisons_failed"] == 0 and len(report["comparisons"]) == 3 * (len(targets) - 1)
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({key: value for key, value in report.items() if key.endswith("passed") or key.endswith("failed")}))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
