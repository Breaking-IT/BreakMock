#!/usr/bin/env python3
"""Exact response bytes for unmatched requests and deterministic near-miss APIs."""
import argparse,json,urllib.request,urllib.error,difflib,pathlib,re
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18307');p.add_argument('--candidate',default='http://127.0.0.1:18308');p.add_argument('--output',default='testdata/reports/diagnostics.json');a=p.parse_args()
def call(base,path,body=None,method=None,headers=None):
 data=json.dumps(body).encode() if isinstance(body,(dict,list)) else body.encode() if isinstance(body,str) else body
 r=urllib.request.Request(base+path,data=data,method=method,headers={'Content-Type':'application/json','Accept-Encoding':'identity',**(headers or {})})
 try:f=urllib.request.urlopen(r,timeout=10)
 except urllib.error.HTTPError as e:f=e
 return f.status,f.read()
assert json.loads(call(a.reference,'/__admin/version')[1])['version']=='4.0.0-beta.40'
from oracle import identify
identify(a.reference,'java');identify(a.candidate,'go')
results=[]
JAVA_IDENTITY=re.compile(rb'\[B@[0-9a-f]{1,8}( *)')
def mask_identity(response):
 # WireMock prints a binary mismatch's actual body as Java's default byte[]
 # toString: "[B@" and a per-object JVM identity hash, never derived from the
 # request. Replace only that token, keeping its column width, so alignment and
 # every other byte are still compared. Each side must contain the Java shape.
 status,body=response
 masked,count=JAVA_IDENTITY.subn(lambda m:b'[B@#'.ljust(len(m.group(0))),body)
 return (status,masked),count
def case(name,patterns,path='/actual',method='GET',body=None,headers=None,extra=None,api=None,java_identity=False):
 responses=[]
 for base in [a.reference,a.candidate]:
  call(base,'/__admin/reset',{},'POST')
  for i,pattern in enumerate(patterns):
   mapping={'id':f'11111111-1111-4111-8111-{i+1:012d}','request':pattern,'response':{'status':200},**(extra or {})}
   status,data=call(base,'/__admin/mappings',mapping,'POST')
   assert status==201,(name,base,status,data)
  responses.append(call(base,api or path,body,method,headers))
 result={'name':name}
 if java_identity:
  masked=[mask_identity(r) for r in responses]
  ok=all(count==1 for _,count in masked) and masked[0][0]==masked[1][0]
  result['comparison']='exact bytes; JVM byte[] identity token masked width-for-width; both sides must contain one'
 else:ok=responses[0]==responses[1]
 result['pass']=ok
 if not ok:
  repeated=call(a.reference,api or path,body,method,headers)
  result['reference_repeat']={'status':repeated[0],'body':repeated[1].decode(errors='replace')}
  result['reference_is_nondeterministic']=repeated!=responses[0]
  result.update(reference={'status':responses[0][0],'body':responses[0][1].decode(errors='replace')},candidate={'status':responses[1][0],'body':responses[1][1].decode(errors='replace')})
  print(''.join(difflib.unified_diff(result['reference']['body'].splitlines(True),result['candidate']['body'].splitlines(True)))[:1800])
 results.append(result);print(('PASS ' if ok else 'FAIL ')+name)
case('empty server',[])
case('URL mismatch',[{'url':'/expected'}])
case('method mismatch',[{'url':'/actual','method':'POST'}])
case('path mismatch',[{'urlPath':'/expected'}],path='/actual?q=yes')
case('regex mismatch',[{'urlPattern':'/expected/.*'}])
case('path regex mismatch',[{'urlPathPattern':'/expected/.*'}])
case('template mismatch',[{'urlPathTemplate':'/expected/{id}'}])
case('regex question hint',[{'urlPattern':'/expected?query=yes'}])
case('invalid URL prefix',[{'url':'expected'}])
case('named mapping',[{'url':'/expected'}],extra={'name':'My expected mapping'})
case('narrow console',[{'url':'/expected'}],headers={'X-WireMock-Console-Width':'60'})
case('long URL wrap',[{'url':'/expected/'+'a'*120}],path='/actual/'+'z'*105)
case('header absent',[{'headers':{'X-Color':{'equalTo':'red'}}}])
case('header mismatch',[{'headers':{'X-Color':{'equalTo':'red'}}}],headers={'X-Color':'blue'})
case('header contains',[{'headers':{'X-Color':{'contains':'red'}}}],headers={'X-Color':'blue'})
case('query absent',[{'queryParameters':{'q':{'equalTo':'yes'}}}])
case('query mismatch',[{'queryParameters':{'q':{'equalTo':'yes'}}}],path='/actual?q=no')
case('query multivalue',[{'queryParameters':{'q':{'equalTo':'yes'}}}],path='/actual?q=no&q=maybe')
case('cookie mismatch',[{'cookies':{'session':{'equalTo':'expected'}}}],headers={'Cookie':'session=actual'})
case('form mismatch',[{'formParameters':{'key':{'equalTo':'expected'}}}],method='POST',body='key=actual',headers={'Content-Type':'application/x-www-form-urlencoded'})
case('text body mismatch',[{'bodyPatterns':[{'equalTo':'expected'}]}],method='POST',body='actual')
case('text body contains',[{'bodyPatterns':[{'contains':'expected'}]}],method='POST',body='actual')
case('binary body mismatch',[{'bodyPatterns':[{'binaryEqualTo':'YWJj'}]}],method='POST',body='actual',java_identity=True)
case('scenario mismatch',[{'url':'/actual'}],extra={'scenarioName':'flow','requiredScenarioState':'Ready'})
case('path parameter mismatch',[{'urlPathTemplate':'/actual/{id}','pathParameters':{'id':{'equalTo':'123'}}}],path='/actual/456')
case('closest URL distance',[{'url':'/nowhere'},{'url':'/actuaX'},{'url':'/somewhere'}])
case('near API empty',[],method='POST',body={'method':'GET','url':'/actual','headers':{}},api='/__admin/near-misses/request')
for name,pattern in [('URL',{'url':'/expected'}),('method',{'method':'POST'}),('header',{'headers':{'X-Test':{'equalTo':'hello'}}}),('query',{'queryParameters':{'q':{'equalTo':'yes'}}})]:
 case('near API '+name,[pattern],method='POST',body={'method':'GET','url':'/actual','headers':{}},api='/__admin/near-misses/request')
case('near API ranked',[{'url':'/nowhere'},{'url':'/actuaX'},{'url':'/somewhere'},{'url':'/a'}],method='POST',body={'method':'GET','url':'/actual','headers':{}},api='/__admin/near-misses/request')
case('JSON body mismatch',[{'bodyPatterns':[{'equalToJson':'{"x":1}'}]}],method='POST',body={'x':2})
case('XML body mismatch',[{'bodyPatterns':[{'equalToXml':'<root><x>1</x></root>'}]}],method='POST',body='<root><x>2</x></root>')
case('JSONPath absent',[{'bodyPatterns':[{'matchesJsonPath':'$.absent'}]}],method='POST',body={'x':2})
case('XPath absent',[{'bodyPatterns':[{'matchesXPath':'/root/absent'}]}],method='POST',body='<root><x>2</x></root>')
case('JSONPath nested mismatch',[{'bodyPatterns':[{'matchesJsonPath':{'expression':'$.x','equalTo':'1'}}]}],method='POST',body={'x':2})
case('header absent condition',[{'headers':{'X-Test':{'absent':True}}}],headers={'X-Test':'actual'})
case('body multiline wrap',[{'bodyPatterns':[{'equalTo':'long expected text '*12}]}],method='POST',body='long actual text '*12)
case('near API URLPattern',[{'urlPattern':'/expected/.*'}],method='POST',body={'method':'GET','url':'/actual','headers':{}},api='/__admin/near-misses/request')
case('near API full request',[{'url':'/expected'}],method='POST',body={'method':'POST','url':'/actual?q=yes','absoluteUrl':'http://example.com/actual?q=yes','headers':{'Zed':'one','Alpha':['two','three']},'bodyAsBase64':'aGVsbG8=','loggedDate':1000},api='/__admin/near-misses/request')
report={'reference':'4.0.0-beta.40','comparison':'exact status and body bytes; no substitutions','passed':sum(x['pass'] for x in results),'total':len(results),'results':results};pathlib.Path(a.output).write_text(json.dumps(report,indent=2)+'\n');print(f"{report['passed']}/{report['total']} strict passes");raise SystemExit(report['passed']!=report['total'])
