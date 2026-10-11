#!/usr/bin/env python3
"""Compare complete TCP response bytes and reset behavior for WireMock faults."""
import argparse,json,socket,urllib.request,urllib.parse,pathlib
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18307');p.add_argument('--candidate',default='http://127.0.0.1:18308');p.add_argument('--output',default='testdata/reports/faults.json');a=p.parse_args();identify(a.reference,'java');identify(a.candidate,'go')
def post(base,path,obj):
 with urllib.request.urlopen(urllib.request.Request(base+path,json.dumps(obj).encode(),headers={'Content-Type':'application/json'}))as r:return r.read()
def raw(base,version,connection,method):
 u=urllib.parse.urlsplit(base);s=socket.create_connection((u.hostname,u.port),timeout=5);s.sendall(f'{method} /fault {version}\r\nHost: example.com\r\n{connection}\r\n'.encode());data=b'';reset=False
 try:
  while True:
   chunk=s.recv(65536)
   if not chunk:break
   data+=chunk
 except ConnectionResetError:reset=True
 finally:s.close()
 return {'bytes_hex':data.hex(),'reset':reset}
results=[]
for fault in ['EMPTY_RESPONSE','CONNECTION_RESET_BY_PEER','RANDOM_DATA_THEN_CLOSE','MALFORMED_RESPONSE_CHUNK']:
 for version,connection in [('HTTP/1.1',''),('HTTP/1.1','Connection: close\r\n'),('HTTP/1.1','Connection: keep-alive\r\n'),('HTTP/1.0','')]:
  for method in ['GET','HEAD']:
   responses=[]
   for base in [a.reference,a.candidate]:
    post(base,'/__admin/reset',{});post(base,'/__admin/mappings',{'id':'11111111-1111-4111-8111-111111111111','request':{'url':'/fault'},'response':{'status':202,'headers':{'X-Test':'ignored'},'fault':fault}});responses.append(raw(base,version,connection,method))
   ok=responses[0]==responses[1];name=f'{fault} {version} {connection.strip()} {method}';results.append({'name':name,'pass':ok,'reference':responses[0],'candidate':responses[1]});print(('PASS 'if ok else 'FAIL ')+name)
report={'comparison':'complete socket bytes plus connection reset, no normalization','reference':'WireMock 4.0.0-beta.40','passed':sum(r['pass']for r in results),'total':len(results),'results':results};pathlib.Path(a.output).write_text(json.dumps(report,indent=2)+'\n');print(f"{report['passed']}/{report['total']} strict passes");raise SystemExit(report['passed']!=report['total'])
