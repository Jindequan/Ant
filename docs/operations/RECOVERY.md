# SQLite backup recovery

Use the server's backup and restore commands for database files. Stop the
server before restoring; the restore command refuses SQLite `-wal` or `-shm`
sidecars so it cannot replace a database that still has an active WAL.

Create a verified snapshot in a private directory:

```sh
chmod 700 ./server/data
./bin/ant-server -db ./server/data/ant.db \
  -backup-to ./server/data/ant-20261005-120000.db
```

Keep the master-key file from the same deployment in a separately protected
backup. SQLite snapshots contain encrypted secret records, but they are still
sensitive operational data.

With the server stopped, restore from a verified private snapshot:

```sh
./bin/ant-server -db ./server/data/ant.db \
  -restore-from ./server/data/ant-20261005-120000.db
```

Restore stages and checks a copy before publishing it, applies supported
schema migrations, sets `restore_guard=1` in the restored database, and writes
a system audit record and receipt containing the source snapshot hash. The
replaced database is preserved as `ant.db.pre-restore-<hash-prefix>`. A failed
staging or audit transaction does not replace the destination.

The command exits after installation and does not start the API. Start the
server, sign in with an administrator present in the restored snapshot, and
keep dispatch closed while checking the snapshot time against provider-side
activity, operation IDs, dispatch permits, unknown outcomes, resource events,
and open occupancy. Rows created after the snapshot are absent and cannot be
reconstructed from SQLite alone. Record the review evidence, then use the
manage-only restore-guard control to reopen dispatch. The server refuses new
claims while the guard remains closed.

This procedure prevents the supported restore path from inheriting an older
`restore_guard=0` value. It cannot identify unknown calls made after the
snapshot; those require external provider evidence and must be reconciled
before reopening dispatch.
