#!/usr/bin/env python3
"""Compare admin semantics on dedicated, disposable WireMock/BreakMock instances.
Resets both servers. Only nondeterminism is normalized: generated UUIDs, timestamps,
request origin/port, Date header, client address, and measured timing values.
Use fresh empty roots for persistence checks. WireMock reference must be 4.0.0-beta.40.
"""
import argparse,json,re,urllib.request,urllib.error,sys,threading
import intended_differences
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18089');p.add_argument('--candidate',default='http://127.0.0.1:18090');p.add_argument('--output');args=p.parse_args()
def call(base,method,path,body=None,headers=None):
 data=json.dumps(body).encode() if isinstance(body,(dict,list)) else body.encode() if isinstance(body,str) else body
 h={'Content-Type':'application/json'};h.update(headers or {});req=urllib.request.Request(base+path,data=data,method=method,headers=h)
 try:r=urllib.request.urlopen(req,timeout=15)
 except urllib.error.HTTPError as e:r=e
 raw=r.read()
 try:value=json.loads(raw) if path.startswith("/__admin/") else raw.decode(errors="strict")
 except (ValueError,UnicodeDecodeError):value=raw.decode(errors='replace')
 return r.status,value,raw.decode(errors="replace")
versions=[call(base,'GET','/__admin/version')[1] for base in (args.reference,args.candidate)]
from oracle import identify
identify(args.reference,'java');identify(args.candidate,'go')
fixed='11111111-1111-4111-8111-111111111111'
def normalize(v,path=(),mapping_id=None):
 # Only transport/journal metadata is normalized. Mock response bodies and metadata
 # stay byte/value-exact, even if they contain dates, UUIDs or server URLs.
 if isinstance(v,(list,tuple)):return [normalize(x,path+('*',),mapping_id) for x in v]
 if not isinstance(v,dict):return v
 out={};health=not path and v.get('status')=='healthy' and 'uptimeInSeconds' in v and 'timestamp' in v
 logged_request='absoluteUrl' in v and 'loggedDate' in v
 mapping='request' in v and 'response' in v and 'id' in v
 if mapping and v.get('id') not in (fixed,'22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333'):mapping_id=v['id']
 event='request' in v and 'wasMatched' in v and 'id' in v
 response_headers=bool(path and path[-1]=='headers' and any(p in ('response','responseDefinition') for p in path))
 request_headers=bool(path and path[-1]=='headers' and 'request' in path)
 for k,x in v.items():
  if k=='timeOffsetNanos' and 'subEvents' in path and v.get('type')=='REQUEST_NOT_MATCHED':out[k]='<duration>';continue
  if health and k in ('uptimeInSeconds','timestamp'):out[k]='<dynamic-health>';continue
  if k=='bodyFileName' and path and path[-1]=='response' and mapping_id and isinstance(x,str):out[k]=x.replace(mapping_id,'<generated-uuid>');continue
  if response_headers and k.lower()=='date':out[k]='<dynamic-date>';continue
  if logged_request and k in ('loggedDate','loggedDateString','clientIp','port'):out[k]='<dynamic>';continue
  if event and k=='timing' and isinstance(x,dict):out[k]={name:('<duration>' if name in ('addedDelay','processTime','responseSendTime','serveTime','totalTime') else value) for name,value in x.items()};continue
  if ((mapping or event) and k in ('id','uuid') or response_headers and k.lower()=='matched-stub-id') and isinstance(x,str) and x not in (fixed,'22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333') and re.fullmatch(r'[0-9a-f]{8}-[0-9a-f-]{27}',x):out[k]='<generated-uuid>';continue
  if isinstance(x,str) and (logged_request and k=='absoluteUrl' or request_headers and k.lower()=='host'):
   for base in (args.reference,args.candidate):x=x.replace(base,'<origin>').replace(base.split('://')[1],'<authority>')
  out[k]=normalize(x,path+(("request",k) if logged_request else (k,)),mapping_id)
 return out

def normalize_wire(raw, original, normalized):
 # Replace only values approved by normalize(), preserving every key, comma,
 # space, newline and object member order in the original wire representation.
 decoder=json.JSONDecoder();edits=[]
 def skip(i):
  while i<len(raw) and raw[i].isspace():i+=1
  return i
 def walk(i,old,new):
  i=skip(i)
  if isinstance(old,dict):
   i=skip(i+1)
   while raw[i]!='}':
    key,end=decoder.raw_decode(raw,i);i=skip(end);i=walk(i+1,old[key],new[key]);i=skip(i)
    if raw[i]==',':i=skip(i+1)
   return i+1
  if isinstance(old,list):
   i=skip(i+1)
   for index,value in enumerate(old):
    i=walk(i,value,new[index]);i=skip(i)
    if raw[i]==',':i=skip(i+1)
   return i+1
  _,end=decoder.raw_decode(raw,i)
  if old!=new:edits.append((i,end,json.dumps(new,ensure_ascii=False,separators=(',',':'))))
  return end
 try:walk(0,original,normalized)
 except (ValueError,KeyError,IndexError,TypeError):return raw
 for start,end,replacement in reversed(edits):raw=raw[:start]+replacement+raw[end:]
 return raw
results=[]
def step(name,method,path,body=None,headers=None):
 raw=[call(base,method,path,body,headers) for base in (args.reference,args.candidate)]
 semantic=[v[1] if path.startswith('/__admin/docs') else normalize(v[1]) for v in raw]
 normalized_wire=[normalize_wire(v[2],v[1],semantic[i]) if path.startswith('/__admin/') and isinstance(v[1],(dict,list)) else v[2] for i,v in enumerate(raw)]
 semantic_ok=raw[0][0]==raw[1][0] and semantic[0]==semantic[1]
 ok=raw[0][0]==raw[1][0] and normalized_wire[0]==normalized_wire[1]
 intended=intended_differences.check('admin_differential.py',name,*[(v[0],v[2] if isinstance(v[2],bytes) else str(v[2]).encode()) for v in raw])
 if intended:ok=semantic_ok=intended[0]
 results.append({'name':name,'pass':ok,'semantic_pass':semantic_ok,**({'intended_difference':intended[1]} if intended else {}),**({} if ok else {'reference':raw[0],'candidate':raw[1],'normalized_reference_body':normalized_wire[0],'normalized_candidate_body':normalized_wire[1]})})
 print(('PASS ' if ok else 'FAIL ')+name+(f' [intended difference {intended[1]}]' if intended else ''));return raw
step('version JSON','GET','/__admin/version')
step('version text','GET','/__admin/version',headers={'Accept':'text/plain'})
step('health','GET','/__admin/health')
step('default missing certificate','GET','/__admin/certs/wiremock-ca.crt')
for asset in ('docs','docs/swagger','recorder','recorder/','swagger-ui/','swagger-ui/swagger-ui-dist/swagger-ui.css','recorder/lib/jquery-4.0.0.min.js'):
 step('asset '+asset,'GET','/__admin/'+asset)
step('reset','POST','/__admin/reset')
step('empty mappings','GET','/__admin/mappings')
step('empty unmatched response','GET','/admin-unmatched-empty')
step('empty unmatched journal','GET','/__admin/requests')
step('empty unmatched list','GET','/__admin/requests/unmatched')
step('empty unmatched clear','DELETE','/__admin/requests')
step('settings initialize','PUT','/__admin/settings',{'proxyPassThrough':True})
step('settings','GET','/__admin/settings')
step('settings update','PUT','/__admin/settings',{'fixedDelay':0})
step('settings read updated','GET','/__admin/settings')
m={'id':fixed,'request':{'url':'/admin-workflow?q=x&q=y'},'response':{'status':201,'body':'hello','headers':{'Content-Type':'text/plain'}},'metadata':{'suite':'admin'}}
default_id='33333333-3333-4333-8333-333333333333'
step('default mapping omitted objects','POST','/__admin/mappings',{'id':default_id})
step('default mapping get','GET','/__admin/mappings/'+default_id)
step('default mapping duplicate error','POST','/__admin/mappings',{'id':default_id})
step('default mapping legacy edit','POST','/__admin/mappings/edit',{'id':default_id,'response':{'status':0}})
step('default mapping legacy get','GET','/__admin/mappings/'+default_id)
step('default mapping delete','DELETE','/__admin/mappings/'+default_id)
step('invalid UUID error','POST','/__admin/mappings',{'id':'bad'})
step('unknown mapping property error','POST','/__admin/mappings',{'unexpected':True})
step('legacy missing mapping','POST','/__admin/mappings/edit',{'id':default_id})
step('response string status','POST','/__admin/mappings',{'id':default_id,'response':{'status':'201','jsonBody':None}})
step('response string status get','GET','/__admin/mappings/'+default_id)
step('response string status delete','DELETE','/__admin/mappings/'+default_id)
step('response body precedence','POST','/__admin/mappings',{'id':default_id,'response':{'body':'chosen','jsonBody':{'ignored':True},'base64Body':'aWdub3JlZA=='}})
step('response body precedence delete','DELETE','/__admin/mappings/'+default_id)
step('unknown request property','POST','/__admin/mappings',{'request':{'unexpected':True}})
step('unknown response property','POST','/__admin/mappings',{'response':{'unexpected':True}})
for name,body in [('typedtext',{'data':'hello','format':'text'}),('typedjson',{'data':{'z':1,'a':[2,3]}}),('typedbinary',{'data':'hello','format':'binary'}),('base64',{'base64Data':'aGk='}),('file',{'filePath':'hello.txt'}),('charset',{'charset':'UTF-16LE','data':'hi'}),('compression',{'compression':'gzip','data':'hello'})]:
 step('HTTP entity '+name,'POST','/__admin/mappings',{'id':default_id,'response':{'body':body}})
 step('HTTP entity delete '+name,'DELETE','/__admin/mappings/'+default_id)
step('v4 ignored schema and uuid','POST','/__admin/mappings',{'id':default_id,'uuid':'ignored-value','$schema':'https://example.invalid/schema','response':{'body':'ignored','bodyFileName':'selected.txt'}})
step('v4 ignored schema cleanup','DELETE','/__admin/mappings/'+default_id)
step('persistent create','POST','/__admin/mappings',{'id':default_id,'persistent':True,'response':{'body':'persisted'}})
step('persistent reset','POST','/__admin/mappings/reset')
step('persistent survives reset','GET','/__admin/mappings/'+default_id)
step('persistent delete','DELETE','/__admin/mappings/'+default_id)
step('extended settings initialize','POST','/__admin/settings',{'extended':{'z':1,'nested':{'a':1,'b':2},'keep':3}})
step('extended settings initial read','GET','/__admin/settings')
step('extended settings patch','PATCH','/__admin/settings/extended',{'extended':{'z':None,'nested':{'c':3},'new':4}})
step('extended settings patched read','GET','/__admin/settings')
step('settings restore','POST','/__admin/settings',{'fixedDelay':0})
step('unknown scenario error','PUT','/__admin/scenarios/absent/state',{'state':'X'})
step('admin root mappings','GET','/__admin/')
step('unknown admin path','GET','/__admin/does-not-exist')
order_id='22222222-2222-4222-8222-222222222222'
ordered={'id':order_id,'name':'ordered','request':{'url':'/ordered','headers':{'Z-Last':{'equalTo':'z'},'A-First':{'equalTo':'a'}}},'response':{'status':200,'jsonBody':{'z':1,'a':[{'last':'😀','first':'<&>'}]},'headers':{'Z-Last':'z','A-First':'a'}},'metadata':{'z':1,'a':2}}
step('ordered user maps create','POST','/__admin/mappings',ordered)
step('ordered user maps get','GET','/__admin/mappings/'+order_id)
ordered['metadata']={'a':3,'z':4}
step('ordered user maps update','PUT','/__admin/mappings/'+order_id,ordered)
step('ordered user maps delete','DELETE','/__admin/mappings/'+order_id)
step('create mapping','POST','/__admin/mappings',m)
step('get mapping','GET','/__admin/mappings/'+fixed)
step('list mapping','GET','/__admin/mappings')
step('metadata find','POST','/__admin/mappings/find-by-metadata',{'equalToJson':'{"suite":"admin"}'})
step('unmatched mappings','GET','/__admin/mappings/unmatched')
step('serve matched','GET','/admin-workflow?q=x&q=y',headers={'Cookie':'session=abc','X-Example':'hello'})
step('journal count','POST','/__admin/requests/count',{'urlPath':'/admin-workflow'})
step('journal find','POST','/__admin/requests/find',{'urlPath':'/admin-workflow'})
step('journal list','GET','/__admin/requests')
step('near misses request pattern','POST','/__admin/near-misses/request-pattern',{'urlPath':'/admin-workflow'})
step('unmatched near misses empty','GET','/__admin/requests/unmatched/near-misses')
step('journal unmatched filter','GET','/__admin/requests?unmatched=true')
step('journal future filter','GET','/__admin/requests?since=2999-01-01T00:00:00Z')
step('journal past filter','GET','/__admin/requests?since=2000-01-01T00:00:00Z')
step('journal invalid date','GET','/__admin/requests?since=bad')
step('journal invalid stub UUID','GET','/__admin/requests?matchingStub=bad')
step('no unmatched mappings','GET','/__admin/mappings/unmatched')
# Initial status depends on prior process history; active and stopped are checked below.
step('recording start','POST','/__admin/recordings/start',{'persist':False})
step('recording active status','GET','/__admin/recordings/status')
step('recording empty stop','POST','/__admin/recordings/stop')
step('recording stopped status','GET','/__admin/recordings/status')
step('recording double stop','POST','/__admin/recordings/stop')
step('snapshot local stub','POST','/__admin/recordings/snapshot',{'filters':{'allowNonProxied':True},'persist':False,'repeatsAsScenarios':False})
extracted=step('snapshot extract text body','POST','/__admin/recordings/snapshot',{'filters':{'allowNonProxied':True},'persist':False,'repeatsAsScenarios':False,'extractBodyCriteria':{'textSizeThreshold':1}})
file_results=[]
for base,result in zip((args.reference,args.candidate),extracted):
 filename=result[1]['mappings'][0]['response']['bodyFileName']
 file_results.append(call(base,'GET','/__admin/files/'+filename))
 call(base,'DELETE','/__admin/files/'+filename)
file_ok=file_results[0]==file_results[1] and file_results[0][2]=='hello'
results.append({'name':'snapshot extracted file bytes','pass':file_ok,'semantic_pass':file_ok,**({} if file_ok else {'reference':file_results[0],'candidate':file_results[1]})})
step('remove requests by metadata','POST','/__admin/requests/remove-by-metadata',{'equalToJson':'{"suite":"admin"}'})
step('clear journal','DELETE','/__admin/requests')
step('file put','PUT','/__admin/files/admin-workflow.txt','hello')
step('file get','GET','/__admin/files/admin-workflow.txt')
step('file list','GET','/__admin/files')
step('file delete','DELETE','/__admin/files/admin-workflow.txt')
step('delete mapping','DELETE','/__admin/mappings/'+fixed)
step('clear mappings','DELETE','/__admin/mappings')
step('import','POST','/__admin/mappings/import',{'mappings':[m]})
step('save','POST','/__admin/mappings/save')
step('delete saved mapping','DELETE','/__admin/mappings/'+fixed)
step('reset persisted','POST','/__admin/mappings/reset')
step('get persisted','GET','/__admin/mappings/'+fixed)
step('metadata remove','POST','/__admin/mappings/remove-by-metadata',{'equalToJson':'{"suite":"admin"}'})
step('scenario create','POST','/__admin/mappings',{'id':fixed,'scenarioName':'workflow','requiredScenarioState':'Started','newScenarioState':'Done','request':{'url':'/scenario'},'response':{'status':200}})
step('scenario list','GET','/__admin/scenarios')
step('scenario state','PUT','/__admin/scenarios/workflow/state',{'state':'Done'})
step('scenario invalid state','PUT','/__admin/scenarios/workflow/state',{'state':'absent'})
step('scenario null state','PUT','/__admin/scenarios/workflow/state',{})
step('scenario empty body reset','PUT','/__admin/scenarios/workflow/state','')
step('scenario reset','POST','/__admin/scenarios/reset')

class Upstream(BaseHTTPRequestHandler):
 def do_GET(self):
  body=b'upstream body';self.send_response(202);self.send_header('Content-Type','text/plain');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*args):pass
upstream=ThreadingHTTPServer(('127.0.0.1',0),Upstream);threading.Thread(target=upstream.serve_forever,daemon=True).start()
step('record proxy start','POST','/__admin/recordings/start',{'targetBaseUrl':'http://127.0.0.1:'+str(upstream.server_port),'persist':False,'captureHeaders':{'X-Capture':{}}})
step('record proxy serve first','GET','/recorded/item?x=1',headers={'X-Capture':'captured'})
step('record proxy serve repeated','GET','/recorded/item?x=1',headers={'X-Capture':'captured'})
step('record proxy stop','POST','/__admin/recordings/stop')
step('record replay first','GET','/recorded/item?x=1',headers={'X-Capture':'captured'})
step('record replay second','GET','/recorded/item?x=1',headers={'X-Capture':'captured'})
upstream.shutdown()
# Webhook journal subevents retain the actual callback payload and response.
class Callback(BaseHTTPRequestHandler):
 def do_POST(self):
  self.rfile.read(int(self.headers.get('Content-Length','0')))
  self.send_response_only(202);self.send_header('X-Callback','accepted');self.end_headers();self.wfile.write(b'accepted')
 def log_message(self,*args):pass
callback=ThreadingHTTPServer(('127.0.0.1',0),Callback)
threading.Thread(target=callback.serve_forever,daemon=True).start()
step('webhook clear mappings','DELETE','/__admin/mappings')
step('webhook clear journal','DELETE','/__admin/requests')
step('webhook mapping','POST','/__admin/mappings',{'id':fixed,'request':{'url':'/webhook-trigger'},'response':{'body':'ok'},'serveEventListeners':[{'name':'webhook','parameters':{'method':'POST','url':f'http://127.0.0.1:{callback.server_port}/callback','headers':{'X-Origin':'{{originalRequest.path}}'},'body':'{{originalRequest.method}}'}}]})
step('webhook trigger','GET','/webhook-trigger')
import time
for base in (args.reference,args.candidate):
 deadline=time.monotonic()+3
 while time.monotonic()<deadline:
  journal=call(base,'GET','/__admin/requests')[1]
  if journal.get('requests') and len(journal['requests'][0]['subEvents'])>=2:break
  time.sleep(.01)
step('webhook journal','GET','/__admin/requests')
callback.shutdown()
step('closest unmatched response','GET','/webhook-trigger-missing')
step('closest unmatched journal','GET','/__admin/requests')
step('closest unmatched near misses','GET','/__admin/requests/unmatched/near-misses')
step('final reset','POST','/__admin/reset')
report={'reference':versions[0],'candidate':versions[1],'comparison':'strict response-body bytes after value-only nondeterminism substitutions','passed':sum(r['pass'] for r in results),'semantic_passed':sum(r['semantic_pass'] for r in results),'total':len(results),'results':results};print(f"{report['passed']}/{report['total']} passed")
if args.output:open(args.output,'w').write(json.dumps(report,indent=2)+'\n')
sys.exit(0 if all(r['pass'] for r in results) else 1)
