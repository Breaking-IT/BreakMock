#!/usr/bin/env python3
"""Large JSON response benchmark comparing BreakMock builds (or WireMock) over real HTTP.

Each workload serves one large JSON document (inline jsonBody, inline body, a
body file, response templates, and gzip). Every response is checked against the
bytes the first measured server produced, so all builds must serve identical
bodies. With journals enabled, the newest logged event is compared too.
Results are same-host observations; the load generator shares the CPU.
"""
import argparse, hashlib, json, os, pathlib, platform, subprocess, tempfile, threading, time, urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent


def document(size, template=''):
    """Deterministic order listing of roughly size bytes (mirrors the Go benchmark)."""
    customer = {'plan': '{{request.query.customer}}', 'helper': "{{jsonPath request.body '$.customer'}}"}.get(template, 'C-1001')
    request_id = '{{request.headers.X-Request-Id}}' if template else 'r-1'
    parts, length, i = [], 0, 0
    while length < size:
        item = ('{"id":%d,"status":"SHIPPED","total":%d.%02d,"note":"Fragile \\u00e9 <handle> with care","items":[{"sku":"SKU-%05d","qty":%d,"price":19.95,"tags":["a","b"]},{"sku":"SKU-%05d","qty":1,"price":5.5,"tags":[]}],"address":{"street":"Main %d","city":"Amsterdam","zip":"1011AB"}}'
                % (i, i * 7 % 1000, i % 100, i, i % 5 + 1, i + 1, i))
        parts.append(item); length += len(item) + 1; i += 1
    return '{"customer":"%s","currency":"EUR","orders":[%s],"requestId":"%s"}' % (customer, ','.join(parts), request_id)


def workloads(size):
    static, plan, helper = document(size), document(size, 'plan'), document(size, 'helper')
    json_headers = {'Content-Type': 'application/json'}
    get = {'method': 'GET', 'url': '/orders?customer=C-1001', 'headers': ['X-Request-Id: r-1']}
    post = {'method': 'POST', 'url': '/orders', 'headers': ['X-Request-Id: r-1', 'Content-Type: application/json'], 'body': b'{"customer":"C-1001"}'}
    match_get = {'method': 'GET', 'urlPath': '/orders'}
    template = ['response-template']
    return {
        'jsonBody': (get, match_get, {'status': 200, 'headers': json_headers, 'jsonBody': json.loads(static)}, None),
        'body': (get, match_get, {'status': 200, 'headers': json_headers, 'body': static}, None),
        'bodyFileName': (get, match_get, {'status': 200, 'headers': json_headers, 'bodyFileName': 'orders.json'}, ('orders.json', static)),
        'templateBody': (get, match_get, {'status': 200, 'headers': json_headers, 'body': plan, 'transformers': template}, None),
        'templateFile': (get, match_get, {'status': 200, 'headers': json_headers, 'bodyFileName': 'orders-template.json', 'transformers': template}, ('orders-template.json', plan)),
        'templateHelper': (post, {'method': 'POST', 'urlPath': '/orders'}, {'status': 200, 'headers': json_headers, 'body': helper, 'transformers': template}, None),
        'jsonBodyGzip': (dict(get, headers=get['headers'] + ['Accept-Encoding: gzip']), match_get, {'status': 200, 'headers': json_headers, 'jsonBody': json.loads(static)}, None),
    }


def admin(port, path, data=None, method=None):
    body = None if data is None else json.dumps(data).encode()
    request = urllib.request.Request(f'http://127.0.0.1:{port}{path}', data=body, method=method, headers={'Content-Type': 'application/json'})
    return urllib.request.urlopen(request, timeout=30).read()


def fetch(port, spec):
    request = urllib.request.Request(f'http://127.0.0.1:{port}{spec["url"]}', data=spec.get('body'), method=spec['method'])
    for header in spec['headers']:
        name, value = header.split(':', 1); request.add_header(name.strip(), value.strip())
    with urllib.request.urlopen(request, timeout=30) as response:
        return response.read()


def cpu_seconds(pid):
    value = subprocess.check_output(['ps', '-o', 'time=', '-p', str(pid)], text=True).strip()
    days = 0
    if '-' in value:
        day, value = value.split('-', 1); days = int(day)
    return days * 86400 + sum(float(v) * 60 ** i for i, v in enumerate(reversed(value.split(':'))))


def rss_mib(pid):
    try:
        return int(subprocess.check_output(['ps', '-o', 'rss=', '-p', str(pid)], text=True).strip()) / 1024
    except Exception:
        return 0


def normalized_event(raw):
    event = json.loads(raw)['requests'][0]
    for key in ('id', 'timing'):
        event.pop(key, None)
    for key in ('loggedDate', 'loggedDateString', 'clientIp'):
        event['request'].pop(key, None)
    for headers in (event['request'].get('headers', {}),):
        for key in ('Host', 'Connection'):
            headers.pop(key, None)
    return event


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary', action='append', required=True, help='name=path of a BreakMock executable or WireMock .jar; repeat to compare')
    p.add_argument('--size', type=int, default=100 * 1024, help='approximate JSON document size in bytes')
    p.add_argument('--rounds', type=int, default=3)
    p.add_argument('--duration', default='5s')
    p.add_argument('--warmup', default='2s')
    p.add_argument('--concurrency', type=int, default=32)
    p.add_argument('--journal', choices=['disabled', 'capped', 'both'], default='both')
    p.add_argument('--workloads', default='', help='comma-separated subset')
    p.add_argument('--journal-check', choices=['strict', 'report'], default='strict',
                   help='strict fails on a differing logged event; report records the difference and continues')
    p.add_argument('--output', required=True)
    a = p.parse_args()
    binaries = dict(item.split('=', 1) for item in a.binary)
    binaries = {name: pathlib.Path(path).resolve() for name, path in binaries.items()}
    driver = ROOT / 'bin/bmbench'
    subprocess.run(['go', 'build', '-o', str(driver), './cmd/bmbench'], cwd=ROOT, check=True)
    selected = workloads(a.size)
    if a.workloads:
        selected = {k: v for k, v in selected.items() if k in a.workloads.split(',')}
    journals = ['disabled', 'capped'] if a.journal == 'both' else [a.journal]
    work = pathlib.Path(tempfile.mkdtemp(prefix='bm-json-'))
    root = work / 'root'; (root / '__files').mkdir(parents=True)
    expected, logged, results, journal_differences = {}, {}, [], set()
    for _, _, _, file in selected.values():
        if file:
            (root / '__files' / file[0]).write_text(file[1])
    names = list(binaries)
    for journal in journals:
        for round_index in range(a.rounds):
            shift = round_index % len(names)
            for name in names[shift:] + names[:shift]:
                port = 18200  # one port, so logged URLs are comparable across servers
                command = [str(binaries[name]), '--port', str(port), '--bind-address', '127.0.0.1', '--root-dir', str(root)]
                if binaries[name].suffix == '.jar':
                    command = ['java', '-jar'] + command + ['--disable-banner']
                command += ['--no-request-journal'] if journal == 'disabled' else ['--max-request-journal-entries', '1000']
                proc = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                try:
                    for _ in range(600):
                        try:
                            admin(port, '/__admin/mappings'); break
                        except Exception:
                            time.sleep(.05)
                    else:
                        raise RuntimeError(f'{name} did not start')
                    for workload, (spec, match, response, _) in selected.items():
                        admin(port, '/__admin/reset', {})
                        admin(port, '/__admin/mappings', {'id': '7d5a1f5e-0000-4000-8000-000000000001', 'request': match, 'response': response})
                        body = fetch(port, spec)
                        key = (workload,)
                        if key not in expected:
                            path = work / f'{workload}.expected'; path.write_bytes(body); expected[key] = path
                        elif body != expected[key].read_bytes():
                            raise RuntimeError(f'{name} {workload}: response differs from the first server')
                        if journal == 'capped':
                            event = normalized_event(admin(port, '/__admin/requests?limit=1'))
                            if workload not in logged:
                                logged[workload] = event
                            elif event != logged[workload]:
                                if a.journal_check == 'strict':
                                    raise RuntimeError(f'{name} {workload}: journal event differs from the first server')
                                differing = sorted(k for k in set(event) | set(logged[workload]) if event.get(k) != logged[workload].get(k))
                                journal_differences.add((name, workload, ','.join(differing)))
                        driver_command = [str(driver), '--url', f'http://127.0.0.1:{port}{spec["url"]}', '--method', spec['method'],
                                          '--concurrency', str(a.concurrency), '--expected-body-file', str(expected[key])]
                        for header in spec['headers']:
                            driver_command += ['--header', header]
                        if spec.get('body'):
                            request_file = work / f'{workload}.request'; request_file.write_bytes(spec['body'])
                            driver_command += ['--body-file', str(request_file)]
                        subprocess.run(driver_command + ['--duration', a.warmup], check=True, stdout=subprocess.DEVNULL)
                        samples, stop = [], threading.Event()
                        sampler = threading.Thread(target=lambda: [samples.append(rss_mib(proc.pid)) for _ in iter(lambda: stop.wait(.1), True)])
                        sampler.start()
                        cpu_start, wall_start = cpu_seconds(proc.pid), time.monotonic()
                        try:
                            run = json.loads(subprocess.check_output(driver_command + ['--duration', a.duration], text=True))
                            cpu = cpu_seconds(proc.pid) - cpu_start; wall = time.monotonic() - wall_start
                        finally:
                            stop.set(); sampler.join()
                        run.update(server=name, journal=journal, workload=workload, round=round_index + 1, response_body_bytes=len(body),
                                   server_cpu_seconds=cpu, server_cpu_us_per_request=cpu * 1e6 / max(run['requests'], 1),
                                   server_cpu_percent_one_core=100 * cpu / wall, peak_sampled_rss_mib=max(samples or [rss_mib(proc.pid)]),
                                   mib_per_second=run['response_bytes'] / run['seconds'] / (1 << 20))
                        results.append(run)
                        print(f"{journal:8} r{round_index + 1} {name:10} {workload:15} {run['rps']:10.0f} rps {run['mib_per_second']:8.1f} MiB/s "
                              f"cpu {run['server_cpu_us_per_request']:8.1f} us/req p99 {run['p99_ms']:.2f} ms", flush=True)
                finally:
                    proc.terminate()
                    try:
                        proc.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        proc.kill(); proc.wait()
    summary = {}
    for r in results:
        s = summary.setdefault(f"{r['journal']}/{r['workload']}", {}).setdefault(r['server'], {'rps': [], 'cpu_us': [], 'p99_ms': [], 'rss': []})
        s['rps'].append(r['rps']); s['cpu_us'].append(r['server_cpu_us_per_request']); s['p99_ms'].append(r['p99_ms']); s['rss'].append(r['peak_sampled_rss_mib'])
    report = {
        'platform': platform.platform(), 'machine': platform.machine(), 'logical_cpus': os.cpu_count(),
        'go_version': subprocess.check_output(['go', 'version'], text=True).strip(),
        'java_version': subprocess.run(['java', '-version'], capture_output=True, text=True).stderr.strip() if any(v.suffix == '.jar' for v in binaries.values()) else None,
        'binaries': {k: {'path': str(v), 'sha256': hashlib.sha256(v.read_bytes()).hexdigest()} for k, v in binaries.items()},
        'driver_sha256': hashlib.sha256(driver.read_bytes()).hexdigest(),
        'settings': {'size': a.size, 'rounds': a.rounds, 'duration': a.duration, 'warmup': a.warmup, 'concurrency': a.concurrency,
                     'response_body_bytes': {k[0]: v.stat().st_size for k, v in expected.items()}},
        'notes': ['Unlimited-rate closed loop with persistent connections; driver and servers share the host.',
                  'Every response is compared byte-for-byte with the first server\'s response for that workload.',
                  'With capped journals, the newest logged event (ids, timings and client details removed) must be identical across servers.',
                  'Server CPU is the ps user+system delta over the measurement window divided by completed requests.'],
        'journal_differences': [{'server': n, 'workload': w, 'differing_event_fields': f} for n, w, f in sorted(journal_differences)],
        'summary': summary, 'results': results}
    pathlib.Path(a.output).parent.mkdir(parents=True, exist_ok=True)
    pathlib.Path(a.output).write_text(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
