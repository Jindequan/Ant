# ENG-05 — quota files within the 500-line rule (2026-10-08)

Pure moves inside package `quota`, no behavior change; imports re-derived; `gofmt` clean.

| File | Before | After | Moved to |
|---|---|---|---|
| `service.go` | 818 | 297 | `service_merge.go` (merge/hide/sort), `service_edit.go` (add/edit/remove/sign-in/view), `service_http.go` (page API, guards) |
| `aliyun.go` | 712 | 442 | `aliyun_billing.go` (bill, pay-as-you-go), `aliyun_openapi.go` (AccessKey OpenAPI, RPC signing) |
| `volcengine.go` | 543 | 371 | `volcengine_signin.go` (device login, STS credentials) |

Largest non-test quota file is now `creative.go` (451). Accepted files over 500 lines are untouched,
per the finding's scope guard. Verification: `go test -race -count=1 ./internal/quota/...` and the
full gate set pass.
