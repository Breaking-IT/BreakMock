#!/usr/bin/env python3
"""Profile the production Go handler with a temporary loopback-only pprof wrapper.

Instrumentation changes throughput; use benchmark.py for performance comparisons.
"""
import argparse
import hashlib
import json
import pathlib
import subprocess
import tempfile
import time
import urllib.request

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source-root', default=str(pathlib.Path(__file__).resolve().parent.parent))
p.add_argument('--output-dir', required=True)
a = p.parse_args()
root = pathlib.Path(a.source_root).resolve()
out = pathlib.Path(a.output_dir).resolve()
out.mkdir(parents=True, exist_ok=True)
source = '''package main
import (
 "net"
 "net/http"
 _ "net/http/pprof"
 _ "expvar"
 "runtime"
 bm "github.com/Breaking-IT/BreakMock"
)
func main() {
 runtime.SetMutexProfileFraction(5)
 s,e:=bm.NewServer(bm.Options{MaxJournalEntries:1000}); if e!=nil {panic(e)}
 _,e=s.Store.AddMapping(bm.Mapping{"request":map[string]any{"method":"GET","urlPath":"/bench"},"response":map[string]any{"body":"Hello, benchmark!","headers":map[string]any{"Content-Type":"text/plain"}}}); if e!=nil {panic(e)}
 go func(){panic(http.ListenAndServe("127.0.0.1:18523",nil))}()
 l,e:=net.Listen("tcp","127.0.0.1:18522"); if e!=nil {panic(e)}
 server:=http.Server{Handler:s,ConnContext:bm.HeaderOrderConnContext}
 panic(server.Serve(bm.WrapWireListener(l,true)))
}
'''
def get(path):
    with urllib.request.urlopen('http://127.0.0.1:18523/' + path, timeout=20) as response:
        return response.read()

with tempfile.TemporaryDirectory(prefix='breakmock-go-profile-') as directory:
    temp = pathlib.Path(directory)
    (temp / 'main.go').write_text(source)
    (out / 'wrapper.go.txt').write_text(source)
    binary = temp / 'server'
    driver = temp / 'bmbench'
    subprocess.run(['go', 'build', '-o', str(binary), str(temp / 'main.go')], cwd=root, check=True)
    subprocess.run(['go', 'build', '-o', str(driver), './cmd/bmbench'], cwd=root, check=True)
    proc = subprocess.Popen([str(binary)], cwd=directory, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    load = None
    try:
        for _ in range(100):
            if proc.poll() is not None:
                raise RuntimeError('Profile server exited')
            try:
                get('debug/vars')
                break
            except OSError:
                time.sleep(.1)
        else:
            raise RuntimeError('Profile server startup timeout')
        cmd = [str(driver), '--url', 'http://127.0.0.1:18522/bench?name=Ada', '--concurrency', '32', '--expected-body', 'Hello, benchmark!']
        subprocess.run(cmd + ['--duration', '3s'], check=True, stdout=subprocess.DEVNULL)
        before = json.loads(get('debug/vars'))['memstats']
        load = subprocess.Popen(cmd + ['--duration', '15s'], stdout=subprocess.PIPE, text=True)
        (out / 'cpu.pprof').write_bytes(get('debug/pprof/profile?seconds=10'))
        stdout, _ = load.communicate(timeout=30)
        if load.returncode:
            raise RuntimeError('Profile load failed')
        run = json.loads(stdout)
        after = json.loads(get('debug/vars'))['memstats']
        for name in ('allocs', 'mutex'):
            (out / (name + '.pprof')).write_bytes(get('debug/pprof/' + name))
        report = {
            'source_root': str(root),
            'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
            'load': run,
            'allocated_bytes_per_request': (after['TotalAlloc'] - before['TotalAlloc']) / run['requests'],
            'allocations_per_request': (after['Mallocs'] - before['Mallocs']) / run['requests'],
            'gc_cycles': after['NumGC'] - before['NumGC'],
            'notes': ['Profiled throughput is not a benchmark result.', 'Allocation deltas cover the 15s load plus profiling/probe overhead; 3s warmup excluded.', 'pprof allocation totals include warmup; mutex delay is cumulative across waiting goroutines, not CPU time.'],
        }
        (out / 'summary.json').write_text(json.dumps(report, indent=2) + '\n')
    finally:
        if load is not None and load.poll() is None:
            load.terminate()
            load.wait(timeout=10)
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
    for name, extra in [('cpu', []), ('allocs', ['-sample_index=alloc_space']), ('mutex', [])]:
        with (out / (name + '-top.txt')).open('w') as f:
            subprocess.run(['go', 'tool', 'pprof', '-top', '-nodecount=20', *extra, str(binary), str(out / (name + '.pprof'))], stdout=f, check=True)
    print(json.dumps(report, indent=2))
