#!/usr/bin/env python3
# Copyright 2026 Breaking-IT. Apache-2.0.
# Cases adapted from WireMock 3.13.2 response-templating helper tests,
# Copyright 2017-2025 Thomas Akehurst, Apache-2.0 (see upstream LICENSE/NOTICE).
"""Replay identical templates and request bytes against two dedicated servers.
Compares HTTP status and exact response-body bytes, without JSON normalization.
Known invalid-JSON diagnostic cases compare status only when the reference emits
HTTP 500: JVM stacktraces contain process-specific identities. These are counted
separately, never as exact-byte passes.
Does not compare Date/Server/transfer framing, random outputs, or entire WireMock suite.
Both servers' mappings and request journals are reset. Never use production servers.
"""
import argparse,json,urllib.request,urllib.error,pathlib,datetime
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18087');p.add_argument('--candidate',default='http://127.0.0.1:18088');p.add_argument('--discover',action='store_true',help='Include known unsupported helper coverage');p.add_argument('--report','--output',dest='report');a=p.parse_args()
def call(base,path,data=None,method=None,headers=None):
 if isinstance(data,dict):data=json.dumps(data).encode();headers={'Content-Type':'application/json',**(headers or {})}
 if isinstance(data,str):data=data.encode()
 try:r=urllib.request.urlopen(urllib.request.Request(base+path,data=data,method=method,headers=headers or {}),timeout=10)
 except urllib.error.HTTPError as e:r=e
 return r.status,r.read()
s,b=call(a.reference,'/__admin/version');assert s==200 and json.loads(b).get('version')=='3.13.2',('wrong reference',b)
cases=[]
def add(name,template,body='',path='/template-test?q=one&q=two',headers=None):cases.append((name,template,body,path,headers or {}))
for name,t in [
 ('method','{{request.method}}'),('path','{{request.path}}'),('path index','{{request.path.[0]}}'),('pathSegments','{{request.pathSegments.[0]}}'),
 ('query first','{{request.query.q}}'),('query index','{{request.query.q.[1]}}'),('query first last','{{request.query.q.first}}|{{request.query.q.last}}'),('query absent','{{request.query.none}}'),
 ('assign',"{{#assign 'x'}}hello{{/assign}}{{x}}"),('val default',"{{val missing default='fallback'}}"),('val or',"{{val missing or='fallback'}}"),('val assignment',"{{val 123 assign='x'}}{{x}}"),
 ('array',"{{array 'a' 'b'}}"),('array each',"{{#each (array 'a' 'b')}}{{@index}}={{this}};{{/each}}"),('range','{{range 1 3}}'),('range each','{{#each (range 1 3)}}{{this}}{{/each}}'),
 ('random integer fixed','{{randomInt lower=3 upper=4}}'),('random string empty',"{{randomValue length=0}}"),('pick one',"{{pickRandom 'only'}}"),
 ('math addition',"{{math 2 '+' 3}}"),('math subtraction',"{{math 8 '-' 3}}"),('math multiply',"{{math 2 '*' 3}}"),('math divide',"{{math 7 '/' 2}}"),('math modulus',"{{math 7 '%' 2}}"),
 ('trim',"{{trim '  hello  '}}"),('base64',"{{base64 'hello'}}"),('base64 decode',"{{base64 'aGVsbG8=' decode=true}}"),('url encode',"{{urlEncode 'a b&c'}}"),('url decode',"{{urlEncode 'a+b%26c' decode=true}}"),
 ('upper',"{{upper 'hello'}}"),('lower',"{{lower 'HELLO'}}"),('capitalize',"{{capitalize 'hello world'}}"),('capitalizeFirst',"{{capitalizeFirst 'hello world'}}"),('replace',"{{replace 'abcabc' 'a' 'z'}}"),('cut',"{{cut 'abcabc' 'a'}}"),('substring',"{{substring 'abcdef' 1 4}}"),
 ('size','{{size request.body}}'),('contains',"{{#if (contains 'hello' 'ell')}}yes{{else}}no{{/if}}"),('matches',"{{#if (matches 'abc' '[a-z]+')}}yes{{/if}}"),
 ('regex extract',"{{regexExtract 'abc123' '([0-9]+)'}}"),('regex multi',"{{regexExtract 'abc,def,ghi' '([A-Za-z]+)' 'parts'}}{{#each parts}}{{this}} {{/each}}"),
 ('arrayJoin',"{{arrayJoin ',' (array 'a' 'b') prefix='[' suffix=']'}}"),
 ('date parsed',"{{date (parseDate '2020-02-03' format='yyyy-MM-dd') format='yyyy/MM/dd'}}"),('date offset',"{{date (parseDate '2020-02-03' format='yyyy-MM-dd') format='yyyy-MM-dd' offset='2 days'}}"),
 ('date epoch',"{{date (parseDate '1970-01-01' format='yyyy-MM-dd') format='epoch'}}"),
 ('form data',"{{formData 'a=hello+world&b=2' 'form' urlDecode=true}}{{form.a}}|{{form.b}}"),
 ]:add(name,t)
add('escaping','{{request.query.q}}|{{{request.query.q}}}',path='/template-test?q=%3Ctag%3E%26%22')
add('headers','{{request.headers.X-Custom}}',headers={'X-Custom':'hello'})
add('headers arbitrary case','{{request.headers.x-CuStOm}}',headers={'X-Custom':'hello'})
add('cookies','{{request.cookies.session}}',headers={'Cookie':'session=abc'})
for name,t,body in [
 ('json string',"{{jsonPath request.body '$.name'}}",'{"name":"A&B"}'),('json array',"{{jsonPath request.body '$.items'}}",'{"items":[1,2]}'),('json filter',"{{jsonPath request.body '$.items[?(@.id == 2)].name'}}",'{"items":[{"id":2,"name":"two"}]}'),
 ('json object',"{{jsonPath request.body '$.obj'}}",'{"obj":{"a":"b","c":[1,2]}}'),('json default',"{{jsonPath request.body '$.missing' default='none'}}",'{}'),
 ('parseJson property',"{{#parseJson 'obj'}}{{{request.body}}}{{/parseJson}}{{obj.name}}",'{"name":"abc"}'),('toJson',"{{toJson (parseJson request.body)}}",'{"a":1,"b":[2,3]}'),('formatJson compact',"{{formatJson request.body format='compact'}}",'{ "a": 1 }'),('formatJson pretty',"{{formatJson request.body}}",'{"a":1,"b":[2,3]}'),
 ('xpath element',"{{xPath request.body '/a/b'}}",'<a><b>value</b></a>'),('xpath text',"{{xPath request.body '/a/b/text()'}}",'<a><b>foo &amp; bar</b></a>'),('xpath each',"{{#each (xPath request.body '/a/b')}}{{this.text}};{{/each}}",'<a><b>one</b><b>two</b></a>'),
]:add(name,t,body)
if a.discover:
 for name,t,body in [
 ('arrayAdd',"{{arrayAdd (array 'a' 'b') 'c'}}",''),('arrayRemove',"{{arrayRemove (array 'a' 'b') 'a'}}",''),('jsonMerge',"{{jsonMerge request.body '{\"b\":2}'}}",'{"a":1}'),('jsonRemove',"{{jsonRemove request.body '$.a'}}",'{"a":1,"b":2}'),('formatXml','{{formatXml request.body}}','<a><b>1</b></a>'),('truncateDate',"{{truncateDate (parseDate '2020-02-03' format='yyyy-MM-dd') 'first day of month'}}",''),('json order',"{{toJson (parseJson request.body)}}",'{"z":1,"a":2}'),('json invalid',"{{jsonPath request.body '$.a'}}",'not-json')]:add(name,t,body)
for name,t,body in [
 ('arrayAdd start',"{{arrayAdd (array 'a' 'b') 'c' position='start'}}",''),('arrayAdd middle',"{{arrayAdd (array 'a' 'b') 'c' position=1}}",''),('arrayRemove start',"{{arrayRemove (array 'a' 'b') position='start'}}",''),
 ('json merge deep',"{{jsonMerge request.body '{\"a\":{\"second\":2},\"z\":null}' removeNulls=true}}",'{"z":1,"a":{"first":1}}'),
 ('json merge block',"{{#jsonMerge request.body}}{\"b\":2}{{/jsonMerge}}",'{"a":1}'),
 ('json remove array',"{{jsonRemove request.body '$.items[1]'}}",'{"items":[1,2,3]}'),
 ('json remove missing',"{{jsonRemove request.body '$.missing'}}",'{ "a": 1 }'),
 ('json array add root',"{{jsonArrayAdd request.body '3'}}",'[1,2]'),
 ('json array add path',"{{jsonArrayAdd request.body '[3,4]' jsonPath='$.items' flatten=true maxItems=3}}",'{"items":[1,2]}'),
 ('json array add block',"{{#jsonArrayAdd request.body}}{\"z\":1,\"a\":2}{{/jsonArrayAdd}}",'[]'),
 ('json object iteration',"{{#each (parseJson request.body)}}{{@key}}={{this}};{{/each}}",'{"z":1,"a":2}'),
 ('json path nested order',"{{jsonPath request.body '$.obj'}}",'{"obj":{"z":1,"a":2}}'),
 ('json path wildcard order',"{{jsonPath request.body '$.*'}}",'{"z":1,"a":2}'),
 ('jsonPath parsed input',"{{jsonPath (parseJson request.body) '$.a'}}",'{"a":2}'),
 ('parseJson display','{{parseJson request.body}}','{"z":1,"a":[2,3]}'),
 ('xml compact',"{{formatXml request.body format='compact'}}",'<a>  <b>1</b> </a>'),
 ('xml empty',"{{formatXml request.body}}",'<a><b/></a>'),
 ('soap XPath',"{{soapXPath request.body 'test/value/text()'}}",'<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><test><value>hello</value></test></soap:Body></soap:Envelope>'),
 ('parseDate standalone',"{{parseDate '2020-02-03' format='yyyy-MM-dd'}}",''),
 ('date truncate last',"{{date (truncateDate (parseDate '2020-02-03' format='yyyy-MM-dd') 'last day of month') format='yyyy-MM-dd'}}",''),
 ('date timezone',"{{date (parseDate '2020-02-03T10:00:00Z') timezone='Europe/Amsterdam' format='yyyy-MM-dd HH:mm:ss'}}",''),
]:add(name,t,body)

for name,value in [('quotes','%22hello%22'),('html','%3Ctag%3E%26'),('backslash','a%5Cb'),('newline','a%0Ab')]:
 add('jsonBody template '+name,{'value':'{{request.query.q}}'},path='/template-test?q='+value)

failed=[]
records=[]
for name,t,body,path,headers in cases:
 results=[]
 for base in (a.reference,a.candidate):
  call(base,'/__admin/reset',method='POST')
  s,b=call(base,'/__admin/mappings',{'request':{'urlPath':'/template-test'},'response':{'status':200,**({'jsonBody':t} if isinstance(t,dict) else {'body':t}),'transformers':['response-template']}})
  results.append(call(base,path,body,headers=headers) if s==201 else ('mapping rejected',s,b))
 diagnostic = name in ('jsonBody template quotes','jsonBody template newline') and results[0][0]==500
 comparison='error_diagnostic_status_only' if diagnostic else 'status_and_exact_body_bytes'
 ok=results[0][0]==results[1][0] if diagnostic else results[0]==results[1]
 print(('PASS ' if ok else 'FAIL ')+name+(' [status only; JVM diagnostic body excluded]' if diagnostic else ''))
 record={'name':name,'passed':ok,'comparison':comparison,'reference_status':results[0][0],'candidate_status':results[1][0],'reference_body':results[0][-1][:1200].decode(errors='replace'),'candidate_body':results[1][-1][:1200].decode(errors='replace'),'reference_body_bytes':len(results[0][-1]),'candidate_body_bytes':len(results[1][-1])}
 records.append(record)
 if not ok:failed.append(name);print(' reference:',repr(results[0][-1][:300]),'\n candidate:',repr(results[1][-1][:300]))
summary={'reference_version':'3.13.2','timestamp_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'comparison':'Per-case comparison: exact status/body bytes except explicitly labeled JVM diagnostics','cases':len(cases),'passed':len(cases)-len(failed),'exact_body_pass':sum(r['passed'] and r['comparison']=='status_and_exact_body_bytes' for r in records),'diagnostic_status_pass':sum(r['passed'] and r['comparison']=='error_diagnostic_status_only' for r in records),'failed':failed}
summary['strict_passed']=summary['exact_body_pass']
summary['strict_failed']=len(cases)-summary['strict_passed']
summary['strict_unsupported']=[r['name'] for r in records if r['comparison']=='error_diagnostic_status_only']
print(json.dumps(summary))
if a.report:
 pathlib.Path(a.report).parent.mkdir(parents=True,exist_ok=True)
 pathlib.Path(a.report).write_text(json.dumps({**summary,'results':records},indent=2)+'\n')
raise SystemExit(bool(failed))
