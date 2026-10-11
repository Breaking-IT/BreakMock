#!/usr/bin/env python3
"""Strict response bytes for WireMock 4 messaging admin workflows.
Only generated UUID scalar values and actual event/channel timestamps are replaced.
Use isolated disposable roots. Resetting clears mappings and journals.
"""
import argparse,json,re,time,urllib.request,urllib.error
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18301');p.add_argument('--candidate',default='http://127.0.0.1:18302');p.add_argument('--output');a=p.parse_args();bases=(a.reference,a.candidate)
for base,kind in zip(bases,('java','go')):identify(base,kind)
fixed='11111111-1111-4111-8111-111111111111'
def call(base,method,path,body=None):
 req=urllib.request.Request(base+'/__admin'+path,data=json.dumps(body).encode() if body is not None else None,method=method,headers={'Content-Type':'application/json'})
 try:r=urllib.request.urlopen(req,timeout=5)
 except urllib.error.HTTPError as e:r=e
 raw=r.read().decode()
 return r.status,raw

def norm(raw):
 try:value=json.loads(raw)
 except ValueError:return raw
 edits=[];decoder=json.JSONDecoder()
 def skip(i):
  while i<len(raw) and raw[i].isspace():i+=1
  return i
 def walk(i,context=None):
  i=skip(i)
  if raw[i]=='{':
   i=skip(i+1)
   while raw[i]!='}':
    k,end=decoder.raw_decode(raw,i);i=skip(end);i=walk(i+1,k);i=skip(i)
    if raw[i]==',':i=skip(i+1)
   return i+1
  if raw[i]=='[':
   i=skip(i+1)
   while raw[i]!=']':
    i=skip(walk(i,context))
    if raw[i]==',':i=skip(i+1)
   return i+1
  val,end=decoder.raw_decode(raw,i)
  if context in ('id','uuid') and isinstance(val,str) and val!=fixed and re.fullmatch('[0-9a-f-]{36}',val):edits.append((i,end,'"<generated-uuid>"'))
  if context in ('timestamp','loggedDate','loggedDateString'):edits.append((i,end,'"<dynamic-time>"'))
  return end
 walk(0)
 for start,end,v in reversed(edits):raw=raw[:start]+v+raw[end:]
 return raw
results=[]
def step(name,method,path,body=None,expected=None):
 paths=path if isinstance(path,list) else [path,path];raw=[call(base,method,paths[i],body) for i,base in enumerate(bases)];ok=raw[0][0]==raw[1][0] and norm(raw[0][1])==norm(raw[1][1]) and (expected is None or raw[0][0]==expected)
 results.append({'name':name,'pass':ok,**({} if ok else {'reference':raw[0],'candidate':raw[1]})});print(('PASS ' if ok else 'FAIL ')+name,flush=True);return raw
step('reset','POST','/reset')
step('clear initial message mappings','DELETE','/message-mappings')
step('empty mappings','GET','/message-mappings')
step('empty messages','DELETE','/messages')
step('empty journal','GET','/messages')
step('empty count','POST','/messages/count',{})
step('empty find','POST','/messages/find',{})
step('empty wait','POST','/messages/wait',{'timeoutMillis':1})
step('empty wait count','POST','/messages/wait-for-count',{'timeoutMillis':1,'count':1})
step('missing event','GET','/messages/'+fixed)
step('missing event delete','DELETE','/messages/'+fixed)
step('empty channel send','POST','/channels/send',{'type':'websocket','message':{'body':'hello'}})
for name,path,body in [('provider name','/channel-providers',{'driverType':'in-memory'}),('provider driver','/channel-providers',{'name':'missing-driver'}),('channel provider','/channels',{'channelName':'test'}),('channel name','/channels',{'providerName':'none'}),('send message','/channels/send',{'type':'fixed'}),('action message','/message-mappings',{'actions':[{'type':'send'}]}),('action target provider','/message-mappings',{'actions':[{'type':'send','message':{'body':'x'},'channelTarget':{'type':'fixed-channel','channelName':'test'}}]}),('http request pattern','/message-mappings',{'trigger':{'type':'http-request'}}),('http stub ID','/message-mappings',{'trigger':{'type':'http-stub'}})]:
 step('missing '+name,'POST',path,body,422)
step('mapping defaults','POST','/message-mappings',{'id':fixed})
step('mapping get','GET','/message-mappings/'+fixed)
step('mapping list','GET','/message-mappings')
for name,body in [('plain','hello'),('typedtext',{'data':'hello','format':'text'}),('typedjson',{'data':{'z':1,'a':[2,3]}}),('typedbinary',{'data':'hello','format':'binary'}),('base64',{'base64Data':'aGk='}),('file',{'filePath':'hello.txt'}),('charset',{'charset':'UTF-16LE','data':'hi'}),('compression',{'compression':'gzip','data':'hello'})]:
 step('mapping action entity '+name,'PUT','/message-mappings/'+fixed,{'actions':[{'type':'send','message':{'body':body}}]})
step('mapping nested channel patterns','PUT','/message-mappings/'+fixed,{'trigger':{'type':'message','channel':{'type':'websocket','initiatingRequestPattern':{'url':'/ws'}}},'actions':[{'type':'send','message':{'body':'hello'},'channelTarget':{'type':'request-initiated','channelType':'websocket','requestPattern':{'url':'/ws'}}}]})
step('mapping edit','PUT','/message-mappings/'+fixed,{'name':'message mapping','priority':2,'trigger':{'type':'message','message':{'body':{'contains':'hello'}}},'actions':[],'metadata':{'suite':'messages'}})
step('metadata find','POST','/message-mappings/find-by-metadata',{'equalToJson':'{"suite":"messages"}'})
step('provider register','POST','/channel-providers',{'name':'memory-admin','driverType':'in-memory'})
for name in ('z-provider','a-provider','middle-provider'):
 step('provider register '+name,'POST','/channel-providers',{'name':name,'driverType':'in-memory'})
step('providers hash iteration order','GET','/channel-providers')
for name in ('z-provider','a-provider','middle-provider'):
 step('provider remove '+name,'DELETE','/channel-providers/'+name)
step('provider get','GET','/channel-providers/memory-admin')
step('provider list','GET','/channel-providers')
step('provider update','PUT','/channel-providers/memory-admin',{'name':'memory-admin','driverType':'in-memory','settings':{'z':1,'a':2}})
channel=step('fixed channel create','POST','/channels',{'providerName':'memory-admin','channelName':'admin-test'})
step('fixed channel duplicate conflict','POST','/channels',{'providerName':'memory-admin','channelName':'admin-test'})
ids=[json.loads(v[1])['id'] for v in channel]
step('fixed channel list','GET','/channels')
step('fixed channel get','GET',['/channels/'+v for v in ids])
step('fixed channel send','POST','/channels/send',{'type':'fixed','providerName':'memory-admin','channelName':'admin-test','message':{'body':'hello world'}})
step('message journal','GET','/messages')
step('message count body','POST','/messages/count',{'body':{'contains':'hello'}})
found=step('message find body','POST','/messages/find',{'body':{'contains':'hello'}})
eventids=[json.loads(v[1])['messageServeEvents'][0]['id'] for v in found]
step('message get','GET',['/messages/'+v for v in eventids])
step('message wait','POST','/messages/wait',{'pattern':{'body':{'contains':'hello'}},'timeoutMillis':1})
step('message wait count','POST','/messages/wait-for-count',{'pattern':{'body':{'contains':'hello'}},'timeoutMillis':1,'count':1})
step('message remove','POST','/messages/remove',{'body':{'contains':'hello'}})
step('message empty after removal','GET','/messages')
step('missing target mapping','POST','/message-mappings',{'id':'22222222-2222-4222-8222-222222222222','trigger':{'type':'message'},'actions':[{'type':'send','channelTarget':{'type':'fixed-channel','providerName':'missing-provider','channelName':'missing-channel'},'message':{'body':'x'}}]})
step('missing action target returns404','POST','/channels/send',{'type':'fixed','providerName':'memory-admin','channelName':'admin-test','message':{'body':'fail'}})
step('failed action is not journalled','GET','/messages')
step('missing target mapping delete','DELETE','/message-mappings/22222222-2222-4222-8222-222222222222')
step('fixed channel delete','DELETE',['/channels/'+v for v in ids])
step('fixed channel missing','GET',['/channels/'+v for v in ids])
step('provider delete','DELETE','/channel-providers/memory-admin')
step('provider missing','GET','/channel-providers/memory-admin')
step('metadata remove','POST','/message-mappings/remove-by-metadata',{'equalToJson':'{"suite":"messages"}'})
step('mapping missing','GET','/message-mappings/'+fixed)
step('duplicate mapping first','POST','/message-mappings',{'id':fixed,'name':'first'})
step('duplicate mapping second','POST','/message-mappings',{'id':fixed,'name':'second'})
step('duplicate mapping list','GET','/message-mappings')
step('duplicate mapping get','GET','/message-mappings/'+fixed)
step('duplicate mapping edit','PUT','/message-mappings/'+fixed,{'name':'edited'})
step('duplicate mapping list after edit','GET','/message-mappings')
step('duplicate mapping delete all','DELETE','/message-mappings/'+fixed)
step('duplicate mapping empty','GET','/message-mappings')
step('mapping reset','DELETE','/message-mappings')
step('reset final','POST','/reset')
report={'reference':'WireMock4.0.0-beta.40','comparison':'strict response bytes with generated scalar values replaced','passed':sum(r['pass'] for r in results),'total':len(results),'results':results}
if a.output:open(a.output,'w').write(json.dumps(report,indent=2)+'\n')
print(f"{report['passed']}/{report['total']} passed");raise SystemExit(report['passed']!=report['total'])
