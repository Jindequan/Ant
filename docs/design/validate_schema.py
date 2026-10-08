"""Validate the design migration only; this is not an ANT runtime acceptance test."""
from pathlib import Path
import json
import sqlite3

db = sqlite3.connect(":memory:")
db.executescript((Path(__file__).parent / "sql/001-initial.sql").read_text())
checks = ["migration executes"]

db.execute("INSERT INTO config_revisions VALUES('cfg','{}',0)")
db.execute("INSERT INTO accounts VALUES('acct','synthetic-test-owner',1,1,'{}')")
db.execute("INSERT INTO bindings VALUES('binding','acct','test','ai','https://test.invalid',"
           "'ref-only','cred-v1',1,1,'{}')")
db.execute("INSERT INTO resources VALUES('quota','account','acct','requests','request',0,'counter','fixed','r1','{}')")
db.execute("INSERT INTO resource_states(resource_id,window_key,capacity_units) VALUES('quota','w1',50)")
db.execute("INSERT INTO resource_offers VALUES('offer','binding','ai','text',1,1,'{}')")
db.execute("INSERT INTO resource_pools VALUES('pool','ns','Synthetic pool',1,1,'{}')")
db.execute("INSERT INTO pool_members VALUES('pool','offer',10,1,1)")
db.execute("INSERT INTO operations(namespace,operation_id,pool_id,fingerprint,state,runtime_id,owner_id,"
           "generation,lease_until,deadline_at,max_attempts,strategy_json,created_at,updated_at) "
           "VALUES('ns','op','pool','fp','running','rt','owner',1,100,200,2,'{}',0,0)")
db.execute("INSERT INTO execution_plans VALUES('plan','cfg','{}','hash')")

def add_attempt_sql(attempt_id):
    return ("INSERT INTO attempts(attempt_id,namespace,operation_id,binding_id,account_id,offer_id,plan_id,"
            "generation,request_digest,guard_refs_json,phase,lease_until,created_at,updated_at) "
            "VALUES(?, 'ns','op','binding','acct','offer','plan',1,'digest','[]','prepared',100,0,0)", (attempt_id,))

db.execute(*add_attempt_sql("attempt"))
db.execute("INSERT INTO dispatch_permits(permit_id,attempt_id,runtime_id,generation,control_version,"
           "credential_revision,expires_at,request_digest,status,granted_at) "
           "VALUES('permit','attempt','rt',1,1,'cred-v1',100,'digest','granted',0)")
db.execute("INSERT INTO command_receipts VALUES('ns','cmd','settle','input-hash','{}',0)")
db.execute("INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,"
           "attempt_id,event_kind,committed_delta,evidence_json,recorded_at) "
           "VALUES('event','ns','cmd','quota','w1','attempt','consume',1,'[]',0)")
db.commit()

def rejected(name, sql, params=()):
    db.execute("SAVEPOINT constraint_test")
    try:
        db.execute(sql, params)
    except sqlite3.IntegrityError:
        checks.append(name)
    else:
        raise AssertionError("Constraint did not reject: " + name)
    finally:
        db.execute("ROLLBACK TO constraint_test")
        db.execute("RELEASE constraint_test")

rejected("single active attempt", *add_attempt_sql("attempt2"))
rejected("single permit per attempt", "INSERT INTO dispatch_permits SELECT 'permit2',attempt_id,"
         "runtime_id,generation,control_version,credential_revision,expires_at,request_digest,status,"
         "granted_at,consumed_at FROM dispatch_permits WHERE permit_id='permit'")
rejected("reservation window foreign key", "INSERT INTO reservations VALUES('r','attempt','quota','missing',"
         "'counter',1,'reserved')")
rejected("nonnegative reservation summary", "UPDATE resource_states SET reserved_units=-1")
rejected("permit budget upper bound", "UPDATE operations SET permits_issued=3")
rejected("receipt deduplication", "INSERT INTO command_receipts VALUES('ns','cmd','settle','other','{}',0)")
rejected("event immutable update", "UPDATE resource_events SET committed_delta=2 WHERE event_id='event'")
rejected("event immutable delete", "DELETE FROM resource_events WHERE event_id='event'")
rejected("invalid operation state", "UPDATE operations SET state='invalid'")

db.execute("SAVEPOINT atomic_test")
try:
    db.execute("UPDATE resource_states SET reserved_units=3")
    db.execute(*add_attempt_sql("attempt3"))
except sqlite3.IntegrityError:
    db.execute("ROLLBACK TO atomic_test")
else:
    raise AssertionError("Expected conflicting attempt")
db.execute("RELEASE atomic_test")
assert db.execute("SELECT reserved_units FROM resource_states").fetchone()[0] == 0
checks.append("transaction rollback has no partial reservation")

db.execute("UPDATE operations SET state='unknown', result_json='{\"state\":\"unknown\"}'")
rejected("terminal state cannot revive", "UPDATE operations SET state='running',result_json=NULL")
rejected("terminal result immutable", "UPDATE operations SET result_json='{}'")
db.execute("INSERT INTO operation_events VALUES('ns','op',1,'accepted','{}',0)")
rejected("pool member foreign key", "INSERT INTO pool_members VALUES('pool','missing',20,1,1)")
rejected("pool membership is unique", "INSERT INTO pool_members VALUES('pool','offer',20,1,1)")
rejected("pool priority is bounded", "UPDATE pool_members SET priority=-1")
rejected("pool version is positive", "UPDATE resource_pools SET version=0")
rejected("pool namespace cannot change operation identity", "UPDATE operations SET pool_id='missing'")
rejected("operation event sequence is unique", "INSERT INTO operation_events VALUES('ns','op',1,'accepted','{}',0)")
rejected("operation event immutable", "DELETE FROM operation_events")
db.execute("SAVEPOINT pool_order_atomic")
db.execute("UPDATE pool_members SET priority=20")
try:
    db.execute("UPDATE resource_pools SET version=0")
except sqlite3.IntegrityError:
    db.execute("ROLLBACK TO pool_order_atomic")
else:
    raise AssertionError("Expected invalid pool version")
db.execute("RELEASE pool_order_atomic")
assert db.execute("SELECT priority FROM pool_members").fetchone()[0] == 10
checks.append("pool update rollback has no partial order")
db.commit()
assert not db.execute("PRAGMA foreign_key_check").fetchall()
assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
checks.append("foreign key and integrity checks")
tables = db.execute("SELECT count(*) FROM sqlite_master WHERE type='table'").fetchone()[0]
db.close()
print(json.dumps({"scope": "SQLite design migration, not application", "sqliteVersion": sqlite3.sqlite_version,
                  "tableCount": tables, "passed": len(checks), "checks": checks}, ensure_ascii=False, indent=2))
