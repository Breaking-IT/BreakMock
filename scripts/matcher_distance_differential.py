#!/usr/bin/env python3
"""Compare raw near-miss responses, including structural matcher distances."""
import argparse,base64,json,pathlib,urllib.request
from oracle import identify
p=argparse.ArgumentParser();p.add_argument('--reference',required=True);p.add_argument('--candidate',required=True);p.add_argument('--output',default='testdata/reports/matcher-distance.json');args=p.parse_args()
identify(args.reference,'java');identify(args.candidate,'go')
def call(base,path,data):
 return urllib.request.urlopen(urllib.request.Request(base+path,data=json.dumps(data).encode(),headers={'Content-Type':'application/json','Accept-Encoding':'identity'})).read()
cases=[]
for expected,actual in [('1','2'),('{"x":1}','{"x":2}'),('{"x":1,"y":2}','{"x":2,"y":3}'),('{"x":1}','{}'),('{}','{"x":1}'),('[1,2]','[1]'),('[1]','[1,2]'),('{"a":{"x":1}}','{}'),('{"a":{"x":1}}','{"a":3}'),('[1,2]','[2,1]'),('[1,2,3]','[2,3,4]'),('[{"a":1},{"b":2}]','[{"b":3},{"a":2}]')]:
 for options in [{},{'ignoreArrayOrder':True},{'ignoreExtraElements':True},{'ignoreArrayOrder':True,'ignoreExtraElements':True}]:cases.append(({'equalToJson':expected,**options},actual))
for expected,actual in [('<a/>','<b/>'),('<a>1</a>','<a>2</a>'),('<a><b>1</b></a>','<a><b>2</b></a>'),('<a x="1"/>','<a x="2"/>'),('<a x="1"/>','<a/>'),('<a/>','<a x="2"/>'),('<a><b/><c/></a>','<a><c/></a>'),('<a><b/></a>','<a><b/><c/></a>'),('<a><b/><c/></a>','<a><c/><b/></a>'),('<a xmlns="urn:a"/>','<a xmlns="urn:b"/>'),('<a>${xmlunit.ignore}</a>','<a>2</a>'),('<a><!--hi-->1</a>','<a>2</a>'),('<a><![CDATA[1]]></a>','<a>2</a>')]:
 for options in [{},{'exemptedComparisons':['TEXT_VALUE']},{'enablePlaceholders':True}]:cases.append(({'equalToXml':expected,**options},actual))
for n in [1001,2001,4001]:cases.append(({"equalTo":"a"*n},"a"*(n-1)+"b"))
for expected,actual in [("<?pi one?><a/>","<?pi two?><a/>"),("<a><?pi one?></a>","<a><?pi two?></a>")]:cases.append(({"equalToXml":expected},actual))
for attr in ["schemaLocation","noNamespaceSchemaLocation"]:
 for opts in [{},{"exemptedComparisons":["SCHEMA_LOCATION","NO_NAMESPACE_SCHEMA_LOCATION"]}]:cases.append(({"equalToXml":f'<a xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:{attr}="one.xsd"/ >'.replace("/ >","/>"),**opts},f'<a xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:{attr}="two.xsd"/ >'.replace("/ >","/>")))
for op in ["equalToNumber","greaterThanNumber","greaterThanEqualNumber","lessThanNumber","lessThanEqualNumber"]:
 for actual in ["0","1","2","-1","NaN","Infinity","1000000000000000000000000000000000000000000"]:cases.append(({op:1},actual))
for expression in ['$.a','$.values[*]','$.absent']:
 for expected in ['hello','world']:cases.append(({'matchesJsonPath':{'expression':expression,'equalTo':expected}},'{"a":"jello","values":["worlds","yellow"]}'))
for expression in ['/a/b/text()','/a/b','/missing']:
 cases.append(({'matchesXPath':{'expression':expression,'equalTo':'hello'}},'<a><b>jello</b><b>world</b></a>'))
results=[]
for i,(pattern,actual) in enumerate(cases):
 responses=[]
 for base in [args.reference,args.candidate]:
  call(base,'/__admin/reset',{})
  call(base,'/__admin/mappings',{'id':'11111111-1111-4111-8111-111111111111','request':{'bodyPatterns':[pattern]},'response':{'status':200}})
  responses.append(call(base,'/__admin/near-misses/request',{'method':'POST','url':'/','headers':{},'bodyAsBase64':base64.b64encode(actual.encode()).decode()}))
 ok=responses[0]==responses[1];distances=[json.loads(x)['nearMisses'][0]['matchResult']['distance'] for x in responses]
 result={'name':str(i),'passed':ok,'pattern':pattern,'actual':actual,'distances':distances};results.append(result)
 if not ok:print('FAIL',result)
report={'reference':'WireMock 4.0.0-beta.40','comparison':'exact near-miss response bytes','passed':sum(x['passed'] for x in results),'total':len(results),'results':results};pathlib.Path(args.output).write_text(json.dumps(report,indent=2)+'\n');print(f"{report['passed']}/{report['total']}");raise SystemExit(report['passed']!=report['total'])
