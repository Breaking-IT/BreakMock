#!/usr/bin/env python3
"""Same-host comparison; reports RSS and throughput, not a universal performance claim."""
import argparse,json,subprocess,time,urllib.request,pathlib,platform,hashlib,threading
p=argparse.ArgumentParser();p.add_argument('--jar',required=True);p.add_argument('--output',default='testdata/reports/benchmark.json');p.add_argument('--duration',default='5s');p.add_argument('--rounds',type=int,default=3);p.add_argument('--journal-limit',type=int,default=0,help='0 disables journal; positive sets same bounded journal on both');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parent.parent
subprocess.run(['go','build','-o','bin/breakmock','./cmd/breakmock'],cwd=root,check=True);subprocess.run(['go','build','-o','bin/bmbench','./cmd/bmbench'],cwd=root,check=True)
def req(port,path,data=None):
 b=None if data is None else json.dumps(data).encode()
 return urllib.request.urlopen(urllib.request.Request(f'http://127.0.0.1:{port}'+path,data=b,headers={'Content-Type':'application/json'}),timeout=10).read()
def rss(pid):
 try:return int(subprocess.check_output(['ps','-o','rss=','-p',str(pid)],text=True).strip())/1024
 except:return 0
results=[]
for round in range(a.rounds):
 for kind in (['java','go'] if round%2==0 else ['go','java']):
  port=18101 if kind=='java' else 18102
  cmd=['java','-jar',a.jar,'--port',str(port),'--no-request-journal','--disable-banner'] if kind=='java' else [str(root/'bin/breakmock'),'--port',str(port),'--no-request-journal','--bind-address','127.0.0.1']
  if a.journal_limit>0:
   cmd.remove('--no-request-journal');cmd+=['--max-request-journal-entries',str(a.journal_limit)]
  proc=subprocess.Popen(cmd,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,cwd=root)
  try:
   t=time.monotonic()
   for _ in range(100):
    if proc.poll() is not None:raise RuntimeError(f'{kind} exited')
    try:
     version=json.loads(req(port,'/__admin/version'))['version'];break
    except:time.sleep(.1)
   else:raise RuntimeError('startup timeout')
   if kind=='java' and version!='3.13.2':raise RuntimeError('incorrect reference '+version)
   if kind=='go' and not version.startswith('BreakMock'):raise RuntimeError('incorrect candidate '+version)
   startup=time.monotonic()-t;idle=rss(proc.pid)
   for workload,response in [('static',{'status':200,'body':'Hello, benchmark!','headers':{'Content-Type':'text/plain'}}),('template',{'status':200,'body':"Hello {{request.query.name}}: {{math 2 '+' 3}}",'transformers':['response-template'],'headers':{'Content-Type':'text/plain'}})]:
    req(port,'/__admin/reset',{});req(port,'/__admin/mappings',{'request':{'method':'GET','urlPath':'/bench'},'response':response})
    expected=b'Hello, benchmark!' if workload=='static' else b'Hello Ada: 5'
    actual=req(port,'/bench?name=Ada')
    if actual!=expected:raise RuntimeError(f'{kind} {workload} incorrect response {actual!r}')
    command=[str(root/'bin/bmbench'),'--url',f'http://127.0.0.1:{port}/bench?name=Ada','--concurrency','32']
    subprocess.run(command+['--duration','3s'],check=True,stdout=subprocess.DEVNULL)
    sample=[];stop=threading.Event()
    def sample_rss():
     while not stop.wait(.1):sample.append(rss(proc.pid))
    sampler=threading.Thread(target=sample_rss);sampler.start()
    try:run=json.loads(subprocess.check_output(command+['--duration',a.duration],text=True))
    finally:stop.set();sampler.join()
    run.update(server=kind,round=round+1,workload=workload,version=version,idle_rss_mib=idle,peak_sampled_rss_mib=max(sample or [rss(proc.pid)]),startup_seconds=startup)
    results.append(run);print(json.dumps(run),flush=True)
  finally:
   proc.terminate()
   try:proc.wait(timeout=10)
   except subprocess.TimeoutExpired:proc.kill();proc.wait()
report={'platform':platform.platform(),'machine':platform.machine(),'java_version':subprocess.run(['java','-version'],capture_output=True,text=True).stderr,'go_version':subprocess.check_output(['go','version'],text=True).strip(),'reference_jar_sha256':hashlib.sha256(pathlib.Path(a.jar).read_bytes()).hexdigest(),'notes':['Same host and workload driver; CPU shared with client and any other host processes.',('Request journals disabled on both servers.' if a.journal_limit==0 else f'Request journals bounded to {a.journal_limit} entries on both servers.'),'Three-second warmup per workload; sequential alternating server order; 32 clients.','RSS sampled every 100ms; not an allocation or production capacity guarantee.'],'results':results}
pathlib.Path(a.output).parent.mkdir(parents=True,exist_ok=True);pathlib.Path(a.output).write_text(json.dumps(report,indent=2))
