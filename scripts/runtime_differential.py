#!/usr/bin/env python3
"""Deterministic runtime differential against WireMock 3.13.2.
Compares status, exact body bytes, and application response headers. Date,
server headers, generated IDs, and transport framing are excluded. Dedicated
servers are required: each case resets mappings, requests and settings.
"""
import argparse, base64, gzip, http.client, http.server, json, threading, time, urllib.parse
p=argparse.ArgumentParser();p.add_argument('--reference',default='http://127.0.0.1:18121');p.add_argument('--candidate',default='http://127.0.0.1:18122');p.add_argument('--output');args=p.parse_args()

def call(base,path,method='GET',body=None,headers=None):
    if isinstance(body,(dict,list)):body=json.dumps(body,ensure_ascii=False).encode();headers={'Content-Type':'application/json',**(headers or {})}
    if isinstance(body,str):body=body.encode()
    u=urllib.parse.urlsplit(base);conn=http.client.HTTPConnection(u.hostname,u.port,timeout=10)
    start=time.monotonic()
    try:
        conn.request(method,path,body,headers or {});r=conn.getresponse();payload=r.read()
        selected={}
        for k,v in r.getheaders():
            if k.lower() in ('content-type','content-encoding','x-result','set-cookie','x-upstream','x-extra'):selected.setdefault(k.lower(),[]).append(v)
        decoded=gzip.decompress(payload) if selected.get('content-encoding')==['gzip'] else payload
        return {'status':r.status,'body':base64.b64encode(payload).decode(),'decoded_body':base64.b64encode(decoded).decode(),'headers':selected,'elapsed_ms':(time.monotonic()-start)*1000}
    finally:conn.close()
def decode(result):return json.loads(base64.b64decode(result['body']))
version=decode(call(args.reference,'/__admin/version'));assert version.get('version')=='3.13.2',('wrong reference',version)
class Backend(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def handle(self):
        try:super().handle()
        except (ConnectionResetError,BrokenPipeError):pass
    def do_GET(self):self.respond()
    def do_POST(self):self.respond()
    def respond(self):
        body=self.rfile.read(int(self.headers.get('Content-Length','0')))
        data=json.dumps({'method':self.command,'path':self.path,'body':base64.b64encode(body).decode(),'extra':self.headers.get_all('X-Extra'),'remove':self.headers.get('X-Remove'),'host':self.headers.get('Host')},separators=(',',':')).encode()
        self.send_response(201)
        if self.path.startswith('/gzip'):
            data=gzip.compress(b'compressed upstream response',mtime=0);self.send_header('Content-Encoding','gzip');self.send_header('Content-Type','application/octet-stream')
        elif self.path.startswith('/redirect'):
            self.send_header('Location','/destination')
        else:self.send_header('Content-Type','application/json')
        self.send_header('X-Upstream','yes');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
    def log_message(self,*args):pass
backend=http.server.ThreadingHTTPServer(('127.0.0.1',0),Backend);threading.Thread(target=backend.serve_forever,daemon=True).start();proxy='http://127.0.0.1:'+str(backend.server_port)
results=[]
def mapping(response,request=None,**extra):return {'request':request or {'urlPath':'/runtime'},'response':{'status':200,**response},**extra}
def case(name,mappings,requests=None,settings=None,min_delay=None):
    observations=[]
    for base in (args.reference,args.candidate):
        call(base,'/__admin/reset','POST');call(base,'/__admin/settings','POST',settings or {'fixedDelay':0})
        creation=[]
        for m in mappings:
            res=call(base,'/__admin/mappings','POST',m);creation.append(res['status'])
        records=[]
        if all(code==201 for code in creation):
            for req in requests or [{'path':'/runtime'}]:
                req=dict(req)
                if 'admin' in req:
                    call(base,req.pop('admin'),req.pop('method','POST'),req.pop('body',None));continue
                records.append(call(base,**req))
        observations.append((creation,records))
    lhs,rhs=observations
    comparisons=[]
    for a,b in zip(lhs[1],rhs[1]):
        comparisons.append(a['status']==b['status'] and a['body']==b['body'] and a['headers']==b['headers'])
        if min_delay is not None:comparisons.append(a['elapsed_ms']>=min_delay and b['elapsed_ms']>=min_delay)
    passed=lhs[0]==rhs[0] and all(c==201 for c in lhs[0]) and len(lhs[1])==len(rhs[1]) and all(comparisons)
    result={'name':name,'passed':passed,'decoded_body_equal':[a['decoded_body']==b['decoded_body'] for a,b in zip(lhs[1],rhs[1])],'reference':lhs,'candidate':rhs};results.append(result)
    print(('PASS ' if passed else 'FAIL ')+name)
    if not passed:
        print(json.dumps({'name':name,'reference':[(r['status'],r['body'][:160],r['headers'])for r in lhs[1]],'candidate':[(r['status'],r['body'][:160],r['headers'])for r in rhs[1]]}))

try:
    case('lower priority wins',[mapping({'body':'first'},priority=10),mapping({'body':'second'},priority=1)])
    case('latest equal priority wins',[mapping({'body':'first'}),mapping({'body':'second'})])
    case('priority fallback',[mapping({'body':'fallback'},request={'method':'ANY'},priority=1000),mapping({'body':'specific'},request={'url':'/runtime'})],[{'path':'/runtime'},{'path':'/other'}])
    case('scenario transitions and reset',[mapping({'body':'started'},scenarioName='flow',requiredScenarioState='Started',newScenarioState='Done'),mapping({'body':'done'},scenarioName='flow',requiredScenarioState='Done')],[{'path':'/runtime'},{'path':'/runtime'},{'admin':'/__admin/scenarios/reset'},{'path':'/runtime'}])
    case('multiple response headers',[mapping({'body':'cookies','headers':{'Set-Cookie':['one=1; Path=/','two=2; Path=/'],'X-Result':['a','b']}})])
    case('binary all octets',[mapping({'base64Body':base64.b64encode(bytes(range(256))).decode(),'headers':{'Content-Type':'application/octet-stream'}})])
    for name,body in [('nested order',{'z':1,'a':'<tag>&','inner':{'z':True,'a':None}}),('unicode',{'text':'é漢字😀'}),('numeric',{'small':0.000001,'large':1e20})]:case('JSON '+name,[mapping({'jsonBody':body})])
    # Send raw mapping text to preserve alternative JSON spellings.
    for name,raw in [('escaped unicode',r'{"text":"\u0061\u00e9"}'),('number spelling','{"n":1e3,"zero":-0,"fraction":1.00}'),('duplicate keys','{"a":1,"b":2,"a":3}'),('escaped slash',r'{"path":"a\/b"}'),('precise integers','{"big":9007199254740993,"negative":-9223372036854775809}')]:
        case('JSON '+name,['{"request":{"url":"/runtime"},"response":{"jsonBody":'+raw+'}}'])
    for name,raw in [('tiny','{"v":1e-100}'),('large exponent','{"v":1e100}'),('negative zero decimals','{"a":-0.000,"b":-0.0e2}'),('scaled exponent','{"a":1.2300e2,"b":1.2300e-2}'),('unpaired high surrogate',r'{"v":"\uD800"}'),('unpaired low surrogate',r'{"v":"\uDC00"}'),('paired surrogate',r'{"v":"\uD83D\uDE00"}'),('separators','{"v":"\u2028\u2029"}')]:
        case('JSON edge '+name,['{"request":{"url":"/runtime"},"response":{"jsonBody":'+raw+'}}'])
    case('JSON template',[mapping({'jsonBody':{'z':'{{request.query.name}}','a':'fixed'},'transformers':['response-template']})],[{'path':'/runtime?name=Ada'}])
    case('template header array',[mapping({'body':'ok','headers':{'X-Result':['{{request.query.a}}','{{request.query.b}}']},'transformers':['response-template']})],[{'path':'/runtime?a=one&b=two'}])
    case('HEAD request',[mapping({'body':'hello world','headers':{'Content-Type':'text/plain'}})],[{'path':'/runtime','method':'HEAD'}])
    case('204 ignores body',[mapping({'status':204,'body':'ignored'})])
    case('global fixed delay',[mapping({'body':'delayed'})],settings={'fixedDelay':40},min_delay=30)
    case('local delay overrides global',[mapping({'body':'delayed','fixedDelayMilliseconds':40})],settings={'fixedDelay':200},min_delay=30)
    case('chunked dribble response bytes',[mapping({'body':'abcdefghijk','chunkedDribbleDelay':{'numberOfChunks':3,'totalDuration':30}})],min_delay=20)
    case('proxy raw path and query',[mapping({'proxyBaseUrl':proxy},request={'urlPathPattern':'.*'})],[{'path':'/a%2Fb?x=a%20b&x=2'}])
    case('proxy base path',[mapping({'proxyBaseUrl':proxy+'/base'},request={'urlPathPattern':'.*'})],[{'path':'/nested?q=one'}])
    case('proxy removes URL prefix',[mapping({'proxyBaseUrl':proxy,'proxyUrlPrefixToRemove':'/prefix'},request={'urlPathPattern':'.*'})],[{'path':'/prefix/nested?q=one'}])
    case('proxy binary request',[mapping({'proxyBaseUrl':proxy},request={'urlPathPattern':'.*'})],[{'path':'/body','method':'POST','body':bytes(range(256))}])
    case('proxy additional request headers',[mapping({'proxyBaseUrl':proxy,'additionalProxyRequestHeaders':{'X-Extra':'added'}},request={'urlPathPattern':'.*'})],[{'path':'/headers'}])
    case('proxy removes request headers',[mapping({'proxyBaseUrl':proxy,'removeProxyRequestHeaders':['x-remove']},request={'urlPathPattern':'.*'})],[{'path':'/headers','headers':{'X-Remove':'private'}}])
    case('proxy response header override',[mapping({'proxyBaseUrl':proxy,'headers':{'X-Upstream':'override'}},request={'urlPathPattern':'.*'})],[{'path':'/headers'}])
    case('proxy additional multivalue headers',[mapping({'proxyBaseUrl':proxy,'additionalProxyRequestHeaders':{'X-Extra':['first','second']}},request={'urlPathPattern':'.*'})],[{'path':'/headers'}])
    case('proxy remove then add header',[mapping({'proxyBaseUrl':proxy,'removeProxyRequestHeaders':['x-extra'],'additionalProxyRequestHeaders':{'X-Extra':'replacement'}},request={'urlPathPattern':'.*'})],[{'path':'/headers','headers':{'X-Extra':'original'}}])
    case('proxy additional host header',[mapping({'proxyBaseUrl':proxy,'additionalProxyRequestHeaders':{'Host':'example.test'}},request={'urlPathPattern':'.*'})],[{'path':'/headers'}])
    case('proxy hop-by-hop request header',[mapping({'proxyBaseUrl':proxy},request={'urlPathPattern':'.*'})],[{'path':'/headers','headers':{'Connection':'X-Remove','X-Remove':'hop-only'}}])
    case('gzip request matching',[mapping({'body':'decoded'},request={'url':'/runtime','bodyPatterns':[{'equalTo':'hello compressed'}]})],[{'path':'/runtime','method':'POST','body':gzip.compress(b'hello compressed',mtime=0),'headers':{'Content-Encoding':'gzip','Content-Type':'text/plain'}}])
    case('inline negotiated gzip',[mapping({'body':'hello world '*400,'headers':{'Content-Type':'text/plain'}})],[{'path':'/runtime','headers':{'Accept-Encoding':'gzip'}}])
    case('proxy gzip bytes',[mapping({'proxyBaseUrl':proxy},request={'urlPathPattern':'.*'})],[{'path':'/gzip','headers':{'Accept-Encoding':'gzip'}}])
finally:backend.shutdown();backend.server_close()
report={'reference':'WireMock 3.13.2','passed':sum(r['passed'] for r in results),'total':len(results),'results':results}
if args.output:
    with open(args.output,'w')as f:json.dump(report,f,indent=2)
print(f"{report['passed']}/{report['total']} passed")
raise SystemExit(report['passed']!=report['total'])
