from pathlib import Path
import subprocess,json,hashlib
root=Path(__file__).resolve().parents[3];out=root/'docs/evidence/CODEX-T1-REWORK3-AUDIT'
pairs=[(out/'audit_test.go.txt',root/'server/internal/config/codex_fourth_audit_test.go'),(out/'audit.test.ts.txt',root/'web/tests/codex-fourth-audit.test.ts')]
assert all(not dst.exists() for _,dst in pairs)
results=[]
try:
 for src,dst in pairs:dst.write_bytes(src.read_bytes())
 for name,cwd,cmd in [('go',root/'server',['go','test','-count=1','-run','TestCodexFourth','-v','./internal/config']),('web',root/'web',['npm','test','--','--run','tests/codex-fourth-audit.test.ts'])]:
  r=subprocess.run(cmd,cwd=cwd,capture_output=True,text=True,timeout=120);(out/(name+'-probes.log')).write_text(r.stdout+r.stderr);results.append({'name':name,'command':cmd,'exitCode':r.returncode});print(name,r.returncode,flush=True)
finally:
 for _,dst in pairs:dst.unlink(missing_ok=True)
 before=json.loads((out/'reviewed-source-sha256.json').read_text());changed=[p for p,h in before.items() if not (root/p).exists() or hashlib.sha256((root/p).read_bytes()).hexdigest()!=h]
 result={'results':results,'implementationFilesChanged':changed,'temporaryTestsRemoved':all(not d.exists() for _,d in pairs)};(out/'probe-results.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)
