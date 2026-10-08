# ENG-09 — repository hygiene (2026-10-08)

1. `/Users/devin/code/Ant/--help` deleted after inspection: a `scripts/snapshot.py` manifest with
   empty `generatedAt`, dated 2026-10-02, referenced nowhere.
2. `.archive/2026-10-07-old-frontend/web/node_modules` (164 MB; `package-lock.json` present): **not
   deleted** — awaiting the user's OK.
3. `make test-web` references in historical evidence: note only, unchanged.
