# WeKan on FerretDB — Compatibility Roadmap

This roadmap tracks what is needed to run [WeKan](https://github.com/wekan/wekan)
(Meteor 3 / MongoDB) on **FerretDB v1.24.2 with the SQLite backend** (this repo,
`main-v1` branch), and compares that against **FerretDB v2** (`main`, PostgreSQL +
DocumentDB).

Everything is consolidated into the single **[Compatibility matrix](#compatibility-matrix)**
below: every capability, by category, with whether WeKan uses it, the status in v1
and v2, what is missing, and which tests cover it. The v1 and v2 columns were
validated against the actual code on the `main-v1` and `main` branches (see
[Validation notes](#validation-notes)).

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

---

## Legend

**WeKan** — does WeKan depend on this feature?
`✅` uses it · `⚙️` admin/metrics only (off the hot path) · `—` not used by WeKan.

**v1 (main-v1)** / **v2 (main)** — implementation status:

- `✅` full — implemented and verifiable in that branch's own source/tests.
- `⚠️` partial / compatibility-only — see the note in the cell.
- `❌` not implemented — confirmed absent from that branch.
- `✅ᴰ` (**v2 only**) — **delegated to DocumentDB.** v2 forwards the query/pipeline to
  the PostgreSQL DocumentDB extension untouched (`internal/handler/msg_aggregate.go`
  parses no stages), so the feature is **not present in FerretDB/main's own code** and
  its real status is determined by DocumentDB, not verifiable from this repo. Treated
  as "generally works via DocumentDB" but not source-confirmed here.
- `—` n/a.

Notes in the **v1** column name the covering integration test (in `integration/`)
where one exists. Unless stated otherwise, **v1 feature rows are exercised on the
SQLite backend** in this stack; the Go compatibility layer is backend-independent, but
only SQLite is run in CI here (PostgreSQL is untested here; MySQL/HANA are partial
backends). Source paths in `v1` cells are relative to this repo; WeKan sources link to
`github.com/wekan/wekan`.

---

## Compatibility matrix

| Capability | WeKan | v1 (`main-v1`) | v2 (`main`) |
|---|:--:|---|---|
| **— Storage backends / databases —** | | | |
| SQLite (embedded, single file) | ✅ (this stack) | ✅ `internal/backends/sqlite` — the tested target | ❌ no `internal/backends` |
| PostgreSQL (vanilla, no extension) | — | ✅ `internal/backends/postgresql` (untested here) | ❌ requires the DocumentDB extension |
| PostgreSQL + DocumentDB extension | — | ❌ | ✅ the only engine (`internal/documentdb/`) |
| MySQL | — | ⚠️ `internal/backends/mysql` (partial) | ❌ |
| SAP HANA | — | ⚠️ `internal/backends/hana` (partial) | ❌ |
| Embeddable / no external DB server | ✅ | ✅ (SQLite in-process) | ❌ (needs PostgreSQL) |
| MongoDB wire target | — | ~5.0 (reports FCV 7.0) | 5.0+ |
| **— CRUD & cursors —** | | | |
| `find` / `insert` / `update` / `delete` | ✅ | ✅ `msg_find.go` / `msg_insert.go` / `msg_update.go` / `msg_delete.go` | ✅ (compatibility.md) |
| `findAndModify`, `findOneAndUpdate` (`$inc` counters, `upsert`, `returnDocument`) | ✅ [`models/counters.js`](https://github.com/wekan/wekan/blob/main/models/counters.js) | ✅ `msg_findandmodify.go` — **verified** | ✅ |
| `updateOne` `$setOnInsert` + `upsert` (race-safe card numbering) | ✅ [`models/boards.js`](https://github.com/wekan/wekan/blob/main/models/boards.js) | ✅ | ✅ |
| `count`, `distinct`, `getMore`, `killCursors` | ✅ | ✅ | ✅ |
| `rawCollection()` (node-mongodb driver) | ✅ | ✅ | ✅ |
| `bulkWrite` | — | ⚠️ (per-op via driver) | ❌ not implemented yet |
| **— Update operators —** | | | |
| `$set` `$unset` `$inc` `$push` `$pull` `$addToSet` `$rename` `$pop` `$mul` `$min` `$max` `$currentDate` `$bit` | ✅ (`$set`/`$unset`/`$push`/`$pull`/`$addToSet`/`$inc`/`$rename`) | ✅ all present (`common/update.go`) | ✅ᴰ |
| `$each` + `$slice` / `$sort` / `$position` push-modifiers | ✅ | ✅ `update_array_operators.go` · `update_push_modifiers_test.go` | ✅ᴰ |
| `$setOnInsert` | ✅ | ✅ | ✅ᴰ |
| `$pullAll` | ✅ [`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js) | ✅ `update_pullall_test.go` | ✅ᴰ |
| **— Query filter operators —** | | | |
| `$eq` `$ne` `$gt` `$gte` `$lt` `$lte` `$in` `$nin` `$exists` `$type` `$size` `$all` `$elemMatch` `$mod` `$bits*` | ✅ (`$in`/`$gte`/`$ne`/`$exists`/`$size`) | ✅ `filter.go` (all four `$bits*`) | ✅ᴰ |
| `$regex` incl. `$options:'i'`, `$not:{$regex}` (**WeKan's entire search**) | ✅ [`client/lib/filter.js`](https://github.com/wekan/wekan/blob/main/client/lib/filter.js) | ✅ `filterFieldRegex` | ✅ᴰ |
| `$and` `$or` `$nor` `$not` | ✅ (`$or`/`$not`) | ✅ | ✅ᴰ |
| `$expr` | — | ✅ `filterExprOperator` | ✅ᴰ |
| `$where` (server-side JavaScript) | — | ✅ via embedded goja engine; `this` bound to doc, expression or function form (`filterWhereOperator`) · `query_where_test.go` | ❌ no JS engine in DocumentDB; zero refs in v2 |
| `$text` | — | ⚠️ partial (`filterTextOperator`): matches `$search` terms against the doc's string fields (recurses into sub-docs/arrays); multi-term OR, case-insensitive whole-word, `$caseSensitive`, quoted phrases, leading `-` negation. No stemming, no scoring, no `$meta:"textScore"`, does not consult the text index · `query_text_test.go` | ✅ᴰ (full-text-search guides) |
| geospatial (`$near` / `$geoWithin` / `2dsphere` query) | — | ❌ | ✅ᴰ (DocumentDB; not sourced in FerretDB/main) |
| **— Aggregation stages —** | | | |
| `$match` `$group` `$project` `$sort` `$limit` `$skip` `$unwind` `$addFields` `$set` `$unset` `$count` `$collStats` | ⚙️ [`models/server/metrics.js`](https://github.com/wekan/wekan/blob/main/models/server/metrics.js) | ✅ `stages/…` (map + `init()`-injected) | ✅ᴰ |
| `$lookup` | ⚙️ (Prometheus "top boards") | ⚠️ basic equality-join only; `pipeline`/`let` sub-form `ErrNotImplemented` · `aggregate_lookup_test.go` | ✅ᴰ (form coverage DocumentDB-determined) |
| `$replaceRoot` `$replaceWith` `$sortByCount` `$sample` `$facet` `$unionWith` | — | ✅ (`$facet`/`$bucket`/`$unionWith` `init()`-injected) · `aggregate_stages_extra_test.go`, `aggregate_facet_test.go`, `aggregate_unionwith_test.go` | ✅ᴰ |
| `$bucket` `$bucketAuto` | — | ⚠️ (`$bucketAuto` `granularity` `ErrNotImplemented`) · `aggregate_bucket_test.go` | ✅ᴰ |
| `$setWindowFields` | — | ⚠️ stage + window ops (see window rows) · `aggregate_setwindowfields_test.go` | ✅ᴰ |
| `$graphLookup` `$merge` `$out` `$geoNear` | — | ❌ `ErrNotImplemented` (in `unsupportedStages`) | ✅ᴰ |
| `$changeStream` (stage) | — | ❌ `ErrNotImplemented` | ❌ only in `internal/mongoerrors`; no handler |
| **— Aggregation expression operators —** | | | |
| WeKan-used: `$map` `$objectToArray` `$ifNull` `$anyElementTrue` `$eq` `$ne` `$or` | ⚙️ [`server/models/attachmentStorageSettings.js`](https://github.com/wekan/wekan/blob/main/server/models/attachmentStorageSettings.js) | ✅ `aggregate_expr_operators_test.go` | ✅ᴰ |
| Comparison/boolean/conditional: `$cmp` `$gt…$lte` `$and` `$not` `$cond` `$switch` `$allElementsTrue` | — | ✅ `aggregate_expr_bool_test.go` | ✅ᴰ |
| Arithmetic: `$add` `$subtract` `$multiply` `$divide` `$mod` `$abs` `$ceil` `$floor` `$trunc` `$round` `$pow` `$sqrt` `$exp` `$ln` `$log` `$max` `$min` `$avg` | — | ✅ `aggregate_expr_arithmetic_test.go` | ✅ᴰ |
| String: `$concat` `$toUpper` `$toLower` `$strLen*` `$substr*` `$split` `$trim*` `$indexOf*` `$replaceOne` `$replaceAll` `$regexMatch` | — | ✅ `aggregate_expr_string_test.go` | ✅ᴰ |
| Array: `$size` `$arrayElemAt` `$concatArrays` `$isArray` `$in` `$reverseArray` `$slice` `$range` `$indexOfArray` `$arrayToObject` `$filter` `$reduce` `$sortArray` `$set*` `$zip` | — | ✅ `aggregate_expr_array_test.go` | ✅ᴰ |
| Type-conversion: `$toString` `$toInt` `$toLong` `$toDouble` `$toBool` `$toObjectId` `$toDate` `$convert` `$isNumber` `$literal` `$let` `$getField` `$setField` `$unsetField` `$binarySize` `$rand` | — | ✅ `aggregate_expr_convert_test.go` | ✅ᴰ |
| Date: `$year`…`$millisecond` `$isoWeek*` `$dateToString` `$dateFromString` `$dateTo/FromParts` `$dateAdd` `$dateSubtract` `$dateDiff` `$dateTrunc` | — | ✅ `aggregate_expr_date_test.go` | ✅ᴰ |
| Trig / hyperbolic / angle / `$log10`: `$sin` `$cos` `$tan` `$asin` `$acos` `$atan` `$atan2` `$sinh` `$cosh` `$tanh` `$asinh` `$acosh` `$atanh` `$degreesToRadians` `$radiansToDegrees` `$log10` | — | ✅ (return `double`) · `aggregate_expr_trig_test.go` | ✅ᴰ |
| `$bsonSize` | — | ✅ (BSON byte size as `int32`; `null`→`null`; type error otherwise) · `aggregate_expr_bsonsize_test.go` | ✅ᴰ |
| `$function` (server-side JavaScript, `{body,args,lang:"js"}`) | — | ✅ via embedded goja engine (`operators/function.go`) · `aggregate_expr_function_test.go` | ❌ no JS engine in DocumentDB; zero refs in v2 |
| `$toDecimal` | — | ❌ v1's `internal/types` has **no `Decimal128` type** | ✅ᴰ |
| `$meta` | — | ❌ needs per-query metadata plumbing v1 does not produce | ✅ᴰ |
| **— Window operators (inside `$setWindowFields`) —** | | | |
| `$rank` `$denseRank` `$documentNumber` `$shift` (require `sortBy`) | — | ✅ · `aggregate_setwindowfields_test.go` | ✅ᴰ |
| Window accumulators `$sum` `$avg` `$min` `$max` `$count` `$push` `$first` `$last` `$stdDevPop` `$stdDevSamp` (full-partition or `window:{documents:[l,u]}`) | — | ✅ · `aggregate_setwindowfields_test.go` | ✅ᴰ |
| `$derivative` `$integral` `$expMovingAvg` `$covariancePop` `$covarianceSamp` `$linearFill` `$locf` `$minN`/`$maxN`, `range` windows | — | ❌ deferred (return not-implemented) | ✅ᴰ |
| **— Indexes —** | | | |
| Single-field, compound, unique (incl. compound-unique) | ✅ [`server/lib/mongoStartup.js`](https://github.com/wekan/wekan/blob/main/server/lib/mongoStartup.js) | ✅ `msg_createindexes.go` | ✅ (`createIndexes` supported) |
| `sparse` | — | ⚠️ accepted, silently ignored (*"to make Meteor apps work"*) | ✅ᴰ |
| `expireAfterSeconds` (**TTL**) | — | ✅ createIndexes + listIndexes + background reaper (`handler.go runTTLCleanup`) | ✅ (`ttl-indexes` guide) |
| **text** (`"text"` key, `weights` / `default_language` / `language_override` / `textIndexVersion`) | — | ⚠️ accepted, stored & round-tripped via listIndexes; **no real inverted index** (see `$text`) · `query_text_test.go` | ✅ᴰ (full-text-search guides) |
| `hidden` | — | ⚠️ accepted/stored/reported; **not** hidden from the planner · `createindexes_options_test.go` | ✅ᴰ |
| `collation` | — | ⚠️ accepted/stored/reported; **no** locale-aware collation · `createindexes_options_test.go` | ✅ᴰ |
| `partialFilterExpression` | — | ⚠️ accepted/stored/reported; index **not** restricted to matching docs · `createindexes_options_test.go` | ✅ᴰ |
| `2dsphere` (`"2dsphere"` key + `2dsphereIndexVersion`) | — | ⚠️ accepted/stored/reported; **no** geospatial queries · `createindexes_options_test.go` | ✅ᴰ |
| `storageEngine` `bits` `min` `max` `bucketSize` `wildcardProjection` | — | ❌ `ErrNotImplemented` | ✅ᴰ |
| **— Reactivity (Meteor pub/sub) —** | | | |
| `polling` (poll-and-diff) — primary supported path | ✅ [`start-wekan.sh`](https://github.com/wekan/wekan/blob/main/start-wekan.sh) | ✅ (~2000 ms latency; no oplog dependency) | ✅ |
| Capped collections + tailable / awaitData cursors | ⚙️ | ✅ SQLite backend (`msg_find.go`, `msg_getmore.go`) | ⚠️ᴰ (DocumentDB; not sourced here) |
| MongoDB oplog tailing (`local.oplog.rs` i/u/d) | ⚙️ (Admin Panel introspection only) | ⚠️ tailing-only via `backends/decorators/oplog/`; capped oplog created manually + `FERRETDB_REPL_SET_NAME` | ❌ no Mongo oplog (uses Postgres WAL) |
| Change streams (`$changeStream` / `watch`) | — | ❌ ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)) → use `METEOR_REACTIVITY_ORDER=polling` | ❌ no `watch`/change-stream handler in v2 |
| Real replication / elected primary | — | ❌ (`replSetInitiate` is a no-op) | ⚠️ PostgreSQL WAL streaming replication — **not** a MongoDB replica set / oplog |
| **— Sessions / transactions —** | | | |
| `startSession` | — | ⚠️ compat: returns a session record with a generated UUID (`msg_startsession.go`) · `sessions_transactions_test.go` | ✅ logical session record only (`msg_startsession.go`) |
| `commitTransaction` / `abortTransaction` | — | ⚠️ compat no-op `{ok:1}`; **no** atomicity/isolation · `sessions_transactions_test.go` | ❌ "Not implemented yet" (compatibility.md; #1548/#1547) |
| `endSessions` `refreshSessions` `killSessions` `killAllSessions` `killAllSessionsByPattern` | — | ⚠️ compat no-op `{ok:1}` (`msg_endsessions.go`) | ✅ᴰ (session-mgmt commands) |
| Retryable-write / session fields (`lsid` `txnNumber` `autocommit` `startTransaction` `stmtId` `stmtIds`) | — | ⚠️ accepted & ignored on insert/update/delete/findAndModify · `sessions_transactions_test.go` | ⚠️ not evidenced in v2 |
| Real multi-document transactions (atomicity/isolation) | — | ❌ every write auto-commits (SQLite) | ❌ commit/abort unimplemented (#1548/#1547) |
| **— Admin / diagnostic —** | | | |
| `serverStatus` `buildInfo` `hello`/`ismaster` `ping` `collStats` `dbStats` | ✅ | ✅ | ✅ |
| `listCollections` `listDatabases` `listIndexes` `createIndexes` `dropIndexes` `create` `drop` `compact` | ✅ | ✅ | ✅ |
| `getParameter` | ✅ [`meteorMongoIntegration.js`](https://github.com/wekan/wekan/blob/main/models/lib/meteorMongoIntegration.js) | ✅ `msg_getparameter.go` (verified, `TestCommandsAdministrationGetParameter`) | ✅ |
| `replSetInitiate` | — | ⚠️ compat no-op `{ok:1}` (echoes config `_id`); **no** real replica set (`msg_replsetinitiate.go`) · `replsetinitiate_test.go` | ❌ not registered; TODO in `msg_hello.go` (#3936) |
| `replSetGetStatus` | ⚙️ (GridFS admin tooling; skippable with `fs`) | ❌ not registered in `commands.go` | ❌ not registered |
| **— Not required by WeKan —** | | | |
| GridFS storage / commands | — (attachments on filesystem) | ⚠️ driver convention over `fs.files`/`fs.chunks` CRUD | ✅ (same driver convention) |
| `mapReduce` | — | ❌ not registered | ❌ not registered (no handler) |

**Backends note.** In v1 the whole matrix is implemented in the portable Go layer
(`internal/handler/…`), so it applies to any `internal/backends/*` engine — but **only
SQLite is exercised/tested in this stack**. MySQL and SAP HANA are partial backends;
vanilla PostgreSQL is complete but untested here.

**v2 delegation note (why so many `✅ᴰ`).** v2 is a thin proxy: `msg_aggregate.go`
forwards the pipeline to DocumentDB without parsing stages, and query operators are
likewise executed by the DocumentDB PostgreSQL extension. So for aggregation stages,
expression operators, most query operators and the richer index options, **the real
support lives in DocumentDB and cannot be sourced from FerretDB/main** — hence `✅ᴰ`
("delegated; generally works via DocumentDB, not verified here") rather than a plain
`✅`. What *is* code-confirmed as **missing in v2** is listed as `❌`: server-side
JavaScript (`$where`, `$function`), change streams, real multi-document transactions
(`commitTransaction`/`abortTransaction`), `replSetInitiate`, `replSetGetStatus` and
`mapReduce`.

---

## Validation notes

Both feature columns were checked against the branches' real code (2026-07):

- **v1 (`main-v1`)** — verified from the registration maps and handlers: the
  aggregation `Stages` map plus `init()`-injected `$facet`/`$bucket`/`$bucketAuto`/
  `$unionWith`; the `Operators` / `unsupportedOperators` maps; `filter.go`'s operator
  cases (incl. `$where`, `$text`); the update-operator set; `msg_createindexes.go`
  option handling; and `commands.go`. Correction applied vs. an earlier draft:
  `replSetGetStatus` is **not** implemented in v1 (`❌`, not `⚠️`); `$where` is a
  genuine goja implementation (`✅`, matching `$function`).
- **v2 (`main`)** — verified that there is **no `internal/backends`** (only
  `internal/documentdb/`), that `msg_aggregate.go` forwards pipelines untouched, and
  from v2's own `website/docs/migration/compatibility.md`. Corrections applied vs. an
  earlier draft that over-credited v2: `$where`, `$function`, `$changeStream`, change
  streams, `replSetInitiate`, `replSetGetStatus` and `mapReduce` are **not implemented
  in v2** (`❌`); `commitTransaction`/`abortTransaction` are explicitly "Not
  implemented yet" so **real multi-document transactions are `❌`** (only `startSession`
  session-bookkeeping works); and v2's replication is **PostgreSQL WAL streaming, not a
  MongoDB replica set/oplog** (`⚠️`). Everything DocumentDB executes on v2's behalf is
  marked `✅ᴰ` because it is not verifiable from FerretDB/main's source.

---

## Bottom line

WeKan's **core functionality runs on FerretDB v1.24.2 SQLite** with
`METEOR_REACTIVITY_ORDER=polling` and filesystem attachments — no blocking gaps for
boards/cards/lists/CRUD/search. The admin-only aggregation gaps (Prometheus
`/metrics` "top boards" via `$lookup`; attachment-stats via `$map`/`$objectToArray`/
`$ifNull`/`$anyElementTrue`/`$eq`/`$ne`/`$or`) are closed and tested.

The one real gap for WeKan is **low-latency reactivity**: change streams are
unsupported in v1 ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)); use
`METEOR_REACTIVITY_ORDER=polling`, or set up basic oplog tailing manually. A
configuration choice, not a blocker.

Beyond WeKan's needs, this branch also adds broad MongoDB compatibility (trigonometry,
`$bsonSize`, `$where`/`$function` server-side JS, `$setWindowFields`, text/`hidden`/
`collation`/`partialFilterExpression`/`2dsphere` index options, session/transaction
compat commands, `replSetInitiate`) — see the matrix for exact full/partial status.
Notably, several of these (server-side JS, session/transaction *commands*,
`replSetInitiate`) exist in **v1 but not in v2** — as compatibility shims — while v2
leads on everything DocumentDB implements natively.

A ready-to-run stack is provided in `docker-compose.yml` (+ `Dockerfile`):
`docker compose up --build` builds FerretDB v1 (SQLite) and runs WeKan against it.

---

## FerretDB v1 vs v2 — why the matrix differs

The two FerretDB lines have fundamentally different architectures, which is why they
support different databases:

- **v1** (`main-v1`, module `github.com/FerretDB/FerretDB`) implements the
  MongoDB-compatibility layer **itself, in Go** (`internal/handler/…` parses and
  executes commands; `internal/handler/common/aggregations/…` implements operators and
  stages), on top of a **pluggable storage layer** (`internal/backends/{sqlite,
  postgresql, mysql, hana}`). Compatibility is *partial* but portable — any backend a
  Go driver can talk to. This branch has greatly expanded that Go layer.
- **v2** (`main`, module `github.com/FerretDB/FerretDB/v2`) is a **thin proxy** that
  translates the MongoDB wire protocol to SQL and delegates all compatibility to
  **PostgreSQL + the [DocumentDB extension](https://github.com/documentdb/documentdb)**
  (`internal/documentdb/…`). There is **no `internal/backends`** — PostgreSQL (with the
  extension) is the only engine. Aggregation/operator coverage is *far broader* because
  DocumentDB does the heavy lifting, but a number of MongoDB features are still not
  implemented in the v2 proxy itself (server-side JavaScript, multi-document
  transactions, change streams, replica-set admin commands, `mapReduce`).

**Takeaway:** v1 is the only line that runs on **SQLite / MySQL / SAP HANA / vanilla
PostgreSQL** and is embeddable; v2 has **broader aggregation/query compatibility** via
DocumentDB (text/geo, the full operator set), but **multi-document transactions and
change streams are not yet implemented** in either line, and only v1 carries the
server-side-JS and session/replica-set *compatibility shims*.

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
   - **`documentdb`** — v2's current path (PostgreSQL + DocumentDB): broad features.
   - **`go`** — v1's Go compatibility layer (`internal/handler/common/…`, now with
     this branch's expanded operators, stages, window functions, text/index options
     and session/transaction compat) over `internal/backends/{sqlite, postgresql,
     mysql, hana}`: portable, partial.

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
   `go` engine advertises the reduced set; `documentdb` advertises the broader set.

5. **Incremental convergence.** Close `go`-engine gaps (the `⚠️`/`❌` v1 cells in the
   matrix) against the shared conformance suite so SQLite/MySQL/Hana parity approaches
   DocumentDB. Features impractical to reimplement portably in Go — real multi-document
   **transactions**, **change streams**, **text/geo search** — are surfaced as
   available/unavailable per engine via capability flags rather than silently failing
   (note: multi-document transactions and change streams are currently missing in
   *both* lines). v1's partial **MySQL** and **SAP HANA** backends are carried forward
   under the `go` engine and completed as needed.

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
SQLite, vanilla PostgreSQL, MySQL and SAP HANA via the `go` engine, and broad
MongoDB compatibility via the `documentdb` engine — the WeKan-on-SQLite stack in
this branch becomes the `go`-engine reference deployment.
