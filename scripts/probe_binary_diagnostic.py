#!/usr/bin/env python3
"""Compare unmodified Java WireMock binary diagnostics with Java itself.

This is a diagnostic probe, not a replacement for any compatibility assertion.
It records exact status/body equality and exits nonzero when Java responses differ.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

from oracle import identify

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--jar', required=True)
p.add_argument('--output', required=True)
p.add_argument('--repeats', type=int, default=8)
p.add_argument('--base-port', type=int, default=18711)
a = p.parse_args()
if a.repeats < 2:
    p.error('at least two requests are required')
root = pathlib.Path(__file__).resolve().parent.parent
jar = pathlib.Path(a.jar).resolve()
pin = json.loads((root / 'testdata/reference.json').read_text())
digest = hashlib.sha256(jar.read_bytes()).hexdigest()
if digest != pin['jar_sha256']:
    raise SystemExit('Reference JAR checksum mismatch')

def call(base, path, body):
    request = urllib.request.Request(base + path, data=body, headers={
        'Content-Type': 'application/json', 'Accept-Encoding': 'identity',
    })
    try:
        response = urllib.request.urlopen(request, timeout=10)
    except urllib.error.HTTPError as e:
        response = e
    with response:
        raw = response.read()
        return {'status': response.status, 'body': raw.decode('utf-8'),
                'body_sha256': hashlib.sha256(raw).hexdigest()}

mapping = {'id': '11111111-1111-4111-8111-000000000001',
           'request': {'bodyPatterns': [{'binaryEqualTo': 'YWJj'}]},
           'response': {'status': 200}}
processes = []
results = [[], []]
with tempfile.TemporaryDirectory(prefix='breakmock-java-self-check-') as directory:
    try:
        bases = []
        for i in range(2):
            files = pathlib.Path(directory) / str(i)
            files.mkdir()
            port = a.base_port + i
            base = f'http://127.0.0.1:{port}'
            proc = subprocess.Popen(['java', '-jar', str(jar), '--port', str(port),
                                     '--root-dir', str(files), '--disable-banner'],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            processes.append(proc)
            for _ in range(100):
                if proc.poll() is not None:
                    raise RuntimeError('Reference server exited')
                try:
                    identify(base, 'java', timeout=1)
                    break
                except (OSError, ValueError):
                    time.sleep(.1)
            else:
                raise RuntimeError('Reference startup timeout')
            assert call(base, '/__admin/mappings', json.dumps(mapping).encode())['status'] == 201
            bases.append(base)
        for _ in range(a.repeats):
            for i, base in enumerate(bases):
                result = call(base, '/actual', b'actual')
                assert result['status'] == 404
                results[i].append(result)
    finally:
        for proc in processes:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()

across = [left == right for left, right in zip(*results)]
adjacent = [[left == right for left, right in zip(rows, rows[1:])] for rows in results]
report = {'reference': '4.0.0-beta.40', 'jar_sha256': digest,
          'java_version': subprocess.run(['java', '-version'], capture_output=True, text=True).stderr,
          'comparison': 'Exact status and body bytes; no substitutions or exclusions',
          'mapping': mapping, 'request': {'method': 'POST', 'path': '/actual', 'body': 'actual'},
          'responses': results, 'cross_instance_equal': across,
          'adjacent_same_instance_equal': adjacent}
output = pathlib.Path(a.output)
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(report, indent=2) + '\n')
print(f'Java-vs-Java: {sum(across)}/{len(across)} exact pairs equal')
print('Repeated same-instance equality:', [sum(row) for row in adjacent])
raise SystemExit(not (all(across) and all(all(row) for row in adjacent)))
