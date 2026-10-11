#!/usr/bin/env python3
"""One clean-artifact and one no-change build per port; dependency downloads excluded."""
import argparse,json,pathlib,subprocess,tempfile,time,os,platform
p=argparse.ArgumentParser();p.add_argument('--output',default='testdata/reports/rust/builds.json');a=p.parse_args();root=pathlib.Path(__file__).resolve().parent.parent;results=[]
with tempfile.TemporaryDirectory(prefix='breakmock-build-measure-')as tmp:
 tmp=pathlib.Path(tmp)
 for kind in ['go','rust']:
  env=os.environ.copy()
  if kind=='go':
   env['GOCACHE']=str(tmp/'go-cache');binary=tmp/'breakmock-go';command=['go','build','-o',str(binary),'./cmd/breakmock']
  else:
   binary=tmp/'rust-target/release/breakmock-rust';command=['cargo','build','--release','--locked','--offline','--manifest-path','rust/Cargo.toml','--target-dir',str(tmp/'rust-target')]
  for stage in ['clean_artifacts','no_change_incremental']:
   start=time.perf_counter();proc=subprocess.run(command,cwd=root,env=env,capture_output=True,text=True);elapsed=time.perf_counter()-start
   if proc.returncode:raise RuntimeError(proc.stderr)
   row={'language':kind,'stage':stage,'wall_seconds':elapsed,'binary_bytes':binary.stat().st_size};results.append(row);print(json.dumps(row),flush=True)
report={'platform':platform.platform(),'go_version':subprocess.check_output(['go','version'],text=True).strip(),'rust_version':subprocess.check_output(['rustc','--version'],text=True).strip(),'notes':['One sample per build mode; not a statistical comparison.','Fresh Go GOCACHE and Rust target directory. Downloaded module/crate sources and OS file cache already warm.','Go default optimized build; Rust release thin LTO/codegen-units1 as configured. No-change rebuild includes dependency fingerprint checks.','No Java source build measured: comparison uses published reference JAR.','Temporary caches removed after completion; no user caches cleared.'],'results':results}
path=pathlib.Path(a.output);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2)+'\n')
