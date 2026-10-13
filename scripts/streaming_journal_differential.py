#!/usr/bin/env python3
"""Strict response bytes for native WebSocket/SSE channel journals.
Only generated UUID scalar values and actual event/channel timestamps are replaced.
Use isolated disposable roots. Resetting clears mappings and journals.
"""
import argparse,json,re,time,urllib.request,urllib.error,socket,struct,urllib.parse,http.client
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
class WS:
 def __init__(self,base):
  u=urllib.parse.urlsplit(base);self.s=socket.create_connection((u.hostname,u.port),timeout=3);self.f=self.s.makefile('rb')
  request=(f'GET /stream?q=Ada HTTP/1.1\r\nHost: stream.example\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: MDEyMzQ1Njc4OUFCQ0RFRg==\r\nSec-WebSocket-Version: 13\r\n\r\n').encode();self.s.sendall(request)
  self.status=int(self.f.readline().split()[1]);assert self.status==101,self.status
  while self.f.readline()!=b'\r\n':pass
 def write(self,data,opcode=1,fin=True):
  if isinstance(data,str):data=data.encode()
  n=len(data);head=bytes([(128 if fin else 0)|opcode]);head+=bytes([128|n]) if n<126 else bytes([128|126])+struct.pack('!H',n) if n<65536 else bytes([128|127])+struct.pack('!Q',n)
  mask=b'ABCD';self.s.sendall(head+mask+bytes(c^mask[i%4] for i,c in enumerate(data)))
 def frame(self):
  head=self.f.read(2)
  if len(head)<2:return b'EOF'
  n=head[1]&127;extra=b''
  if n==126:extra=self.f.read(2);n=struct.unpack('!H',extra)[0]
  elif n==127:extra=self.f.read(8);n=struct.unpack('!Q',extra)[0]
  assert not(head[1]&128),'masked server frame'
  return head+extra+self.f.read(n)
 def close(self):
  self.f.close();self.s.close()

for base in bases:
 call(base,'DELETE','/mappings');call(base,'DELETE','/message-mappings');call(base,'DELETE','/messages')
 for channel in json.loads(call(base,'GET','/channels')[1])['channels']:call(base,'DELETE','/channels/'+channel['id'])
step('websocket HTTP mapping','POST','/mappings',{'id':fixed,'request':{'urlPath':'/stream'},'response':{'openWebsocketChannel':True}})
step('websocket message mapping','POST','/message-mappings',{'id':fixed,'trigger':{'type':'message','message':{'body':{'equalTo':'hello'}}},'actions':[{'type':'send','message':{'body':'reply'}}]})
clients=[WS(base) for base in bases]
for base in bases:
 deadline=time.monotonic()+2
 while not json.loads(call(base,'GET','/channels')[1])['channels'] and time.monotonic()<deadline:time.sleep(.01)
step('websocket channel list','GET','/channels')
def await_events(count,deadline=3):
 # Both servers can return a reply frame before journaling the events that
 # produced it (WireMock journals "received" after writing the reply), so wait
 # until each journal reaches the expected size before comparing. A server that
 # never reaches it is compared as is, and the step fails.
 for base in bases:
  end=time.monotonic()+deadline
  while len(json.loads(call(base,'GET','/messages')[1]).get('messageServeEvents',[]))<count and time.monotonic()<end:time.sleep(.01)
for client in clients:client.write('hello');client.frame()
await_events(2)
step('websocket matched received and sent journal','GET','/messages')
step('websocket count channel','POST','/messages/count',{'channel':{'urlPath':'/stream'}})
step('websocket count body','POST','/messages/count',{'body':{'equalTo':'hello'}})
for client in clients:client.write('unmatched')
await_events(3)
step('websocket unmatched journal','GET','/messages')
for client in clients:client.write(b'\x00\xff',2)
await_events(4)
step('websocket binary journal','GET','/messages')
step('websocket remove matched messages','POST','/messages/remove',{'body':{'equalTo':'hello'}})
for client in clients:client.close()
time.sleep(.1)
step('websocket channels after close','GET','/channels')
step('websocket journal after close','GET','/messages')
step('clear websocket messages','DELETE','/messages')
step('clear websocket mappings','DELETE','/mappings')
step('sse HTTP mapping','POST','/mappings',{'id':fixed,'request':{'urlPath':'/stream'},'response':{'openSseChannel':True}})
streams=[]
for base in bases:
 u=urllib.parse.urlsplit(base);c=http.client.HTTPConnection(u.hostname,u.port,timeout=3);c.request('GET','/stream',headers={'Host':'stream.example'});r=c.getresponse();assert r.status==200;streams.append((c,r))
for base in bases:
 deadline=time.monotonic()+2
 while not json.loads(call(base,'GET','/channels')[1])['channels'] and time.monotonic()<deadline:time.sleep(.01)
step('sse channel list','GET','/channels')
step('sse admin push','POST','/channels/send',{'type':'sse','initiatingRequest':{'urlPath':'/stream'},'message':{'body':'event'}})
for c,r in streams:
 while r.readline()!=b'\r\n':pass
step('sse sent journal','GET','/messages')
step('sse count channel','POST','/messages/count',{'channel':{'urlPath':'/stream'}})
for base in bases:
 for channel in json.loads(call(base,'GET','/channels')[1])['channels']:call(base,'DELETE','/channels/'+channel['id'])
for c,r in streams:r.close();c.close()
step('sse channels after close','GET','/channels')
step('sse journal after close','GET','/messages')
for base in bases:
 call(base,'DELETE','/mappings');call(base,'DELETE','/message-mappings');call(base,'DELETE','/messages')
report={'reference':'WireMock4.0.0-beta.40','comparison':'strict response bytes with generated UUID and timestamp scalar replacements','passed':sum(r['pass'] for r in results),'total':len(results),'results':results}
if a.output:open(a.output,'w').write(json.dumps(report,indent=2)+'\n')
print(f"{report['passed']}/{report['total']} passed");raise SystemExit(report['passed']!=report['total'])
