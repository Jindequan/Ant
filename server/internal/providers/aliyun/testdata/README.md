Canonical Bailian CLI gateway replies (`/cli/api.json`), read by the module's tests and by
quota's Bailian tests. The envelope and the subscription, usage and tool-benefit shapes are as
observed on 2026-10-07 (docs/research/ALIYUN-BAILIAN.md); `addons.json` uses the documented field
names with made-up amounts; `no-plan.json` (an account without a Token Plan) is constructed: the
real reply for that case wasn't observed, and the module treats any reply without an instance
code as "no Token Plan".
