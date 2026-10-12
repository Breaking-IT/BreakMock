#!/usr/bin/env python3
"""Exercise the real BreakMock executable, shared API, and embedded TCP proxies.

Uses only the Python standard library. Builds no substitute server: --binary is
started with the production --config path. Every listener is bound to loopback.
"""
import argparse
import contextlib
import json
import pathlib
import socket
import socketserver
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


class Echo(socketserver.BaseRequestHandler):
    def handle(self):
        while True:
            try:
                data = self.request.recv(65536)
                if not data:
                    return
                self.request.sendall(data)
            except (ConnectionError, OSError):
                return


class EchoServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def call(base, path, method='GET', body=None):
    request = urllib.request.Request(
        base + path, method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers={'Content-Type': 'application/json'})
    try:
        response = urllib.request.urlopen(request, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, response.read()


def api(base, path, method='GET', body=None, status=200):
    actual, data = call(base, '/__admin/toxiproxy' + path, method, body)
    assert actual == status, (path, actual, status, data)
    return json.loads(data) if data else None


def echo_at(proxy_port, payload=b'BreakMock TCP bytes\x00\xff\r\n'):
    with socket.create_connection(('127.0.0.1', proxy_port), timeout=3) as sock:
        sock.settimeout(3)
        start = time.monotonic()
        sock.sendall(payload)
        output = bytearray()
        while len(output) < len(payload):
            part = sock.recv(len(payload) - len(output))
            assert part, 'proxy closed before returning all bytes'
            output.extend(part)
        elapsed = time.monotonic() - start
        assert bytes(output) == payload, (bytes(output), payload)
        return elapsed


def assert_released(proxy_port):
    with socket.socket() as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(('127.0.0.1', proxy_port))


@contextlib.contextmanager
def running(binary, directory, config):
    config_path = pathlib.Path(directory) / 'breakmock.json'
    config_path.write_text(json.dumps(config))
    with open(pathlib.Path(directory) / 'server.log', 'w+') as log:
        process = subprocess.Popen([str(binary), '--config', str(config_path)], stdout=log, stderr=log)
        base = 'http://127.0.0.1:' + str(config['server']['port'])
        try:
            deadline = time.monotonic() + 10
            while True:
                if process.poll() is not None:
                    log.seek(0)
                    raise AssertionError('server exited during startup:\n' + log.read())
                try:
                    if call(base, '/__admin/mappings')[0] == 200:
                        break
                except (OSError, urllib.error.URLError):
                    pass
                assert time.monotonic() < deadline, 'server did not become ready'
                time.sleep(.025)
            yield process, base
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=6)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=pathlib.Path, required=True)
    parser.add_argument('--reference', help='Optional running Java URL for exact disabled-route and enabled WireMock smoke checks')
    args = parser.parse_args()
    binary = args.binary.resolve()
    results = []

    def passed(name):
        results.append(name)
        print('PASS ' + name, flush=True)

    with EchoServer(('127.0.0.1', 0), Echo) as echo:
        threading.Thread(target=echo.serve_forever, daemon=True).start()
        upstream = '127.0.0.1:' + str(echo.server_address[1])
        with tempfile.TemporaryDirectory(prefix='breakmock-toxi-default-') as directory:
            config = {'server': {'port': port(), 'bindAddress': '127.0.0.1', 'rootDir': directory}}
            with running(binary, directory, config) as (_, base):
                assert api(base, '/proxies') == {}, 'default must create no proxy listeners'
                call(base, '/__admin/mappings', 'POST', {'request': {'method': 'GET', 'url': '/direct'},
                     'response': {'status': 200, 'body': 'ordinary mock response'}})
                assert call(base, '/direct') == (200, b'ordinary mock response')
                assert api(base, '/proxies') == {}, 'ordinary requests must not create proxies'
                passed('default management API available; no proxies or toxics active')

        with tempfile.TemporaryDirectory(prefix='breakmock-toxi-disabled-') as directory:
            config = {'server': {'port': port(), 'bindAddress': '127.0.0.1', 'rootDir': directory}, 'toxiproxy': {'enabled': False}}
            with running(binary, directory, config) as (_, base):
                legacy = call(base, '/__admin/toxiproxy/proxies')
                assert legacy[0] == 404, legacy
                if args.reference:
                    assert legacy == call(args.reference, '/__admin/toxiproxy/proxies'), 'disabled route differs from Java bytes'
                passed('explicitly disabled extension preserves legacy admin route')

        with tempfile.TemporaryDirectory(prefix='breakmock-toxi-enabled-') as directory:
            http_port, echo_port, http_proxy_port = port(), port(), port()
            config = {
                'server': {'port': http_port, 'bindAddress': '127.0.0.1', 'rootDir': directory},
                'toxiproxy': {'enabled': True, 'proxies': [{
                    'name': 'echo', 'listen': '127.0.0.1:' + str(echo_port),
                    'upstream': upstream, 'enabled': True,
                    'toxics': [{'name': 'configured_latency', 'type': 'latency', 'stream': 'downstream',
                                'toxicity': 1, 'attributes': {'latency': 80, 'jitter': 0}}]}]}}
            with running(binary, directory, config) as (process, base):
                proxy = api(base, '/proxies/echo')
                assert proxy['enabled'] is True and proxy['upstream'] == upstream, proxy
                assert any(t['name'] == 'configured_latency' for t in proxy['toxics']), proxy
                elapsed = echo_at(echo_port)
                assert elapsed >= .065, elapsed
                passed('shared config preloads proxy and active latency toxic')

                api(base, '/proxies/echo/toxics/configured_latency', 'POST',
                    {'attributes': {'latency': 150, 'jitter': 0}})
                elapsed = echo_at(echo_port)
                assert elapsed >= .13, elapsed
                api(base, '/proxies/echo/toxics/configured_latency', 'DELETE', status=204)
                assert api(base, '/proxies/echo/toxics') == []
                echo_at(echo_port)
                passed('native toxic update/delete changes TCP behavior and preserves bytes')

                api(base, '/proxies/echo/toxics', 'POST', {
                    'name': 'cutoff', 'type': 'timeout', 'stream': 'downstream',
                    'toxicity': 1, 'attributes': {'timeout': 120}})
                with socket.create_connection(('127.0.0.1', echo_port), timeout=3) as sock:
                    sock.settimeout(3)
                    start = time.monotonic()
                    sock.sendall(b'this response must be suppressed')
                    try:
                        received = sock.recv(4096)
                    except ConnectionResetError:
                        received = b''
                    elapsed = time.monotonic() - start
                    assert received == b'', received
                    assert .08 <= elapsed < 2.5, elapsed
                passed('native timeout toxic suppresses response and closes real connection')
                api(base, '/proxies/echo/toxics/cutoff', 'DELETE', status=204)

                mapping = {'request': {'method': 'GET', 'url': '/integration-exact'},
                           'response': {'status': 201, 'body': 'exact mock bytes\n',
                                        'headers': {'Content-Type': 'text/plain'}}}
                assert call(base, '/__admin/mappings', 'POST', mapping)[0] == 201
                api(base, '/proxies', 'POST', {'name': 'self',
                    'listen': '127.0.0.1:' + str(http_proxy_port),
                    'upstream': '127.0.0.1:' + str(http_port), 'enabled': True}, status=201)
                direct = call(base, '/integration-exact')
                assert direct == (201, b'exact mock bytes\n'), direct
                assert call('http://127.0.0.1:' + str(http_proxy_port), '/integration-exact') == direct
                assert process.poll() is None
                passed('one executable proxies HTTP to its own WireMock-compatible mappings')

                if args.reference:
                    parity_mapping = {
                        'id': 'beafbabe-1234-4000-8000-000000000001',
                        'request': {'method': 'GET', 'urlPath': '/toxi-byte-parity'},
                        'response': {'status': 202, 'body': 'Hello {{request.query.name}}!\n',
                                     'transformers': ['response-template'],
                                     'headers': {'Content-Type': 'text/plain'}}}
                    path = '/__admin/mappings/' + parity_mapping['id']
                    try:
                        for origin in (base, args.reference):
                            call(origin, path, 'DELETE')
                        actual = call(base, '/__admin/mappings', 'POST', parity_mapping)
                        expected = call(args.reference, '/__admin/mappings', 'POST', parity_mapping)
                        assert actual == expected, ('enabled admin create bytes', actual, expected)
                        assert call(base, path) == call(args.reference, path), 'enabled admin get bytes'
                        target = '/toxi-byte-parity?name=Ada'
                        assert call(base, target) == call(args.reference, target) == (202, b'Hello Ada!\n')
                        passed('enabled extension preserves exact Java admin JSON and template response bytes')
                    finally:
                        for origin in (base, args.reference):
                            call(origin, path, 'DELETE')

                api(base, '/proxies/echo', 'POST', {'enabled': False})
                assert api(base, '/proxies/echo')['enabled'] is False
                try:
                    with socket.create_connection(('127.0.0.1', echo_port), timeout=.5):
                        raise AssertionError('disabled proxy still accepts connections')
                except (ConnectionRefusedError, TimeoutError):
                    pass
                api(base, '/proxies/echo', 'POST', {'enabled': True})
                echo_at(echo_port)
                passed('native proxy update disables and re-enables listener')

                api(base, '/proxies/echo/toxics', 'POST', {'name': 'reset_me', 'type': 'latency',
                    'stream': 'downstream', 'toxicity': 1, 'attributes': {'latency': 80, 'jitter': 0}})
                api(base, '/proxies/echo', 'POST', {'enabled': False})
                api(base, '/reset', 'POST', status=204)
                assert api(base, '/proxies/echo')['enabled'] is True
                assert api(base, '/proxies/echo/toxics') == []
                echo_at(echo_port)
                assert call(base, '/integration-exact') == direct
                passed('Toxiproxy reset leaves WireMock mappings intact')

                assert call(base, '/__admin/reset', 'POST')[0] == 200
                assert call(base, '/integration-exact')[0] == 404
                assert set(api(base, '/proxies')) == {'echo', 'self'}
                echo_at(echo_port)
                passed('WireMock reset leaves Toxiproxy listeners intact')

                api(base, '/proxies/self', 'DELETE', status=204)
                assert_released(http_proxy_port)
                assert api(base, '/proxies/self', status=404)['error']
                passed('native proxy delete releases its TCP listener')

                # Keep a live connection open to ensure shutdown also closes active links.
                with socket.create_connection(('127.0.0.1', echo_port), timeout=3) as active:
                    active.settimeout(3)
                    assert call(base, '/__admin/shutdown', 'POST')[0] == 200
                    assert process.wait(timeout=6) == 0
                    try:
                        assert active.recv(1) == b''
                    except ConnectionResetError:
                        pass
                assert_released(echo_port)
                assert_released(http_port)
                passed('shared shutdown exits cleanly, closes active links, releases listeners')
        echo.shutdown()
    print(f'{len(results)}/{len(results)} integration checks passed')


if __name__ == '__main__':
    main()
