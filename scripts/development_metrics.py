#!/usr/bin/env python3
"""Summarize local token counters for explicitly supplied task logs; never exports messages."""
import argparse,json,pathlib,datetime
p=argparse.ArgumentParser();p.add_argument('--logs',required=True,help='JSON list of own task log paths');p.add_argument('--start',required=True);p.add_argument('--end',required=True);p.add_argument('--output',required=True);a=p.parse_args()
keys=['input_tokens','cached_input_tokens','output_tokens','reasoning_output_tokens','total_tokens'];rows=[]
for path in json.loads(pathlib.Path(a.logs).read_text()):
 before={k:0 for k in keys};after=before.copy();models=set();role='root';observed=False
 with open(path)as f:
  for line in f:
   e=json.loads(line);v=e.get('payload',{});stamp=e.get('timestamp','')
   if e.get('type')=='session_meta':
    source=v.get('source',{});role=source.get('subagent',{}).get('thread_spawn',{}).get('agent_path','root')if isinstance(source,dict)else'root'
   if e.get('type')=='turn_context'and a.start<=stamp<=a.end and v.get('model'):models.add(v['model'])
   if e.get('type')!='event_msg'or v.get('type')!='token_count':continue
   usage=v.get('info',{}).get('total_token_usage')
   if not usage:continue
   if stamp<a.start:before={k:usage.get(k,0)for k in keys}
   if stamp<=a.end:after={k:usage.get(k,0)for k in keys};observed=True
 delta={k:after[k]-before[k]for k in keys}
 if min(delta.values())<0:raise RuntimeError(f'Counter reset in {role}; refusing invalid subtraction')
 delta['uncached_input_tokens']=delta['input_tokens']-delta['cached_input_tokens'];rows.append({'agent':role,'observed_counter':observed,'models_observed':sorted(models),**delta})
totals={k:sum(r[k]for r in rows)for k in keys+['uncached_input_tokens']}
report={'start_utc':a.start,'end_utc':a.end,'scope':'Observed cumulative counters across root and three explicitly selected subagent logs.','notes':['Counters are local usage observations, not an invoice or compiler token consumption.','Reasoning output is a subset of output tokens; do not add it again.','Input includes repeated cached context. Uncached input = input minus cached input.','Different scope and reuse of earlier work prevent attributing differences to programming language alone.','Snapshot may precede final response; no conversation content or local log paths exported.'],'agents':rows,'totals':totals}
path=pathlib.Path(a.output);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(totals))
