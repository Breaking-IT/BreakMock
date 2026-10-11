#!/usr/bin/env python3
"""Serve unmodified upstream WireMock mapping files on both servers."""
import argparse,json,urllib.request,urllib.error,pathlib
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://localhost:18111');p.add_argument('--candidate',default='http://localhost:18112');p.add_argument('--output',default='testdata/reports/fixtures.json');a=p.parse_args()
def request(base,path,method='GET',body=None):
 try:r=urllib.request.urlopen(urllib.request.Request(base+path,method=method,data=body,headers={'Accept':'text/plain'}))
 except urllib.error.HTTPError as e:r=e
 return r.status,r.read(),r.headers.get('Content-Type')
v=request(a.reference,'/__admin/version')[1]
assert v.decode().strip()=='3.13.2',v
for base in [a.reference,a.candidate]:request(base,'/__admin/mappings/reset','POST')
cases=[('GET','/remote-load/1',None),('GET','/remote-load/2',None),('GET','/image-file',None),('GET','/binary-file-from-header',None),('GET','/text-file',None),('GET','/text-file-from-header',None),('GET','/todo/items',None),('POST','/todo/items',b'Cancel newspaper subscription'),('GET','/todo/items',None)]
results=[]
for method,path,body in cases:
 r=request(a.reference,path,method,body);c=request(a.candidate,path,method,body);passed=r==c
 result={'name':method+' '+path,'passed':passed,'reference_status':r[0],'candidate_status':c[0],'body_bytes_equal':r[1]==c[1],'reference_body_length':len(r[1]),'candidate_body_length':len(c[1]),'reference_content_type':r[2],'candidate_content_type':c[2]};results.append(result);print(('PASS 'if passed else 'FAIL ')+result['name'])
report={'reference':'WireMock 3.13.2','source':'src/test/resources/remoteloader (unmodified)','passed':sum(x['passed']for x in results),'total':len(results),'results':results}
pathlib.Path(a.output).write_text(json.dumps(report,indent=2));raise SystemExit(report['passed']!=report['total'])
