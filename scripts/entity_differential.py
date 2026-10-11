#!/usr/bin/env python3
"""WireMock v4 entity bodies, charset encoding and templating: exact HTTP body bytes."""
import argparse,base64,http.client,json,pathlib,urllib.parse
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18407');p.add_argument('--candidate',default='http://127.0.0.1:18408');p.add_argument('--output',default='testdata/reports/entities.json');a=p.parse_args()
identify(a.reference,'java');identify(a.candidate,'go')
def call(base,path,body=None,method=None):
 u=urllib.parse.urlsplit(base);c=http.client.HTTPConnection(u.hostname,u.port,timeout=10)
 c.request(method or ('POST' if body is not None else 'GET'),path,json.dumps(body).encode() if body is not None else None,{'Content-Type':'application/json','Host':'example.test'})
 r=c.getresponse();data=r.read();result=(r.status,data);c.close();return result
cases=[]
for body in [{'filePath':'absent.txt'},{'dataStore':'unknown','dataRef':'unknown'},{'dataStore':'__files','dataRef':'absent.txt'}]:cases.append(('missing '+repr(body),{'body':body}))
for entity in ['hello',{'data':'hello'},{'data':'hello','format':'text'},{'data':'hi','format':'binary'},{'base64Data':'AP9BQkM='},{'base64Data':'w6k=','format':'text'},{'data':{'z':1,'a':[True,None,'x']}},{'data':'hello','compression':'gzip'}, {'data':'x','compression':'none'}]:
 cases.append((repr(entity),{'body':entity}))
for charset in ['UTF-8','ISO-8859-1','UTF-16','UTF-16BE','UTF-16LE','US-ASCII','windows-1252','Shift_JIS']:
 for fmt in ['text','binary']:cases.append((charset+' '+fmt,{'body':{'data':'café € 漢 🙂','charset':charset,'format':fmt}}))
for response in [{'jsonBody':{'z':1,'a':2},'body':'ignored','base64Body':'eA=='},{'base64Body':'eA==','body':'ignored'}, {'body':{'data':'{{request.method}}','format':'binary'},'transformers':['response-template']},{'base64Body':base64.b64encode(b'{{request.method}}').decode(),'transformers':['response-template']},{'body':{'data':'café {{request.path}}','charset':'ISO-8859-1'},'transformers':['response-template']},{'jsonBody':{'x':'{{request.headers.X-Missing}}'},'transformers':['response-template']}]:cases.append(('precedence/template '+repr(response),response))
results=[]
for name,response in cases:
 values=[]
 for base in [a.reference,a.candidate]:
  call(base,'/__admin/reset',{})
  created=call(base,'/__admin/mappings',{'id':'11111111-1111-4111-8111-111111111111','request':{'url':'/entity'},'response':response})
  result=call(base,'/entity') if created[0]==201 else created
  values.append({'status':result[0],'body_hex':result[1].hex()})
 ok=values[0]==values[1];results.append({'name':name,'passed':ok,**({} if ok else {'reference':values[0],'candidate':values[1]})});print(('PASS ' if ok else 'FAIL ')+name)
report={'reference':'WireMock 4.0.0-beta.40','comparison':'exact status and body bytes','passed':sum(r['passed'] for r in results),'total':len(results),'results':results};pathlib.Path(a.output).write_text(json.dumps(report,indent=2)+'\n');print(f"{report['passed']}/{report['total']}");raise SystemExit(report['passed']!=report['total'])
