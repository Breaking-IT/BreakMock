#!/usr/bin/env python3
# Copyright 2026 Breaking-IT. Apache-2.0.
"""Check that responses stay dynamic across requests, edits and caches.

Single-request suites cannot detect a cache that serves stale or shared results.
This suite repeats requests against two dedicated servers, for small templates
and for templates above 64 KiB (the size at which template caching changed):

* varying request values through plan-path and full-evaluator templates;
* random and time helpers, which must differ per request (property checks, as
  the values cannot match Java byte for byte);
* mapping edits, body file edits and jsonBody edits, with and without gzip;
* raw non-ASCII header bytes in matching, templates and the journal;
* journal entries under concurrency, which must hold the body served to that request.

Known divergence: WireMock serves a stale cached template after a templated
body file is edited; BreakMock must serve the edited file (checked, not compared).

Responses are compared as exact status and body bytes unless noted.
Both servers are reset. Never use production servers.
"""
import argparse, base64, concurrent.futures, datetime, gzip, json, pathlib, re, socket, time, urllib.error, urllib.parse, urllib.request

p = argparse.ArgumentParser()
p.add_argument('--reference', default='http://127.0.0.1:18087')
p.add_argument('--candidate', default='http://127.0.0.1:18088')
p.add_argument('--report', '--output', dest='report')
p.add_argument('--discover', action='store_true', help='Include known unsupported cases (template syntax errors)')
a = p.parse_args()
SERVERS = {'reference': a.reference, 'candidate': a.candidate}
MAPPING_ID = '0c7d9a52-1f3e-4b7a-9d2c-5e8f00000001'
records, failed = [], []


def call(base, path, data=None, method=None, headers=None):
    headers = dict(headers or {})
    if isinstance(data, (dict, list)):
        data = json.dumps(data).encode(); headers.setdefault('Content-Type', 'application/json')
    elif isinstance(data, str):
        data = data.encode()
    request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
    try:
        response = urllib.request.urlopen(request, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, {k.lower(): v for k, v in response.headers.items()}, response.read()


def check(name, ok, detail=''):
    records.append({'name': name, 'passed': bool(ok), 'detail': detail[:600]})
    print(('PASS ' if ok else 'FAIL ') + name + ('' if ok else ': ' + detail[:600]))
    if not ok:
        failed.append(name)


def body_of(result):
    """Response body after transport decoding, for gzip comparisons."""
    status, headers, body = result
    if headers.get('content-encoding') == 'gzip':
        body = gzip.decompress(body)
    return status, body


def short(result):
    status, _, body = result
    return f'{status} {body[:160]!r}...{body[-60:]!r} ({len(body)} bytes)'


def padding(size):
    """Static text with newlines so templates are large and multi-line."""
    line = '{"pad":"' + 'p' * 90 + '"},\n'
    return '[' + line * (size // len(line)) + '{"end":true}]'


SIZES = {'small': '', 'large': padding(80 * 1024)}


def put_mapping(base, response, request=None):
    mapping = {'id': MAPPING_ID, 'request': request or {'urlPathPattern': '/dyn/.*'}, 'response': {'status': 200, **response}}
    status, _, body = call(base, '/__admin/mappings/' + MAPPING_ID, mapping, method='PUT')
    if status == 404:
        status, _, body = call(base, '/__admin/mappings', mapping, method='POST')
    if status not in (200, 201):
        raise RuntimeError(f'{base}: mapping rejected {status} {body[:200]!r}')


def reset():
    for base in SERVERS.values():
        call(base, '/__admin/reset', method='POST')


def normalized(server, result):
    """Replace the server's own address, which appears in Jetty error pages."""
    host = urllib.parse.urlsplit(SERVERS[server]).netloc.encode()
    return result[0], result[2].replace(host, b'<server>')


def compare(name, path, data=None, method=None, headers=None, decode=False):
    results = {k: call(base, path, data, method, headers) for k, base in SERVERS.items()}
    left, right = results['reference'], results['candidate']
    same = body_of(left) == body_of(right) if decode else normalized('reference', left) == normalized('candidate', right)
    check(name, same, f"reference {short(left)} | candidate {short(right)}")
    return results


# 1. Varying request values: every request must render its own values.
VALUES = ['alpha', 'beta', '', 'x', '<tag>&"quoted"\'', 'é漢字🙂', 'a b+c%2Fd', '12345', '-1.50', 'null', 'true', '{{not.a.template}}',
          'line\nbreak', 'z' * 300, 'tab\there', 'back\\slash', '$.path', 'üñí', 'X', 'beta']
TEMPLATES = {
    'plan': '{"q":"{{request.query.q}}","h":"{{request.headers.X-V}}","seg":"{{request.path.[1]}}","method":"{{request.method}}","url":"{{request.url}}"}',
    'full evaluator': "{{jsonPath request.body '$.name'}}|{{#each (jsonPath request.body '$.items')}}{{this}},{{/each}}|{{upper request.query.q}}|"
                      "{{#if (eq request.query.q 'X')}}is-X{{else}}not-X{{/if}}|{{#assign 'v'}}{{request.query.q}}{{/assign}}[{{v}}]|{{size request.query.q}}",
    'escaping': '{{request.query.q}}|{{{request.query.q}}}|{{request.headers.X-V}}',
}
if a.discover:
    # Known gap: Handlebars.java syntax error handling (see docs/COMPATIBILITY.md).
    TEMPLATES['parse error'] = "{{jsonPath request.body '$.name'}}|{{#each (parseJson request.body).items}}{{this}}{{/each}}"
for size, pad in SIZES.items():
    for kind, template in TEMPLATES.items():
        reset()
        for base in SERVERS.values():
            put_mapping(base, {'body': template + pad, 'transformers': ['response-template']})
        for i, value in enumerate(VALUES):
            body = json.dumps({'name': value, 'items': [i, value]})
            header_value = value.encode('latin-1', 'replace').decode('latin-1').replace('\n', ' ')
            compare(f'{size} {kind} value {i}', f'/dyn/seg{i}?q=' + urllib.parse.quote(value), body, 'POST',
                    {'Content-Type': 'application/json', 'X-V': header_value})

# 2. Random and time helpers: must change per request on both servers.
UUID = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
RANDOM = "{{randomValue type='UUID'}}|{{randomValue length=16 type='ALPHANUMERIC'}}|{{randomInt lower=0 upper=1000000000}}|{{now format='epoch'}}|{{pickRandom 'a' 'b' 'c' 'd' 'e' 'f' 'g' 'h'}}|"
for size, pad in SIZES.items():
    reset()
    for base in SERVERS.values():
        put_mapping(base, {'body': RANDOM + pad, 'headers': {'X-Id': "{{randomValue type='UUID'}}"}, 'transformers': ['response-template']})
    for server, base in SERVERS.items():
        rows, header_ids = [], []
        for _ in range(30):
            status, headers, body = call(base, '/dyn/random')
            text = body.decode()
            rows.append(text.split('|', 5)); header_ids.append(headers.get('x-id', ''))
        now_ms = time.time() * 1000
        uuids, strings, ints, epochs, picks, rests = zip(*rows)
        check(f'{size} random {server} UUID format', all(UUID.match(u) for u in uuids + tuple(header_ids)), str(uuids[:3]))
        check(f'{size} random {server} UUIDs unique per request', len(set(uuids)) == 30 and len(set(header_ids)) == 30, f'{len(set(uuids))} body, {len(set(header_ids))} header')
        check(f'{size} random {server} strings vary', len(set(strings)) == 30 and all(re.fullmatch('[A-Za-z0-9]{16}', s) for s in strings), str(strings[:3]))
        check(f'{size} random {server} integers vary', len(set(ints)) >= 28, str(ints[:5]))
        check(f'{size} random {server} now is current', all(abs(int(e) - now_ms) < 10_000 for e in epochs), str(epochs[:2]))
        check(f'{size} random {server} pickRandom varies', len(set(picks)) >= 3 and set(picks) <= set('abcdefgh'), str(set(picks)))
        check(f'{size} random {server} static text intact', set(rests) == {pad}, 'static part differs')

# 3. Mapping edits: the newest definition must be served immediately.
EDITS = [
    ('template A', {'body': 'A={{request.query.a}}', 'transformers': ['response-template']}),
    ('template B (same length)', {'body': 'B={{request.query.b}}', 'transformers': ['response-template']}),
    ('template A again', {'body': 'A={{request.query.a}}', 'transformers': ['response-template']}),
    ('helper added', {'body': "U={{upper request.query.a}}", 'transformers': ['response-template']}),
    ('static text', {'body': 'S={{request.query.a}}'}),
    ('templating disabled for same text', {'body': 'A={{request.query.a}}'}),
    ('jsonBody', {'jsonBody': {'v': 1, 'pad': 'P'}}),
    ('jsonBody edited', {'jsonBody': {'v': 2, 'pad': 'P'}}),
    ('jsonBody template', {'jsonBody': {'v': '{{request.query.a}}', 'pad': 'P'}, 'transformers': ['response-template']}),
]
for size, pad in SIZES.items():
    reset()
    for label, response in EDITS:
        response = json.loads(json.dumps(response))
        if 'body' in response:
            response['body'] += pad
        else:
            response['jsonBody']['pad'] += pad
        for base in SERVERS.values():
            put_mapping(base, response)
        for encoding in ('identity', 'gzip'):
            compare(f'{size} mapping edit: {label} [{encoding}]', '/dyn/edit?a=first&b=second', headers={'Accept-Encoding': encoding})

# 4. Body file edits through the admin API, templated and static, with gzip.
FILES = [
    ('template v1', 'T1={{request.query.a}}', True),
    ('template v2 (same length)', 'T2={{request.query.a}}', True),
    ('template v1 again', 'T1={{request.query.a}}', True),
    ('static v1', 'S1={{request.query.a}}', False),
    ('static v2 (same length)', 'S2={{request.query.a}}', False),
]
for size, pad in SIZES.items():
    reset()
    for label, content, templated in FILES:
        for base in SERVERS.values():
            status, _, body = call(base, '/__admin/files/dynamic-body.json', content + pad, method='PUT')
            if status not in (200, 201, 204):
                raise RuntimeError(f'{base}: file write rejected {status} {body[:200]!r}')
            put_mapping(base, {'bodyFileName': 'dynamic-body.json', **({'transformers': ['response-template']} if templated else {})})
        for encoding in ('identity', 'gzip'):
            name = f'{size} file edit: {label} [{encoding}]'
            if label.startswith('template v2'):
                # Known divergence: WireMock keeps serving its cached compiled
                # template after a templated body file is edited. BreakMock serves
                # the new file, which is required here; Java's staleness is recorded.
                results = {k: call(base, '/dyn/file?a=value', headers={'Accept-Encoding': encoding}) for k, base in SERVERS.items()}
                fresh = ('T2=value' + pad).encode()
                check(name + ' (candidate serves edited file)', body_of(results['candidate'])[1] == fresh, short(results['candidate']))
                records.append({'name': name + ' (reference)', 'passed': True, 'known_divergence': 'reference serves stale template: ' + body_of(results['reference'])[1][:12].decode(errors='replace')})
                continue
            compare(name, '/dyn/file?a=value', headers={'Accept-Encoding': encoding})
    for base in SERVERS.values():
        call(base, '/__admin/files/dynamic-body.json', method='DELETE')
    compare(f'{size} file edit: deleted file', '/dyn/file?a=value')

# 5. Raw header bytes: Jetty decodes each header byte as ISO-8859-1, for
#    matching, templates and the journal alike.
def raw_get(base, path, header):
    url = urllib.parse.urlsplit(base)
    with socket.create_connection((url.hostname, url.port), timeout=30) as connection:
        connection.sendall(b'GET ' + path.encode() + b' HTTP/1.1\r\nHost: x\r\nX-V: ' + header + b'\r\nConnection: close\r\n\r\n')
        data = b''
        while chunk := connection.recv(65536):
            data += chunk
    head, _, body = data.partition(b'\r\n\r\n')
    if b'transfer-encoding: chunked' in head.lower():
        decoded = b''
        while True:
            size, _, body = body.partition(b'\r\n')
            size = int(size.split(b';')[0], 16)
            if size == 0:
                break
            decoded, body = decoded + body[:size], body[size + 2:]
        body = decoded
    return head.split(b' ', 2)[1], body


HEADER_BYTES = [b'plain', b'\xe9', b'\xc3\xa9', b'\xf0\x9f\x99\x82', b'a\xe9b\xc3\xa9', b'\xff\xfe', b'tab\there']
for size, pad in SIZES.items():
    reset()
    for base in SERVERS.values():
        put_mapping(base, {'body': '[{{request.headers.X-V}}|{{{request.headers.X-V}}}]' + pad, 'transformers': ['response-template']}, {'urlPath': '/dyn/header'})
        call(base, '/__admin/mappings', {'priority': 1, 'request': {'urlPath': '/dyn/header', 'headers': {'X-V': {'equalTo': 'é'}}}, 'response': {'status': 200, 'body': 'matched é'}}, method='POST')
    for value in HEADER_BYTES:
        results = {k: raw_get(base, '/dyn/header', value) for k, base in SERVERS.items()}
        check(f'{size} header bytes {value!r}', results['reference'] == results['candidate'], f"reference {results['reference'][1][:80]!r} | candidate {results['candidate'][1][:80]!r}")
    logged = {}
    for k, base in SERVERS.items():
        events = json.loads(call(base, '/__admin/requests')[2])['requests']
        logged[k] = [e['request']['headers'].get('X-V') for e in events]
    check(f'{size} header bytes journal', logged['reference'] == logged['candidate'], f"reference {logged['reference']} | candidate {logged['candidate']}")

# 6. Journal entries hold the body served to each request, under concurrency.
for size, pad in SIZES.items():
    reset()
    for base in SERVERS.values():
        put_mapping(base, {'body': '{"id":"{{request.query.id}}"}' + pad, 'transformers': ['response-template']})
    for server, base in SERVERS.items():
        def fetch(i):
            result = call(base, f'/dyn/journal?id={i}', headers={'Accept-Encoding': 'gzip' if i % 3 == 0 else 'identity'})
            return f'/dyn/journal?id={i}', result
        with concurrent.futures.ThreadPoolExecutor(16) as pool:
            served = dict(pool.map(fetch, range(120)))
        _, _, raw = call(base, '/__admin/requests?limit=500')
        events = json.loads(raw)['requests']
        logged = {e['request']['url']: base64.b64decode(e['response']['bodyAsBase64']) for e in events}
        missing = [u for u in served if u not in logged]
        wrong = [u for u, result in served.items() if u in logged and logged[u] not in (result[2], gzip.decompress(result[2]) if result[1].get('content-encoding') == 'gzip' else result[2])]
        rendered = [u for u, result in served.items() if body_of(result)[1] != ('{"id":"%s"}' % u.rsplit('=', 1)[1] + pad).encode()]
        check(f'{size} journal {server} logs every request', not missing, f'{len(missing)} missing')
        check(f'{size} journal {server} logged bodies match served bodies', not wrong, f'{len(wrong)} differ, e.g. {wrong[:3]}')
        check(f'{size} journal {server} concurrent responses rendered per request', not rendered, f'{len(rendered)} wrong, e.g. {rendered[:3]}')

summary = {'reference_version': '4.0.0-beta.40', 'timestamp_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
           'checks': len(records), 'passed': len(records) - len(failed), 'failed': failed}
print(json.dumps(summary))
if a.report:
    pathlib.Path(a.report).parent.mkdir(parents=True, exist_ok=True)
    pathlib.Path(a.report).write_text(json.dumps({**summary, 'results': records}, indent=2) + '\n')
raise SystemExit(bool(failed))
