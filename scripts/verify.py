#!/usr/bin/env python3
"""Build and run isolated differential suites against pinned Java WireMock."""
import argparse,json,pathlib,subprocess,tempfile,time,urllib.request,shutil,sys
p=argparse.ArgumentParser();p.add_argument('--jar',required=True);a=p.parse_args();root=pathlib.Path(__file__).resolve().parent.parent
subprocess.run(['go','build','-o','bin/breakmock','./cmd/breakmock'],cwd=root,check=True)
reference='http://127.0.0.1:18111';candidate='http://127.0.0.1:18112';results=[]
for suite in ['fixtures_differential.py','differential.py','matcher_differential.py','template_differential.py','admin_differential.py','runtime_differential.py','gzip_differential.py']:
 processes=[]
 with tempfile.TemporaryDirectory(prefix='breakmock-verify-')as tmp:
  try:
   for kind,port in [('java',18111),('go',18112)]:
    files=pathlib.Path(tmp)/kind
    if suite=='fixtures_differential.py':shutil.copytree(root/'testdata/upstream/remoteloader',files)
    else:files.mkdir()
    cmd=['java','-jar',str(pathlib.Path(a.jar).resolve()),'--port',str(port),'--root-dir',str(files),'--disable-banner'] if kind=='java' else [str(root/'bin/breakmock'),'--port',str(port),'--root-dir',str(files),'--bind-address','127.0.0.1']
    proc=subprocess.Popen(cmd,cwd=root,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);processes.append(proc)
    for _ in range(100):
     if proc.poll()is not None:raise RuntimeError(kind+' failed to start (port occupied?)')
     try:
      version=json.load(urllib.request.urlopen(f'http://127.0.0.1:{port}/__admin/version',timeout=1))['version']
      if kind=='java'and version!='3.13.2':raise RuntimeError('wrong reference '+version)
      if kind=='go'and not version.startswith('BreakMock'):raise RuntimeError('wrong candidate '+version)
      break
     except (OSError,ValueError):time.sleep(.1)
    else:raise RuntimeError(kind+' startup timeout')
   cmd=[sys.executable,str(root/'scripts'/suite),'--reference',reference,'--candidate',candidate]
   reports={'differential.py':'compatibility','matcher_differential.py':'matcher','admin_differential.py':'admin','runtime_differential.py':'runtime','template_differential.py':'template','gzip_differential.py':'gzip'}
   if suite in reports:cmd+=['--output',f'testdata/reports/{reports[suite]}.json']
   if suite=='template_differential.py':cmd+=['--discover']
   result=subprocess.run(cmd,cwd=root);results.append({'suite':suite,'exit_code':result.returncode})
  finally:
   for proc in processes:
    proc.terminate()
    try:proc.wait(timeout=10)
    except subprocess.TimeoutExpired:proc.kill();proc.wait()
print(json.dumps(results,indent=2));raise SystemExit(any(r['exit_code']for r in results))
