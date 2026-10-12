#!/usr/bin/env python3
"""Native wait semantics against WireMock: timed future arrivals and exact JSON.
Only generated UUID and timestamp scalar values are replaced; formatting is intact.
"""
import argparse,concurrent.futures,json,re,time,urllib.request,urllib.error
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',required=True);p.add_argument('--candidate',required=True);p.add_argument('--output');p.add_argument('--disabled-journal',action='store_true');a=p.parse_args();bases=[a.reference,a.candidate]
for base,kind in zip(bases,['java','go']):identify(base,kind)
def call(base,path,body=None,method='POST',headers=None):
 req=urllib.request.Request(base+'/__admin'+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Content-Type':'application/json',**(headers or {})})
 try:r=urllib.request.urlopen(req,timeout=3)
 except urllib.error.HTTPError as e:r=e
 return r.status,r.read().decode()
def norm(raw):
 raw=re.sub(r'("id"\s*:\s*")[a-f0-9-]{36}("|$)',r'\1<id>\2',raw)
 return re.sub(r'("timestamp"\s*:\s*")[^"\\]*(")',r'\1<timestamp>\2',raw)
results=[]
if a.disabled_journal:
 for endpoint in ('wait','wait-for-count'):
  raw=[call(base,'/messages/'+endpoint,{'timeoutMillis':10})for base in bases]
  normalized=[(status,body.replace(base,'<origin>'))for base,(status,body)in zip(bases,raw)]
  ok=normalized[0]==normalized[1] and raw[0][0]==500
  results.append({'name':'disabled journal '+endpoint,'pass':ok,**({}if ok else{'reference':raw[0],'candidate':raw[1]})})
 # A fixed Host makes the new error-body comparisons exact without substitutions.
 for timeout in (-1,0,1000):
  raw=[call(base,'/messages/wait-for-count',{'count':-1,'timeoutMillis':timeout},headers={'Host':'wiremock.test'})for base in bases]
  ok=raw[0]==raw[1] and raw[0][0]==500 and 'MessageJournalDisabledException' in raw[0][1]
  results.append({'name':f'disabled journal precedes negative count timeout={timeout}','pass':ok,**({}if ok else{'reference':raw[0],'candidate':raw[1]})})
 report={'comparison':'Exact disabled-journal error bodies after server-origin replacement','passed':sum(r['pass']for r in results),'total':len(results),'results':results}
 if a.output:open(a.output,'w').write(json.dumps(report,indent=2)+'\n')
 print(f"{report['passed']}/{report['total']} passed");raise SystemExit(report['passed']!=report['total'])
for base in bases:
 call(base,'/messages',method='DELETE');call(base,'/message-mappings',method='DELETE');call(base,'/channel-providers',{'name':'wait-provider','driverType':'in-memory'});call(base,'/channels',{'providerName':'wait-provider','channelName':'wait-channel'})
def send(base,body):return call(base,'/channels/send',{'type':'fixed','providerName':'wait-provider','channelName':'wait-channel','message':{'body':body}})
def scenario(name,path,options,arrivals=(),preload=(),min_elapsed=0,max_elapsed=2):
 raw=[];times=[]
 for base in bases:
  call(base,'/messages',method='DELETE')
  for body in preload:send(base,body)
  with concurrent.futures.ThreadPoolExecutor() as pool:
   start=time.monotonic();future=pool.submit(call,base,path,options)
   for delay,body in arrivals:time.sleep(delay);send(base,body)
   value=future.result();elapsed=time.monotonic()-start
  raw.append(value);times.append(elapsed)
 ok=raw[0][0]==raw[1][0] and norm(raw[0][1])==norm(raw[1][1]) and all(min_elapsed<=n<max_elapsed for n in times)
 results.append({'name':name,'pass':ok,'elapsedSeconds':times,**({}if ok else{'reference':raw[0],'candidate':raw[1]})});print(('PASS 'if ok else'FAIL ')+name)
scenario('single timeout','/messages/wait',{'timeoutMillis':60},min_elapsed=.05,max_elapsed=.8)
scenario('single future filtered arrival','/messages/wait',{'pattern':{'body':{'equalTo':'ready'}},'timeoutMillis':1000},[(.03,'other'),(.03,'ready')],min_elapsed=.05,max_elapsed=.8)
scenario('multiple future arrivals','/messages/wait-for-count',{'count':2,'timeoutMillis':1000},[(.03,'first'),(.03,'second')],min_elapsed=.05,max_elapsed=.8)
scenario('partial results at timeout','/messages/wait-for-count',{'count':3,'timeoutMillis':100},[(.03,'only')],min_elapsed=.09,max_elapsed=.8)
scenario('existing count caps results','/messages/wait-for-count',{'count':1,'timeoutMillis':1000},preload=['first','second'],max_elapsed=.8)
scenario('single ignores count','/messages/wait',{'count':3,'timeoutMillis':1000},preload=['existing'],max_elapsed=.8)
scenario('zero timeout returns current count','/messages/wait-for-count',{'count':1,'timeoutMillis':0},preload=['first','second'],max_elapsed=.8)
scenario('zero count returns empty','/messages/wait-for-count',{'count':0,'timeoutMillis':1000},preload=['first','second'],max_elapsed=.8)
scenario('single ignores negative count','/messages/wait',{'count':-1,'timeoutMillis':1000},preload=['existing'],max_elapsed=.8)
scenario('single omitted timeout is immediate','/messages/wait',{},max_elapsed=.8)
scenario('count omitted timeout is immediate','/messages/wait-for-count',{'count':1},max_elapsed=.8)
scenario('omitted count defaults to one','/messages/wait-for-count',{'timeoutMillis':0},preload=['first','second'],max_elapsed=.8)
for timeout in (-1,0,1000):
 raw=[call(base,'/messages/wait-for-count',{'count':-1,'timeoutMillis':timeout},headers={'Host':'wiremock.test'})for base in bases]
 ok=raw[0]==raw[1] and raw[0][0]==500
 results.append({'name':f'negative count exact error timeout={timeout}','pass':ok,**({}if ok else{'reference':raw[0],'candidate':raw[1]})})
 print(('PASS 'if ok else'FAIL ')+results[-1]['name'])
for base in bases:
 channels=json.loads(call(base,'/channels',method='GET')[1])['channels']
 for channel in channels:
  if channel.get('providerName')=='wait-provider':call(base,'/channels/'+channel['id'],method='DELETE')
 call(base,'/channel-providers/wait-provider',method='DELETE');call(base,'/messages',method='DELETE')
report={'comparison':'Strict JSON bytes after generated id/timestamp scalar replacement, plus completion-time bounds','passed':sum(r['pass']for r in results),'total':len(results),'results':results}
if a.output:open(a.output,'w').write(json.dumps(report,indent=2)+'\n')
print(f"{report['passed']}/{report['total']} passed");raise SystemExit(report['passed']!=report['total'])
