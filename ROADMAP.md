# WeKan on FerretDB v1 (SQLite) — Compatibility Roadmap

This roadmap tracks what is needed to run [WeKan](https://github.com/wekan/wekan)
(Meteor 3 / MongoDB) on **FerretDB v1.24.2 with the SQLite backend** (this repo,
`main-v1` branch).

**Assumptions**

- WeKan **attachments are stored on the filesystem** (`WRITABLE_PATH`, storage
  strategy `fs`), not in the database — so **GridFS is out of scope**. GridFS is
  only exercised if an admin explicitly selects the `gridfs` backend or migrates
  legacy CollectionFS data. See WeKan `models/attachments.server.js`,
  `models/lib/fileStoreStrategy.js`, `server/lib/mongoStartup.js` (startup notice:
  *"Attachments and avatars are stored ON DISK under WRITABLE_PATH, NOT in MongoDB"*).
- Core CRUD (`insert`, `find`, `count`, `update` with `$set`, `findOneAndUpdate`)
  has already been **verified working** against this FerretDB v1.24.2 SQLite build
  with the MongoDB wire driver.

**Legend:** `[x]` = supported / already works · `[ ]` = missing / to do.
FerretDB sources are relative paths in this repo; WeKan sources link to
`github.com/wekan/wekan`.

---

## 1) MongoDB features WeKan uses

What WeKan actually depends on (from the WeKan repo). Checked = confirmed in use.

### CRUD & atomic operations
- [x] `find` / `insert` / `update` / `delete`
- [x] `findOneAndUpdate` with `$inc` + `{ upsert: true, returnDocument: 'after' }` — atomic counters ([`models/counters.js`](https://github.com/wekan/wekan/blob/main/models/counters.js))
- [x] `updateOne` with `$setOnInsert` + `upsert` — race-safe per-board card numbering ([`models/boards.js`](https://github.com/wekan/wekan/blob/main/models/boards.js))
- [x] `rawCollection()` access (node-mongodb driver) for the above and index creation

### Update operators
- [x] `$set` (pervasive, ~765 sites), `$unset` (~42)
- [x] `$pull` (~72), `$push` (~46), `$addToSet` (~38)
- [x] `$each` (~7), `$slice` (~4), `$sort` update-modifier (1) — with `$push`/`$addToSet`
- [x] `$setOnInsert` (~44, upserts), `$inc` (3), `$rename` (1), `$pullAll` (1, [`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js))
- Not used: `$pop`, `$mul`, `$min`, `$max`, `$currentDate`, `$bit`

### Query operators
- [x] `$regex` incl. `$options: 'i'` and `$not: { $regex: … }` — **WeKan's entire search/filter is regex-based** ([`client/lib/filter.js`](https://github.com/wekan/wekan/blob/main/client/lib/filter.js), [`models/boards.js`](https://github.com/wekan/wekan/blob/main/models/boards.js))
- [x] `$in`, `$gte`, `$ne`, `$or`, `$not`, `$exists`, `$size`
- Not used by WeKan: `$expr`, geospatial (`$near`/`$geoWithin`), `collation` (`$where` and `$text` are unused by WeKan but now implemented — `$where` via the embedded goja JavaScript engine, `$text` as a partial substring/word matcher)

### Aggregation pipelines (3 sites, server-side admin/metrics only — not on the hot path)
- [x] Stages: `$match`, `$group` (`$sum`), `$sort`, `$project`, `$addFields`, `$count`, **`$lookup`**
- [x] Expression operators: `$map`, `$objectToArray`, `$ifNull`, `$anyElementTrue`, `$or`, `$eq`, `$ne`
- Sites: [`models/server/metrics.js`](https://github.com/wekan/wekan/blob/main/models/server/metrics.js) (Prometheus "top boards"), [`server/models/attachmentStorageSettings.js`](https://github.com/wekan/wekan/blob/main/server/models/attachmentStorageSettings.js) (attachment storage stats)

### Indexes
- [x] Single-field indexes ([`server/lib/mongoStartup.js`](https://github.com/wekan/wekan/blob/main/server/lib/mongoStartup.js) `ensureIndex()`)
- [x] Compound indexes (e.g. `{ boardId: 1, createdAt: -1 }`)
- [x] Unique indexes (e.g. boards, invitation codes)
- Not used: geospatial indexes. **TTL** (`expireAfterSeconds`) and **text** indexes are unused by WeKan but now implemented (text indexes only pragmatically — the options are stored and round-tripped, but no real inverted index is built)

### Reactivity (Meteor pub/sub)
- [x] Configurable driver order via `METEOR_REACTIVITY_ORDER` = `changeStreams,oplog,polling` ([`start-wekan.sh`](https://github.com/wekan/wekan/blob/main/start-wekan.sh))
- [x] **`polling` (poll-and-diff) is a first-class fallback** — no hard oplog/change-stream dependency (~2000 ms latency without oplog)
- [x] Oplog is only *introspected* for the Admin Panel, never required ([`server/statistics.js`](https://github.com/wekan/wekan/blob/main/server/statistics.js))

### Admin / diagnostic commands
- [x] `serverStatus`, `buildInfo`, `hello`, `listCollections`, `createIndexes`
- [x] `compact`, `replSetGetStatus` — GridFS admin tooling only (skippable with `fs` attachments)

### Not used at all (so not required from FerretDB)
- [x] Multi-document transactions / sessions (`startSession`, `withTransaction`) — WeKan does not use them, but the fork now accepts sessions and the transaction commands as compatibility no-ops (each write auto-commits; no real atomicity/isolation) so MongoDB drivers do not error
- [ ] ~~Capped collections~~ — none created by WeKan
- [x] Full-text search (`$text` / text indexes) — WeKan uses `$regex` instead, but both are now implemented partially: text indexes are accepted and round-tripped, and `$text` matches search terms against a document's string fields (no stemming, no relevance scoring, no `$meta: "textScore"`)
- [ ] ~~GridFS~~ — attachments on filesystem
- [ ] ~~TTL indexes, geospatial, `$where`, `mapReduce`, `collation`~~ — none

---

## 2) What FerretDB v1 already implements

From this repo (`internal/handler/…`, `website/docs/reference/supported-commands.md`).

### CRUD & cursors — implemented
- [x] `find`, `insert`, `update`, `delete` (`internal/handler/msg_find.go`, `msg_insert.go`, `msg_update.go`, `msg_delete.go`)
- [x] `findAndModify` — query/sort/remove/update/new/upsert (`msg_findandmodify.go`)
- [x] `count`, `distinct`, `getMore`, `killCursors`

### Update operators — implemented
- [x] `$set`, `$unset`, `$inc`, `$push`, `$pull`, `$addToSet`, `$pop`, `$rename`, `$mul`, `$min`, `$max`, `$currentDate`, `$bit`
- [x] `$each` supported; modifiers `$slice`, `$sort`, `$position` implemented in `internal/handler/common/update_array_operators.go`
- [x] `$pullAll` — implemented in `internal/handler/common/update_array_operators.go`

### Query filter operators — implemented (`internal/handler/common/filter.go`)
- [x] `$and`, `$or`, `$nor`, `$not`, `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`
- [x] `$in`, `$nin`, `$exists`, `$type`, `$size`, `$all`, `$elemMatch`, `$mod`, `$bitsAllSet`
- [x] **`$regex`** with `$options` (`filterFieldRegex` / `filterFieldExprRegex`)
- [x] **`$where`** — implemented via the embedded pure-Go goja JavaScript engine; evaluates a JS expression or function against each document with `this` bound to the document (`filterWhereOperator` / `operators.EvalWhere`)
- [x] **`$text`** — implemented partially (`filterTextOperator`): matches `$search` terms directly against a document's string fields (recursing into sub-documents and arrays) with multi-term OR, case-insensitive whole-word matching, `$caseSensitive`, double-quoted phrases and leading `-` negation; `$language`/`$diacriticSensitive` are accepted and ignored. No stemming, no relevance scoring, no `$meta: "textScore"` (does not consult the collection's text index)

### Aggregation — partial (`internal/handler/common/aggregations/stages/stages.go`)
- [x] Stages: `$addFields`, `$collStats`, `$count`, `$group`, `$limit`, `$match`, `$project`, `$set`, `$skip`, `$sort`, `$unset`, `$unwind`
- [x] Stage: `$lookup` (basic equality-join form `{ from, localField, foreignField, as }` used by WeKan; the `pipeline`/`let` sub-form returns `ErrNotImplemented`)
- [x] Stages: `$replaceRoot`, `$replaceWith`, `$sortByCount`, `$sample`, `$facet`, `$unionWith` (short `"<coll>"` form and object `{ coll, pipeline }` form; injected like `$lookup`)
- [x] Stages: `$bucket`, `$bucketAuto` (`$bucketAuto` `granularity` option returns `ErrNotImplemented`)
- [x] Accumulator/operator `$sum`, `$count`
- [x] Stage: `$setWindowFields` (`internal/handler/common/aggregations/stages/setwindowfields.go`) — partitions by `partitionBy`, sorts each partition by `sortBy`, and computes `output` fields with window operators. Implemented window operators: `$rank`, `$denseRank`, `$documentNumber` and `$shift` (rank/position, `sortBy` required) and the window accumulators `$sum`, `$avg`, `$min`, `$max`, `$count`, `$push`, `$first`, `$last`, `$stdDevPop`, `$stdDevSamp` over the default full-partition window or an explicit `window: {documents: [lower, upper]}` (integer offsets plus the `"unbounded"`/`"current"` keywords). Deferred (return `ErrNotImplemented`): `$derivative`, `$integral`, `$expMovingAvg`, `$covariancePop`, `$covarianceSamp`, `$linearFill`, `$locf`, `$minN`/`$maxN` and `range` windows — these need extra numeric/interpolation machinery that is not yet plumbed through v1
- [ ] Stages: `$graphLookup`, `$merge`, `$out`, `$changeStream`, `$geoNear`, … (return `ErrNotImplemented`)
- [x] Aggregation expression operators used by WeKan: `$eq`, `$ne`, `$or`, `$ifNull`, `$anyElementTrue`, `$objectToArray`, `$map`
- [x] Comparison/boolean/conditional expression operators: `$cmp`, `$gt`, `$gte`, `$lt`, `$lte`, `$and`, `$not`, `$cond`, `$switch`, `$allElementsTrue`
- [x] Arithmetic expression operators: `$add`, `$subtract`, `$multiply`, `$divide`, `$mod`, `$abs`, `$ceil`, `$floor`, `$trunc`, `$round`, `$pow`, `$sqrt`, `$exp`, `$ln`, `$log`, `$max`, `$min`, `$avg`
- [x] String expression operators: `$concat`, `$toUpper`, `$toLower`, `$strLenCP`, `$strLenBytes`, `$strcasecmp`, `$substr`, `$substrCP`, `$substrBytes`, `$split`, `$trim`, `$ltrim`, `$rtrim`, `$indexOfCP`, `$indexOfBytes`, `$replaceOne`, `$replaceAll`, `$regexMatch`
- [x] Array expression operators: `$size`, `$arrayElemAt`, `$concatArrays`, `$isArray`, `$in`, `$reverseArray`, `$slice`, `$range`, `$indexOfArray`, `$arrayToObject`, `$filter`, `$reduce`, `$sortArray`, `$setUnion`, `$setIntersection`, `$setDifference`, `$setEquals`, `$setIsSubset`, `$zip`
- [x] Type-conversion and related expression operators: `$toString`, `$toInt`, `$toLong`, `$toDouble`, `$toBool`, `$toObjectId`, `$toDate`, `$convert`, `$isNumber`, `$literal`, `$let`, `$getField`, `$setField`, `$unsetField`, `$binarySize`, `$rand`
- [x] Date expression operators: `$year`, `$month`, `$dayOfMonth`, `$hour`, `$minute`, `$second`, `$millisecond`, `$dayOfWeek`, `$dayOfYear`, `$week`, `$isoDayOfWeek`, `$isoWeek`, `$isoWeekYear`, `$dateToString`, `$dateFromString`, `$dateToParts`, `$dateFromParts`, `$dateAdd`, `$dateSubtract`, `$dateDiff`, `$dateTrunc`
- [x] Trigonometric, hyperbolic, angle-conversion and `$log10` expression operators: `$sin`, `$cos`, `$tan`, `$asin`, `$acos`, `$atan`, `$atan2`, `$sinh`, `$cosh`, `$tanh`, `$asinh`, `$acosh`, `$atanh`, `$degreesToRadians`, `$radiansToDegrees`, `$log10` (all return a `double`)
- [x] Data-size expression operator: `$bsonSize` (returns the size in bytes of the BSON encoding of a document as an `int32`, `null` for a `null` argument, and a type-mismatch error for a non-document argument)
- [x] Server-side JavaScript expression operator: `$function` (`{body, args, lang: "js"}`) — implemented via the embedded pure-Go goja JavaScript engine; evaluates the `args` aggregation expressions and runs the user-supplied JS function over them (`internal/handler/common/aggregations/operators/function.go`)
- [ ] Remaining **aggregation expression operators** — a few niche ones still unimplemented: `$toDecimal` (v1's `internal/types` has no `Decimal128` type, so it cannot return a correct `decimal` value without adding a new BSON type through the whole stack), `$meta` (requires per-query metadata plumbing — text score / index key / record id — that the v1 aggregation pipeline does not produce) — these remain in `unsupportedOperators`. The `$setWindowFields`-only window operators (`$rank`, `$denseRank`, `$documentNumber`, `$shift` and the window accumulators including `$stdDevPop`/`$stdDevSamp`) are now implemented directly inside the `$setWindowFields` stage; the remaining window operators (`$derivative`, `$integral`, `$expMovingAvg`, `$covariancePop`, `$covarianceSamp`, `$linearFill`, `$locf`, `$minN`/`$maxN`) are still deferred

### Indexes — partial (`internal/handler/msg_createindexes.go`, `website/docs/indexes.md`)
- [x] Single-field, **compound**, and **unique** indexes (incl. compound-unique)
- [x] `sparse` accepted (silently ignored — comment: *"Ignore for now to make Meteor apps work"*)
- [x] `expireAfterSeconds` (**TTL**) indexes — parsed by createIndexes, reported by listIndexes, and enforced by a background reaper (`internal/handler/handler.go` `runTTLCleanup`)
- [x] **text** indexes (`"text"` key value with `weights`/`default_language`/`language_override`/`textIndexVersion`) — accepted by createIndexes and stored/round-tripped through listIndexes (weights default to 1 per field); pragmatic only, no real inverted/full-text index is built (see the `$text` query operator)
- [x] `hidden`, `collation`, `partialFilterExpression`, `2dsphere` (`"2dsphere"` key value with `2dsphereIndexVersion`) — accepted by createIndexes and stored/round-tripped through listIndexes; accepted and reported only, **not enforced**: no planner-hiding of `hidden` indexes, no locale-aware collation, no partial-index filtering and no geospatial queries
- [ ] `storageEngine`, `bits`, `min`, `max`, `bucketSize`, `wildcardProjection` — `ErrNotImplemented`

### Reactivity — limited
- [x] **Tailable / awaitData cursors** on **capped collections** (poll-based; `msg_find.go`, `msg_getmore.go`)
- [x] Capped collections in the **SQLite backend** (`internal/backends/sqlite/database.go`, `insert.go`, `query.go`)
- [x] **Basic OpLog tailing** via `internal/backends/decorators/oplog/` — writes `local.oplog.rs` records (i/u/d)
- [ ] **Change streams** (`$changeStream` / `watch`) — not supported ([issue #1415](https://github.com/FerretDB/FerretDB/issues/1415))
- [ ] Real **replication** — still not supported; oplog is tailing-only, the capped `local.oplog.rs` must be created **manually** and `FERRETDB_REPL_SET_NAME` set (`website/docs/configuration/oplog-support.md`). The `replSetInitiate` command is now accepted as a compatibility no-op (see the Admin / diagnostic section) but sets up no real replica set

### Sessions / transactions — implemented as compatibility no-ops
- [x] `startSession`, `commitTransaction`, `abortTransaction`, retryable writes — this fork now implements them as compatibility commands (`internal/handler/msg_startsession.go`, `msg_committransaction.go`, `msg_aborttransaction.go`, `msg_endsessions.go`, registered in `internal/handler/commands.go`). `startSession` returns a session record with a generated UUID; `commitTransaction`, `abortTransaction`, `endSessions`, `refreshSessions`, `killSessions`, `killAllSessions` and `killAllSessionsByPattern` return `{ok: 1}`. Write commands accept and ignore the retryable-write / session fields (`lsid`, `txnNumber`, `autocommit`, `startTransaction`, `stmtId`, `stmtIds`). IMPORTANT: these are NO-OP compatibility commands — there are still no real multi-document transactions with the SQLite backend, every write auto-commits on its own, and the commands provide no atomicity or isolation (`abortTransaction` does not roll back writes); logical sessions carry no server-side state

### Admin / diagnostic — implemented
- [x] `serverStatus`, `buildInfo`, `hello`/`ismaster`, `ping`, `collStats`, `dbStats`
- [x] `listCollections`, `listDatabases`, `listIndexes`, `createIndexes`, `dropIndexes`, `create`, `drop`, `compact`
- [x] `getParameter` — implemented (`internal/handler/msg_getparameter.go`; returns `authenticationMechanisms`, `featureCompatibilityVersion`, etc.; verified on SQLite by `TestCommandsAdministrationGetParameter`)
- [x] `replSetInitiate` — implemented as a **compatibility no-op** (`internal/handler/msg_replsetinitiate.go`): accepted with or without a config document and returns `{ok: 1}` (echoing the config `_id` / set name when supplied) so tools and drivers that bootstrap a replica set do not hard-fail. IMPORTANT: it does **not** set up real replication — no oplog is created, no primary is elected and the topology is unchanged; the oplog remains tailing-only and must still be configured manually (see the Reactivity section)

---

## 3) Missing from FerretDB v1 to run WeKan on FerretDB v1 SQLite

Gap analysis: WeKan needs (§1) vs FerretDB has (§2). `[x]` = already works, no action.
`[ ]` = gap to close (in FerretDB) or configure around (in WeKan).

### ✅ Already satisfied — WeKan core runs on FerretDB v1 SQLite today
- [x] Core CRUD + `findOneAndUpdate($inc)` atomic counters (**verified** against this build)
- [x] All update operators WeKan uses: `$set/$unset/$push/$pull/$addToSet/$setOnInsert/$inc/$rename`
- [x] All query operators WeKan uses, incl. **`$regex` + `$options:'i'`** and `$not:{$regex}` (WeKan search)
- [x] Single-field, compound, and unique indexes
- [x] `sparse` index option (silently ignored — harmless for WeKan)
- [x] Admin/startup commands: `serverStatus`, `buildInfo`, `hello`, `listCollections`, `createIndexes`

### ✅ Admin-only aggregation gaps — now implemented in this branch (core kanban always worked)
- [x] **`$lookup` aggregation stage** (basic equality-join form `{ from, localField, foreignField, as }`) → unblocks Prometheus `/metrics` "top boards by activity" ([`models/server/metrics.js`](https://github.com/wekan/wekan/blob/main/models/server/metrics.js)). The `pipeline`/`let` sub-form is still unimplemented.
- [x] **Aggregation expression operators** `$map`, `$objectToArray`, `$ifNull`, `$anyElementTrue`, `$eq`, `$ne`, `$or` → previously broke attachment storage stats ([`server/models/attachmentStorageSettings.js`](https://github.com/wekan/wekan/blob/main/server/models/attachmentStorageSettings.js) `countByStorageSafe` / `countGridFsStoredSafe`); now implemented.
- [x] **Server-side JavaScript** — the `$where` query operator and the `$function` aggregation expression operator are now implemented via the embedded pure-Go goja JavaScript engine (unused by WeKan, but available for MongoDB compatibility).

### 🔁 Reactivity — configure WeKan around the gap
- [ ] **Change streams unsupported** → set `METEOR_REACTIVITY_ORDER=polling` (poll-and-diff, ~2000 ms latency). This is the primary supported path. *(FerretDB to-do: implement change streams — [#1415](https://github.com/FerretDB/FerretDB/issues/1415).)*
- [ ] **No real replication oplog** → optional lower-latency path needs manual setup: create capped `local.oplog.rs`, set `FERRETDB_REPL_SET_NAME`, point `MONGO_OPLOG_URL` at it, and use `METEOR_REACTIVITY_ORDER=oplog,polling`. Needs end-to-end validation with Meteor's oplog tailer.

### 🔎 To verify (partial support — likely fine, confirm under load)
- [x] **`$push`/`$addToSet` with `$each` + `$slice`/`$sort`** — the `$push` modifiers `$slice`, `$sort` and `$position` are implemented in `internal/handler/common/update_array_operators.go` and covered by integration tests (`integration/update_push_modifiers_test.go`). WeKan sites ([`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js), [`server/propagateOrgTeamMembers.js`](https://github.com/wekan/wekan/blob/main/server/propagateOrgTeamMembers.js)).
- [x] **`$pullAll`** (1 site, [`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js)) — implemented and covered by integration tests (`integration/update_pullall_test.go`).
- [x] **`getParameter`** — already implemented (`internal/handler/msg_getparameter.go`); verified working on SQLite, so WeKan startup ([`models/lib/meteorMongoIntegration.js`](https://github.com/wekan/wekan/blob/main/models/lib/meteorMongoIntegration.js)) is fine.

### 🚫 Not needed (WeKan doesn't use them; no action)
- [x] Transactions/sessions, capped collections, TTL indexes, geospatial, `collation`, `mapReduce` — unused by WeKan (`$where`, `$text` and text indexes are likewise unused by WeKan but now implemented: `$where` via the embedded goja JavaScript engine, and `$text`/text indexes partially — search terms are matched against string fields and index options are stored, but no real inverted index is built)
- [x] GridFS commands (`replSetGetStatus`, GridFS `compact`) — attachments on filesystem

---

## Bottom line

WeKan's **core functionality runs on FerretDB v1.24.2 SQLite** with
`METEOR_REACTIVITY_ORDER=polling` and filesystem attachments. No blocking gaps
exist for boards/cards/lists/CRUD/search.

The admin-only aggregation gaps are now **closed** in this branch: the `$lookup`
stage (basic equality-join) and the `$eq`/`$ne`/`$or`/`$ifNull`/`$anyElementTrue`/
`$objectToArray`/`$map` expression operators are implemented and tested, so the
Prometheus `/metrics` and attachment-stats aggregations work. The `$push`
`$slice`/`$sort`/`$position` modifiers and `$pullAll` are likewise implemented/covered.

The only remaining gap is:

1. **Low-latency reactivity** — change streams are unsupported
   ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)); use
   `METEOR_REACTIVITY_ORDER=polling`, or set up basic oplog tailing manually.
   A configuration choice, not a blocker.

A ready-to-run stack is provided in `docker-compose.yml` (+ `Dockerfile`):
`docker compose up --build` builds FerretDB v1 (SQLite) and runs WeKan against it.

A handful of **partial** update-operator modifiers (`$each`+`$slice`/`$sort`,
`$pullAll`) should be validated end-to-end.

---

## FerretDB v1 vs v2

The two FerretDB lines have fundamentally different architectures, which is why
they support different databases.

- **v1** (`main-v1` here, module `github.com/FerretDB/FerretDB`) implements the
  MongoDB-compatibility layer **itself, in Go** (`internal/handler/…` parses and
  executes commands; `internal/handler/common/aggregations/…` implements operators
  and stages), on top of a **pluggable storage layer** (`internal/backends/{sqlite,
  postgresql, mysql, hana}`). Compatibility is *partial* but portable — any backend
  a Go driver can talk to. This branch has greatly expanded that Go layer.
- **v2** (`main`, module `github.com/FerretDB/FerretDB/v2`) is a **thin proxy** that
  translates the MongoDB wire protocol to SQL and delegates all compatibility to
  **PostgreSQL + the [DocumentDB extension](https://github.com/documentdb/documentdb)**
  (`internal/documentdb/…`). There is **no `internal/backends`** — PostgreSQL (with
  the extension) is the only engine. Compatibility is *far more complete* because
  DocumentDB does the heavy lifting, but it is Postgres-extension-bound.

| Capability | v1 (`main-v1`) | v2 (`main`) |
|---|---|---|
| Architecture | Go compatibility layer over pluggable backends | Wire→SQL proxy delegating to DocumentDB |
| **SQLite** (embedded, single file) | ✅ `internal/backends/sqlite` | ❌ not supported |
| **PostgreSQL** (vanilla, no extension) | ✅ `internal/backends/postgresql` | ❌ requires the DocumentDB extension |
| **PostgreSQL + DocumentDB extension** | ❌ | ✅ the only engine |
| **MySQL** | ✅ `internal/backends/mysql` (partial) | ❌ |
| **SAP HANA** | ✅ `internal/backends/hana` (partial) | ❌ |
| Embeddable / no external DB server | ✅ (SQLite in-process) | ❌ (needs PostgreSQL) |
| MongoDB wire target | ~5.0 (reports FCV 7.0) | 5.0+ |
| Aggregation stages | partial — greatly expanded in this branch | ✅ full (DocumentDB) |
| Aggregation expression operators | large subset (≈all common ones after this branch) | ✅ full (DocumentDB) |
| `$lookup` (cross-collection) | ✅ basic equality-join (this branch) | ✅ full |
| TTL indexes (`expireAfterSeconds`) | ✅ this branch (SQLite reaper) | ✅ (DocumentDB) |
| Text search (`$text`) / geospatial | ❌ | ✅ (DocumentDB) |
| Transactions / sessions | ⚠️ compatibility no-ops in this fork (sessions accepted, transaction commands return `{ok:1}`, no real atomicity/isolation) | ✅ (`msg_startsession.go`, via PostgreSQL) |
| Change streams | ❌ (basic oplog tailing only) | evolving (via DocumentDB) |
| Compatibility maintenance | must be written in Go (this fork) | inherited from the DocumentDB project |
| Deployment weight | light (one binary + a data file) | heavier (PostgreSQL + DocumentDB extension) |

**Takeaway:** v1 is the only line that runs on **SQLite / MySQL / SAP HANA /
vanilla PostgreSQL** and is embeddable; v2 is the only line with **near-complete
MongoDB compatibility** (transactions, full aggregation, text/geo) — but only on
PostgreSQL + DocumentDB.

---

## Merging FerretDB v1 and v2

Goal: one FerretDB that keeps v2's completeness **and** v1's reach (SQLite,
vanilla PostgreSQL, MySQL, SAP HANA, embeddable). The obstacle is that v2's
compatibility lives inside a **PostgreSQL C extension (DocumentDB)** that cannot
run inside SQLite or the other backends, while v1's compatibility lives in **Go**
and is portable but incomplete. So a merge cannot simply "use DocumentDB
everywhere"; it must make the compatibility engine **pluggable**.

### Proposal: a pluggable compatibility "engine" behind a shared frontend

Adopt v2 as the base module and reintroduce v1's portability as a selectable
engine, sharing everything above the engine boundary.

1. **Shared wire frontend + conformance suite.** Factor the parts that are
   engine-independent — `clientconn`, wire parsing, command dispatch, error
   mapping (v1 `internal/handler/*`, v2 `internal/mongoerrors`) — into a common
   frontend used by every engine. Make the existing `integration/` compat test
   suite the **single conformance suite** every engine must run in CI; each
   engine's failing tests become its published gap list.

2. **`Engine` strategy interface.** Define one interface (roughly `RunCommand` /
   CRUD / aggregate / index ops) with two implementations:
   - **`documentdb`** — v2's current path (PostgreSQL + DocumentDB): full features.
   - **`go`** — v1's Go compatibility layer (`internal/handler/common/…`, now with
     this branch's ~110 operators, the added stages, and TTL) over
     `internal/backends/{sqlite, postgresql, mysql, hana}`: portable, partial.

3. **Engine/back-end selection from the connection target.** Choose the engine
   from a scheme or env var, e.g. `FERRETDB_ENGINE` or the URL:
   `sqlite:file:/state/` and `mysql://…` → `go` engine; a plain
   `postgres://…` → `go` engine (vanilla Postgres, no extension); a
   `postgres://…?documentdb=on` → `documentdb` engine (full features). This lets a
   single binary serve an embedded SQLite dev instance *and* a
   DocumentDB-backed production instance.

4. **Capability negotiation.** Because engines differ, report per-engine
   capabilities through `buildInfo`/`hello`/`getParameter` (e.g. `transactions`,
   `changeStreams`, `textSearch`) so clients adapt — exactly how WeKan already
   chooses `METEOR_REACTIVITY_ORDER=polling` when change streams are absent. The
   `go` engine advertises the reduced set; `documentdb` advertises the full set.

5. **Incremental convergence.** Close `go`-engine gaps (the `[ ]` items in §2)
   against the shared conformance suite so SQLite/MySQL/Hana parity approaches
   DocumentDB. Features impractical to reimplement portably in Go — multi-document
   **transactions**, **change streams**, **text/geo search** — stay
   `documentdb`-only and are surfaced as unavailable via capability flags rather
   than silently failing. v1's partial **MySQL** and **SAP HANA** backends are
   carried forward under the `go` engine and completed as needed.

### Alternatives considered

- **Port DocumentDB to SQLite ("DocumentDB-lite").** Reimplement DocumentDB's BSON
  operators as SQLite loadable extension functions (or in Go over SQLite). This
  would give SQLite near-v2 fidelity from one implementation, but it is essentially
  rebuilding DocumentDB — very large, and duplicated maintenance. Not recommended
  short-term; revisit only if the `go` engine's gap list proves too costly to
  maintain.
- **Keep two separate products.** Simplest, but perpetuates divergence and means
  SQLite users never benefit from v2's work. The shared frontend + conformance
  suite (step 1) is worth doing even if full engine-pluggability is deferred.

**Recommended path:** steps 1–4 first (shared frontend, `Engine` interface with
`documentdb` + `go`, target-based selection, capability negotiation), then step 5
(convergence) as an ongoing effort. This yields a single FerretDB that supports
SQLite, vanilla PostgreSQL, MySQL and SAP HANA via the `go` engine, and full
MongoDB compatibility via the `documentdb` engine — the WeKan-on-SQLite stack in
this branch becomes the `go`-engine reference deployment.
