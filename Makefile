# ANT build and verification entry points (T1 rework). Target names are
# frozen by docs/plans/EXECUTION-STANDARD.md. Per R14: freshness (-count=1)
# and race detection are part of the targets themselves, not optional flags;
# unimplemented-stage targets fail non-zero on purpose.
SHELL := /bin/bash
SERVER := server

.PHONY: check build run demo test-contract test-integration test-fault bench clean verify-manifest

check:
	python3 scripts/render_stage_runbooks.py --check
	cd $(SERVER) && test -z "$$(gofmt -l .)" || { echo 'gofmt needed:'; gofmt -l .; exit 1; }
	cd $(SERVER) && go vet ./...
	cd $(SERVER) && go build ./...
	python3 scripts/check-deps.py

# The page is embedded in the server binary (server/internal/webui).
build:
	cd $(SERVER) && go build -o ../bin/ant-server ./cmd/ant-server

run: build
	mkdir -p data && ./bin/ant-server -db ./data/ant.db

demo: build
	mkdir -p data && ./bin/ant-server -db ./data/demo.db -demo

test-contract:
	cd $(SERVER) && go test -race -count=1 ./internal/...
test-integration:
	cd $(SERVER) && go test -race -count=1 -timeout 300s ./tests/integration/...
test-fault:
	cd $(SERVER) && go test -race -count=1 ./tests/fault/...
	cd $(SERVER) && go test -race -count=1 -run 'TestPreparationRejectsAuthorizationAfterPoolOrderChanges|TestPreparationAuthorizationFailureRetryDoesNotLeakPermit' ./tests/integration/...
	cd $(SERVER) && go test -race -count=1 -run 'TestObservationRefreshChecksWatermarksAndReplays|TestAdjustmentIsEvidenceBoundIdempotentAndCannotOverCorrect|TestSettlementIdentityAndMappingIntegrity' ./internal/ledger/commands/...
	cd $(SERVER) && go test -race -count=1 -run 'TestEncodeRejectsPreparedPlanFromDifferentUsageMappingRevision' ./internal/providers/deepseek/...
bench:
	@test -n "$$(ls $(SERVER)/tests/bench/*_test.go 2>/dev/null)" || { echo "bench: no benchmarks yet (T6 scope) — failing by design"; exit 1; }
	cd $(SERVER) && go test -bench=. -benchmem -count=1 ./tests/bench/...

# R14/B6: regenerate the source manifest and diff against the last signed
# baseline (Codex T1: docs/evidence/T1-CODEX/final-source-manifest.json,
# coverage per D-B6-v2 including gate scripts and consumed test inputs).
# Drift must be reviewed and the manifest re-signed before evidence is updated.
verify-manifest:
	@python3 scripts/snapshot.py /tmp/ant-manifest-check.json >/dev/null
	@python3 scripts/manifest-diff.py docs/evidence/T1-CODEX/final-source-manifest.json /tmp/ant-manifest-check.json

clean:
	rm -rf bin
