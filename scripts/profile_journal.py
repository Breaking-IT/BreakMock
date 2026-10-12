#!/usr/bin/env python3
"""macOS wall-stack sampling of the bounded-journal workload; not a benchmark."""
import argparse,hashlib,json,pathlib,re,subprocess,time,urllib.request
p=argparse.ArgumentParser();p.add_argument('--binary',required=True);p.add_argument('--output-dir',required=True);p.add_argument('--label',required=True);a=p.parse_args()
root=pathlib.Path(__file__).resolve().parent.parent;out=pathlib.Path(a.output_dir).resolve();out.mkdir(parents=True,exist_ok=True);binary=pathlib.Path(a.binary).resolve()
proc=subprocess.Popen([str(binary),'--port','18521','--max-request-journal-entries','1000','--bind-address','127.0.0.1'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
try:
 for _ in range(100):
  if proc.poll() is not None:raise RuntimeError('Server exited')
  try:urllib.request.urlopen('http://127.0.0.1:18521/__admin/mappings').read();break
  except OSError:time.sleep(.1)
 else:raise RuntimeError('Startup timeout')
 mapping={'request':{'urlPath':'/bench'},'response':{'body':'Hello, benchmark!','headers':{'Content-Type':'text/plain'}}}
 urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:18521/__admin/mappings',data=json.dumps(mapping).encode(),headers={'Content-Type':'application/json'})).read()
 cmd=[str(root/'bin/bmbench'),'--url','http://127.0.0.1:18521/bench?name=Ada','--concurrency','32','--expected-body','Hello, benchmark!']
 subprocess.run(cmd+['--duration','3s'],check=True,stdout=subprocess.DEVNULL)
 with (out/(a.label+'-load.json')).open('w')as f:
  load=subprocess.Popen(cmd+['--duration','15s'],stdout=f)
  try:subprocess.run(['/usr/bin/sample',str(proc.pid),'10','1','-file',str(out/(a.label+'-sample.txt'))],check=True)
  finally:
   if load.wait()!=0:raise RuntimeError('Load failed')
finally:proc.terminate();proc.wait()
s=(out/(a.label+'-sample.txt')).read_text();worker=sum(map(int,re.findall(r'^\s+(\d+) Thread_\d+: tokio-rt-worker',s,re.M)));collapsed=s.split('Sort by top of stack, same collapsed',1)[1];m=re.search(r'^\s+__psynch_mutexwait.*?\s(\d+)\s*$',collapsed,re.M);wait=int(m[1])if m else 0
report={'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'worker_thread_samples':worker,'mutex_wait_top_samples':wait,'mutex_wait_fraction_of_worker_samples':wait/worker if worker else None,'notes':['Wall-stack samples include blocked threads, not CPU-time percentages.','10s sample window requested at 1ms interval; sampler overhead changes throughput.','Separate unprofiled benchmark runs determine performance.','Mutex leaf counts cover process; inspected call paths originate in request worker state locking.']}
(out/(a.label+'-summary.json')).write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report))
