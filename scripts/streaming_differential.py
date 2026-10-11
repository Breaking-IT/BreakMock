#!/usr/bin/env python3
# Copyright 2026 Breaking-IT. Apache-2.0.
# Native streaming cases derived from WireMock 4.0.0-beta.40 acceptance tests,
# Copyright 2025-2026 Thomas Akehurst, Apache-2.0.
"""Exact WebSocket frame and SSE event byte comparisons; isolated destructive fixtures.
Dynamic handshake headers and channel/journal IDs are not compared or normalized.
Each transport case compares the unchanged data frames received from both servers.
"""
import argparse,base64,datetime,http.client,json,pathlib,socket,struct,time,urllib.parse,urllib.request,urllib.error,zlib
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18403');p.add_argument('--candidate',default='http://127.0.0.1:18404');p.add_argument('--limit-reference');p.add_argument('--limit-candidate');p.add_argument('--output',default='testdata/reports/streaming.json');a=p.parse_args();identify(a.reference,'java');identify(a.candidate,'go')
def call(base,path,obj=None,method=None):
 data=None if obj is None else json.dumps(obj).encode()
 try:r=urllib.request.urlopen(urllib.request.Request(base+path,data=data,method=method,headers={'Content-Type':'application/json'}),timeout=5)
 except urllib.error.HTTPError as e:r=e
 return r.status,r.read()
def setup(base,kind='websocket',actions=None,trigger=None):
 status,body=call(base,'/__admin/channels')
 for channel in json.loads(body).get('channels',[]):call(base,'/__admin/channels/'+channel['id'],method='DELETE')
 for path in ['/mappings','/message-mappings','/messages']:call(base,'/__admin'+path,method='DELETE')
 status,body=call(base,'/__admin/mappings',{'request':{'urlPath':'/stream'},'response':{'openWebsocketChannel' if kind=='websocket' else 'openSseChannel':True}});assert status==201,(status,body)
 if actions is not None:
  status,body=call(base,'/__admin/message-mappings',{'trigger':trigger or {'type':'message'},'actions':actions});assert status==201,(status,body)
def send(body,headers=None):
 return {'type':'send','message':{'body':{'data':body},**({'headers':headers} if headers else {})}}
class WS:
 def __init__(self,base,compression=False):
  u=urllib.parse.urlsplit(base);self.s=socket.create_connection((u.hostname,u.port),timeout=3);self.f=self.s.makefile('rb')
  extension='Sec-WebSocket-Extensions: permessage-deflate\r\n' if compression else ''
  request=(f'GET /stream?q=Ada HTTP/1.1\r\nHost: stream.example\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: MDEyMzQ1Njc4OUFCQ0RFRg==\r\nSec-WebSocket-Version: 13\r\n{extension}\r\n').encode();self.s.sendall(request)
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
def ws_case(base,body='Hello',actions=None,data='hello',opcode=1,fragment=False,ping=False):
 setup(base,actions=actions or [send(body)])
 c=WS(base)
 try:
  if ping:c.write('ping',9);return c.frame()
  if fragment:c.write(data[:2],opcode,False);c.write(data[2:],0)
  else:c.write(data,opcode)
  frames=c.frame(); last=frames
  while last!=b'EOF' and not(last[0]&128):last=c.frame();frames+=last
  return frames
 finally:c.close()
def sse_case(base,message,http_trigger=False):
 action={'type':'send','message':message,'channelTarget':{'type':'request-initiated','channelType':'sse','requestPattern':{'urlPath':'/stream'}}}
 setup(base,'sse',actions=[action] if http_trigger else None,trigger={'type':'http-request','requestPattern':{'urlPath':'/trigger'}})
 if http_trigger:call(base,'/__admin/mappings',{'request':{'urlPath':'/trigger'},'response':{'body':'ok'}})
 u=urllib.parse.urlsplit(base);c=http.client.HTTPConnection(u.hostname,u.port,timeout=3);c.request('GET','/stream');r=c.getresponse();assert r.status==200
 def event():
  chunks=[]
  while True:
   line=r.readline();chunks.append(line)
   if not line or line==b'\r\n':return b''.join(chunks)
 first=event()
 try:
  if http_trigger:status,body=call(base,'/trigger')
  else:status,body=call(base,'/__admin/channels/send',{'type':'sse','initiatingRequest':{},'message':message})
  assert status==200,(status,body)
  return first+event()
 finally:r.close();c.close()
def ws_close(base,client=False):
 setup(base);c=WS(base)
 try:
  if client:c.write(struct.pack('!H',1000)+b'bye',8)
  else:
   for _ in range(100):
    status,body=call(base,'/__admin/channels');channels=[x for x in json.loads(body)['channels'] if x['type']=='websocket']
    if channels:break
    time.sleep(.005)
   status,body=call(base,'/__admin/channels/'+channels[-1]['id'],method='DELETE');assert status==200,(status,body)
  return c.frame()
 finally:c.close()
def ws_compressed_limit(base,size):
 setup(base);c=WS(base,compression=True)
 try:
  compressor=zlib.compressobj(wbits=-15);data=(compressor.compress(b'x'*size)+compressor.flush(zlib.Z_SYNC_FLUSH))[:-4]
  mask=b'ABCD';head=bytes([0xc1,128|len(data)]) if len(data)<126 else bytes([0xc1,128|126])+struct.pack('!H',len(data))
  c.s.sendall(head+mask+bytes(v^mask[i%4] for i,v in enumerate(data)));return c.frame()
 finally:c.close()
def ws_invalid(base,frame):
 setup(base);c=WS(base)
 try:c.s.sendall(frame);return c.frame()
 finally:c.close()
def ws_http_handshake(base,headers):
 setup(base);u=urllib.parse.urlsplit(base);c=http.client.HTTPConnection(u.hostname,u.port,timeout=3)
 c.request('GET','/stream',headers={'Host':'stream.example',**headers});r=c.getresponse();data=str(r.status).encode()+b'\n'+r.read();c.close();return data
def ws_precedence(base,newest=False):
 setup(base,actions=[send('old')])
 for priority,body in [(1,'priority'),(5,'new')]:
  call(base,'/__admin/message-mappings',{'priority':5 if newest else priority,'trigger':{'type':'message'},'actions':[send(body)]})
 c=WS(base)
 try:c.write('hello');return c.frame()
 finally:c.close()
def ws_request_matching(base):
 setup(base,actions=[send('matched')],trigger={'type':'message','channel':{'type':'websocket','initiatingRequestPattern':{'urlPath':'/stream','queryParameters':{'q':{'equalTo':'Ada'}}}},'message':{'body':{'matches':'hello.*'}}})
 c=WS(base)
 try:c.write('hello world');return c.frame()
 finally:c.close()
def ws_unmatched_http(base):
 setup(base,actions=[dict(send('unmatched'),channelTarget={'type':'request-initiated','channelType':'websocket','requestPattern':{}})],trigger={'type':'http-request','requestPattern':{'urlPath':'/not-mapped'}})
 c=WS(base)
 try:
  status,body=call(base,'/not-mapped');assert status==404,status
  return c.frame()
 finally:c.close()
def ws_http_stub(base):
 setup(base);stubid='823e64e0-cb6f-4abb-bfc3-000000000010'
 call(base,'/__admin/mappings',{'id':stubid,'request':{'urlPath':'/trigger'},'response':{'body':'ok'}})
 call(base,'/__admin/message-mappings',{'trigger':{'type':'http-stub','stubId':stubid},'actions':[dict(send('HTTP {{request.path}}'),channelTarget={'type':'request-initiated','channelType':'websocket','requestPattern':{'urlPath':'/stream'}})]})
 c=WS(base)
 try:call(base,'/trigger');return c.frame()
 finally:c.close()
def wait_for_channels(base,count):
 # Jetty may flush upgrade headers before its onOpen callback registers the channel.
 # Wait for the observable registration, then compare unchanged transport bytes.
 deadline=time.monotonic()+3
 while time.monotonic()<deadline:
  status,body=call(base,'/__admin/channels')
  if status==200 and len(json.loads(body).get('channels',[]))>=count:return
  time.sleep(.005)
 raise TimeoutError('streaming channels not registered')
def ws_broadcast(base):
 setup(base,actions=[dict(send('broadcast'),channelTarget={'type':'request-initiated','channelType':'websocket','requestPattern':{'urlPath':'/stream'}})])
 c,d=WS(base),WS(base)
 try:wait_for_channels(base,2);c.write('hello');return c.frame()+d.frame()
 finally:c.close();d.close()
def ws_sse_broadcast(base):
 actions=[]
 for typ,text in [('websocket','ws'),('sse','sse')]:actions.append(dict(send(text),channelTarget={'type':'request-initiated','channelType':typ,'requestPattern':{}}))
 setup(base,actions=actions);call(base,'/__admin/mappings',{'request':{'url':'/events'},'response':{'openSseChannel':True}})
 u=urllib.parse.urlsplit(base);client=http.client.HTTPConnection(u.hostname,u.port,timeout=3)
 client.request('GET','/events');stream=client.getresponse();initial=stream.read(8)
 c,d=WS(base),WS(base)
 try:
  wait_for_channels(base,3)
  c.write('broadcast');out=initial+c.frame()+d.frame();out+=stream.readline()+stream.readline()
  c.write('ping',9);d.write('ping',9);out+=c.frame()+d.frame();return out
 finally:c.close();d.close();stream.close();client.close()
def ws_no_open_trigger(base):
 setup(base,actions=[dict(send('on open'),channelTarget={'type':'request-initiated','channelType':'websocket','requestPattern':{}})],trigger={'type':'http-request','requestPattern':{'urlPath':'/stream'}})
 c=WS(base)
 try:
  c.s.settimeout(.15)
  try:return c.frame()
  except (socket.timeout,TimeoutError):return b'NO_FRAME'
 finally:c.close()
def sse_disconnected_channel(base):
 sse_case(base,{'body':{'data':'disconnect'}})
 time.sleep(.05)
 status,body=call(base,'/__admin/channels');channels=json.loads(body)['channels']
 result={'retained':len(channels),'open':channels[0]['open'] if channels else None}
 for channel in channels:call(base,'/__admin/channels/'+channel['id'],method='DELETE')
 return json.dumps(result,sort_keys=True).encode()
records=[]
def check(name,fn,comparison="exact_transport_bytes"):
 outputs=[]
 for base in [a.reference,a.candidate]:
  try:outputs.append({'bytes':base64.b64encode(fn(base)).decode()})
  except Exception as e:outputs.append({'error':str(e)})
 passed=outputs[0]==outputs[1] and 'error' not in outputs[0]
 print(('PASS ' if passed else 'FAIL ')+name,flush=True)
 if not passed:print(repr(outputs)[:600],flush=True)
 records.append({'name':name,'passed':passed,'comparison':comparison,'reference':outputs[0],'candidate':outputs[1]})
for body in ['hello','', 'Grüße 😀','line1\nline2','x'*130,'x'*66000]:check('websocket send length '+str(len(body)),lambda b,body=body:ws_case(b,body=body))
check('websocket fragmented input',lambda b:ws_case(b,body='{{message.body}}',data='fragmented',fragment=True))
check('websocket message/request/parameters template',lambda b:ws_case(b,actions=[dict(send('{{message.body}} {{request.query.q}} {{parameters.value}}'),transformerParameters={'value':'yes'})]))
check('websocket binary send',lambda b:ws_case(b,actions=[{'type':'send','message':{'body':{'data':'AAEC/w==','format':'binary'}}}]))
check('websocket binary receive',lambda b:ws_case(b,body='binary',data=b'\x00\xff',opcode=2))
check('websocket ping pong',lambda b:ws_case(b,ping=True))
for body in ['hello','', 'a\nb\n','a\rb\r\nc','😀']:
 check('sse body '+repr(body),lambda b,body=body:sse_case(b,{'body':{'data':body}}))
check('sse all event headers',lambda b:sse_case(b,{'body':{'data':'hello'},'headers':{'comment':'note','id':'42','event':'update','retry':'1000'}}))
check('sse repeated header first',lambda b:sse_case(b,{'body':{'data':'hello'},'headers':{'event':['first','second']}}))
check('sse binary data',lambda b:sse_case(b,{'body':{'data':'AAEC/w==','format':'binary'}}))
check('sse HTTP trigger all headers',lambda b:sse_case(b,{'body':{'data':'hello'},'headers':{'comment':'note','id':'42','event':'update','retry':'1000'}},True))
check('sse HTTP trigger repeated headers',lambda b:sse_case(b,{'body':{'data':'hello'},'headers':{'event':['first','second']}},True))
check('sse HTTP template body+headers',lambda b:sse_case(b,{'body':{'data':'{{request.path}}'},'headers':{'event':'{{request.method}}'}},True))
check('websocket client close echo',lambda b:ws_close(b,True))
check('websocket admin close',lambda b:ws_close(b))
check('websocket receive 65537 byte text',lambda b:ws_case(b,data='x'*65537,body='received'))
for name,frame in [('unmasked',b'\x81\x01a'),('reserved-bit',b'\xc1\x80ABCD'),('unknown-opcode',b'\x83\x80ABCD'),('fragmented-ping',b'\x09\x80ABCD'),('invalid-utf8',b'\x81\x81ABCD'+bytes([255^65])),('one-byte-close',b'\x88\x81ABCD'+bytes([1^65]))]:
 check('websocket protocol '+name,lambda b,frame=frame:ws_invalid(b,frame))
check('websocket ordinary HTTP no upgrade',lambda b:ws_http_handshake(b,{}))
check('websocket unsupported version',lambda b:ws_http_handshake(b,{'Upgrade':'websocket','Connection':'Upgrade','Sec-WebSocket-Version':'12','Sec-WebSocket-Key':'MDEyMzQ1Njc4OUFCQ0RFRg=='}))
check('websocket highest priority mapping',lambda b:ws_precedence(b))
check('websocket newest mapping same priority',lambda b:ws_precedence(b,True))
check('websocket initiating request and message patterns',ws_request_matching)
check('websocket HTTP-stub trigger',ws_http_stub)
check('websocket unmatched HTTP trigger',ws_unmatched_http)
check('websocket broadcast two channels',ws_broadcast)
for body in [{'data':'café','charset':'ISO-8859-1'},{'data':'hello {{message.body}}','format':'binary'},{'data':'AAEC/w==','encoding':'base64','format':'binary'}]:
 check('websocket typed entity '+repr(body),lambda b,body=body:ws_case(b,actions=[{'type':'send','message':{'body':body}}]))
check('websocket and SSE broadcast type filtering',ws_sse_broadcast)
check('SSE retains peer-disconnected channel until write or close',sse_disconnected_channel,'channel_lifecycle_observation')
check('HTTP trigger precedes new channel registration',ws_no_open_trigger,'bounded_no_frame_observation')
check('websocket action missing file error',lambda b:ws_case(b,actions=[{'type':'send','message':{'body':{'filePath':'absent-file'}}}]))
check('websocket action missing fixed channel error',lambda b:ws_case(b,actions=[dict(send('hello'),channelTarget={'type':'fixed-channel','channelName':'missing','providerName':'missing'})]))
check('websocket action unknown helper error',lambda b:ws_case(b,actions=[send('{{unknown 1}}')]))
if a.limit_reference and a.limit_candidate:
 identify(a.limit_reference,'java');identify(a.limit_candidate,'go')
 limited={a.reference:a.limit_reference,a.candidate:a.limit_candidate}
 check('configured WebSocket text message limit 8',lambda b:ws_case(limited[b],data='123456789'))
 check('configured WebSocket binary message limit 12',lambda b:ws_case(limited[b],data=b'0123456789abc',opcode=2))
 check('configured WebSocket idle timeout',lambda b:ws_invalid(limited[b],b''))
 for size in [100,10000]:check('compressed WebSocket decoded text limit '+str(size),lambda b,size=size:ws_compressed_limit(limited[b],size))
summary={'reference_version':'4.0.0-beta.40','timestamp_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'cases':len(records),'strict_passed':sum(x['passed'] and x['comparison']=='exact_transport_bytes' for x in records),'strict_failed':sum(not x['passed'] and x['comparison']=='exact_transport_bytes' for x in records),'lifecycle_passed':sum(x['passed'] and x['comparison']!='exact_transport_bytes' for x in records),'results':records}
pathlib.Path(a.output).parent.mkdir(parents=True,exist_ok=True);pathlib.Path(a.output).write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps({k:v for k,v in summary.items() if k!='results'}));raise SystemExit(bool(summary['strict_failed']))
