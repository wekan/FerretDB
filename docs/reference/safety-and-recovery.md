---
sidebar_position: 3
description: Automatic corruption detection, bloat repair and OpLog bounding in the SQLite backend
---

# Safety and recovery

The SQLite backend runs several **automatic, non-destructive safety measures** so a
database file cannot silently bloat or hide corruption. They are general-purpose and
apply to any MongoDB-wire client (for example a Meteor 3 application tailing the
OpLog).

## Automatic corruption detection

Every time a persistent SQLite database file is opened, FerretDB runs SQLite's fast,
read-only integrity check (`PRAGMA quick_check`). When it reports anything other than
`ok`, FerretDB logs the problem at `ERROR` level, naming the affected database, for
example:

```
ERROR  autorepair  SQLite CORRUPTION DETECTED - restore from backup or re-migrate text data  db=mydb quick_check=...
```

Corruption **cannot be repaired in place** — SQLite has no safe in-place repair for a
damaged B-tree. Detection is therefore reported so the operator or the client can
recover the data (restore from a backup copy, or re-migrate the text data from the
original source). Attachments/blobs stored outside the database on the filesystem are
never affected.

## Automatic bloat repair

A collection that churns heavily — for example the capped `local.oplog.rs` that a
Meteor client tails, or any hot collection under a poll-and-diff workload — leaves
free pages behind on delete. SQLite does not return those pages to the operating
system automatically, so the file stays large and every scan and lock touches more of
it, which raises CPU.

On open, FerretDB checks the file's page counts (`PRAGMA page_count` /
`PRAGMA freelist_count`). When the file is large enough to matter (at least ~1 MiB)
**and** at least a quarter of its pages are free, FerretDB runs `VACUUM` to rebuild
the file compactly and return the free space:

```
INFO  autorepair  VACUUMing bloated database to reclaim free pages  db=mydb pages=... freePages=...
```

`VACUUM` is content-preserving: it rewrites the database without changing any data.
Tiny databases are never VACUUMed.

## Bounded OpLog

When a replica-set name is configured, FerretDB auto-creates the capped
`local.oplog.rs` collection so clients can tail changes instead of polling. It is
capped **small** (16 MiB) on purpose: only a short sliding window of the most recent
mutations is needed for tailing, and a small OpLog keeps the `local` database file
tiny and the constant tailing cheap. A client that ever falls behind the window
simply falls back to poll-and-diff — no data is lost. The capped-collection cleanup
trims older entries to this size roughly once a minute.

## Configuration

| Setting | Default | Effect |
| --- | --- | --- |
| `FERRETDB_SQLITE_AUTO_REPAIR` | on | Set to `false` to disable both the corruption check and the automatic `VACUUM` on open. |

These measures are best-effort: any failure is logged but never blocks opening the
database or starting the server.
