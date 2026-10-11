#!/usr/bin/env python3
# Copyright 2026 Breaking-IT. Apache-2.0.
# Imports selected parameterized fixtures from WireMock's Apache-2.0 tests.
import csv,json,pathlib,re,sys
root=pathlib.Path(sys.argv[1]);cases=[]
string=r'"(?:\\.|[^"\\])*"'
for cls in ['JsonArrayAddHelperTest','JsonMergeHelperTest','JsonRemoveHelperTest']:
 source=(root/'src/test/java/com/github/tomakehurst/wiremock/extension/responsetemplating/helpers'/f'{cls}.java').read_text()
 for m in re.finditer(r'@CsvSource\(\{(.*?)\}\)\s+void\s+(\w+)',source,re.S):
  name=m[2]
  for i,match in enumerate(re.finditer(string+r'(?:\s*\+\s*'+string+r')*',m[1])):
   row=''.join(json.loads(s) for s in re.findall(string,match[0]));values=next(csv.reader([row],quotechar="'",skipinitialspace=True))
   p={};t=None
   if name in ['addsAnItemToAnArrayAndReturnsString','addsMultipleItemsToAnArrayAndReturnsString','theNumberOfItemsInTheOutputArrayCanBeLimited']:
    p={'array':values[0],'item':values[1]};opt=' flatten=true' if name.startswith('addsMultiple') else ''
    if name.startswith('theNumber'): opt=' flatten='+values[2]+' maxItems=3'
    t='{{jsonArrayAdd parameters.array parameters.item'+opt+'}}'
   elif name=='mergesJsonObjects':
    p={'base':values[0],'merge':values[1]};t='{{jsonMerge parameters.base parameters.merge}}'
   elif name in ['removesElementsFromStringInputAndReturnsString','noOpIfJsonpathResultIsNotFoundForInputJson']:
    p={'input':values[0],'path':values[1]};t='{{jsonRemove parameters.input parameters.path}}'
   if t:cases.append({'name':f'upstream {cls}.{name}[{i}]','template':t,'parameters':p})
out=pathlib.Path(__file__).resolve().parent.parent/'testdata/template_upstream_cases.json';out.write_text(json.dumps(cases,indent=2)+'\n');print(f'Imported {len(cases)} literal upstream fixture rows')
