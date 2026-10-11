#!/usr/bin/env python3
"""Compare BreakMock against a running WireMock 4.0.0-beta.40 reference.
Bodies are compared byte-for-byte for stub responses; admin JSON is compared
semantically, excluding generated IDs and timestamps. This is a bounded corpus,
not a claim of complete WireMock compatibility.
"""
import argparse, base64, json, urllib.request, urllib.error, sys
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://localhost:18081');p.add_argument('--candidate',default='http://localhost:18082');p.add_argument('--output');a=p.parse_args()
def call(base,path,method='GET',body=None,headers=None):
    if isinstance(body,(dict,list)): body=json.dumps(body).encode();headers={'Content-Type':'application/json',**(headers or {})}
    if isinstance(body,str):body=body.encode()
    try:r=urllib.request.urlopen(urllib.request.Request(base+path,data=body,method=method,headers=headers or {}),timeout=10)
    except urllib.error.HTTPError as e:r=e
    return r.status,dict(r.headers),r.read()
reference_version=call(a.reference,'/__admin/version')
assert reference_version[0]==200 and json.loads(reference_version[2]).get('version')=='4.0.0-beta.40', ('Wrong reference server',reference_version)
def mapping(request,response=None,**extra):return {'request':request,'response':response or {'status':200,'body':'matched'},**extra}
cases=[]
def add(name,m,path,method='GET',body=None,headers=None):cases.append((name,m,path,method,body,headers))
add('exact URL',mapping({'method':'GET','url':'/hello'},{'status':201,'body':'Hello, world!','headers':{'Content-Type':'text/plain','X-Test':'yes'}}),'/hello')
add('URL path ignores query',mapping({'urlPath':'/hello'}),'/hello?a=1')
add('URL regular expression',mapping({'urlPattern':'/items/[0-9]+\\?x=.*'}),'/items/123?x=yes')
add('path regular expression',mapping({'urlPathPattern':'/items/[0-9]+'}),'/items/123?x=yes')
add('query matching',mapping({'urlPath':'/q','queryParameters':{'q':{'equalTo':'hello world'}}}),'/q?q=hello%20world')
add('header case insensitive names',mapping({'url':'/h','headers':{'x-token':{'equalTo':'abc'}}}),'/h',headers={'X-Token':'abc'})
add('case insensitive values',mapping({'url':'/h','headers':{'X-Token':{'equalTo':'ABC','caseInsensitive':True}}}),'/h',headers={'X-Token':'abc'})
add('cookie matching',mapping({'url':'/cookie','cookies':{'session':{'equalTo':'abc'}}}),'/cookie',headers={'Cookie':'session=abc; other=x'})
add('basic authentication',mapping({'url':'/auth','basicAuthCredentials':{'username':'u','password':'p'}}),'/auth',headers={'Authorization':'Basic '+base64.b64encode(b'u:p').decode()})
add('JSON equality',mapping({'method':'POST','url':'/json','bodyPatterns':[{'equalToJson':'{"a":1,"b":2}'}]}),'/json','POST','{"b":2,"a":1}')
add('JSON extra elements',mapping({'url':'/json','bodyPatterns':[{'equalToJson':'{"a":1}','ignoreExtraElements':True}]}),'/json','POST','{"b":2,"a":1}')
add('JSON unordered arrays',mapping({'url':'/json','bodyPatterns':[{'equalToJson':'[1,2]','ignoreArrayOrder':True}]}),'/json','POST','[2,1]')
add('JSONPath',mapping({'url':'/json','bodyPatterns':[{'matchesJsonPath':'$.user.name'}]}),'/json','POST','{"user":{"name":"Ada"}}')
add('XML equality',mapping({'url':'/xml','bodyPatterns':[{'equalToXml':'<a><b>1</b></a>'}]}),'/xml','POST','<a> <b>1</b> </a>')
add('XPath',mapping({'url':'/xml','bodyPatterns':[{'matchesXPath':'/a/b[text()="1"]'}]}),'/xml','POST','<a><b>1</b></a>')
add('binary response',mapping({'url':'/binary'},{'status':200,'base64Body':base64.b64encode(bytes(range(256))).decode()}),'/binary')
add('JSON response serialization',mapping({'url':'/json-response'},{'status':200,'jsonBody':{'z':1,'a':'<tag>','nested':[True,None]}}),'/json-response')
add('template request',mapping({'urlPath':'/template'},{'status':200,'body':'{{request.method}} {{request.path}} {{request.query.q}} {{request.headers.X-Test}}','transformers':['response-template']}),'/template?q=hello',headers={'X-Test':'world'})
add('template escaping',mapping({'urlPath':'/template'},{'status':200,'body':'{{request.query.q}}|{{{request.query.q}}}','transformers':['response-template']}),'/template?q=%3Ctag%3E%26%22')
add('template JSONPath',mapping({'url':'/template'},{'status':200,'body':"Hello {{jsonPath request.body '$.name'}}",'transformers':['response-template']}),'/template','POST','{"name":"Ada"}')
add('template math',mapping({'url':'/template'},{'status':200,'body':"{{math 2 '+' 3}}",'transformers':['response-template']}),'/template')
add('template each',mapping({'url':'/template'},{'status':200,'body':"{{#each (jsonPath request.body '$.names')}}{{this}};{{/each}}",'transformers':['response-template']}),'/template','POST','{"names":["Ada","Bob"]}')
add('template response header',mapping({'urlPath':'/template'},{'status':200,'body':'ok','headers':{'X-Test':'{{request.query.q}}'},'transformers':['response-template']}),'/template?q=value')
add('absent header',mapping({'url':'/absent','headers':{'X-Missing':{'absent':True}}}),'/absent')
add('negative contains',mapping({'url':'/negative','bodyPatterns':[{'doesNotContain':'bad'}]}),'/negative','POST','good')
add('logical OR',mapping({'url':'/or','bodyPatterns':[{'or':[{'equalTo':'yes'},{'equalTo':'ok'}]}]}),'/or','POST','ok')
results=[]
for name,m,path,method,body,headers in cases:
    responses=[]
    for base in [a.reference,a.candidate]:
        call(base,'/__admin/reset','POST')
        created=call(base,'/__admin/mappings','POST',m)
        if created[0]!=201:responses.append((created[0],{},b'mapping rejected: '+created[2]));continue
        responses.append(call(base,path,method,body,headers))
    expected,actual=responses
    checks={'status':expected[0]==actual[0],'body_bytes':expected[2]==actual[2]}
    for h in ['Content-Type','X-Test']:
        eh={k.lower():v for k,v in expected[1].items()}.get(h.lower());ah={k.lower():v for k,v in actual[1].items()}.get(h.lower());checks[h]=eh==ah
    result={'name':name,'passed':all(checks.values()),'checks':checks}
    if not result['passed']:result.update(reference={'status':expected[0],'body':repr(expected[2][:500]),'headers':expected[1]},candidate={'status':actual[0],'body':repr(actual[2][:500]),'headers':actual[1]})
    results.append(result)
    print(('PASS' if result['passed'] else 'FAIL')+' '+name)
# Admin semantics: UUID held fixed so mapping responses are comparable.
fixed=mapping({'url':'/admin-test'},id='11111111-1111-4111-8111-111111111111')
for base in [a.reference,a.candidate]:call(base,'/__admin/reset','POST');call(base,'/__admin/mappings','POST',fixed);call(base,'/admin-test')
for name,path,method,body in [('mapping list','/__admin/mappings','GET',None),('mapping get','/__admin/mappings/'+fixed['id'],'GET',None),('request count','/__admin/requests/count','POST',{'url':'/admin-test'}),('scenario list','/__admin/scenarios','GET',None)]:
    r=call(a.reference,path,method,body);c=call(a.candidate,path,method,body)
    try:rb=json.loads(r[2]);cb=json.loads(c[2]);ok=rb==cb
    except:ok=r[2]==c[2]
    result={'name':name,'passed':r[0]==c[0] and ok}
    if not result['passed']:result.update(reference=repr(r),candidate=repr(c))
    results.append(result);print(('PASS' if result['passed'] else 'FAIL')+' '+name)
report={'reference':'WireMock 4.0.0-beta.40','passed':sum(r['passed'] for r in results),'total':len(results),'results':results}
if a.output:
    with open(a.output,'w')as f:json.dump(report,f,indent=2)
print(f"{report['passed']}/{report['total']} passed")
sys.exit(0 if report['passed']==report['total'] else 1)
