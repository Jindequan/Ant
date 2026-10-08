-- Non-executable commercial supply configuration. Existing ledger/config
-- tables and declarations are preserved without reinterpretation.
CREATE TABLE supply_inventory (
 namespace TEXT PRIMARY KEY,
 version INTEGER NOT NULL CHECK(version > 0),
 definition_json TEXT NOT NULL CHECK(json_valid(definition_json)),
 updated_at INTEGER NOT NULL
) STRICT;
