# WeKan on FerretDB — Compatibility Roadmap

This roadmap tracks what is needed to run [WeKan](https://github.com/wekan/wekan)
(Meteor 3 / MongoDB) on **FerretDB v1.24.2 with the SQLite backend** (this repo,
`main-v1` branch), and compares that against **FerretDB v2** (`main`, PostgreSQL +
DocumentDB).

Everything is consolidated into the single **[Compatibility matrix](#compatibility-matrix)**
below: every capability, by category, with whether WeKan uses it, the status in v1
and v2, what is missing, and which tests cover it.

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
`✅` full · `⚠️` partial or compatibility-only (see note) · `❌` not implemented · `—` n/a.

Notes in the v1 column name the covering integration test (in `integration/`) where
one exists. Unless stated otherwise, **v1 feature rows are exercised on the SQLite
backend** in this stack; the Go compatibility layer is backend-independent, but only
SQLite is run in CI here (PostgreSQL is untested here; MySQL/HANA are partial
backends). v2 rows are inherited from DocumentDB and covered by v2's own suite.

Source paths in `v1` cells are relative to this repo; WeKan sources link to
`github.com/wekan/wekan`.

---

## Compatibility matrix

| Capability | WeKan | v1 (`main-v1`) | v2 (`main`) |
|---|:--:|---|---|
| **— Storage backends / databases —** | | | |
| SQLite (embedded, single file) | ✅ (this stack) | ✅ `internal/backends/sqlite` — the tested target | ❌ not supported |
| PostgreSQL (vanilla, no extension) | — | ✅ `internal/backends/postgresql` (untested here) | ❌ requires the DocumentDB extension |
| PostgreSQL + DocumentDB extension | — | ❌ | ✅ the only engine |
| MySQL | — | ⚠️ `internal/backends/mysql` (partial) | ❌ |
| SAP HANA | — | ⚠️ `internal/backends/hana` (partial) | ❌ |
| Embeddable / no external DB server | ✅ | ✅ (SQLite in-process) | ❌ (needs PostgreSQL) |
| MongoDB wire target | — | ~5.0 (reports FCV 7.0) | 5.0+ |
| **— CRUD & cursors —** | | | |
| `find` / `insert` / `update` / `delete` | ✅ | ✅ `msg_find.go` / `msg_insert.go` / `msg_update.go` / `msg_delete.go` · upstream suite | ✅ |
| `findAndModify`, `findOneAndUpdate` (`$inc` counters, `upsert`, `returnDocument`) | ✅ [`models/counters.js`](https://github.com/wekan/wekan/blob/main/models/counters.js) | ✅ `msg_findandmodify.go` — **verified** | ✅ |
| `updateOne` `$setOnInsert` + `upsert` (race-safe card numbering) | ✅ [`models/boards.js`](https://github.com/wekan/wekan/blob/main/models/boards.js) | ✅ | ✅ |
| `count`, `distinct`, `getMore`, `killCursors` | ✅ | ✅ | ✅ |
| `rawCollection()` (node-mongodb driver) | ✅ | ✅ | ✅ |
| **— Update operators —** | | | |
| `$set` `$unset` `$inc` `$push` `$pull` `$addToSet` `$rename` `$pop` `$mul` `$min` `$max` `$currentDate` `$bit` | ✅ (`$set`/`$unset`/`$push`/`$pull`/`$addToSet`/`$inc`/`$rename`) | ✅ upstream suite | ✅ |
| `$each` + `$slice` / `$sort` / `$position` push-modifiers | ✅ | ✅ `update_array_operators.go` · `update_push_modifiers_test.go` | ✅ |
| `$setOnInsert` | ✅ | ✅ | ✅ |
| `$pullAll` | ✅ [`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js) | ✅ `update_pullall_test.go` | ✅ |
| **— Query filter operators —** | | | |
| `$eq` `$ne` `$gt` `$gte` `$lt` `$lte` `$in` `$nin` `$exists` `$type` `$size` `$all` `$elemMatch` `$mod` `$bits*` | ✅ (`$in`/`$gte`/`$ne`/`$exists`/`$size`) | ✅ `filter.go` · upstream suite | ✅ |
| `$regex` incl. `$options:'i'`, `$not:{$regex}` (**WeKan's entire search**) | ✅ [`client/lib/filter.js`](https://github.com/wekan/wekan/blob/main/client/lib/filter.js) | ✅ `filterFieldRegex` | ✅ |
| `$and` `$or` `$nor` `$not` | ✅ (`$or`/`$not`) | ✅ | ✅ |
| `$expr` | — | ✅ `filterExprOperator` | ✅ |
| `$where` (server-side JavaScript) | — | ⚠️ full-ish via embedded goja engine; `this` bound to doc, expression or function form (`filterWhereOperator`) · `query_where_test.go` | ⚠️ limited (DocumentDB server-side JS) |
| `$text` | — | ⚠️ partial (`filterTextOperator`): matches `$search` terms against the doc's string fields (recurses into sub-docs/arrays); multi-term OR, case-insensitive whole-word, `$caseSensitive`, quoted phrases, leading `-` negation. No stemming, no scoring, no `$meta:"textScore"`, does not consult the text index · `query_text_test.go` | ✅ (DocumentDB) |
| geospatial (`$near` / `$geoWithin`) | — | ❌ | ✅ (DocumentDB) |
| **— Aggregation stages —** | | | |
| `$match` `$group` `$project` `$sort` `$limit` `$skip` `$unwind` `$addFields` `$set` `$unset` `$count` `$collStats` | ⚙️ [`models/server/metrics.js`](https://github.com/wekan/wekan/blob/main/models/server/metrics.js) | ✅ `stages/…` | ✅ |
| `$lookup` (basic equality-join `{from,localField,foreignField,as}`) | ⚙️ (Prometheus "top boards") | ⚠️ equality-join only; `pipeline`/`let` sub-form `ErrNotImplemented` · `aggregate_lookup_test.go` | ✅ full |
| `$replaceRoot` `$replaceWith` `$sortByCount` `$sample` `$facet` `$unionWith` | — | ✅ `aggregate_stages_extra_test.go`, `aggregate_facet_test.go`, `aggregate_unionwith_test.go` | ✅ |
| `$bucket` `$bucketAuto` | — | ⚠️ (`$bucketAuto` `granularity` `ErrNotImplemented`) · `aggregate_bucket_test.go` | ✅ |
| `$setWindowFields` | — | ⚠️ stage + window ops (see window-operator rows) · `aggregate_setwindowfields_test.go` | ✅ |
| `$graphLookup` `$merge` `$out` `$geoNear` `$changeStream` | — | ❌ `ErrNotImplemented` | ✅ (`$changeStream` evolving) |
| **— Aggregation expression operators —** | | | |
| WeKan-used: `$map` `$objectToArray` `$ifNull` `$anyElementTrue` `$eq` `$ne` `$or` | ⚙️ [`server/models/attachmentStorageSettings.js`](https://github.com/wekan/wekan/blob/main/server/models/attachmentStorageSettings.js) | ✅ `aggregate_expr_operators_test.go` | ✅ |
| Comparison/boolean/conditional: `$cmp` `$gt…$lte` `$and` `$not` `$cond` `$switch` `$allElementsTrue` | — | ✅ `aggregate_expr_bool_test.go` | ✅ |
| Arithmetic: `$add` `$subtract` `$multiply` `$divide` `$mod` `$abs` `$ceil` `$floor` `$trunc` `$round` `$pow` `$sqrt` `$exp` `$ln` `$log` `$max` `$min` `$avg` | — | ✅ `aggregate_expr_arithmetic_test.go` | ✅ |
| String: `$concat` `$toUpper` `$toLower` `$strLen*` `$substr*` `$split` `$trim*` `$indexOf*` `$replaceOne` `$replaceAll` `$regexMatch` | — | ✅ `aggregate_expr_string_test.go` | ✅ |
| Array: `$size` `$arrayElemAt` `$concatArrays` `$isArray` `$in` `$reverseArray` `$slice` `$range` `$indexOfArray` `$arrayToObject` `$filter` `$reduce` `$sortArray` `$set*` `$zip` | — | ✅ `aggregate_expr_array_test.go` | ✅ |
| Type-conversion: `$toString` `$toInt` `$toLong` `$toDouble` `$toBool` `$toObjectId` `$toDate` `$convert` `$isNumber` `$literal` `$let` `$getField` `$setField` `$unsetField` `$binarySize` `$rand` | — | ✅ `aggregate_expr_convert_test.go` | ✅ |
| Date: `$year`…`$millisecond` `$isoWeek*` `$dateToString` `$dateFromString` `$dateTo/FromParts` `$dateAdd` `$dateSubtract` `$dateDiff` `$dateTrunc` | — | ✅ `aggregate_expr_date_test.go` | ✅ |
| Trigonometric / hyperbolic / angle / `$log10`: `$sin` `$cos` `$tan` `$asin` `$acos` `$atan` `$atan2` `$sinh` `$cosh` `$tanh` `$asinh` `$acosh` `$atanh` `$degreesToRadians` `$radiansToDegrees` `$log10` | — | ✅ (return `double`) · `aggregate_expr_trig_test.go` | ✅ |
| `$bsonSize` | — | ✅ (BSON byte size as `int32`; `null`→`null`; type error otherwise) · `aggregate_expr_bsonsize_test.go` | ✅ |
| `$function` (server-side JavaScript, `{body,args,lang:"js"}`) | — | ✅ via embedded goja engine (`operators/function.go`) · `aggregate_expr_function_test.go` | ⚠️ limited |
| `$toDecimal` | — | ❌ v1's `internal/types` has **no `Decimal128` type**; would need a new BSON type through the whole stack | ✅ |
| `$meta` | — | ❌ needs per-query metadata plumbing (text score / index key / record id) v1 does not produce | ✅ |
| **— Window operators (inside `$setWindowFields`) —** | | | |
| `$rank` `$denseRank` `$documentNumber` `$shift` (require `sortBy`) | — | ✅ · `aggregate_setwindowfields_test.go` | ✅ |
| Window accumulators `$sum` `$avg` `$min` `$max` `$count` `$push` `$first` `$last` `$stdDevPop` `$stdDevSamp` (full-partition or `window:{documents:[l,u]}`) | — | ✅ · `aggregate_setwindowfields_test.go` | ✅ |
| `$derivative` `$integral` `$expMovingAvg` `$covariancePop` `$covarianceSamp` `$linearFill` `$locf` `$minN`/`$maxN`, `range` windows | — | ❌ deferred (return not-implemented) — need numeric/interpolation machinery not yet plumbed | ✅ |
| **— Indexes —** | | | |
| Single-field, compound, unique (incl. compound-unique) | ✅ [`server/lib/mongoStartup.js`](https://github.com/wekan/wekan/blob/main/server/lib/mongoStartup.js) | ✅ `msg_createindexes.go` | ✅ |
| `sparse` | — | ⚠️ accepted, silently ignored (*"to make Meteor apps work"*) | ✅ |
| `expireAfterSeconds` (**TTL**) | — | ✅ createIndexes + listIndexes + background reaper (`handler.go runTTLCleanup`) | ✅ |
| **text** (`"text"` key, `weights` / `default_language` / `language_override` / `textIndexVersion`) | — | ⚠️ accepted, stored & round-tripped via listIndexes (weights default 1/field); **no real inverted index** (see `$text`) · `query_text_test.go` | ✅ |
| `hidden` | — | ⚠️ accepted/stored/reported; **not** hidden from the planner · `createindexes_options_test.go` | ✅ |
| `collation` | — | ⚠️ accepted/stored/reported; **no** locale-aware collation · `createindexes_options_test.go` | ✅ |
| `partialFilterExpression` | — | ⚠️ accepted/stored/reported; index **not** restricted to matching docs · `createindexes_options_test.go` | ✅ |
| `2dsphere` (`"2dsphere"` key + `2dsphereIndexVersion`) | — | ⚠️ accepted/stored/reported; **no** geospatial queries · `createindexes_options_test.go` | ✅ |
| `storageEngine` `bits` `min` `max` `bucketSize` `wildcardProjection` | — | ❌ `ErrNotImplemented` | ✅ |
| **— Reactivity (Meteor pub/sub) —** | | | |
| `polling` (poll-and-diff) — primary supported path | ✅ [`start-wekan.sh`](https://github.com/wekan/wekan/blob/main/start-wekan.sh) | ✅ (~2000 ms latency; no oplog dependency) | ✅ |
| Capped collections + tailable / awaitData cursors | ⚙️ | ✅ SQLite backend (`msg_find.go`, `msg_getmore.go`) | ✅ |
| Basic OpLog tailing (`local.oplog.rs` i/u/d) | ⚙️ (Admin Panel introspection only) | ⚠️ tailing-only via `backends/decorators/oplog/`; capped oplog created manually + `FERRETDB_REPL_SET_NAME` | ⚠️ |
| Change streams (`$changeStream` / `watch`) | — | ❌ ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)) → use `METEOR_REACTIVITY_ORDER=polling` | ⚠️ evolving (DocumentDB) |
| Real replication / elected primary | — | ❌ (`replSetInitiate` is a no-op — see Admin) | ✅ |
| **— Sessions / transactions —** | | | |
| `startSession` | — | ⚠️ compat: returns a session record with a generated UUID (`msg_startsession.go`) · `sessions_transactions_test.go` | ✅ |
| `commitTransaction` / `abortTransaction` | — | ⚠️ compat no-op `{ok:1}`; **no** atomicity/isolation, `abort` does not roll back · `sessions_transactions_test.go` | ✅ |
| `endSessions` `refreshSessions` `killSessions` `killAllSessions` `killAllSessionsByPattern` | — | ⚠️ compat no-op `{ok:1}` (`msg_endsessions.go`) | ✅ |
| Retryable-write / session fields (`lsid` `txnNumber` `autocommit` `startTransaction` `stmtId` `stmtIds`) | — | ⚠️ accepted & ignored on insert/update/delete/findAndModify · `sessions_transactions_test.go` | ✅ |
| Real multi-document transactions | — | ❌ every write auto-commits (SQLite backend) | ✅ (via PostgreSQL) |
| **— Admin / diagnostic —** | | | |
| `serverStatus` `buildInfo` `hello`/`ismaster` `ping` `collStats` `dbStats` | ✅ | ✅ | ✅ |
| `listCollections` `listDatabases` `listIndexes` `createIndexes` `dropIndexes` `create` `drop` `compact` | ✅ | ✅ | ✅ |
| `getParameter` | ✅ [`meteorMongoIntegration.js`](https://github.com/wekan/wekan/blob/main/models/lib/meteorMongoIntegration.js) | ✅ `msg_getparameter.go` (verified, `TestCommandsAdministrationGetParameter`) | ✅ |
| `replSetInitiate` | — | ⚠️ compat no-op `{ok:1}` (echoes config `_id`); **no** real replica set (`msg_replsetinitiate.go`) · `replsetinitiate_test.go` | ✅ |
| `replSetGetStatus` | ⚙️ (GridFS admin tooling; skippable with `fs`) | ⚠️ | ✅ |
| **— Not required by WeKan —** | | | |
| GridFS storage / commands | — (attachments on filesystem) | ⚠️ present but out of scope | ✅ |
| `mapReduce` | — | ❌ | ⚠️ |

**Backends note.** In v1 the whole matrix above is implemented in the portable Go
layer (`internal/handler/…`), so it applies to any `internal/backends/*` engine —
but **only SQLite is exercised/tested in this stack**. MySQL and SAP HANA are partial
backends; vanilla PostgreSQL is complete but untested here. In v2 there is a single
engine (PostgreSQL + DocumentDB), so "supported" always means "on that engine".

---

## Bottom line

WeKan's **core functionality runs on FerretDB v1.24.2 SQLite** with
`METEOR_REACTIVITY_ORDER=polling` and filesystem attachments — no blocking gaps for
boards/cards/lists/CRUD/search. The admin-only aggregation gaps (Prometheus
`/metrics` "top boards" via `$lookup`; attachment-stats via `$map`/`$objectToArray`/
`$ifNull`/`$anyElementTrue`/`$eq`/`$ne`/`$or`) are closed and tested.

The one real gap for WeKan is **low-latency reactivity**: change streams are
unsupported ([#1415](https://github.com/FerretDB/FerretDB/issues/1415)); use
`METEOR_REACTIVITY_ORDER=polling`, or set up basic oplog tailing manually. A
configuration choice, not a blocker.

Beyond WeKan's needs, this branch also adds broad MongoDB compatibility (trigonometry,
`$bsonSize`, `$where`/`$function` server-side JS, `$setWindowFields`, text/`hidden`/
`collation`/`partialFilterExpression`/`2dsphere` index options, session/transaction
compat commands, `replSetInitiate`) — see the matrix for exact full/partial status.

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
  extension) is the only engine. Compatibility is *far more complete* because DocumentDB
  does the heavy lifting, but it is Postgres-extension-bound.

**Takeaway:** v1 is the only line that runs on **SQLite / MySQL / SAP HANA / vanilla
PostgreSQL** and is embeddable; v2 is the only line with **near-complete MongoDB
compatibility** (real transactions, full aggregation, text/geo) — but only on
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
   `go` engine advertises the reduced set; `documentdb` advertises the full set.

5. **Incremental convergence.** Close `go`-engine gaps (the `⚠️`/`❌` v1 cells in the
   matrix) against the shared conformance suite so SQLite/MySQL/Hana parity approaches
   DocumentDB. Features impractical to reimplement portably in Go — real multi-document
   **transactions**, **change streams**, **text/geo search** — stay `documentdb`-only
   and are surfaced as unavailable via capability flags rather than silently failing.
   v1's partial **MySQL** and **SAP HANA** backends are carried forward under the `go`
   engine and completed as needed.

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
