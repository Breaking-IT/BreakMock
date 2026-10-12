#!/usr/bin/env python3
"""Same-host comparison; reports server CPU, RSS and throughput, not a universal performance claim."""
from oracle import identify
import argparse,json,subprocess,time,urllib.request,pathlib,platform,hashlib,threading,os
p=argparse.ArgumentParser();p.add_argument('--jar',required=True);p.add_argument('--output',default='testdata/reports/benchmark.json');p.add_argument('--duration',default='5s');p.add_argument('--rounds',type=int,default=3);p.add_argument('--journal-limit',type=int,default=0,help='0 disables journal; positive sets same bounded journal on both');p.add_argument('--rate',type=int,default=0,help='Fixed offered RPS; 0 is unlimited');p.add_argument('--warmup',default='3s');p.add_argument('--servers',default='java,go');p.add_argument('--go-binary');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parent.parent
servers=a.servers.split(',')
if not servers or any(k not in ('java','go') for k in servers):p.error('servers must be java,go')
go_binary=pathlib.Path(a.go_binary).resolve() if a.go_binary else root/'bin/breakmock'
if not a.go_binary:subprocess.run(['go','build','-o',str(go_binary),'./cmd/breakmock'],cwd=root,check=True)
subprocess.run(['go','build','-o','bin/bmbench','./cmd/bmbench'],cwd=root,check=True)
# Capture the actual executables before measurements; checkout HEAD alone is insufficient.
binary_sha256={k:hashlib.sha256(path.read_bytes()).hexdigest() for k,path in [('go',go_binary)] if k in servers}
binary_sha256['driver']=hashlib.sha256((root/'bin/bmbench').read_bytes()).hexdigest()
source_revision=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
source_dirty=bool(subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True).strip())
def req(port,path,data=None):
 b=None if data is None else json.dumps(data).encode()
 return urllib.request.urlopen(urllib.request.Request(f'http://127.0.0.1:{port}'+path,data=b,headers={'Content-Type':'application/json'}),timeout=10).read()
def rss(pid):
 try:return int(subprocess.check_output(['ps','-o','rss=','-p',str(pid)],text=True).strip())/1024
 except:return 0
def cpu_seconds(pid):
 # ps cumulative process CPU time includes all server threads, excluding the driver.
 value=subprocess.check_output(['ps','-o','time=','-p',str(pid)],text=True).strip()
 days=0
 if '-' in value:
  day,value=value.split('-',1);days=int(day)
 parts=[float(x) for x in value.split(':')]
 return days*86400+sum(v*60**i for i,v in enumerate(reversed(parts)))
results=[]
for round in range(a.rounds):
 for kind in servers[round%len(servers):]+servers[:round%len(servers)]:
  port={'java':18101,'go':18102}[kind]
  cmd=['java','-jar',a.jar,'--port',str(port),'--no-request-journal','--disable-banner'] if kind=='java' else [str(go_binary),'--port',str(port),'--no-request-journal','--bind-address','127.0.0.1']
  if a.journal_limit>0:
   cmd.remove('--no-request-journal');cmd+=['--max-request-journal-entries',str(a.journal_limit)]
  proc=subprocess.Popen(cmd,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,cwd=root)
  try:
   t=time.monotonic()
   for _ in range(100):
    if proc.poll() is not None:raise RuntimeError(f'{kind} exited')
    try:
     identity=identify(f'http://127.0.0.1:{port}',kind);version=identity['implementation_version'] or identity['version'];break
    except:time.sleep(.1)
   else:raise RuntimeError('startup timeout')
   if kind=='java' and version!='4.0.0-beta.40':raise RuntimeError('incorrect reference '+version)
   startup=time.monotonic()-t;idle=rss(proc.pid)
   for workload,response in [('static',{'status':200,'body':'Hello, benchmark!','headers':{'Content-Type':'text/plain'}}),('template',{'status':200,'body':"Hello {{request.query.name}}: {{math 2 '+' 3}}",'transformers':['response-template'],'headers':{'Content-Type':'text/plain'}})]:
    req(port,'/__admin/reset',{});req(port,'/__admin/mappings',{'request':{'method':'GET','urlPath':'/bench'},'response':response})
    expected=b'Hello, benchmark!' if workload=='static' else b'Hello Ada: 5'
    actual=req(port,'/bench?name=Ada')
    if actual!=expected:raise RuntimeError(f'{kind} {workload} incorrect response {actual!r}')
    command=[str(root/'bin/bmbench'),'--url',f'http://127.0.0.1:{port}/bench?name=Ada','--concurrency','32','--rate',str(a.rate),'--expected-body',expected.decode()]
    subprocess.run(command+['--duration',a.warmup],check=True,stdout=subprocess.DEVNULL)
    sample=[];stop=threading.Event()
    def sample_rss():
     while not stop.wait(.1):sample.append(rss(proc.pid))
    sampler=threading.Thread(target=sample_rss);sampler.start()
    cpu_start=cpu_seconds(proc.pid);wall_start=time.monotonic()
    try:
     run=json.loads(subprocess.check_output(command+['--duration',a.duration],text=True))
     cpu_end=cpu_seconds(proc.pid);wall_end=time.monotonic()
    finally:stop.set();sampler.join()
    cpu=cpu_end-cpu_start;wall=wall_end-wall_start
    run.update(server_cpu_seconds=cpu,cpu_measurement_wall_seconds=wall,server_cpu_percent_one_core=100*cpu/wall,server_cpu_us_per_request=cpu*1e6/run['requests'])
    if a.rate and abs(run['rps']/a.rate-1)>.02:raise RuntimeError(f'achieved RPS outside 2% of target: {run}')
    run.update(server=kind,round=round+1,workload=workload,version=version,idle_rss_mib=idle,peak_sampled_rss_mib=max(sample or [rss(proc.pid)]),startup_seconds=startup)
    results.append(run);print(json.dumps(run),flush=True)
  finally:
   proc.terminate()
   try:proc.wait(timeout=10)
   except subprocess.TimeoutExpired:proc.kill();proc.wait()
report={'platform':platform.platform(),'machine':platform.machine(),'logical_cpus':os.cpu_count(),'server_revision':source_revision,'source_dirty_at_start':source_dirty,'binary_sha256':binary_sha256,'java_version':subprocess.run(['java','-version'],capture_output=True,text=True).stderr,'servers':servers,'go_version':subprocess.check_output(['go','version'],text=True).strip(),'reference_jar_sha256':hashlib.sha256(pathlib.Path(a.jar).read_bytes()).hexdigest(),'notes':['Same host and workload driver; CPU shared with client and any other host processes.',('Request journals disabled on both servers.' if a.journal_limit==0 else f'Request journals bounded to {a.journal_limit} entries on both servers.'),f'{a.warmup} warmup per workload; sequential alternating server order; 32 clients.',f'Target RPS {a.rate} (0 means unlimited); fixed-rate arrivals dispatched in 1ms batches; drops/errors fail the run.','CPU is cumulative process user+system time delta from ps, all server threads, excluding load generator; 100% means one fully occupied core. CPU window includes driver launch and report serialization.','Fixed-rate latency includes scheduling/queue delay from each intended arrival; up to one second of arrivals can queue. Achieved RPS must be within 2% of target.','RSS sampled every 100ms; not an allocation or production capacity guarantee.'],'results':results}
pathlib.Path(a.output).parent.mkdir(parents=True,exist_ok=True);pathlib.Path(a.output).write_text(json.dumps(report,indent=2))
