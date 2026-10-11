#!/usr/bin/env python3
"""Strict gzip bytes compared against WireMock, including framing and DEFLATE.
The deterministic corpus spans Jetty/zlib buffers and compressible/random data.
"""
import argparse,base64,gzip,hashlib,http.client,json,urllib.parse
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18203');p.add_argument('--candidate',default='http://127.0.0.1:18204');p.add_argument('--output');p.add_argument('--capture-fixtures');args=p.parse_args()
def call(base,path,body=None,headers=None,method=None):
 u=urllib.parse.urlsplit(base);c=http.client.HTTPConnection(u.hostname,u.port,timeout=30)
 if isinstance(body,dict):body=json.dumps(body).encode();headers={'Content-Type':'application/json',**(headers or {})}
 c.request(method or ('POST' if body is not None else 'GET'),path,body,headers or {});r=c.getresponse();data=r.read();result=(r.status,dict((k.lower(),v)for k,v in r.getheaders()),data);c.close();return result
assert json.loads(call(args.reference,'/__admin/version')[2])['version']=='3.13.2'
def input_bytes(kind,size):
 if kind=='repeat':return (b'hello world '*((size+11)//12))[:size]
 if kind=='counter':return bytes(i%256 for i in range(size))
 x=0x12345678;out=bytearray()
 for _ in range(size):
  x^=(x<<13)&0xffffffff;x^=x>>17;x^=(x<<5)&0xffffffff;out.append(x&255)
 return bytes(out)
corpus=[(kind,size)for kind in ['repeat','counter','random']for size in [32,64,511,8191,8192,8193,65535,65536,65537,262144,1048576]]
results=[];fixtures=[]
for kind,size in corpus:
 data=input_bytes(kind,size);responses=[]
 mapping={'request':{'url':'/gzip-corpus'},'response':{'base64Body':base64.b64encode(data).decode(),'headers':{'Content-Type':'text/plain'}}}
 for base in [args.reference,args.candidate]:
  status,_,body=call(base,'/__admin/reset',b'');assert status==200
  status,_,body=call(base,'/__admin/mappings',mapping);assert status==201,(status,body)
  responses.append(call(base,'/gzip-corpus',headers={'Accept-Encoding':'gzip'}))
 ref,candidate=responses
 encoded=ref[1].get('content-encoding')=='gzip' and candidate[1].get('content-encoding')=='gzip'
 passed=ref[0]==candidate[0]==200 and encoded and ref[2]==candidate[2]
 integrity=encoded and gzip.decompress(ref[2])==data and gzip.decompress(candidate[2])==data
 row={'kind':kind,'size':size,'passed':passed and integrity,'decoded_equal':integrity,'reference_sha256':hashlib.sha256(ref[2]).hexdigest(),'candidate_sha256':hashlib.sha256(candidate[2]).hexdigest(),'reference_size':len(ref[2]),'candidate_size':len(candidate[2])};results.append(row)
 fixtures.append({'kind':kind,'size':size,'sha256':row['reference_sha256']})
 print(('PASS 'if row['passed']else'FAIL ')+kind+' '+str(size))

for name,accept,ctype,size,extra in (
 [("encoding "+v,v,"text/plain",100,{})for v in ['gzip','GZIP','gzip;q=0','gzip;q=0.00','gzip;q=0.001','gzip;q=0.5','*','*;q=1','br, gzip','gzip;q=0, *;q=1','identity','gzip;q=0.000','gzip;q=0.5, gzip;q=0','deflate, gzip;q=0.0','gzip; q=0']]
 +[("type "+v,'gzip',v,100,{})for v in ['application/octet-stream','application/pdf','application/zip','image/png','image/svg+xml','application/problem+json','text/event-stream','application/x-protobuf','application/gzip','application/x-gzip','application/x-bzip2','application/vnd.api+json','audio/mpeg','video/mp4','font/woff2','text/html; charset=utf-8','image/custom','audio/custom','TEXT/PLAIN']]
 +[("threshold "+str(n),'gzip','text/plain',n,{})for n in [0,1,30,31,32,33]]
 +[("inline threshold "+str(n),'gzip','text/plain',n,{})for n in [0,1,30,31,32,33]]
 +[("explicit length "+str(n),'gzip','text/plain',n,{'headers':{'Content-Type':'text/plain','Content-Length':str(n)}})for n in [0,1,31,32,100]]
 +[("method "+m,'gzip','text/plain',100,{'_method':m})for m in ['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS']]
 +[("excluded path "+ext,'gzip','text/plain',100,{'_path':'/file.'+ext})for ext in ['svgz','png','PNG','zip','mp3','pdf','txt']]
 +[("chunked gzip",'gzip','text/plain',4800,{'chunkedDribbleDelay':{'numberOfChunks':3,'totalDuration':30}})]
 ):
 data=input_bytes('repeat',size);responses=[]
 path=extra.get('_path','/gzip-corpus');method=extra.get('_method','GET')
 mapping={'request':{'url':path},'response':{'base64Body':base64.b64encode(data).decode(),'headers':{'Content-Type':ctype},**{k:v for k,v in extra.items() if not k.startswith('_')}}}
 if name.startswith('inline threshold'):
  mapping['response'].pop('base64Body');mapping['response']['body']=data.decode()
 for base in [args.reference,args.candidate]:
  call(base,'/__admin/reset',b'');assert call(base,'/__admin/mappings',mapping)[0]==201
  responses.append(call(base,path,headers={'Accept-Encoding':accept},method=method))
 ref,candidate=responses;passed=ref[0]==candidate[0] and ref[1].get('content-encoding')==candidate[1].get('content-encoding') and ref[2]==candidate[2]
 row={'name':name,'passed':passed,'reference_encoding':ref[1].get('content-encoding'),'candidate_encoding':candidate[1].get('content-encoding'),'reference_sha256':hashlib.sha256(ref[2]).hexdigest(),'candidate_sha256':hashlib.sha256(candidate[2]).hexdigest()};results.append(row)
 print(('PASS 'if passed else'FAIL ')+name)
 if not passed:print(json.dumps(row))
report={'reference':'WireMock 3.13.2','passed':sum(r['passed']for r in results),'total':len(results),'results':results}
if args.output:
 with open(args.output,'w')as f:json.dump(report,f,indent=2)
if args.capture_fixtures:
 with open(args.capture_fixtures,'w')as f:json.dump({'reference':'WireMock 3.13.2, OpenJDK system zlib 1.2.12','fixtures':fixtures},f,indent=2)
print(f"{report['passed']}/{report['total']} strict gzip cases pass")
raise SystemExit(report['passed']!=report['total'])
