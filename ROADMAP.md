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
- Not used: `$text`, `$where`, `$expr`, geospatial (`$near`/`$geoWithin`), `collation`

### Aggregation pipelines (3 sites, server-side admin/metrics only — not on the hot path)
- [x] Stages: `$match`, `$group` (`$sum`), `$sort`, `$project`, `$addFields`, `$count`, **`$lookup`**
- [x] Expression operators: `$map`, `$objectToArray`, `$ifNull`, `$anyElementTrue`, `$or`, `$eq`, `$ne`
- Sites: [`models/server/metrics.js`](https://github.com/wekan/wekan/blob/main/models/server/metrics.js) (Prometheus "top boards"), [`server/models/attachmentStorageSettings.js`](https://github.com/wekan/wekan/blob/main/server/models/attachmentStorageSettings.js) (attachment storage stats)

### Indexes
- [x] Single-field indexes ([`server/lib/mongoStartup.js`](https://github.com/wekan/wekan/blob/main/server/lib/mongoStartup.js) `ensureIndex()`)
- [x] Compound indexes (e.g. `{ boardId: 1, createdAt: -1 }`)
- [x] Unique indexes (e.g. boards, invitation codes)
- Not used: **TTL** (`expireAfterSeconds`), **text** indexes, geospatial indexes

### Reactivity (Meteor pub/sub)
- [x] Configurable driver order via `METEOR_REACTIVITY_ORDER` = `changeStreams,oplog,polling` ([`start-wekan.sh`](https://github.com/wekan/wekan/blob/main/start-wekan.sh))
- [x] **`polling` (poll-and-diff) is a first-class fallback** — no hard oplog/change-stream dependency (~2000 ms latency without oplog)
- [x] Oplog is only *introspected* for the Admin Panel, never required ([`server/statistics.js`](https://github.com/wekan/wekan/blob/main/server/statistics.js))

### Admin / diagnostic commands
- [x] `serverStatus`, `buildInfo`, `hello`, `listCollections`, `createIndexes`
- [x] `compact`, `replSetGetStatus` — GridFS admin tooling only (skippable with `fs` attachments)

### Not used at all (so not required from FerretDB)
- [ ] ~~Multi-document transactions / sessions~~ (`startSession`, `withTransaction`) — none
- [ ] ~~Capped collections~~ — none created by WeKan
- [ ] ~~Full-text search~~ (`$text` / text indexes) — replaced by `$regex`
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
- [ ] `$text`, `$where` — not implemented

### Aggregation — partial (`internal/handler/common/aggregations/stages/stages.go`)
- [x] Stages: `$addFields`, `$collStats`, `$count`, `$group`, `$limit`, `$match`, `$project`, `$set`, `$skip`, `$sort`, `$unset`, `$unwind`
- [x] Stage: `$lookup` (basic equality-join form `{ from, localField, foreignField, as }` used by WeKan; the `pipeline`/`let` sub-form returns `ErrNotImplemented`)
- [x] Stages: `$replaceRoot`, `$replaceWith`, `$sortByCount`, `$sample`, `$facet`, `$unionWith` (short `"<coll>"` form and object `{ coll, pipeline }` form; injected like `$lookup`)
- [x] Accumulator/operator `$sum`, `$count`
- [ ] Stages: `$graphLookup`, `$merge`, `$out`, `$bucket`, `$setWindowFields`, `$changeStream`, `$geoNear`, … (return `ErrNotImplemented`)
- [x] Aggregation expression operators used by WeKan: `$eq`, `$ne`, `$or`, `$ifNull`, `$anyElementTrue`, `$objectToArray`, `$map`
- [x] Comparison/boolean/conditional expression operators: `$cmp`, `$gt`, `$gte`, `$lt`, `$lte`, `$and`, `$not`, `$cond`, `$switch`, `$allElementsTrue`
- [x] Arithmetic expression operators: `$add`, `$subtract`, `$multiply`, `$divide`, `$mod`, `$abs`, `$ceil`, `$floor`, `$trunc`, `$round`, `$pow`, `$sqrt`, `$exp`, `$ln`, `$log`, `$max`, `$min`, `$avg`
- [x] String expression operators: `$concat`, `$toUpper`, `$toLower`, `$strLenCP`, `$strLenBytes`, `$strcasecmp`, `$substr`, `$substrCP`, `$substrBytes`, `$split`, `$trim`, `$ltrim`, `$rtrim`, `$indexOfCP`, `$indexOfBytes`, `$replaceOne`, `$replaceAll`, `$regexMatch`
- [x] Array expression operators: `$size`, `$arrayElemAt`, `$concatArrays`, `$isArray`, `$in`, `$reverseArray`, `$slice`, `$range`, `$indexOfArray`, `$arrayToObject`, `$filter`, `$reduce`, `$sortArray`, `$setUnion`, `$setIntersection`, `$setDifference`, `$setEquals`, `$setIsSubset`, `$zip`
- [x] Type-conversion and related expression operators: `$toString`, `$toInt`, `$toLong`, `$toDouble`, `$toBool`, `$toObjectId`, `$toDate`, `$convert`, `$isNumber`, `$literal`, `$let`, `$getField`, `$setField`, `$unsetField`, `$binarySize`, `$rand`
- [x] Date expression operators: `$year`, `$month`, `$dayOfMonth`, `$hour`, `$minute`, `$second`, `$millisecond`, `$dayOfWeek`, `$dayOfYear`, `$week`, `$isoDayOfWeek`, `$isoWeek`, `$isoWeekYear`, `$dateToString`, `$dateFromString`, `$dateToParts`, `$dateFromParts`, `$dateAdd`, `$dateSubtract`, `$dateDiff`, `$dateTrunc`
- [ ] Remaining **aggregation expression operators** — a few niche ones (`$function`, `$toDecimal`, `$bsonSize`, `$meta`, trigonometric, and window operators like `$rank`/`$shift`/`$setWindowFields`-only accumulators) — unimplemented

### Indexes — partial (`internal/handler/msg_createindexes.go`, `website/docs/indexes.md`)
- [x] Single-field, **compound**, and **unique** indexes (incl. compound-unique)
- [x] `sparse` accepted (silently ignored — comment: *"Ignore for now to make Meteor apps work"*)
- [ ] `expireAfterSeconds` (**TTL**), text (`weights`/`default_language`), `partialFilterExpression`, `2dsphere`, `collation`, `hidden` — `ErrNotImplemented`

### Reactivity — limited
- [x] **Tailable / awaitData cursors** on **capped collections** (poll-based; `msg_find.go`, `msg_getmore.go`)
- [x] Capped collections in the **SQLite backend** (`internal/backends/sqlite/database.go`, `insert.go`, `query.go`)
- [x] **Basic OpLog tailing** via `internal/backends/decorators/oplog/` — writes `local.oplog.rs` records (i/u/d)
- [ ] **Change streams** (`$changeStream` / `watch`) — not supported ([issue #1415](https://github.com/FerretDB/FerretDB/issues/1415))
- [ ] Real **replication** / `replSetInitiate` — oplog is tailing-only; the capped `local.oplog.rs` must be created **manually** and `FERRETDB_REPL_SET_NAME` set (`website/docs/configuration/oplog-support.md`)

### Sessions / transactions — not supported
- [ ] `startSession`, `commitTransaction`, `abortTransaction`, retryable writes — all `❌`

### Admin / diagnostic — implemented
- [x] `serverStatus`, `buildInfo`, `hello`/`ismaster`, `ping`, `collStats`, `dbStats`
- [x] `listCollections`, `listDatabases`, `listIndexes`, `createIndexes`, `dropIndexes`, `create`, `drop`, `compact`
- [x] `getParameter` — implemented (`internal/handler/msg_getparameter.go`; returns `authenticationMechanisms`, `featureCompatibilityVersion`, etc.; verified on SQLite by `TestCommandsAdministrationGetParameter`)
- [ ] `replSetInitiate` — `❌`

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

### 🔁 Reactivity — configure WeKan around the gap
- [ ] **Change streams unsupported** → set `METEOR_REACTIVITY_ORDER=polling` (poll-and-diff, ~2000 ms latency). This is the primary supported path. *(FerretDB to-do: implement change streams — [#1415](https://github.com/FerretDB/FerretDB/issues/1415).)*
- [ ] **No real replication oplog** → optional lower-latency path needs manual setup: create capped `local.oplog.rs`, set `FERRETDB_REPL_SET_NAME`, point `MONGO_OPLOG_URL` at it, and use `METEOR_REACTIVITY_ORDER=oplog,polling`. Needs end-to-end validation with Meteor's oplog tailer.

### 🔎 To verify (partial support — likely fine, confirm under load)
- [x] **`$push`/`$addToSet` with `$each` + `$slice`/`$sort`** — the `$push` modifiers `$slice`, `$sort` and `$position` are implemented in `internal/handler/common/update_array_operators.go` and covered by integration tests (`integration/update_push_modifiers_test.go`). WeKan sites ([`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js), [`server/propagateOrgTeamMembers.js`](https://github.com/wekan/wekan/blob/main/server/propagateOrgTeamMembers.js)).
- [x] **`$pullAll`** (1 site, [`server/models/integrations.js`](https://github.com/wekan/wekan/blob/main/server/models/integrations.js)) — implemented and covered by integration tests (`integration/update_pullall_test.go`).
- [x] **`getParameter`** — already implemented (`internal/handler/msg_getparameter.go`); verified working on SQLite, so WeKan startup ([`models/lib/meteorMongoIntegration.js`](https://github.com/wekan/wekan/blob/main/models/lib/meteorMongoIntegration.js)) is fine.

### 🚫 Not needed (WeKan doesn't use them; no action)
- [x] Transactions/sessions, capped collections, TTL indexes, text indexes/`$text`, geospatial, `collation`, `mapReduce`, `$where` — unused by WeKan
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
