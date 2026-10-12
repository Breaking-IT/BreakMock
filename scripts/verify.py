#!/usr/bin/env python3
"""Build and run isolated differential suites against pinned Java WireMock."""
from oracle import identify
import argparse,hashlib,json,pathlib,subprocess,tempfile,time,urllib.request,shutil,sys,os
p=argparse.ArgumentParser();p.add_argument('--jar',required=True);p.add_argument('--candidate-language',choices=['go'],default='go');p.add_argument('--output-dir');p.add_argument('--suite',action='append');p.add_argument('--candidate-arg',action='append',default=[],help='Extra candidate startup argument, repeatable; use --candidate-arg=--flag for flags');a=p.parse_args();root=pathlib.Path(__file__).resolve().parent.parent
pin=json.loads((root/'testdata/reference.json').read_text())
if hashlib.sha256(pathlib.Path(a.jar).read_bytes()).hexdigest()!=pin['jar_sha256']:raise SystemExit('Reference JAR does not match testdata/reference.json SHA-256')
subprocess.run(['go','build','-o','bin/breakmock','./cmd/breakmock'],cwd=root,check=True)
binary=root/'bin/breakmock'
output=pathlib.Path(a.output_dir or 'testdata/reports');output.mkdir(parents=True,exist_ok=True)
reference='http://127.0.0.1:18111';candidate='http://127.0.0.1:18112';results=[]
suites=['fixtures_differential.py','differential.py','matcher_differential.py','template_differential.py','admin_differential.py','runtime_differential.py','gzip_differential.py','diagnostics_differential.py','matcher_distance_differential.py','webhook_differential.py','fault_differential.py','entity_differential.py','messaging_differential.py','streaming_differential.py','status_differential.py','streaming_journal_differential.py','message_wait_differential.py','journal_concurrency_differential.py']
for suite,disabled_journal in [(suite,False) for suite in suites]+[('message_wait_differential.py',True)]:
 suite_name=suite+(':disabled-journal' if disabled_journal else '')
 if a.suite and suite not in a.suite and suite_name not in a.suite:continue
 processes=[]
 with tempfile.TemporaryDirectory(prefix='breakmock-verify-')as tmp:
  try:
   for kind,port in ([('java',18111),('go',18112)]+([('java',18113),('go',18114)] if suite=='streaming_differential.py' else [])):
    files=pathlib.Path(tmp)/(kind+str(port))
    if suite=='fixtures_differential.py':shutil.copytree(root/'testdata/upstream/remoteloader',files)
    else:files.mkdir()
    cmd=['java','-jar',str(pathlib.Path(a.jar).resolve()),'--port',str(port),'--root-dir',str(files),'--disable-banner'] if kind=='java' else [str(binary),'--port',str(port),'--root-dir',str(files),'--bind-address','127.0.0.1']
    if kind!='java':cmd+=a.candidate_arg
    if disabled_journal:cmd+=['--no-request-journal']
    if suite=='journal_concurrency_differential.py':cmd+=['--max-request-journal-entries','64']
    if port in (18113,18114):cmd+=['--websocket-idle-timeout','200','--websocket-max-text-message-size','8','--websocket-max-binary-message-size','12']
    proc=subprocess.Popen(cmd,cwd=root,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);processes.append(proc)
    for _ in range(100):
     if proc.poll()is not None:raise RuntimeError(kind+' failed to start (port occupied?)')
     try:
      identify(f'http://127.0.0.1:{port}',kind,timeout=1)
      break
     except (OSError,ValueError):time.sleep(.1)
    else:raise RuntimeError(kind+' startup timeout')
   cmd=[sys.executable,str(root/'scripts'/suite),'--reference',reference,'--candidate',candidate]
   reports={'differential.py':'compatibility','matcher_differential.py':'matcher','admin_differential.py':'admin','runtime_differential.py':'runtime','template_differential.py':'template','gzip_differential.py':'gzip','diagnostics_differential.py':'diagnostics','matcher_distance_differential.py':'matcher-distance','webhook_differential.py':'webhook','fault_differential.py':'faults','entity_differential.py':'entities','messaging_differential.py':'messaging','streaming_differential.py':'streaming','status_differential.py':'status','streaming_journal_differential.py':'streaming-journal','message_wait_differential.py':'message-wait','journal_concurrency_differential.py':'journal-concurrency'}
   if suite in reports:cmd+=['--output',str(output/((reports[suite]+'-disabled' if disabled_journal else reports[suite])+'.json'))]
   if disabled_journal:cmd+=['--disabled-journal']
   if suite=='journal_concurrency_differential.py':cmd+=['--capacity','64','--scenario-check']
   if suite=='template_differential.py':cmd+=['--discover']
   if suite=='streaming_differential.py':cmd+=['--limit-reference','http://127.0.0.1:18113','--limit-candidate','http://127.0.0.1:18114']
   result=subprocess.run(cmd,cwd=root,timeout=180);results.append({'suite':suite_name,'exit_code':result.returncode})
  finally:
   for proc in processes:
    proc.terminate()
    try:proc.wait(timeout=10)
    except subprocess.TimeoutExpired:proc.kill();proc.wait()
(output/'suites.json').write_text(json.dumps({'reference':'4.0.0-beta.40','candidate_language':a.candidate_language,'candidate_args':a.candidate_arg,'suites':results},indent=2)+'\n')
print(json.dumps(results,indent=2));raise SystemExit(any(r['exit_code']for r in results))
