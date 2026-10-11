#!/usr/bin/env python3
"""Compare custom HTTP status line bytes and bodies, including v4 ignoring configured statusMessage."""
import argparse,http.client,json,pathlib,socket,urllib.parse,urllib.request
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18407');p.add_argument('--candidate',default='http://127.0.0.1:18408');p.add_argument('--output',default='testdata/reports/status.json');a=p.parse_args();identify(a.reference,'java');identify(a.candidate,'go')
def post(base,path,obj):
 with urllib.request.urlopen(urllib.request.Request(base+path,json.dumps(obj).encode(),headers={'Content-Type':'application/json'}))as r:return r.read()
def get(base):
 u=urllib.parse.urlsplit(base);s=socket.create_connection((u.hostname,u.port),timeout=3);s.sendall(b'GET /reason HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n');f=s.makefile('rb');line=f.readline();headers={}
 while True:
  h=f.readline()
  if h==b'\r\n':break
  k,v=h.split(b':',1);headers[k.lower()]=v.strip()
 body=f.read();f.close();s.close()
 return {'status_line_hex':line.hex(),'body_hex':body.hex()}
results=[]
for status,reason in [(209,v) for v in ['Custom Reason','','café','line\r\nbreak','漢字','🙂','tab\there','control\x01\x7f']]+[(n,'ignored') for n in [200,203,413,418,425,451,500,506,599]]:
 values=[]
 for base in [a.reference,a.candidate]:
  post(base,'/__admin/reset',{});post(base,'/__admin/mappings',{'request':{'url':'/reason'},'response':{'status':status,'statusMessage':reason,'body':'body','headers':{'Content-Length':'4'}}});values.append(get(base))
 ok=values[0]==values[1];results.append({'name':str(status)+' '+repr(reason),'passed':ok,**({} if ok else {'reference':values[0],'candidate':values[1]})})
report={'reference':'WireMock 4.0.0-beta.40','comparison':'exact status line and body bytes','passed':sum(r['passed']for r in results),'total':len(results),'results':results};pathlib.Path(a.output).write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report,indent=2));raise SystemExit(report['passed']!=report['total'])
