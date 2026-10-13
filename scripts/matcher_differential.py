#!/usr/bin/env python3
"""Compare matcher selection against WireMock. Requires two dedicated servers.
This corpus compares status/body for positive and negative matching; it does not
compare generated unmatched-request diagnostic bodies or prove full parity.
"""
import argparse, json, urllib.request, urllib.error
import intended_differences
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18085');p.add_argument('--candidate',default='http://127.0.0.1:18083');p.add_argument('--output');args=p.parse_args()
def call(base,path,body=None,headers=None,method=None):
    if isinstance(body,dict):body=json.dumps(body).encode();headers={'Content-Type':'application/json',**(headers or {})}
    if isinstance(body,str):body=body.encode()
    try:r=urllib.request.urlopen(urllib.request.Request(base+path,data=body,headers=headers or {},method=method),timeout=10)
    except urllib.error.HTTPError as e:r=e
    return r.status,r.read()
version_status,version_data=call(args.reference,'/__admin/version')
assert version_status==200 and json.loads(version_data).get('version')=='4.0.0-beta.40', ('Wrong reference server',version_data)
from oracle import identify
identify(args.reference,"java");identify(args.candidate,"go")
cases=[]
def add(name,pattern,body=None,path='/test',headers=None,method=None):cases.append((name,pattern,body,path,headers,method))
for op,val,body in [('equalTo','abc','abc'),('equalTo','abc','abcd'),('contains','bc','abc'),('doesNotContain','bc','abc'),('matches','a(?=b)b','ab'),('matches','(a)\\1','aa'),('matches','a','ba'),('doesNotMatch','a',''),('before','2026-01-01','2025-01-01'),('after','2026-01-01','2027-01-01')]:add(op+str(len(cases)),{'bodyPatterns':[{op:val}]},body)
for path in ['/test?q=1&q=2','/test?q=1','/test?q=2&q=1&q=3']:
 for op in ['includes','hasExactly']:add(op+path,{'queryParameters':{'q':{op:[{'equalTo':'1'},{'equalTo':'2'}]}}},path=path)
for pattern,body in [({'equalToJson':'{"a":"${json-unit.any-number}"}'},'{"a":2}'),({'equalToJson':'{"a":"${json-unit.any-number}"}'},'{"a":"2"}'),({'equalToJson':'{"a":"${json-unit.ignore-element}"}'},'{}'),({'equalToJson':'[1,2]','ignoreExtraElements':True},'[0,1,2]'),({'equalToJson':'[1,2]','ignoreExtraElements':True},'[1,0,2]'),({'matchesJsonPath':'$.a'},'{"a":[]}'),({'matchesJsonPath':{'expression':'$.a','equalTo':'true'}},'{"a":true}'),({'matchesJsonPath':'$.things[?(@.id == 2)]'},'{"things":[{"id":2}]}'),({'matchesJsonSchema':'{"type":"object","required":["a"]}'},'{"a":2}'),({'matchesJsonSchema':'{"type":"object","required":["a"]}'},'{}'),({'matchesXPath':{'expression':'/a/b/text()','equalTo':'yes'}},'<a><b>yes</b></a>'),({'matchesXPath':{'expression':'/a/b','equalToXml':'<b>yes</b>'}},'<a><b>yes</b></a>'),({'matchesXPath':'/x:a/x:b','xPathNamespaces':{'x':'urn:foo'}},'<a xmlns="urn:foo"><b>yes</b></a>'),({'equalToXml':'<a><b> yes </b></a>'},'<a><b>yes</b></a>')]:add('body '+str(len(cases)),{'bodyPatterns':[pattern]},body)
for op in ['doesNotMatch','doesNotContain']:add('missing '+op,{'headers':{'X-Missing':{op:'abc'}}})
add('template',{'urlPathTemplate':'/users/{id}','pathParameters':{'id':{'matches':'[0-9]+'}}},path='/users/12')
add('encoded path',{'urlPath':'/a%20b'},path='/a%20b')
# Cases adapted from WireMock 4.0.0-beta.40 EqualToXmlPatternTest, date matcher tests,
# and request matcher tests; retain upstream Apache 2.0 attribution in NOTICE.
for xml in ['<a><b>1</b><c>2</c></a>', '<a><c>2</c><b>1</b></a>', '<a><b>2</b><c>1</c></a>']:
 add('XML order '+str(len(cases)),{'bodyPatterns':[{'equalToXml':'<a><b>1</b><c>2</c></a>'}]},xml)
for flag in [True,False]:
 for xml in ['<a><b>1</b><b>2</b></a>','<a><b>2</b><b>1</b></a>']:
  add('XML same-name order '+str(len(cases)),{'bodyPatterns':[{'equalToXml':'<a><b>1</b><b>2</b></a>','ignoreOrderOfSameNode':flag}]},xml)
for expected,actual,options in [('<a>${xmlunit.ignore}</a>','<a>123</a>',{'enablePlaceholders':True}),('<a>${xmlunit.ignore}</a>','<a>123</a>',{'enablePlaceholders':False}),('<a>#{xmlunit.ignore}</a>','<a>123</a>',{'enablePlaceholders':True,'placeholderOpeningDelimiterRegex':'#\\{','placeholderClosingDelimiterRegex':'}'}),('<a x="1"/>','<a x="2"/>',{'exemptedComparisons':['ATTR_VALUE']}),('<a xmlns="urn:x"/>','<b:a xmlns:b="urn:x"/>',{}),('<a xmlns="urn:x"/>','<a xmlns="urn:y"/>',{}),('<a xmlns="urn:x"/>','<a xmlns="urn:y"/>',{'exemptedComparisons':['NAMESPACE_URI']}),('<a><!--hi--><b>hello</b></a>','<a><b>hello</b></a>',{}),('<a><![CDATA[hello]]></a>','<a>hello</a>',{})]:
 add('XML feature '+str(len(cases)),{'bodyPatterns':[{'equalToXml':expected,**options}]},actual)
for op in ['equalToDateTime','before','after']:
 for expected,actual,options in [('2026-01-01T00:00:00Z','2025-12-31T19:00:00-05:00',{}),('2026-01-01T00:00:00','2026-01-01T00:00:00-05:00',{}),('2026-01-01T00:00:00Z','1767225600',{'actualFormat':'unix'}),('2026-01-01T00:00:00Z','1767225600000',{'actualFormat':'epoch'}),('2026-01-01T00:00:00Z','2026-01-01T19:23:45Z',{'truncateActual':'first hour of day'}),('2026-01-01','01/01/2026',{'actualFormat':'dd/MM/yyyy'}),('now +2 days','2000-01-01T00:00:00Z',{}),('now -2 days','2100-01-01T00:00:00Z',{}),('now','2000-01-01T00:00:00Z',{'expectedOffset':1,'expectedOffsetUnit':'MONTHS'})]:
  add('date '+str(len(cases)),{'bodyPatterns':[{op:expected,**options}]},actual)
for body in ['{"suite":"a"}','{"suite":"b"}']:
 add('root JSONPath '+str(len(cases)),{'bodyPatterns':[{'matchesJsonPath':"$[?(@.suite == 'a')]"}]},body)
for pattern in [{'clientIp':{'equalTo':'127.0.0.1'}},{'clientIp':{'equalTo':'192.0.2.1'}},{'headers':{'X-Missing':{'absent':False}}}]:add('edge '+str(len(cases)),pattern)
for schema in [{'type':'number'},{'type':'string'},{'type':'object','properties':{'id':{'type':'integer'}},'required':['id'],'additionalProperties':False}]:
 for body in ['123','"hi"','{"id":1}','{"id":"1"}','{"id":1,"x":2}']:
  add('schema '+str(len(cases)),{'bodyPatterns':[{'matchesJsonSchema':schema}]},body)
for expression,nested in [('$.missing',{'absent':True}),('$.empty',{'absent':True}),('$.values',{'equalTo':'b'}),('$.values',{'equalToJson':'["a","b"]'}),('$.values[*]',{'equalToJson':'["a","b"]'}),('$.values[*]',{'equalTo':'b'}),('$.nothing',{'absent':True})]:
 add('JSONPath array '+str(len(cases)),{'bodyPatterns':[{'matchesJsonPath':{'expression':expression,**nested}}]},'{"values":["a","b"],"empty":[],"nothing":null}')
multipart_body='--test-boundary\r\nContent-Disposition: form-data; name="first"; filename="one.txt"\r\nContent-Type: text/plain\r\n\r\none\r\n--test-boundary\r\nContent-Disposition: form-data; name="second"; filename="two.txt"\r\nContent-Type: text/plain\r\n\r\ntwo\r\n--test-boundary--\r\n'
for mode in ['ANY','ALL']:
 for body_pattern in [{'equalTo':'one'},{'contains':'o'},{'equalTo':'missing'}]:
  add('multipart '+str(len(cases)),{'multipartPatterns':[{'matchingType':mode,'bodyPatterns':[body_pattern]}]},multipart_body,headers={'Content-Type':'multipart/form-data; boundary=test-boundary'})
for filename in ['one.txt','two.txt','missing.txt']:
 add('multipart file '+str(len(cases)),{'multipartPatterns':[{'matchingType':'ANY','fileName':filename,'bodyPatterns':[{'contains':'o'}]}]},multipart_body,headers={'Content-Type':'multipart/form-data; boundary=test-boundary'})
for body_pattern in [{'binaryEqualTo':'b25l'},{'and':[{'contains':'o'},{'doesNotContain':'x'}]}]:
 add('multipart binary '+str(len(cases)),{'multipartPatterns':[{'matchingType':'ANY','bodyPatterns':[body_pattern]}]},multipart_body,headers={'Content-Type':'multipart/form-data; boundary=test-boundary'})
for pattern in [{'headers':{'content-type':{'contains':'text/'}}},{'headers':{'Content-Disposition':{'contains':'name="first"'}}},{'headers':{'missing':{'absent':True}}}]:
 add('multipart headers '+str(len(cases)),{'multipartPatterns':[{'matchingType':'ANY',**pattern}]},multipart_body,headers={'Content-Type':'multipart/form-data; boundary=test-boundary'})
for expression,nested in [('/a/b',{'equalTo':'<b>yes</b>\n'}),('/a',{'equalTo':'<a>\n  <b>yes</b>\n</a>\n'}),('count(/a/b)',{'equalTo':'1.0'}),('boolean(/a/b)',{'equalTo':'true'})]:
 add('XPath serialization '+str(len(cases)),{'bodyPatterns':[{'matchesXPath':{'expression':expression,**nested}}]},'<a><b>yes</b></a>')
for bp in [{'equalTo':''},{'binaryEqualTo':''},{'contains':''},{'matches':'.*'},{'absent':True},{'not':{'contains':'x'}}]:
 add('empty body '+str(len(cases)),{'bodyPatterns':[bp]},'')
for value in ['hello+world','hello%20world','no','hello+world&x=extra']:
 add('form '+str(len(cases)),{'formParameters':{'x':{'equalTo':'hello world'}}},'x='+value,headers={'Content-Type':'application/x-www-form-urlencoded'})
for headers in [{'X-Test':'yes'},{'X-Test':'no'},{}]:
 add('header logical '+str(len(cases)),{'headers':{'X-Test':{'or':[{'equalTo':'yes'},{'absent':True}]}}},headers=headers)
for expression in ['/a/@id','string(/a/b)','count(/a/b)']:
 add('XPath primitive '+str(len(cases)),{'bodyPatterns':[{'matchesXPath':{'expression':expression,'equalTo':'1'}}]},'<a id="1"><b>1</b></a>')
from matcher_extended_cases import add_extended
add_extended(add)
for key in ['oneOf','noneOf']:
 for methods in [[],['GET'],['GET','POST'],['ANY'],['QUERY']]:
  for actual in ['GET','POST','QUERY']:add('v4 method '+key+str(methods)+actual,{'method':{key:methods}},method=actual)
for operator in ['equalToNumber','greaterThanNumber','greaterThanEqualNumber','lessThanNumber','lessThanEqualNumber']:
 for actual in ['1','1.0','0','2','-1','1e0','1.0f',' 1 ','NaN','Infinity','0x1.0p0','nonsense']:
  add('v4 number '+operator+actual,{'bodyPatterns':[{operator:1}]},actual)
for expression in ['$.empty','$.obj','$.missing','$.values[*]']:
 for nested in [None,{'equalTo':'null'},{'absent':True}]:add('v4 JSONPath empty/null '+expression+str(nested),{'bodyPatterns':[{'matchesJsonPath':expression if nested is None else {'expression':expression,**nested}}]},'{"empty":[],"obj":{},"values":[null]}')
failed=[]
results=[]
for name,pattern,body,path,headers,method in cases:
 responses=[]
 for base in [args.reference,args.candidate]:
  call(base,'/__admin/reset',method='POST')
  call(base,'/__admin/mappings',{'priority':1000,'request':{'method':'ANY'},'response':{'status':200,'body':'unmatched'}})
  status,data=call(base,'/__admin/mappings',{'request':pattern,'response':{'status':200,'body':'matched'}})
  if status!=201:responses.append(('rejected',status,data));continue
  status,data=call(base,path,body,headers,method);responses.append((status,data))
 ok=responses[0]==responses[1]
 intended=intended_differences.check('matcher_differential.py',name,*responses) if all(len(r)==2 for r in responses) else None
 if intended:ok=intended[0]
 results.append({'name':name,'passed':ok,'reference':repr(responses[0]),'candidate':repr(responses[1]),'request':pattern,'body':body,'path':path,**({'intended_difference':intended[1]} if intended else {})})
 print(('PASS ' if ok else 'FAIL ')+name+(f' [intended difference {intended[1]}]' if intended else ''))
 if not ok:failed.append((name,responses));print(responses)
print(f'{len(cases)-len(failed)}/{len(cases)} matched reference')
if args.output:
 with open(args.output,'w') as f:json.dump({'reference':'WireMock 4.0.0-beta.40','passed':len(cases)-len(failed),'total':len(cases),'results':results},f,indent=2)
raise SystemExit(bool(failed))
