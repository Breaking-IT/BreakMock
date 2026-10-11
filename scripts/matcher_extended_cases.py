"""Additional cases derived from WireMock matching tests and Java matcher semantics."""
def add_extended(add):
 def body(name,pattern,values):
  for i,value in enumerate(values):add(name+' '+str(i),{'bodyPatterns':[pattern]},value)
 body('upstream JSONPath decimal',{'matchesJsonPath':"$.numbers[?(@.decimal == '2.3000')]"},['{"numbers":[{"decimal":2.3}]}','{"numbers":[{"decimal":2.3000}]}','{"numbers":[{"decimal":3.2}]}'])
 body('upstream JSONPath numeric regex',{'matchesJsonPath':'$.numbers[?(@.number =~ /2/i)]'},['{"numbers":[{"number":2}]}','{"numbers":[{"number":7}]}'])
 body('upstream JSONPath size',{'matchesJsonPath':'$[?(@.numbers.size() == 2)]'},['{"numbers":[1,2]}','{"numbers":[1]}'])
 body('upstream JSONPath nested object',{'matchesJsonPath':'$..thingOne[?(@.innerOne == 11)]'},['{"things":{"thingOne":{"innerOne":11}}}','{"things":{"thingOne":{"innerOne":12}}}'])
 for expr,matcher in [('$.numbers.length()',{'equalTo':'2'}),('$.numbers.min()',{'equalTo':'1.0'}),('$.numbers.max()',{'equalTo':'2.0'}),('$.numbers.sum()',{'equalTo':'3.0'}),('$.numbers.avg()',{'equalTo':'1.5'}),('$.object.keys()',{'contains':'a'})]:
  body('JSONPath function '+expr,{'matchesJsonPath':{'expression':expr,**matcher}},['{"numbers":[1,2],"object":{"a":1,"b":2}}'])
 for expression in ["$.items[?(@.n in [1,2])]","$.items[?(@.n nin [1,2])]","$.items[?(@.n >= 2 && @.n < 4)]","$.items[?(@.s =~ /hello/i)]"]:
  body('JSONPath operator '+expression,{'matchesJsonPath':expression},['{"items":[{"n":2,"s":"HELLO"}]}','{"items":[{"n":5,"s":"world"}]}'])
 for expected in ['9007199254740993','1.00','0.000000000000000001','1e100']:
  body('JSON numeric precision '+expected,{'equalToJson':expected},[expected,'9007199254740992','1'])
 for expected,actual in [('[1,2]','[1,0,2]'),('[1,2]','[0,1,2]'),('[1,2]','[1,2,0]'),('[1,1]','[1,2]')]:
  body('JSON extras '+actual,{'equalToJson':expected,'ignoreExtraElements':True},[actual])
 for name,regex,values in [('possessive','a++',['aa','a+']),('possessive no backtrack','a++a',['aa','aaa']),('intersection','[a-z&&[^aeiou]]+',['bcdf','abc']),('linebreak',r'a\Rb',['a\nb','a\r\nb']),('unicode case','(?i)k',['K','K']),('unicode flags','(?iu)k',['K','K']),('ASCII digits',r'(?=\d)\d+',['12','١٢'])]:body('Java regex '+name,{'matches':regex},values)
 body('matchesPathTemplate body',{'matchesPathTemplate':'/one/{id}/two'},['/one/3/two','/one/3/other'])
 for value in ['i','I','ı','İ']:
  add('Java equalTo ignoreCase '+value,{'bodyPatterns':[{'equalTo':'i','caseInsensitive':True}]},value)
 for host in [{'equalTo':'127.0.0.1'},{'equalTo':'wrong.test'},{'matches':'127.*'}]:add('host matcher '+str(host),{'host':host})
 add('URL precedence',{'url':'/test','urlPath':'/wrong'})
 add('unused pathParameters',{'url':'/test','pathParameters':{'missing':{'equalTo':'x'}}})
 for value in ['hello%20world','hello+world','hello%2Fworld']:
  for expected in ['hello world',value]:add('path encoding '+value+' '+expected,{'urlPathTemplate':'/users/{id}','pathParameters':{'id':{'equalTo':expected}}},path='/users/'+value)
 add('repeated cookies best match',{'cookies':{'session':{'equalTo':'second'}}},headers={'Cookie':'session=first; session=second'})
 add('basic auth overrides authorization matcher',{'basicAuthCredentials':{'username':'a','password':'b'},'headers':{'Authorization':{'equalTo':'bad'}}},headers={'Authorization':'Basic YTpi'})
 for op in ['includes','hasExactly']:add('empty multivalue '+op,{'queryParameters':{'q':{op:[]}}})
 for actual in ['a=1','a=2','a=1&a=2']:
  add('form no content type '+actual,{'formParameters':{'a':{'equalTo':'1'}}},actual,headers={'Content-Type':'text/plain'})
 for body_text in ['<a x="1" y="2"/>','<a x="1" y="3"/>']:
  body('XML attribute placeholders',{'equalToXml':'<a x="${xmlunit.ignore}" y="2"/>','enablePlaceholders':True},[body_text])
 for expression in ['$.items[?(@.x == null)]','$.items[?(@.x != null)]','$.items[?(@.x)]','$.items[?(!@.x)]','$.items[?(@.x subsetof [1,2])]','$.items[?(@.x anyof [1,2])]','$.items[?(@.x noneof [1,2])]','$.items[?(@.x empty true)]']:
  body('Jayway edge '+expression,{'matchesJsonPath':expression},['{"items":[{"x":null}]}','{"items":[{}]}','{"items":[{"x":[1]}]}','{"items":[{"x":[]}]}'])
 for regex,values in [('(?i)[a-z]+',['ABC','abc','K']),('(?i)[^a-z]+',['ABC','123']),('(?i:a)', ['A','a']),('(?i)\\w+',['HELLO','K']),('(?i)hello',['HELLO','hello']),('a{2,3}+a',['aaa','aaaa']),('(ab)++ab',['abab','ab'])]:body('Java regex extra '+regex,{'matches':regex},values)
 for regex,values in [('(ab)++',['abab','ab','']),('(?:ab){2,3}+',['abab','ababab','ab']),('((a)b)++',['abab','ab']),('(?i)[a-z]+',['ABC','abc','K'])]:body('Java possessive group '+regex,{'matches':regex},values)
 for regex,values in [('(?i:a)k',['Ak','AK','ak']),('(?iu:k)k',['Kk','KK']),('(?U)\\d+',['١٢','12']),('[a-z&&[def]]+',['def','abc']),('[a-z&&[^m-p]]+',['abc','mnop']),('[a-d[m-p]]+',['amp','xyz']),('[^a-z&&[def]]+',['abc','def','123']),('(?i)\\Qhello.world\\E',['HELLO.WORLD','helloxworld'])]:body('Java regex scopes '+regex,{'matches':regex},values)
 for expected,actual in [('<?pi one?><a/>','<?pi two?><a/>'),('<?pi one?><a/>','<?pi one?><a/>'),('<a><?pi one?></a>','<a><?pi two?></a>')]:
  for exempt in [[],['PROCESSING_INSTRUCTION_DATA']]:body('XML PI '+str(exempt)+expected,{'equalToXml':expected,'exemptedComparisons':exempt},[actual])
 for fmt,value in [('uuuu-MM-dd','2026-01-02'),('yyyy-M-d','2026-1-2'),("dd MMMM yyyy 'at' hh:mm a",'02 January 2026 at 03:04 PM'),("yyyy-MM-dd'T'HH:mm:ss[.SSS][XXX]",'2026-01-02T15:04:05.123Z'),("yyyy-MM-dd'T'HH:mm:ss[.SSS][XXX]",'2026-01-02T15:04:05'),("yyyy-MM-dd'T'HH:mm:ss.SSSSSSXXX",'2026-01-02T15:04:05.123456Z'),("yyyy-MM-dd'T'HH:mm:ssXXX",'2026-01-02T15:04:05+02:00')]:
  for op,expected in [('before','2027-01-01'),('after','2025-01-01')]:body('Java date pattern '+fmt+op, {op:expected,'actualFormat':fmt},[value])
 for regex,values in [('(?iu)i',['i','I','ı','İ']),('(?iu)é',['é','É']),('(?i-m:a)B',['aB','AB','Ab']),('(?i)(?-i:a)B',['aB','ab','AB']),('(?U)\\p{L}+',['hello','Καλημέρα','123'])]:body('Java Unicode flag edge '+regex,{'matches':regex},values)
 for attr in ['schemaLocation','noNamespaceSchemaLocation']:
  for exempt in [[],['SCHEMA_LOCATION','NO_NAMESPACE_SCHEMA_LOCATION']]:body('XML schema '+attr+str(exempt),{'equalToXml':f'<a xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:{attr}="one.xsd"/>','exemptedComparisons':exempt},[f'<a xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:{attr}="two.xsd"/>'])
