-- Preserve the sealed hook registry and exact candidate order alongside the
-- preparation decision. Kept separate from the plan snapshot so size-limited
-- plan traces cannot erase execution-selection evidence.
ALTER TABLE decision_records
  ADD COLUMN trace_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(trace_json));
