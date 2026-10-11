#!/usr/bin/env python3
"""Compare webhook-generated method, target, configured headers and binary body.
Transport-generated headers are reported separately and never claimed byte-identical.
All callbacks target this script's loopback server. Both mock servers are reset.
"""
import argparse,base64,json,queue,threading,time,urllib.request
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18301');p.add_argument('--candidate',default='http://127.0.0.1:18302');p.add_argument('--output');a=p.parse_args()
identify(a.reference,'java');identify(a.candidate,'go');events=queue.Queue()
class Handler(BaseHTTPRequestHandler):
 def capture(self):
  body=self.rfile.read(int(self.headers.get('Content-Length',0)))
  events.put({'method':self.command,'path':self.path,'headers':list(self.headers.items()),'bodyBase64':base64.b64encode(body).decode()})
  self.send_response(202);self.end_headers();self.wfile.write(b'accepted')
 do_GET=do_POST=do_PUT=do_PATCH=do_DELETE=capture
 def log_message(self,*args):pass
server=ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start()
def call(base,method,path,body=None):
 req=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,method=method,headers={'Content-Type':'application/json','X-Origin':'original-value'})
 with urllib.request.urlopen(req,timeout=5) as r:return r.read()
results=[];transport=[]
cases=[('listener text',{'method':'POST','body':'{{originalRequest.method}} {{originalRequest.path}} {{parameters.extra}}','extra':'value'},False),('legacy text',{'method':'PUT','body':'{{originalRequest.headers.X-Origin}}'},True),('binary',{'method':'PATCH','base64Body':'AP9hYg=='},False),('GET ignores body',{'method':'GET','body':'ignored'},False),('DELETE ignores body',{'method':'DELETE','body':'ignored'},False),('missing method rejects callback',{},False),('fixed delay',{'method':'POST','body':'delay','delay':{'type':'fixed','milliseconds':40}},False)]
try:
 for name,options,legacy in cases:
  captured=[]
  for base in (a.reference,a.candidate):
   call(base,'POST','/__admin/reset')
   params={'url':f'http://127.0.0.1:{server.server_port}/callback/{{{{parameters.suffix}}}}','suffix':'end','headers':{'X-Original':['{{originalRequest.path}}','two']},**options}
   mapping={'request':{'url':'/trigger?key=value'},'response':{'body':'ok'}}
   if legacy:mapping['postServeActions']={'webhook':params}
   else:mapping['serveEventListeners']=[{'name':'webhook','parameters':params}]
   call(base,'POST','/__admin/mappings',mapping);start=time.monotonic();call(base,'GET','/trigger?key=value')
   try:event=events.get(timeout=.25 if name=='missing method rejects callback' else 3)
   except queue.Empty:event={'error':'No callback received'}
   elapsed=time.monotonic()-start
   if 'headers' in event:
    transport.append({'case':name,'server':base,'headers':event['headers']})
    event['headers']=[pair for pair in event['headers'] if pair[0].lower().startswith('x-')]
   if 'delay' in options:event['delayRespected']=elapsed>=.035
   captured.append(event)
  ok=captured[0]==captured[1] and ('error' not in captured[0] or name=='missing method rejects callback' and captured[0].get('error')=='No callback received');results.append({'name':name,'pass':ok,**({} if ok else {'reference':captured[0],'candidate':captured[1]})});print(('PASS ' if ok else 'FAIL ')+name)
finally:
 server.shutdown()
 for base in (a.reference,a.candidate):call(base,'POST','/__admin/reset')
report={'comparison':'Exact generated method, target, configured header values/order and base64 body; transport defaults reported separately','passed':sum(r['pass'] for r in results),'total':len(results),'results':results,'transportHeaders':transport}
if a.output:open(a.output,'w').write(json.dumps(report,indent=2)+'\n')
print(f"{report['passed']}/{report['total']} passed");raise SystemExit(report['passed']!=report['total'])
