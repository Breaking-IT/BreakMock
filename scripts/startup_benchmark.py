#!/usr/bin/env python3
"""Balanced cold-process startup to a verified templated HTTP response (warm OS cache)."""
import argparse,json,pathlib,platform,subprocess,tempfile,time,urllib.request
p=argparse.ArgumentParser();p.add_argument('--jar',required=True);p.add_argument('--go-binary',required=True);p.add_argument('--rounds',type=int,default=15);p.add_argument('--output',default='testdata/reports/startup.json');a=p.parse_args()
servers=['java','go'];results=[]
with tempfile.TemporaryDirectory(prefix='breakmock-startup-')as directory:
 root=pathlib.Path(directory);(root/'mappings').mkdir();(root/'mappings/ready.json').write_text(json.dumps({'id':'12345678-1234-1234-1234-123456789abc','request':{'method':'GET','url':'/ready'},'response':{'body':"Ready {{math 2 '+' 3}}",'transformers':['response-template']}}))
 for round in range(a.rounds):
  for kind in servers[round%len(servers):]+servers[:round%len(servers)]:
   port={'java':18901,'go':18902}[kind]
   command=['java','-jar',str(pathlib.Path(a.jar).resolve()),'--disable-banner']if kind=='java'else[str(pathlib.Path(a.go_binary).resolve())]
   command+=['--port',str(port),'--root-dir',directory,'--no-request-journal']
   start=time.perf_counter();proc=subprocess.Popen(command,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   try:
    while time.perf_counter()-start<15:
     if proc.poll()is not None:raise RuntimeError(f'{kind} exited during startup')
     try:
      with urllib.request.urlopen(f'http://127.0.0.1:{port}/ready',timeout=.1)as r:
       if r.status!=200 or r.read()!=b'Ready 5':raise RuntimeError('incorrect readiness response')
      elapsed=time.perf_counter()-start;break
     except (ConnectionError,OSError):time.sleep(.002)
    else:raise RuntimeError(f'{kind} startup timeout')
    rss=int(subprocess.check_output(['ps','-o','rss=','-p',str(proc.pid)],text=True).strip())/1024
    row={'server':kind,'round':round+1,'startup_seconds':elapsed,'ready_rss_mib':rss};results.append(row);print(json.dumps(row),flush=True)
   finally:
    proc.terminate()
    try:proc.wait(timeout=10)
    except subprocess.TimeoutExpired:proc.kill();proc.wait()
report={'platform':platform.platform(),'notes':['Fresh process each sample; warm OS file cache, not a cold machine.','Balanced server ordering. Readiness requires disk mapping load and correct templated response.','2ms poll interval; measurements include process launch and HTTP probe overhead.','No concurrent benchmark workloads or builds intended.'],'results':results}
path=pathlib.Path(a.output);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2)+'\n')
