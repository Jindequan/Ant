from pathlib import Path
import hashlib,json,subprocess

root=Path(__file__).resolve().parents[3]
out=root/'docs/evidence/CODEX-T1-REWORK2-AUDIT'
pairs=[(out/'rework3_audit_test.go.txt',root/'server/tests/integration/rework3_audit_test.go'),(out/'rework3-audit.test.ts.txt',root/'web/tests/rework3-audit.test.ts')]
assert all(not dest.exists() for _,dest in pairs)
results=[]
try:
 for src,dest in pairs:dest.write_bytes(src.read_bytes())
 for name,cwd,cmd in [('go',root/'server',['go','test','-count=1','-run','TestCodexThirdReview','-v','./tests/integration']),('web',root/'web',['npm','test','--','--run','tests/rework3-audit.test.ts'])]:
  r=subprocess.run(cmd,cwd=cwd,capture_output=True,text=True,timeout=120)
  (out/(name+'-probes.log')).write_text(r.stdout+r.stderr)
  results.append({'name':name,'command':cmd,'exitCode':r.returncode});print(name,r.returncode,flush=True)
finally:
 for _,dest in pairs:dest.unlink(missing_ok=True)
 original=json.loads((out/'reviewed-source-sha256.json').read_text())
 changes=[name for name,h in original.items() if not (root/name).exists() or hashlib.sha256((root/name).read_bytes()).hexdigest()!=h]
 result={'results':results,'implementationFilesChanged':changes,'temporaryTestsRemoved':all(not dest.exists() for _,dest in pairs)}
 (out/'probe-results.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)
