from pathlib import Path
import subprocess,json,hashlib
root=Path(__file__).resolve().parents[3];out=root/'docs/evidence/CODEX-T1-REWORK-AUDIT'
pairs=[(out/'rework_audit_test.go.txt',root/'server/tests/integration/rework_audit_test.go'),(out/'rework-audit.test.ts.txt',root/'web/tests/rework-audit.test.ts')]
assert all(not dest.exists() for _,dest in pairs)
results=[]
try:
 for src,dest in pairs:dest.write_bytes(src.read_bytes())
 for name,cwd,cmd in [('go',root/'server',['go','test','-count=1','-run','TestCodexRework','-v','./tests/integration']),('web',root/'web',['npm','test','--','--run','tests/rework-audit.test.ts'])]:
  result=subprocess.run(cmd,cwd=cwd,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=120)
  (out/(name+'-probes.log')).write_text(result.stdout);results.append(dict(name=name,command=cmd,exitCode=result.returncode));print(name,'exit',result.returncode,flush=True)
finally:
 for _,dest in pairs:dest.unlink(missing_ok=True)
 original=json.loads((out/'reviewed-source-sha256.json').read_text())
 changes=[name for name,h in original.items() if not (root/name).exists() or hashlib.sha256((root/name).read_bytes()).hexdigest()!=h]
 (out/'probe-results.json').write_text(json.dumps(dict(results=results,implementationFilesChanged=changes,temporaryTestsRemoved=all(not dest.exists() for _,dest in pairs)),indent=2)+'\n');print('Implementation changed:',changes,flush=True)
