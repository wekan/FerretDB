# Changelog

<!-- markdownlint-disable MD024 MD034 -->

## [v1.31.0](https://github.com/wekan/FerretDB/releases/tag/v1.31.0) (2026-07-17)

### Fixed 🐛

- SQLite backend: `collectionCreate` now ADOPTS an orphaned physical table instead of crashing on it (wekan/wekan#6476). The table name is a deterministic hash of the collection name, and the collision check only looked at the IN-MEMORY metadata, so a table could exist on disk while its metadata row was gone — an orphan left by an interrupted MongoDB→FerretDB migration or a crash. `collectionCreate` then ran a plain `CREATE TABLE` (and `CREATE UNIQUE INDEX`) that failed with `table "<db>.<coll>_<hash>" already exists`, and on the index failure it rolled back and DROPPED the orphan (losing its data). Because this path runs from an upsert (`msg_update.go` `updateDocument` → `CreateCollection`), the error surfaced as an *unhandledRejection* in WeKan's SyncedCron, which exited the process — systemd then crash-looped WeKan (restart counter climbing to 99) so **its web port never stayed open** ("Wekan starts but doesn't open a listening port"). Both the table and its index are now created with `IF NOT EXISTS`, so an orphan is re-adopted (same collection, same deterministic table) and its metadata re-registered — startup self-heals instead of crashing by @xet7. Thanks to uusijani, a1bert01 and xet7.

- SQLite backend: push `$in` and `$regex` field filters down to SQLite, not just scalar equality (wekan/wekan#6467, wekan/wekan#6468 follow-up). WeKan's sidebar "Filter by label" (`{labelIds: {$in: [...]}}`) and "Filter by card title" (`{title: {$regex: text, $options: "i"}}`) were not pushed down, so every such filter did `SELECT _ferretdb_sjson FROM <table>` over the WHOLE collection and decoded + regex-matched every row's sjson in Go — minutes on a 53k-card board. `$in` now emits `expr IN (?, …)` (keeping the array-containment arm for non-`_id` fields, since Mongo `$in` also matches an array element), and a **literal** `$regex` emits `expr LIKE ?` so SQLite prunes non-matching rows itself. Superset semantics are preserved (the in-Go filter stays authoritative): `$in` is pushed only when EVERY element is a pushdown-safe string/ObjectID (dropping one would make `IN` a subset), and `$regex` only for a plain ASCII literal with no metacharacters/`LIKE` wildcards and no `x` option — because SQLite's `LIKE` folds case for ASCII only, a non-ASCII literal could MISS a case-insensitive match and drop a card, so those stay in Go. Ranges (`$gt`/`$lte`/…, whose JSON-text ordering is wrong for numbers/dates), `$ne` and non-literal/`$options:"x"` regexes are deliberately NOT pushed by @xet7. Thanks to xet7.

- SQLite backend: push numeric/date RANGE filters (`$gt`/`$gte`/`$lt`/`$lte`) down to SQLite (wekan/wekan#6467 follow-up; WeKan's "Filter by date"). These were the last common WeKan filter still full-scanning + decoding every card's sjson in Go. Ranges are pushed with the `->>` (SQL-value) accessor instead of `->` (JSON text): `->` compared lexically (`"10" < "9"`), which is why range was not pushed before, but `->>` extracts the value NUMERICALLY. sjson stores int32/int64, doubles and dates all as JSON numbers (a date as its Unix-millis), so `_ferretdb_sjson->>"dueAt" <= ?` with a millis bound is a correct, index-shaped comparison; `{$gte: A, $lte: B}` (WeKan's week filters) emits both arms ANDed. Superset semantics hold (the Go filter stays authoritative): a null/missing/string field yields `NULL`/non-numeric and is pruned — matching Mongo's type-bracketed `$lt`/`$gt` — and only NUMBER/DATE bounds are pushed (string ranges, with collation/serialization subtleties, stay in Go). Verified end-to-end against real storage (`TestQueryRangePushdownDates`): an in-range date is returned while later/null/missing dates are pruned by @xet7. Thanks to xet7.

### Other Changes 🤖

- Add an end-to-end Go test for the date range pushdown (`internal/backends/sqlite/collection_test.go`, `TestQueryRangePushdownDates`): it inserts documents with past/future/null/missing `dueAt`, then asserts the backend `Query` (which applies only the pushed WHERE) returns the in-range card and prunes the rest, and that `Explain` reports `FilterPushdown`. This proves `->>` reads sjson's millis-encoded date and compares it numerically — the assumption the range pushdown rests on by @xet7. Thanks to xet7.

- Add Go table tests, with negative cases, for the `$in`/`$regex` pushdown (`internal/backends/sqlite/query_test.go`): `$in` on `_id` (plain `IN`) and on a non-`_id` array field (`IN` + array arm), a literal `$regex` (incl. one with a space) as `LIKE`, and the negatives that must stay in Go — empty/`unsafe-element`/`unsafe-string` `$in`, metacharacter/non-ASCII/`x`-option `$regex`, and a `$lte` range. `pushdownSafeLiteralSubstring` gates the regex case (non-empty ASCII, no regex metacharacters, no `LIKE` wildcards, pushdown-safe). `go build`, `go vet` and the full `internal/backends/sqlite/...` suites pass with Go 1.25.11 by @xet7. Thanks to xet7.

- Add a Go regression test for the orphaned-table adoption (`internal/backends/sqlite/metadata/registry_test.go`, `TestCollectionCreateAdoptsOrphanTable`, wekan/wekan#6476): it creates a collection, orphans it (drops the metadata row and forgets it in memory while leaving the physical table + its `_id` index on disk), then asserts `CollectionCreate` succeeds by re-adopting the SAME deterministic table and that the collection is usable (insert round-trips) — where before it errored `table … already exists` and dropped the data. `go build`, `go vet` and the full `internal/backends/sqlite/...` suites pass with Go 1.25.11 by @xet7. Thanks to xet7.

## [v1.30.0](https://github.com/wekan/FerretDB/releases/tag/v1.30.0) (2026-07-17)

### Fixed 🐛

- SQLite backend: stop the connection pool from starving under WeKan's cursor load (wekan/wekan#6467, wekan/wekan#6469). The v1.28.0 CPU fix capped BOTH `MaxIdleConns` and `MaxOpenConns` at `2×GOMAXPROCS` (min 4, max 16). That was wrong for `MaxOpenConns`: Meteor keeps a server-side `find` cursor open between `getMore` round-trips, and in FerretDB each open cursor PINS one pooled connection for its entire lifetime. So a handful of parked Meteor cursors exhausted a 16-connection pool, and every other query — login-token lookups, board loads — then blocked waiting for a free connection. Operators saw boards take *minutes* to load and logins fail with *"Must be logged in"* even though CPU was back to normal (the #6468 pushdown had fixed that). `MaxOpenConns` is now left UNLIMITED so connection checkout never starves; SQLite still serialises writers itself, and the anti-thrash benefit is preserved by keeping only a small set of connections WARM via `MaxIdleConns` (still `2×GOMAXPROCS`, min 4, max 16) — which, together with the #6468 filter pushdown that turned WeKan's whole-collection scans into indexed lookups, is what actually keeps the pure-Go SQLite (modernc) allocator/WAL mutexes and the Go GC from thrashing. The in-memory backend is unchanged (still pinned to a single connection, as each in-memory connection is its own database) by @xet7. Thanks to xet7.

### Other Changes 🤖

- Add Go tests, with negative cases, for the connection-pool limits (`internal/backends/sqlite/metadata/pool/opendb_test.go`). The pool sizing was extracted into `idleConnLimit(gomaxprocs)` and `configurePool(db, memory)` so it is unit-testable. `TestIdleConnLimit` pins the warm-pool sizing (proportional to `GOMAXPROCS`, floor 4, cap 16, and — negative cases — a non-positive `GOMAXPROCS` never yields less than 4, which would deadlock every query). `TestConfigurePoolDoesNotStarveCheckout` is the #6467/#6469 regression guard: it asserts `MaxOpenConnections == 0` (unlimited) and, behaviourally, checks out 32 connections simultaneously (double the old cap) and requires every checkout to succeed — with the regressed cap of 16 the 17th would block until the deadline. `TestConfigurePoolMemoryIsSingleConn` keeps the in-memory single-connection contract. Verified with Go 1.25.11: `go build ./internal/...` and `go vet ./internal/backends/sqlite/...` are clean and the full `internal/backends/sqlite/metadata/pool` suite passes by @xet7. Thanks to xet7.

## [v1.29.0](https://github.com/wekan/FerretDB/releases/tag/v1.29.0) (2026-07-17)

### New Features 🎉

- Accept documents with literal dotted field names (`{"foo.bar": "baz"}`), like MongoDB 3.6+ (wekan/wekan#6473). `Document.ValidateData` rejected every document containing a `.` in any field key at any depth (*"invalid key: … (key must not contain '.' sign)"*), so during WeKan's MongoDB → FerretDB migration every such document — importers, CollectionFS-era metadata, third-party tools can all produce them — failed to insert and, because per-item migration errors are deliberately non-fatal, simply went missing. Dotted keys are now stored and round-tripped literally with MongoDB's own semantics: query and update paths still interpret `.` as a path separator, so a literal dotted key is not addressable by path (exactly as in MongoDB), and the other key rules (`$` prefix, duplicate keys, invalid UTF-8) still reject. Verified end-to-end against a live FerretDB on the SQLite backend with the mongodb driver: top-level and nested dotted keys insert and round-trip, `$set` of a subdocument containing a dotted key works, a dotted-path query does NOT match the literal key, and `$`-prefixed keys are still rejected by @xet7. Thanks to mueschel and xet7.

### Other Changes 🤖

- Update the document validation tests for the dotted-key change (wekan/wekan#6473): `internal/types/document_validation_test.go` now pins that top-level, nested and array-embedded dotted keys are VALID (with negative cases proving `$`-prefixed dotted keys and duplicate dotted keys still reject), and the `Insert`/`Update` dotted-key diff cases in `integration/diff_05_document_validation_test.go` — which documented the old rejection as a difference from MongoDB — are replaced by a `DottedKeysAccepted` group asserting both databases now behave the same: insert + literal round-trip, nested `$set`, and the dotted-path-query-does-not-match-literal-key negative case. `go build ./cmd/... ./internal/...`, `go vet`, and the `internal/types`, `internal/backends/sqlite/...` and `internal/handler/common` test suites pass by @xet7. Thanks to xet7.

- Add Go table tests, with negative cases, for the `$elemMatch` document/field form (`internal/handler/common/filter_elemmatch_test.go`) — the v1.27.0 fix that un-broke WeKan's board access check on the SQLite backend. Pins: the motivating query `{members: {$elemMatch: {userId: X, isActive: true}}}` matches only when the WHOLE sub-query holds on the SAME array element (satisfying it across two different elements must NOT match), single-field and nested-operator field forms, the operator form (`{$gt: value}`) unchanged, non-array/missing fields and non-document elements never match, and the error cases (`$elemMatch` needs an object; `$text` rejected; `$or` not implemented). Thanks to xet7.

## [v1.28.0](https://github.com/wekan/FerretDB/releases/tag/v1.28.0) (2026-07-16)

### New Features 🎉

- SQLite backend: push top-level string/ObjectID equality filters down to SQLite as WHERE clauses (wekan/wekan#6467, wekan/wekan#6468). Previously only a bare `{_id: X}` filter was pushed down, so essentially every real query — e.g. WeKan's `{boardId: X}` on 53k cards — did `SELECT _ferretdb_sjson FROM <table>` over the WHOLE collection and decoded every row's sjson in Go, on every Meteor poll. The WHERE expressions are built exactly like the expression indexes that `Registry.indexesCreate` creates (`_ferretdb_sjson->"field"`), so `_id` lookups now use the unique `_id` index and Mongo-level secondary indexes accelerate their fields. Superset semantics are preserved (the in-Go filter stays authoritative): array containment matches are kept via an index-friendly range arm, and string values whose Go-JSON and SQLite serializations could differ (`<`, `>`, `&`, control characters, U+2028/U+2029) are not pushed down by @xet7 in https://github.com/wekan/FerretDB/commit/ec485d075fd7f98f8cb97732c6379b3ead9fa436 . Thanks to xet7.
- SQLite backend: cap the per-database connection pool at `2×GOMAXPROCS` (min 4, max 16) instead of 100/100. Meteor opens ~100 sockets, and letting them all run concurrent SQLite scans thrashed the pure-Go SQLite (modernc) allocator/WAL mutexes and the Go GC — a reported 821k futex + 530k nanosleep syscalls per 30 s and 250–400% CPU with 1–2 real users (wekan/wekan#6467). Queueing excess queries in database/sql is far cheaper by @xet7 in https://github.com/wekan/FerretDB/commit/ec485d075fd7f98f8cb97732c6379b3ead9fa436 . Thanks to xet7.
- SQLite backend: `InsertAll` no longer takes the registry's GLOBAL write lock when the collection already exists — `CollectionCreate` write-locked the whole registry even as a no-op, so every small Meteor write (sessions, activities, login tokens) stalled all concurrent readers (wekan/wekan#6467) by @xet7 in https://github.com/wekan/FerretDB/commit/ec485d075fd7f98f8cb97732c6379b3ead9fa436 . Thanks to xet7.

### Other Changes 🤖

- Add Go table tests, with negative cases, for the SQLite filter pushdown (`internal/backends/sqlite/query_test.go`): `TestPushdownSafeString` pins which strings are pushdown-safe (Go-JSON and SQLite `->` must serialize them byte-identically — `<`, `>`, `&`, control characters and U+2028/U+2029 are not), and `TestPrepareWhereClause` pins the exact WHERE clauses and args for `_id` (plain equality, matching the unique `_id` expression index) and top-level equality filters including the array-containment range arm and compound filters, plus negative cases proving dotted paths, `$`-operators, non-string values and unsafe strings stay with the in-Go filter. Verified with Go 1.25.11: `go build ./cmd/... ./internal/...` and `go vet ./internal/backends/...` are clean, and the full `internal/backends/sqlite/...` test suites (sqlite, metadata, pool) pass with the pushdown/pool/lock changes in place by @xet7 in https://github.com/wekan/FerretDB/commit/64d70a0e21f498978b4ba79b84da5f7f8b89f561 . Thanks to xet7.
- Integration test setup: stop flooding the test output with endless `traces export: Post "http://127.0.0.1:4318/v1/traces": connection refused` lines when no OTel collector is running. The OTel trace exporter is optional tooling (FerretDB's task runner starts a collector in Docker; `build.sh` options 4–6 do not), but `integration/setup/startup.go` created it unconditionally and it retried forever. The setup now probes 127.0.0.1:4318 once with a 1 s TCP dial and skips the exporter with a single log line when nothing is listening; with a collector present, tracing works exactly as before by @xet7 in https://github.com/wekan/FerretDB/commit/8ca41b0a239e390fffeacc71c0336b1ad076dcb2 . Thanks to xet7.
- Integration test setup: `TestOtelComment` passes also without an OTel collector. Skipping the exporter (previous entry) left Go's global NO-OP tracer provider in place, whose spans carry all-zero trace/span IDs — and `TestOtelComment`, which builds a query comment from the span context, failed with *"invalid span context"*. When no collector is listening, the setup now installs a real SDK tracer provider WITHOUT an exporter (logged as `traces are recorded but not exported`): spans get valid trace/span IDs, nothing is sent anywhere, nothing retries. Verified with build.sh's own flags (`-target-backend=ferretdb-sqlite -target-tls`, no collector): `TestOtelComment` passes. `go.opentelemetry.io/otel/sdk` becomes a direct dependency of the integration module (it was already in the module graph, no version changes) by @xet7 in https://github.com/wekan/FerretDB/commit/98e3a3cc69a0cc93168425a2c386f6f7625e9094 . Thanks to xet7.

## [v1.27.0](https://github.com/wekan/FerretDB/releases/tag/v1.27.0) (2026-07-12)

### New Features 🎉

- Implement the document/field form of the `$elemMatch` query operator (`{arr: {$elemMatch: {field: value, ...}}}`): match array elements that are documents satisfying the WHOLE sub-query on the SAME element. Previously only the operator form (`{arr: {$elemMatch: {$gt: value}}}`) worked — a multi-field field-form expression was rejected with `unknown operator: <field>` (`BadValue`), and a single field fell through to operator parsing and failed the same way. This broke common queries such as WeKan's board access check `{members: {$elemMatch: {userId: X, isActive: true}}}`: on the SQLite backend the board list returned no boards and private boards could not be opened (they worked on MongoDB), because the query was rejected before it ever looked at the data. `filterFieldExprElemMatch` now detects the field form (any non-`$` key) and matches each document array element with `FilterDocument`, leaving the operator form unchanged by @xet7 in https://github.com/wekan/FerretDB/commit/ad4cc23a5c6a0d41bfd16e2c0a8515fbd3ce3bde . Thanks to xet7.

## [v1.26.0](https://github.com/wekan/FerretDB/releases/tag/v1.26.0) (2026-07-11)

### Other Changes 🤖

- Disable telemetry by default and reduce log noise for normal operation. The `--telemetry` flag now defaults to `disable` (was `undecided`, which would otherwise begin reporting to beacon.ferretdb.com after a delay), and the telemetry reporter loop is not started at all, so there is no phone-home and no periodic report/ping overhead. The default log level is now `error` (was `info`) for non-debug builds, so routine activity — listening, per-connection and per-command info — no longer produces log noise; raise it with `--log-level=warn|info|debug` only when diagnosing a problem by @xet7. Thanks to xet7.
- Update the Go toolchain from 1.25.0 to 1.25.11 so the released FerretDB binaries (and the Docker image) are compiled with a patched Go standard library. Quay.io's security scanner flagged 37 fixable `stdlib` advisories against the FerretDB binary bundled into the WeKan image (GO-2026-4601/4602/4603, GO-2026-4864/4865/4869/4870, GO-2026-4946/4947, GO-2026-5037/5038/5039 and others), all fixed in Go ≤ 1.25.11. Bumped the pin in `build.sh` (`GO_VERSION`), `.github/workflows/release-all.yml` (`GO_VERSION`), `Dockerfile` (`golang:1.25.11`) and the `go` directive in `go.mod`, so every build path — the local/CI `build.sh` cross-compile and the `docker.yml` image build — produces a binary with the patched stdlib by @xet7. Thanks to xet7.
- Attach each platform's FerretDB binary to the GitHub Release as an INDIVIDUAL asset (`ferretdb-<arch>` / `ferretdb-<arch>.exe`) instead of packing them all into a single `ferretdb.zip`. `build.sh` now cross-compiles into `./dist/` (menu options 11/12 and `./build.sh dist[-seq|-par]`, replacing the old `zip*` commands), `release-all.yml` uploads every `dist/ferretdb-*` asset separately, and `docker.yml` / `Dockerfile.release` download only the Linux `ferretdb-<arch>` assets they need. Every consumer (the WeKan `release-all.yml` bundle jobs, `docker-compose.yml`, the Sandstorm `build-deps.sh`) now downloads only the one binary for the platform it targets, e.g. `https://github.com/wekan/FerretDB/releases/latest/download/ferretdb-amd64`, rather than fetching and unzipping the whole multi-platform archive by @xet7. Thanks to xet7.
- Publish the FerretDB Docker image for every architecture that `ferretdb.zip` ships. The image build was QEMU-emulating the Go toolchain per target, which limited it to five arches (amd64, arm64, ppc64le, s390x, riscv64). Because FerretDB is pure Go with CGO disabled and the final image is `FROM scratch`, the build stage now runs natively on the build platform and cross-compiles (`GOOS`/`GOARCH`/`GOARM` taken from buildx's `TARGET*` args), so buildx emits every Linux architecture in the zip — `linux/amd64`, `linux/arm64`, `linux/arm/v7` (armhf), `linux/arm/v5` (armel), `linux/386` (i386), `linux/ppc64le`, `linux/s390x`, `linux/riscv64`, `linux/loong64` — without emulating the compiler by @xet7 in https://github.com/wekan/FerretDB/commit/fc316435821ec2c4fde962e29ec0099604ccc88b . Thanks to xet7.
- Split Docker publishing into a separate `docker.yml` workflow and remove the Docker job from `release-all.yml`. `docker.yml` builds the multi-arch image from the PREBUILT binaries in the newest (or a chosen) GitHub Release's `ferretdb.zip` — no recompilation and no QEMU — deriving the platform list from the Linux binaries actually present in the release and pushing `:<version>` and `:latest` to Docker Hub, Quay.io and GHCR (the same `DOCKERHUB_AUTH` / `QUAY_AUTH` / `GHCR_AUTH` secrets). Adds `Dockerfile.release`, which selects the prebuilt binary matching the target platform into a `FROM scratch` image, plus a per-Dockerfile `Dockerfile.release.dockerignore` so it does not disturb the source build's context. `release-all.yml` now only builds `ferretdb.zip` and publishes the Release by @xet7 in https://github.com/wekan/FerretDB/commit/1c6de77071b912759f0746c93d9e2de832c28fd7 . Thanks to xet7.
- Build the GitHub Release notes in `release-all.yml` from three parts: a fixed header line (`FerretDB v1 (SQLite) multi-platform binaries — ferretdb.zip. Built by release-all.yml from the wekan/FerretDB fork.`), then a `Platforms: ...` line listing every architecture present in `ferretdb.zip` (read from its `ferretdb/<arch>/` entries, in `build.sh`'s canonical order with any extras appended), then the `CHANGELOG.md` `## [<version>]` section for the release. `ferretdb.zip` is built before the notes are composed so the platform list reflects exactly what shipped by @xet7 in https://github.com/wekan/FerretDB/commit/549edcc62b00875e6b6681373efb61e6815462fd . Thanks to xet7.
- Harden `docker.yml`: push to Docker Hub, Quay.io and GHCR in separate `buildx` calls so one registry's failure (for example a Quay robot account that only has Read permission, which lets `docker login` succeed but returns 401 on the blob push) no longer aborts the pushes to the other registries; every push is reported in its own log group and the job still fails at the end if any of them failed, but the registries that succeeded are published. Also drop the non-Linux binaries (`win*`, `mac-*`, `freebsd-*`) and `README.md` from the extracted release before building so the Docker build context contains only the Linux binaries that are actually used (down from ~833 MB) by @xet7 in https://github.com/wekan/FerretDB/commit/252a10996bbf4e1bb3731341d61fd412d3406e72 . Thanks to xet7.
- Add a `build.sh` menu option 14 (and `./build.sh docker-release [version]`) that triggers the `docker.yml` workflow — building the multi-arch image from a release's prebuilt binaries and pushing to Docker Hub, Quay.io and GHCR — alongside the existing option 13 (`release-all.yml`). Both share a `trigger_workflow` helper that pushes the current branch and runs the workflow with `gh` by @xet7 in https://github.com/wekan/FerretDB/commit/a570a69626cd6faf4775bc8ea72b84469aed4763 . Thanks to xet7.

## [v1.25.0](https://github.com/wekan/FerretDB/releases/tag/v1.25.0) (2026-07-10)

### New Features 🎉

- Implement the `replSetInitiate` command as a compatibility no-op so that tools and drivers that bootstrap a replica set do not hard-fail: the command is accepted with or without a configuration document (`{replSetInitiate: 1}` or `{replSetInitiate: {_id: "rs0", members: [...]}}`) and returns `{ok: 1}`, echoing back the configuration `_id` (replica set name) when one is supplied. IMPORTANT: this does NOT set up real replication — FerretDB v1's oplog is tailing-only and must still be configured manually by creating a capped `local.oplog.rs` collection and setting the `FERRETDB_REPL_SET_NAME` environment variable; the command creates no oplog, elects no primary and does not change server topology by @xet7 in https://github.com/wekan/FerretDB/commit/ed98fa8eda6b4fad0a1f8de4ce7d6d7572a14a01 . Thanks to xet7.
- Implement the logical session and transaction command family as compatibility commands so MongoDB drivers (and Meteor) that rely on sessions and retryable writes work against the SQLite backend: `startSession` returns a session record with a freshly generated UUID, and `commitTransaction`, `abortTransaction`, `endSessions`, `refreshSessions`, `killSessions`, `killAllSessions` and `killAllSessionsByPattern` return `{ok: 1}`. The ordinary write commands (`insert`, `update`, `delete`, `findAndModify`) now also accept and ignore the retryable-write / session fields `autocommit`, `startTransaction`, `stmtId` and `stmtIds` in addition to the already-accepted `lsid` and `txnNumber`. IMPORTANT: these transaction commands are NO-OP compatibility commands — FerretDB v1 with the SQLite backend has no real multi-document transactions, every write auto-commits on its own, and the commands provide no atomicity or isolation across operations (`abortTransaction` does not roll back writes that already happened); logical sessions are accepted but carry no server-side state by @xet7 in https://github.com/wekan/FerretDB/commit/c229095a16dc18b840d1cd676c67b0c821bb80ba . Thanks to xet7.
- Track logical sessions in a real server-side session registry (ported and adapted from FerretDB v2's `internal/handler/session` package) instead of treating the session commands as pure no-ops: the handler now owns a `session.Registry` that creates and looks up sessions by `lsid`, groups them per user, records their created and last-used times, marks sessions as ended and prunes ended/expired sessions after the 30-minute `LogicalSessionTimeoutMinutes`. `startSession` registers a fresh session, `endSessions` marks the referenced sessions ended, `refreshSessions` bumps their last-used time (creating them implicitly if needed), and `killSessions`, `killAllSessions` and `killAllSessionsByPattern` remove the referenced sessions from the registry — all still returning `{ok: 1}`. The v2 cursor-association tracking is intentionally omitted because v1's cursor registry differs, so the port keeps the session lifecycle only. IMPORTANT: this only tracks session bookkeeping — multi-document transactions remain NO-OP compatibility commands, as FerretDB v1 with the SQLite backend auto-commits every write and provides no atomicity or isolation by @xet7 in https://github.com/wekan/FerretDB/commit/166770d28c574e0a772bf379a75710ed00c0294c . Thanks to xet7.
- Implement the `$setWindowFields` aggregation stage together with the window operators `$rank`, `$denseRank`, `$documentNumber` and `$shift` (rank/position, requiring `sortBy`) and the window accumulators `$sum`, `$avg`, `$min`, `$max`, `$count`, `$push`, `$first`, `$last`, `$stdDevPop` and `$stdDevSamp` (over the default full-partition window or an explicit `window: {documents: [lower, upper]}` with integer offsets and the `"unbounded"`/`"current"` keywords). The window operators `$derivative`, `$integral`, `$expMovingAvg`, `$covariancePop`, `$covarianceSamp`, `$linearFill`, `$locf`, `$minN`/`$maxN` and `range` windows are deferred and return a clear not-implemented error by @xet7 in https://github.com/wekan/FerretDB/commit/540de307697b74ed6f6dcb1ae0bffb499d039081 . Thanks to xet7.
- Implement the `$slice`, `$sort` and `$position` modifiers for the `$push` update operator (used by WeKan) by @xet7. Thanks to xet7.
- Implement the `$eq`, `$ne`, `$or`, `$ifNull`, `$anyElementTrue`, `$objectToArray` and `$map` aggregation expression operators used by WeKan by @xet7. Thanks to xet7.
- Implement the `$cmp`, `$gt`, `$gte`, `$lt`, `$lte`, `$and`, `$not`, `$cond`, `$switch` and `$allElementsTrue` aggregation expression operators by @xet7. Thanks to xet7.
- Implement the arithmetic aggregation expression operators `$add`, `$subtract`, `$multiply`, `$divide`, `$mod`, `$abs`, `$ceil`, `$floor`, `$trunc`, `$round`, `$pow`, `$sqrt`, `$exp`, `$ln`, `$log`, `$max`, `$min` and `$avg` by @xet7. Thanks to xet7.
- Implement the trigonometric, hyperbolic, angle-conversion and `$log10` aggregation expression operators `$sin`, `$cos`, `$tan`, `$asin`, `$acos`, `$atan`, `$atan2`, `$sinh`, `$cosh`, `$tanh`, `$asinh`, `$acosh`, `$atanh`, `$degreesToRadians`, `$radiansToDegrees` and `$log10` by @xet7 in https://github.com/wekan/FerretDB/commit/62a683ebcd2cb7b657fa66bdd41fe9640e57d7bf . Thanks to xet7.
- Implement the `$bsonSize` aggregation expression operator, which returns the size in bytes of the BSON encoding of a document (null for a null argument) by @xet7 in https://github.com/wekan/FerretDB/commit/84d798e69bcce8e355305d573a09a8b19df405b1 . Thanks to xet7.
- Implement the string aggregation expression operators `$concat`, `$toUpper`, `$toLower`, `$strLenCP`, `$strLenBytes`, `$strcasecmp`, `$substr`, `$substrCP`, `$substrBytes`, `$split`, `$trim`, `$ltrim`, `$rtrim`, `$indexOfCP`, `$indexOfBytes`, `$replaceOne`, `$replaceAll` and `$regexMatch` by @xet7. Thanks to xet7.
- Implement the array aggregation expression operators `$size`, `$arrayElemAt`, `$concatArrays`, `$isArray`, `$in`, `$reverseArray`, `$slice`, `$range`, `$indexOfArray`, `$arrayToObject`, `$filter`, `$reduce`, `$sortArray`, `$setUnion`, `$setIntersection`, `$setDifference`, `$setEquals`, `$setIsSubset` and `$zip` by @xet7. Thanks to xet7.
- Implement the type-conversion aggregation expression operators `$toString`, `$toInt`, `$toLong`, `$toDouble`, `$toBool`, `$toObjectId`, `$toDate`, `$convert`, `$isNumber`, `$literal`, `$let`, `$getField`, `$setField`, `$unsetField`, `$binarySize` and `$rand` by @xet7. Thanks to xet7.
- Implement the date aggregation expression operators `$year`, `$month`, `$dayOfMonth`, `$hour`, `$minute`, `$second`, `$millisecond`, `$dayOfWeek`, `$dayOfYear`, `$week`, `$isoDayOfWeek`, `$isoWeek`, `$isoWeekYear`, `$dateToString`, `$dateFromString`, `$dateToParts`, `$dateFromParts`, `$dateAdd`, `$dateSubtract`, `$dateDiff` and `$dateTrunc` by @xet7. Thanks to xet7.
- Implement the `$lookup` aggregation stage (basic equality-join form) used by WeKan by @xet7. Thanks to xet7.
- Implement the `$replaceRoot`, `$replaceWith`, `$sortByCount` and `$sample` aggregation stages by @xet7. Thanks to xet7.
- Implement the `$facet` aggregation stage (multi-sub-pipeline) by @xet7. Thanks to xet7.
- Implement the `$unionWith` aggregation stage (with optional sub-pipeline) by @xet7. Thanks to xet7.
- Implement the `$bucket` and `$bucketAuto` aggregation stages (the `$bucketAuto` `granularity` option is not yet supported) by @xet7. Thanks to xet7.
- Implement TTL indexes (`expireAfterSeconds` on createIndexes, reported by listIndexes, with a background reaper that deletes expired documents) by @xet7. Thanks to xet7.
- Implement the `$where` query operator, which evaluates a JavaScript expression or function against each document (with `this` bound to the document) using the embedded pure-Go goja JavaScript engine by @xet7 in https://github.com/wekan/FerretDB/commit/dd4dafe7ff21734a2308fb75691b2c9e330d2d69 . Thanks to xet7.
- Implement the `$function` aggregation expression operator (`{body, args, lang: "js"}`), which runs a user-supplied JavaScript function over the evaluated argument expressions using the embedded pure-Go goja JavaScript engine by @xet7 in https://github.com/wekan/FerretDB/commit/dd4dafe7ff21734a2308fb75691b2c9e330d2d69 . Thanks to xet7.
- Implement text indexes: `createIndexes` now accepts the `"text"` index key value and the `weights`, `default_language`, `language_override` and `textIndexVersion` options, storing them so the index is created and round-tripped through `listIndexes` (weights default to 1 per field). Note that this is a pragmatic implementation for compatibility: no real inverted/full-text index is built by @xet7 in https://github.com/wekan/FerretDB/commit/21ed80281959b7c074c6f12be3003e8557f0bac7 . Thanks to xet7.
- Implement the `$text` query operator with partial, self-contained semantics: it matches the `$search` terms directly against a document's string fields (recursing into sub-documents and arrays) and supports multi-term OR, case-insensitive whole-word matching, `$caseSensitive`, double-quoted phrases and leading `-` negation; `$language` and `$diacriticSensitive` are accepted and ignored. There is no stemming, no relevance scoring and no `$meta: "textScore"` projection by @xet7 in https://github.com/wekan/FerretDB/commit/21ed80281959b7c074c6f12be3003e8557f0bac7 . Thanks to xet7.
- Accept the `hidden`, `collation`, `partialFilterExpression` and `2dsphere` index options on `createIndexes`: `hidden` (bool), `collation` (document) and `partialFilterExpression` (document) are stored and round-tripped through `listIndexes`, and the `"2dsphere"` index key value together with `2dsphereIndexVersion` is accepted and reported (version defaulting to 3). These options are only accepted, stored and reported — FerretDB does not hide indexes from the query planner, does not apply locale-aware collation, does not restrict a partial index to matching documents and does not run geospatial queries. The remaining options (`storageEngine`, `bits`, `min`, `max`, `bucketSize`, `wildcardProjection`) still return not-implemented by @xet7 in https://github.com/wekan/FerretDB/commit/13e8f22896d52ad98c4c5fc2496ce19d068188aa . Thanks to xet7.

### Other Changes 🤖

- Add a `release-all.yml` GitHub Actions workflow that does a full FerretDB v1 release: it builds `ferretdb.zip` for every platform with `build.sh`, publishes a GitHub Release with it at https://github.com/wekan/FerretDB/releases, and builds + pushes a multi-arch FerretDB Docker image (amd64, arm64, ppc64le, s390x, riscv64) from the source Dockerfile to `wekanteam/ferretdb`, `quay.io/wekan/ferretdb` and `ghcr.io/wekan/ferretdb` — using the same `DOCKERHUB_AUTH` / `QUAY_AUTH` / `GHCR_AUTH` base64("user:token") secrets as wekan/wekan by @xet7 in https://github.com/wekan/FerretDB/commit/67262f0a02d94bcbe2a47bd6831b56ed7f3beb2f . `build.sh` gains option 13 / `./build.sh release [version]` to push the branch and trigger that workflow via `gh`, like wekan/wekan's `releases/release-all.sh`. Thanks to xet7.
- Add `build.sh` menu options 11 (**SEQUENTIAL**) and 12 (**PARALLEL**) — and the non-interactive `./build.sh zip` / `zip-seq` / `zip-par` commands — that cross-compile FerretDB v1 (SQLite backend, `CGO_ENABLED=0`, no QEMU) for every platform Go and `modernc.org/sqlite` support into a single `ferretdb.zip` with the layout `ferretdb/<arch>/ferretdb-<arch>` (`.exe` only on Windows, binaries already `chmod +x`) plus `ferretdb/README.md`; targets that do not compile are skipped, so the zip holds exactly what builds. The build was run and all 16 targets compiled: amd64, arm64, armhf, armel, i386, ppc64le, s390x, riscv64, loong64, win64, win-arm64, win32, mac-amd64, mac-arm64, freebsd-amd64, freebsd-arm64. Go's build scratch dir (`GOTMPDIR`) is placed on the repo disk so a parallel build does not fill a small `/tmp` tmpfs, and `.gitignore` ignores the produced `ferretdb.zip` by @xet7 in https://github.com/wekan/FerretDB/commit/05fa6ee1123071c63eee0cbec6a0c777b7eef276 . Thanks to xet7.
- Fix the SQLite backend unit tests after the `modernc.org/sqlite` bump: the embedded SQLite moved from 3.46.0 to 3.53.2, so update the expected `sqlite_version()` / `BackendVersion` (3.46.0 → 3.53.2) and `sqlite_source_id()` in `pool_test.go`, `registry_test.go` and `backend_test.go`, and run `build.sh` unit tests with `-tags=ferretdb_debug` (the tag `task test-unit` uses) so the debug-only assertions pass — e.g. `TestCheckError`, which asserts `debugbuild.Enabled` by @xet7 in https://github.com/wekan/FerretDB/commit/e566dee9b54a8558bc6b50343bdffa51189c0e4e . Thanks to xet7.
- Add an interactive `build.sh` helper for FerretDB v1 (SQLite): a menu-driven (and non-interactive `./build.sh <command>`) script to install dependencies, build the `ferretdb` binary with the SQLite handler, run FerretDB on the SQLite backend, run the in-process SQLite integration tests (with the required `-target-tls` flag), run unit tests, vet, build/run the Docker stack, and clean; it self-installs a local Go 1.25 toolchain under `./.goroot` when `go` is not on `PATH`. Integration tests can be run **sequentially** (`test-seq`, `-p 1 -parallel 1`) or in **parallel** (`test-par [N]` / `TEST_PARALLEL=N`, defaulting to the CPU count) by @xet7 in https://github.com/wekan/FerretDB/commit/adc3473d . Thanks to xet7.
- Add positive and negative integration tests for the `$pullAll` update operator, used by WeKan (already implemented upstream) by @xet7. Thanks to xet7.
- Add `docker-compose.yml` and `Dockerfile` so `docker compose up --build` builds FerretDB v1 (SQLite backend) and runs WeKan against it (no oplog, `METEOR_REACTIVITY_ORDER=polling`, attachments on filesystem); the previous FerretDB development compose is preserved as `docker-compose.dev.yml` by @xet7 in https://github.com/wekan/FerretDB/commit/ed7a4df1 . Thanks to xet7.
- Update all Go dependencies to their latest releases (grpc, protobuf, OpenTelemetry, `golang.org/x/{crypto,net,sys,text}`, `go.mongodb.org/mongo-driver`, testify and procfs) and bump the `go` directive from 1.24 to 1.25 by @xet7 in https://github.com/wekan/FerretDB/commit/7549f63d311e499c3136a9eac9efed874bccbf6a . Thanks to xet7.
- Update the SQLite backend driver `modernc.org/sqlite` v1.32.0 → v1.53.0 (and `modernc.org/libc` v1.55.3 → v1.74.0), which requires Go 1.25 by @xet7 in https://github.com/wekan/FerretDB/commit/7549f63d311e499c3136a9eac9efed874bccbf6a . Thanks to xet7.
- Keep `github.com/FerretDB/wire` pinned at v0.0.8, because newer releases change the internal BSON API (`GetByIndex`, `Sections`, `CheckNaNs`) used by this v1.24.2 codebase by @xet7 in https://github.com/wekan/FerretDB/commit/7549f63d311e499c3136a9eac9efed874bccbf6a . Thanks to xet7.

## [v1.24.2](https://github.com/FerretDB/FerretDB/releases/tag/v1.24.2) (2025-05-27)

## What's Changed

### Fixed Bugs 🐛

- Ignore `bypassEmptyTsReplacement` to simplify migrations by @chilagrow in https://github.com/FerretDB/FerretDB/pull/5163

### Other Changes 🤖

- Bump deps by @chilagrow in https://github.com/FerretDB/FerretDB/pull/5165
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/5184

[All commits](https://github.com/FerretDB/FerretDB/compare/v1.24.1...v1.24.2).

## [v1.24.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.24.1) (2025-05-08)

### Fixed Bugs 🐛

- Fix stats for MySQL backend by @chuangjinglu in https://github.com/FerretDB/FerretDB/pull/4598

### Documentation 📄

- Add Cozystack to README by @tym83 in https://github.com/FerretDB/FerretDB/pull/4563
- Update Mermaid diagrams by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/4582
- Remove old docs and fix linking to the rest by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4654
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4678
- Update URLs for FerretDB v1 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4677
- Reformat with settings from v2 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4686

### Other Changes 🤖

- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4564
- Bump Go version by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4599
- Port tools from v2 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4602
- Add separate CI job for defining Docker tags by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4607
- Update `definedockertag` logic and use it by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4603
- Update handling of all-in-one Docker images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4608
- `pngcrush` images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4623
- Add workaround for Trivy failures by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4631
- Bump Go by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4687
- Fix linters for v1 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/5101
- Fix Docker tags for pre-release git tags by @AlekSi in https://github.com/FerretDB/FerretDB/pull/5100
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/5134

### New Contributors

- @tym83 made their first contribution in https://github.com/FerretDB/FerretDB/pull/4563
- @chuangjinglu made their first contribution in https://github.com/FerretDB/FerretDB/pull/4598

[All commits](https://github.com/FerretDB/FerretDB/compare/v1.24.0...v1.24.1).

## [v1.24.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.24.0) (2024-08-28)

### What's Changed

#### Embeddable package

As communicated in the previous release, this version renames the `SLogger` field to `Logger`,
finishing the migration from [`zap`](https://github.com/uber-go/zap) to [`slog`](https://pkg.go.dev/log/slog).

### Fixed Bugs 🐛

- Ignore Stable API fields by @Evengard in https://github.com/FerretDB/FerretDB/pull/4067
- Fix Docker's `HEALTHCHECK` in production image by @dasjoe in https://github.com/FerretDB/FerretDB/pull/4547
- Remove duplicate response field on `OP_QUERY` `hello` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4549
- Fix `OP_QUERY` `saslStart` and `saslContinue` for C# driver by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4550
- Fix `saslContinue` completing handshake too early by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4552

### Documentation 📄

- Fix Terser plugin build error by @nalgeon in https://github.com/FerretDB/FerretDB/pull/4506
- Enable zoom on images by @Fashander in https://github.com/FerretDB/FerretDB/pull/4508
- Add blog post on building a RESTful API with Deno by @Fashander in https://github.com/FerretDB/FerretDB/pull/4517
- Update missing image by @Fashander in https://github.com/FerretDB/FerretDB/pull/4522
- Fix broken links by @Fashander in https://github.com/FerretDB/FerretDB/pull/4525
- Fix critical typo in telemetry documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4536
- Bump Docusaurus by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4544
- Added Elestio as one-click deploy option by @kaiwalyakoparkar in https://github.com/FerretDB/FerretDB/pull/4546
- Add docs for new authentication by @Fashander in https://github.com/FerretDB/FerretDB/pull/4557

### Other Changes 🤖

- Implement our own changelog generator by @vigneshsankariyer1234567890 in https://github.com/FerretDB/FerretDB/pull/4219
- Add open issues check in `checkdocs` by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/4258
- Prototype OTel context propagation by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/4483
- Cleanup logging by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4489
- Use `wire` and `wirebson` packages by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4490
- Alignment and Bugfixes for SAP HANA backend by @yonarw in https://github.com/FerretDB/FerretDB/pull/4491
- Port small things from v2 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4495
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4496
- Remove `zap` remnants by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4497
- Update `listDatabases` integration test filter input by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4499
- Update duplicate field handling by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4500
- Convert BSON values of `wirebson` to `types` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4501
- Use `wireclient` package by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4502
- Fix log message by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4503
- Fix `checkdocs` Github cache on CI by @noisersup in https://github.com/FerretDB/FerretDB/pull/4509
- Document logging changes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4510
- Fix CI for documentation preview by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4518
- Add `Taskfile` target to `pngcrush` all new images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4519
- Remove fuzzing by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4520
- Use Go 1.22.6 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4523
- Minor cleanup by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4535
- Use `wireclient` login function in integration test by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4538
- Remove `wireconn` tests for now by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4548
- Pass `GITHUB_TOKEN` to tools tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4558

### New Contributors

- @nalgeon made their first contribution in https://github.com/FerretDB/FerretDB/pull/4506
- @Evengard made their first contribution in https://github.com/FerretDB/FerretDB/pull/4067
- @dasjoe made their first contribution in https://github.com/FerretDB/FerretDB/pull/4547
- @kaiwalyakoparkar made their first contribution in https://github.com/FerretDB/FerretDB/pull/4546
- @vigneshsankariyer1234567890 made their first contribution in https://github.com/FerretDB/FerretDB/pull/4219

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/66?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.23.1...v1.24.0).

## [v1.23.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.23.1) (2024-08-13)

### Fixed Bugs 🐛

- Fix building with `go build -trimpath` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4526

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/67?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.23.0...v1.23.1).

## [v1.23.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.23.0) (2024-07-25)

### What's Changed

#### Embeddable package

This release switches from the [`zap` logging package](https://github.com/uber-go/zap) to the standard [`slog`](https://pkg.go.dev/log/slog).
If the logger was configured by Go programs that import [`github.com/FerretDB/FerretDB/ferretdb` package](https://pkg.go.dev/github.com/FerretDB/FerretDB/ferretdb), they should configure the `SLogger` field instead.
Setting the old `Logger` field will make the program panic and make the issue immediately noticeable.

The next release will completely remove `zap` and rename `SLogger` to just `Logger`.

#### Initial OpenTelemetry tracing support

This release adds initial support for sending OpenTelemetry traces to the OTLP endpoint.
The set of spans and their attributes is not stable yet and will change over time.

All improvements in observability in this release (OpenTelemetry traces, Kubernetes probes, debug archive)
are documented [there](https://docs.ferretdb.io/configuration/observability/).

#### Experimental Systemd configuration in `.deb` and `.rpm` packages

This release adds initial unit files for `systemd` that auto-start FerretDB.
They are likely to change in the future in incompatible ways; for example, we may switch to using a non-root user.

### New Features 🎉

- Add Kubernetes liveness probe by @noisersup in https://github.com/FerretDB/FerretDB/pull/4378
- Add Kubernetes readiness probe by @noisersup in https://github.com/FerretDB/FerretDB/pull/4426
- Implement Docker healthcheck by @noisersup in https://github.com/FerretDB/FerretDB/pull/4364
- Add OpenTelemetry traces and spans by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4477
- Send OpenTelemetry traces and spans to OTLP endpoint by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4484
- Implement `/debug/archive` handler by @sachinpuranik in https://github.com/FerretDB/FerretDB/pull/3895
- Provide systemd unit file in `.deb` and `.rpm` packages by @noisersup in https://github.com/FerretDB/FerretDB/pull/4478

### Enhancements 🛠

- Improve support for named loggers by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4432

### Documentation 📄

- Document Kubernetes probes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4424
- Refactor and document `/debug/archive` handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4485
- Document logging by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4436
- Add release blog post for FerretDB v1.22.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/4401
- Add blog post on running FerretDB and CloudNativePG on Kubernetes by @Fashander in https://github.com/FerretDB/FerretDB/pull/4377
- Add blogpost on "monitoring FerretDB performance using Coroot" by @Fashander in https://github.com/FerretDB/FerretDB/pull/4279
- Crush `.png` images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4441
- Remove broken links by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4433

### Other Changes 🤖

- Replace deprecated syntax in Dockerfiles by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4397
- Update comments about interfaces by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4405
- Check database name for authentication by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4402
- Refactor runnables by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4404
- Add tests for `ctxutil.Sigterm` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4406
- Setup OpenTelemetry exporter for FerretDB by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/4380
- Extract `types` and `zap` code into separate files by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4408
- Bump Go and deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4416
- Implement Kubernetes startup probe by @noisersup in https://github.com/FerretDB/FerretDB/pull/4399
- Disable OTEL in tests where collection name might have non-UTF-8 symbols by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/4423
- Stop Otel exporter gracefully in `envtool` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4425
- Include `OpReply` error handling by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4420
- Test `authSource` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4407
- Return `connectionStatus` command `db` field by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4419
- Implement checkswitch to handle regular switches by @PaveenV in https://github.com/FerretDB/FerretDB/pull/4381
- Cleanup `checkswitch` handling by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4434
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4446
- Ignore `$readPreferences` for `insert` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4440
- Readiness probe cleanup by @noisersup in https://github.com/FerretDB/FerretDB/pull/4447
- Update linters configuration by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4451
- Add support for named `slog` loggers by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4435
- Increase setup timeout in tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4454
- Port pgx logger by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4450
- Handle `authSource` in low level driver by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4449
- Use single definition of order for `checkswitch` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4452
- Clarify the meaning of the passed context by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4455
- Remove `FuncCall` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4476
- Use `slog` in `clientconn` package by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4457
- Use `slog` in `postgresql` backend by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4466
- Use `slog` in `sqlite` backend by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4467
- Use `slog` in `mysql` and `hana` backends by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4463
- Use `slog` in `oplog` and `cursor` packages by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4471
- Use `slog` in `otel` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4474
- Use `slog` in `debug` package by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4473
- Use `slog` in integration tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4481
- Use `slog` in `main.go` and embedded `ferretdb` package by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4462
- Use `slog` in `fsql` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4464
- Use `slog` in handler by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4470
- Use `slog` in envtool by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4480
- Implement `slog.LogValuer` interface for `types` package by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4479
- Drop old dependency by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4486
- Cleanup logging by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4489

### New Contributors

- @PaveenV made their first contribution in https://github.com/FerretDB/FerretDB/pull/4381

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/65?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.22.0...v1.23.0).

## [v1.22.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.22.0) (2024-06-26)

### What's Changed

#### Docker images changes

Production Docker images now use a non-root user with UID 1000 and GID 1000.

### New Features 🎉

- Make maximum document size configurable by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4294
- Enable initial user setup for new authentication by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4310

### Fixed Bugs 🐛

- Fix TCP port for debug handler in Docker images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4218
- Fix embedded package panic by @noisersup in https://github.com/FerretDB/FerretDB/pull/4278

### Enhancements 🛠

- Use non-privileged `scratch` for production Docker images by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/4211
- Improve error message for `state.json` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4251
- Sort new fields in lexicographic order during update by @wazir-ahmed in https://github.com/FerretDB/FerretDB/pull/4223

### Documentation 📄

- Add blog post for FerretDB v1.21 release by @Fashander in https://github.com/FerretDB/FerretDB/pull/4202
- Add blog post for Openziti by @Fashander in https://github.com/FerretDB/FerretDB/pull/4194
- Fix broken code blocks in documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4239
- Add KubeDB blogpost on deploying FerretDB on Kubernetes by @Fashander in https://github.com/FerretDB/FerretDB/pull/4253
- Add blog post about deploying FerretDB on Taikun CloudWorks by @Fashander in https://github.com/FerretDB/FerretDB/pull/4297
- Update example in documentation by @nullniverse in https://github.com/FerretDB/FerretDB/pull/4305
- Add blog post on Adding MongoDB compatibility to Aiven for PostgreSQL by @Fashander in https://github.com/FerretDB/FerretDB/pull/4349
- Add `restart: on-failure` to all containers by @pravi in https://github.com/FerretDB/FerretDB/pull/4309

### Other Changes 🤖

- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4201
- Make our own low-level driver for testing by @noisersup in https://github.com/FerretDB/FerretDB/pull/4193
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4238
- Fix some comments by @deferdeter in https://github.com/FerretDB/FerretDB/pull/4237
- Add dummy setup flags by @b1ron in https://github.com/FerretDB/FerretDB/pull/4247
- Fix some comments by @dockercui in https://github.com/FerretDB/FerretDB/pull/4257
- Remove old BSON implementation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4262
- Port BSON changes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4263
- Move tools cache directory by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4265
- Use more shards on CI by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4266
- Bump Go to 1.22.3 and deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4272
- Update linters configuration by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4277
- Fix `env-data` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4289
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4302
- Port some changes from v2 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4307
- Extract user creation and move to `backends` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4311
- Populate `env-data` for running `FerretDB` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4319
- Fix `task docker-local` command by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4363
- Add stub for the Docker healthcheck by @noisersup in https://github.com/FerretDB/FerretDB/pull/4355
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4375
- Remove `PLAIN` mechanism from new authentication by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4373
- Fix codecov CLI version by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4379
- Add `TestMain` to each integration test package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4366
- Handle supported mechanisms in `hello` and `getParameters` commands by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4368
- Remove ambiguous comment by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4382
- Revert codecov version fix by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4383
- Fix typo in migration guide by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4384
- Port `wire` package changes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4386
- Port `password` changes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4388
- Include `SpeculativeAuthenticate` changes by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4390
- Fix `saslContinue` prematurely returning `done` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4391

### New Contributors

- @deferdeter made their first contribution in https://github.com/FerretDB/FerretDB/pull/4237
- @dockercui made their first contribution in https://github.com/FerretDB/FerretDB/pull/4257
- @nullniverse made their first contribution in https://github.com/FerretDB/FerretDB/pull/4305
- @pravi made their first contribution in https://github.com/FerretDB/FerretDB/pull/4309

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/64?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.21.0...v1.22.0).

## [v1.21.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.21.0) (2024-02-20)

### New Features 🎉

- Add experimental `SCRAM-SHA-1`/`SCRAM-SHA-256` authentication support by @henvic in https://github.com/FerretDB/FerretDB/pull/4078

### Fixed Bugs 🐛

- Reorganize and fix `update`/`upsert` logic by @wazir-ahmed in https://github.com/FerretDB/FerretDB/pull/4069

### Enhancements 🛠

- Improve capped collection cleanup by @wazir-ahmed in https://github.com/FerretDB/FerretDB/pull/4118
- Make batch sizes configurable by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/4149

### Documentation 📄

- Fix Codapi file error by @Fashander in https://github.com/FerretDB/FerretDB/pull/4077
- Add Tembo QA blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/4081
- Update correct image link by @Fashander in https://github.com/FerretDB/FerretDB/pull/4116
- Add Pulumi blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/4102
- Add Tembo to README by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4168
- Remove some closed issues from documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4172

### Other Changes 🤖

- Use Go 1.22 and bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4094
- Add more fields to requests and responses by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/4096
- Revert SQLite version bump by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4106
- Refactor `bson2` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4105
- Use `bson2` package for wire queries and replies by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4108
- Make logger configurable in the embedded `ferretdb` package by @fadyat in https://github.com/FerretDB/FerretDB/pull/4028
- Fix `envtool run test` `-run` and `-skip` flags by @henvic in https://github.com/FerretDB/FerretDB/pull/4101
- Add MySQL backend collection by @adetunjii in https://github.com/FerretDB/FerretDB/pull/4083
- Ignore `maxTimeMS` argument in `count`, `insert`, `update`, `delete` by @farit2000 in https://github.com/FerretDB/FerretDB/pull/4121
- Use correct salt length by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4126
- Skip stuck tailable cursor test by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4131
- Enforce new authentication by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4075
- Replace `bson` with `bson2` in `wire` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4110
- Improve `OP_MSG` validity checks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4135
- Support speculative authenticate by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4111
- Add MySQL backend by @adetunjii in https://github.com/FerretDB/FerretDB/pull/4137
- Fix `saslContinue` crashing due to not found authentication conversation by @henvic in https://github.com/FerretDB/FerretDB/pull/4129
- Cleanup TODO for speculative authenticate by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4143
- Fix MySQL collection stats by @adetunjii in https://github.com/FerretDB/FerretDB/pull/4145
- Use Go 1.22.1 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4155
- Advertise SCRAM / SASL support in addition to PLAIN by @henvic in https://github.com/FerretDB/FerretDB/pull/4113
- Add linter to check truncate tag in blog posts by @sbshah97 in https://github.com/FerretDB/FerretDB/pull/4139
- Fix PLAIN mechanism authentication incorrectly working by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4163
- Improve `bson2` and `wire` logging by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4148
- Fix logging of deeply nested documents by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4167
- Support localhost exception by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4156
- Do not use the flow style in the diff output by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4170
- Do not use `fjson` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4175
- Remove `fjson` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4176
- Fix `speculativeAuthenticate` panic on empty database by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4178
- Move old `bson` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4177
- Rename `bson2` to `bson` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4179
- Move Docker build files by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4180
- Bump protobuf dependency to make CI happy by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4187
- Use authentication enabled docker for integration test by @chilagrow in https://github.com/FerretDB/FerretDB/pull/4160
- Bump `pgx` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4190

### New Contributors

- @farit2000 made their first contribution in https://github.com/FerretDB/FerretDB/pull/4121
- @sbshah97 made their first contribution in https://github.com/FerretDB/FerretDB/pull/4139

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/63?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.20.1...v1.21.0).

## [v1.20.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.20.1) (2024-02-19)

### What's Changed

#### Docker images changes

~~Production Docker images now use a non-root user with UID 1000 and GID 1000.~~

That change was made in v1.20.0, reverted in v1.20.1, and will be re-introduced in a future release.

### Documentation 📄

- Add blog post on Ubicloud managed postgres by @Fashander in https://github.com/FerretDB/FerretDB/pull/4010
- Add release blog post for v1.19.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/4020
- Truncate release blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/4047
- Add blog post on Disaster Recovery for FerretDB with Elotl Nova by @Fashander in https://github.com/FerretDB/FerretDB/pull/4038
- Update Codapi by @Fashander in https://github.com/FerretDB/FerretDB/pull/4039
- Add blogpost on FerretDB stack on Tembo by @Fashander in https://github.com/FerretDB/FerretDB/pull/4037

### Other Changes 🤖

- Add tests for new SCRAM-SHA-256 authentication support by @henvic in https://github.com/FerretDB/FerretDB/pull/4012
- Add `TODO` comments for logging by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4015
- Add `bson2` helpers for conversions and logging by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4019
- Setup MySQL backend by @adetunjii in https://github.com/FerretDB/FerretDB/pull/4003
- Expose new authentication enabling flag by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4029
- Bump deps and speed-up `checkcomments` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4030
- Display `envtool run test` progress with run and/or skip flags by @fadyat in https://github.com/FerretDB/FerretDB/pull/3999
- Use Ubicloud for CI runners by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4027
- Implement `database.Stats` for MySQL backend by @adetunjii in https://github.com/FerretDB/FerretDB/pull/4034
- Minor cleanups by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4046
- Add experimental pushdown for dot notation by @noisersup in https://github.com/FerretDB/FerretDB/pull/4049
- Bump Go to 1.21.7 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4059
- Add utility for hashing SCRAM-SHA-256 password by @henvic in https://github.com/FerretDB/FerretDB/pull/4031
- Use rootless `scratch` containers for production Docker images by @ahmethakanbesel in https://github.com/FerretDB/FerretDB/pull/4004
- Prepare query statements for MySQL by @adetunjii in https://github.com/FerretDB/FerretDB/pull/4064
- Implement `bson2.RawDocument` checking by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4076
- Add helper for decoding document sequences by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4080
- Add SCRAM-SHA-256 authentication support by @henvic in https://github.com/FerretDB/FerretDB/pull/3989
- Remove SCRAM-SHA-256 implementation TODO links by @henvic in https://github.com/FerretDB/FerretDB/pull/4086
- Update telemetry host by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4085

### New Contributors

- @ahmethakanbesel made their first contribution in https://github.com/FerretDB/FerretDB/pull/4004

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/62?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.19.0...v1.20.0).

## [v1.19.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.19.0) (2024-01-29)

### New Features 🎉

- Support creating an index on nested fields for SQLite by @fadyat in https://github.com/FerretDB/FerretDB/pull/3972

### Fixed Bugs 🐛

- Fix `maxTimeMS` for `getMore` command by @noisersup in https://github.com/FerretDB/FerretDB/pull/3919
- Fix `upsert` with `$setOnInsert` operator by @wazir-ahmed in https://github.com/FerretDB/FerretDB/pull/3931
- Fix validation process for creating duplicate `_id` index by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/3990

### Documentation 📄

- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3955
- Add documentation for oplog by @Fashander in https://github.com/FerretDB/FerretDB/pull/3960
- Fix search queries by @Fashander in https://github.com/FerretDB/FerretDB/pull/3976

### Other Changes 🤖

- Fix Taskfile.yml indentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3964
- Speed-up Docker builds by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3965
- Run more `maxTimeMS` tests by @noisersup in https://github.com/FerretDB/FerretDB/pull/3940
- Store passwords for PLAIN authentication mechanism by @henvic in https://github.com/FerretDB/FerretDB/pull/3928
- Use PBKDF2 for storing `PLAIN` passwords by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3970
- Shard extra CI configurations by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3946
- Small fixes and tweaks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3971
- Implement `updateUser` command by @henvic in https://github.com/FerretDB/FerretDB/pull/3973
- Small assorted tweaks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3979
- Add MySQL backend Registry by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3967
- Add new BSON decoding package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3905
- Refactor `bson2` encoding/decoding by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3987
- Use `usersInfo` for `createUser` and `dropUser` integration tests by @henvic in https://github.com/FerretDB/FerretDB/pull/3980
- Improve `bson2` fuzzing by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3988
- Update contributing documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3994
- Use `ListCollection` with a filter by @sachinpuranik in https://github.com/FerretDB/FerretDB/pull/3995
- Add tests for MySQL registry by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3993
- Prepare CI to having multiple main branches by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4002
- Ignore `$readPreference` field by @b1ron in https://github.com/FerretDB/FerretDB/pull/3996
- Hide `*types.Document` from `wire` struct fields by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4000
- Add deep `bson2` decoding by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3997
- Expose raw documents in the `wire` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/4011

### New Contributors

- @fadyat made their first contribution in https://github.com/FerretDB/FerretDB/pull/3972

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/61?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.18.0...v1.19.0).

## [v1.18.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.18.0) (2024-01-08)

### What's Changed

#### Capped collections

This release adds support for capped collections.
They can be created as usual using `create` command.
Both `max` (maximum number of documents) and `size` (maximum collection size in bytes) parameters are supported.

#### Tailable cursors

This release adds support for tailable cursors.
Both `tailable` and `awaitData` parameters are supported.

#### OpLog tailing

This release adds support for the basic OpLog functionality.
The main supported use case is Meteor's OpLog tailing.
Replication is not supported yet.

OpLog collection does not exist by default.
To enable OpLog functionality, create a capped collection `oplog.rs` in the `local` database.
Setting replica set name using [`--repl-set-name` flag / `FERRETDB_REPL_SET_NAME` environment variable](https://docs.ferretdb.io/configuration/flags/#general)
might also be needed.

### New Features 🎉

- Add support for tailable cursors by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3799
- Implement `awaitData` tailable cursors by @noisersup in https://github.com/FerretDB/FerretDB/pull/3900
- Implement and test OpLog for update operations by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3899
- Enable OpLog and tailable cursors by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3887
- Implement `createUser` command by @henvic in https://github.com/FerretDB/FerretDB/pull/3848
- Implement `dropUser` command by @henvic in https://github.com/FerretDB/FerretDB/pull/3866
- Implement `dropAllUsersFromDatabase` command by @henvic in https://github.com/FerretDB/FerretDB/pull/3867
- Implement `usersInfo` command by @henvic in https://github.com/FerretDB/FerretDB/pull/3897

### Enhancements 🛠

- Don't cleanup capped collections if there is nothing to cleanup by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3909
- Disallow `maxTimeMS` for non-awaitData cursors in `getMore` command by @noisersup in https://github.com/FerretDB/FerretDB/pull/3917
- Add the necessary for replica set fields to `ismaster` response by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3925

### Other Changes 🤖

- Add CI configuration for Citus by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3865
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3880
- Fix tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3871
- Add MySQL backend registry by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3850
- Fix local MySQL setup by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3886
- Fix clean-up on `aggregate` errors by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3892
- Use `dropAllUsersFromDatabase` in tests by @henvic in https://github.com/FerretDB/FerretDB/pull/3891
- Add `awaitData` tests by @noisersup in https://github.com/FerretDB/FerretDB/pull/3872
- Add utilities for working with passwords by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3890
- Add support for `--skip` in `envtool tests run` by @KrishnaSindhur in https://github.com/FerretDB/FerretDB/pull/3805
- Small clean-ups by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3896
- Add basic SAP HANA backend by @yonarw in https://github.com/FerretDB/FerretDB/pull/3719
- Add integration tests for OpLog entries of insert and delete operations by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3862
- Add more cursor tests by @noisersup in https://github.com/FerretDB/FerretDB/pull/3893
- Refactor `ConnInfo` in preparation for new auth by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3901
- Add some small improvements to the linter that checks open issues by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3756
- Forbid `bson.E/D/M/A`, except integration tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3908
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3912
- Make `AssertEqual` helper handle duplicate keys by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3911
- Drop test users on cleanup by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3914
- Cleanup `awaitData` tailable cursor by @noisersup in https://github.com/FerretDB/FerretDB/pull/3915
- Cleanup a closed issue by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3924
- Ignore `sparse` index parameter for now by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3934
- Allow filtering by name in `ListDatabases` and `ListCollections` by @sachinpuranik in https://github.com/FerretDB/FerretDB/pull/3851
- Disallow native passwords for MySQL by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3937
- Fix `awaitData` cursor panic by @noisersup in https://github.com/FerretDB/FerretDB/pull/3935
- Use `usersInfo` in `dropAllUsersFromDatabase` tests by @henvic in https://github.com/FerretDB/FerretDB/pull/3932
- Allow Native Passwords for testcase by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3941

### New Contributors

- @yonarw made their first contribution in https://github.com/FerretDB/FerretDB/pull/3719
- @sachinpuranik made their first contribution in https://github.com/FerretDB/FerretDB/pull/3851

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/60?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.17.0...v1.18.0).

## [v1.17.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.17.0) (2023-12-18)

### New Features 🎉

- Allow building without PostgreSQL or SQLite backend by @anunayasri in https://github.com/FerretDB/FerretDB/pull/3803
- Allow sorting by `$natural` by @noisersup in https://github.com/FerretDB/FerretDB/pull/3822
- Disallow `$natural` in compound sort by @noisersup in https://github.com/FerretDB/FerretDB/pull/3832
- Generate collection UUIDs by @wazir-ahmed in https://github.com/FerretDB/FerretDB/pull/3791
- Support capped collection cleanup by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3831

### Fixed Bugs 🐛

- Fix `listDatabases` filtering when using `nameOnly` by @henvic in https://github.com/FerretDB/FerretDB/pull/3788

### Enhancements 🛠

- Improve `validate` diagnostic command by @b1ron in https://github.com/FerretDB/FerretDB/pull/3804
- Add fields to `listCollections.cursor` response by @henvic in https://github.com/FerretDB/FerretDB/pull/3809

### Documentation 📄

- Add new release FerretDB v1.16.0 blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3808
- Change release blogpost image by @Fashander in https://github.com/FerretDB/FerretDB/pull/3825
- Enable versioning on documentation by @Fashander in https://github.com/FerretDB/FerretDB/pull/3821
- Add documentation for older versions by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3834

### Other Changes 🤖

- Support subdirectories for integration tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3810
- Move tests for tailbable cursors by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3811
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3817
- Refactor cursor creation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3820
- Use single flag to disable all pushdowns by @noisersup in https://github.com/FerretDB/FerretDB/pull/3793
- Add tracing to `envtool tests run` by @hungaikev in https://github.com/FerretDB/FerretDB/pull/3695
- Extract `find` helper functions by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3826
- Fix tests for MongoDB with enabled replica set by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3807
- Ignore `$clusterTime` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3830
- Add MySQL backend metadata by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3828
- Clean-up tests a bit by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3835
- Allow bypassing authentication by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3840
- Add tests for tailable cursors by @noisersup in https://github.com/FerretDB/FerretDB/pull/3833
- Add missing logging parameter by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3847
- Test cross-session cursors by @noisersup in https://github.com/FerretDB/FerretDB/pull/3849
- Use MongoDB 7 by @henvic in https://github.com/FerretDB/FerretDB/pull/3824
- Simplify tailable cursor tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3854
- Add `upsert` tests by @wazir-ahmed in https://github.com/FerretDB/FerretDB/pull/3864
- Add cursor tests by @noisersup in https://github.com/FerretDB/FerretDB/pull/3859

### New Contributors

- @wazir-ahmed made their first contribution in https://github.com/FerretDB/FerretDB/pull/3791
- @henvic made their first contribution in https://github.com/FerretDB/FerretDB/pull/3788
- @anunayasri made their first contribution in https://github.com/FerretDB/FerretDB/pull/3803
- @hungaikev made their first contribution in https://github.com/FerretDB/FerretDB/pull/3695

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/59?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.16.0...v1.17.0).

## [v1.16.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.16.0) (2023-12-04)

### Documentation 📄

- Clarify MongoDB version by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3653
- Add blogpost for release v1.15 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3728
- Update domain name in docs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3757
- Update Docusaurus to v3 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3772
- Update domain name in more places by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3802

### Other Changes 🤖

- Cleanup pushdown terminology by @noisersup in https://github.com/FerretDB/FerretDB/pull/3691
- Make RecordID a signed value by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3740
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3747
- Add MySQL into the build system by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3736
- Add MySQL backend to CI by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3751
- Remove common `handlers.Interface` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3753
- Remove unsafe pushdown by @noisersup in https://github.com/FerretDB/FerretDB/pull/3752
- Support `DeleteAll` for capped collections by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3718
- Add startup warning for debug builds by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3735
- Move `sqlite/*.go` to `internal/handler` by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3755
- Add TODOs about pushdowns by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3762
- Clean-up old code for multiple handlers by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3763
- Add TODOs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3764
- Move some commands from `common` to the handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3766
- Add TODOs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3771
- Allow `system.` prefix for collections for now by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3775
- Setup MySQL integration tests by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3758
- Rename `commonerrors` and `commonparams` by @noisersup in https://github.com/FerretDB/FerretDB/pull/3779
- Add TLS support to proxy mode by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3783
- Provide sort to backend as the document by @noisersup in https://github.com/FerretDB/FerretDB/pull/3754
- Add stubs for authentication commands by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3776
- Move `getParameter` out of `common` package by @noisersup in https://github.com/FerretDB/FerretDB/pull/3789
- Remove `commoncommands` package by @noisersup in https://github.com/FerretDB/FerretDB/pull/3780
- Remove done TODOs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3795
- Ignore `go-consistent` failures by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3794
- Log batches for `find`, `aggregate`, `getMore` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3800
- Set `GOARM` explicitly by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3796

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/58?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.15.0...v1.16.0).

## [v1.15.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.15.0) (2023-11-20)

### What's Changed

#### Artifacts naming scheme

Our release binaries and packages now include `linux` as a part of their file names.
That's a preparation for providing artifacts for other OSes.

### New Features 🎉

- Support `showRecordId` in `find` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3637
- Add JSON format for logging by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3689
- Add option to disable `--debug-addr` by @cosmastech in https://github.com/FerretDB/FerretDB/pull/3698

### Enhancements 🛠

- Allow usage without state dir by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3703
- Allow the usage of existing PostgreSQL schema by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3717
- Generate SQL queries with comments for find operations by @chumaumenze in https://github.com/FerretDB/FerretDB/pull/3697

### Documentation 📄

- Mention proxy flag in docs by @Fashander in https://github.com/FerretDB/FerretDB/pull/3673
- Update README.md to include Vultr by @mrusme in https://github.com/FerretDB/FerretDB/pull/3675
- Add blog post on FastNetMon by @Fashander in https://github.com/FerretDB/FerretDB/pull/3676
- Fix content error by @Fashander in https://github.com/FerretDB/FerretDB/pull/3694
- Add blogpost for "How to Package and Deploy FerretDB with Acorn" by @Fashander in https://github.com/FerretDB/FerretDB/pull/3679
- Enable interactivity on blogpost by @Fashander in https://github.com/FerretDB/FerretDB/pull/3659
- Fix Codapi error on blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3721
- Add migration blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3709

### Other Changes 🤖

- Make tests stable on CI by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3678
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3690
- Use separate PostgreSQL databases in tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3622
- Add test for tailable cursor with non-capped collection by @noisersup in https://github.com/FerretDB/FerretDB/pull/3677
- Use `-` in addition to the empty string by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3704
- Use the standard `*mongo.WriteError` type by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3705
- Fix tests for MongoDB with enabled replica set by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3604
- Handle panicking tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3711
- Make handler accept constructed backend by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3710
- Add issue tracking to checkcomments analyzer by @raeidish in https://github.com/FerretDB/FerretDB/pull/3632
- Add TODOs and fix URLs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3723
- Move diff tests from dance to integration tests by @ksankeerth in https://github.com/FerretDB/FerretDB/pull/3525
- Small assorted tweaks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3724

### New Contributors

- @mrusme made their first contribution in https://github.com/FerretDB/FerretDB/pull/3675
- @cosmastech made their first contribution in https://github.com/FerretDB/FerretDB/pull/3698
- @chumaumenze made their first contribution in https://github.com/FerretDB/FerretDB/pull/3697
- @ksankeerth made their first contribution in https://github.com/FerretDB/FerretDB/pull/3525

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/57?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.14.0...v1.15.0).

## [v1.14.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.14.0) (2023-11-07)

### What's Changed

#### Old PostgreSQL backend

As mentioned in the previous release changes, the old PostgreSQL backend code is completely removed.
PostgreSQL remains our main backend, just with a new code base.

### New Features 🎉

- Implement `compact` command by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3559

### Enhancements 🛠

- Optimize detection of duplicate fields by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3645
- Optimize `insert` performance by batching by @princejha95 in https://github.com/FerretDB/FerretDB/pull/3621

### Documentation 📄

- Fix incorrect schema by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3635
- Add blogpost for FerretDB v1.13.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3639
- Add Vultr blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3646
- Update blog post on Ubuntu by @Fashander in https://github.com/FerretDB/FerretDB/pull/3658
- Add blog post on MongoDB sorting for scalar values by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3200

### Other Changes 🤖

- Disallow capped collection creation when disabled by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3636
- Run backend tests for SAP HANA by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3657
- Update `golangci-lint` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3651
- Remove `pgdb` from `envtool` by @ShatilKhan in https://github.com/FerretDB/FerretDB/pull/3586
- Remove old `pg` handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3661
- Add test for capped collection in `aggregate` `$collStats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3643
- Enable `GOMAXPROCS` autotuning by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3105
- Add integration tests progress reporting by @rubiagatra in https://github.com/FerretDB/FerretDB/pull/3471
- Add timing information to `envtool` output by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3664
- Remove old SAP HANA handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3674
- Rename main_postgeresql to main_postgresql by @gen1us2k in https://github.com/FerretDB/FerretDB/pull/3668
- (WIP) Support `create` for capped collections by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3614
- (WIP) Support `InsertAll` and `FindAll` for capped collections by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3610

### New Contributors

- @ShatilKhan made their first contribution in https://github.com/FerretDB/FerretDB/pull/3586
- @rubiagatra made their first contribution in https://github.com/FerretDB/FerretDB/pull/3471
- @gen1us2k made their first contribution in https://github.com/FerretDB/FerretDB/pull/3668

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/56?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.13.0...v1.14.0).

## [v1.13.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.13.0) (2023-10-23)

### What's Changed

#### New PostgreSQL backend

The new PostgreSQL backend is now enabled by default.
You can still enable the old backend with `--postgresql-old` flag or `FERRETDB_POSTGRESQL_OLD=true` environment variable,
but it will be removed in the next release.

#### Default SQLite directory for Docker images

Our Docker images (but not binaries and `.deb` / `.rpm` packages) now use `/state` directory for the SQLite backend.
That directory is also a Docker volume, so data will be preserved after the container restart by default.

#### `arm/v7` packages

We now provide `linux/arm/v7` binaries, Docker images, and `.deb` / `.rpm` packages.

### New Features 🎉

- Implement pushdown for `aggregate` for PostgreSQL by @noisersup in https://github.com/FerretDB/FerretDB/pull/3607
- Implement sort pushdown for PostgreSQL by @noisersup in https://github.com/FerretDB/FerretDB/pull/3504
- Implement limit pushdown for PostgreSQL by @noisersup in https://github.com/FerretDB/FerretDB/pull/3580
- Implement `indexSizes` for `collStats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3575
- Implement free storage in `collStats`, `dbStats` and `aggregate` `$collStats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3594
- Add capped collection counts in `serverStatus` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3566
- Integrate Statsviz by @codenoid in https://github.com/FerretDB/FerretDB/pull/3591

### Fixed Bugs 🐛

- Fix invalid validation for `_id` field by @slavabobik in https://github.com/FerretDB/FerretDB/pull/3523
- Fix `explain` panic for non-existent collection on PostgreSQL by @noisersup in https://github.com/FerretDB/FerretDB/pull/3541

### Enhancements 🛠

- Add basic logging for PostgreSQL backend by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3560
- Report actual backend name by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3570
- Improve `/debug` page by @codenoid in https://github.com/FerretDB/FerretDB/pull/3592
- Add filter pushdown for `_id: <string>` for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3599

### Documentation 📄

- Add release blog post for FerretDB v1.12 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3555
- Crush images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3561
- Change SQLite directory for Docker images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3571
- Enable Mermaid diagrams in Docusaurus by @sid-js in https://github.com/FerretDB/FerretDB/pull/3532
- Enable linters to accept exclamation marks in headers by @chanon-mike in https://github.com/FerretDB/FerretDB/pull/3578
- Add SQLite info to glossary list by @pvinoda in https://github.com/FerretDB/FerretDB/pull/3593
- Add blog post on using Illa Cloud with FerretDB by @Fashander in https://github.com/FerretDB/FerretDB/pull/3516
- Add SQLite set up docs by @Fashander in https://github.com/FerretDB/FerretDB/pull/3568
- Add "How to Install FerretDB on Ubuntu" blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/2802
- Update ILLA blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3620
- Add links to blog by @Fashander in https://github.com/FerretDB/FerretDB/pull/3623

### Other Changes 🤖

- Improve embedded package documentation by @princejha95 in https://github.com/FerretDB/FerretDB/pull/3537
- Use separate PostgreSQL databases in tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3553
- Make `collStats` calculate collection size accurately for `PostgreSQL` statistics by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3513
- Implement `Collection.Compact` for SQLite by @Akhil-2001 in https://github.com/FerretDB/FerretDB/pull/3536
- Use self-hosted runner for packages building by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3569
- Do not create databases during local setup by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3572
- Build `arm/v7` binaries by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3577
- Add more tests and fixes for `$collStats` aggregation stage by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3565
- Build `arm/v7` `.deb` and `.rpm` packages and binaries by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3576
- Add tests for insertion of documents with invalid `_id` fields by @slavabobik in https://github.com/FerretDB/FerretDB/pull/3579
- Add more data to output of `collStats` and `dbStats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3538
- Update `dataSize` and `dbStats` integration tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3585
- Do not return stats in `Backend.ListDatabases` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3588
- Remove old TODOs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3595
- Use stdlib's `slices` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3590
- Remove done TODO by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3596
- Check that linked issues are open by @KrishnaSindhur in https://github.com/FerretDB/FerretDB/pull/3277
- Make it easier to run old PG handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3598
- Implement `Collection.Compact` for PostgreSQL by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3603
- Do not skip invalid TODOs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3597
- Unskip filter pushdown integration tests by @noisersup in https://github.com/FerretDB/FerretDB/pull/3605
- Call `ANALYZE` less often by @Aditya1404Sal in https://github.com/FerretDB/FerretDB/pull/3563
- Keep envtool's version always up-to-date by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3609
- Fix some tests for SQLite backend by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3617
- Do not create OpLog database/collection on a fly by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3625
- Make `listIndexes` return a sorted list by @codenoid in https://github.com/FerretDB/FerretDB/pull/3602

### New Contributors

- @Akhil-2001 made their first contribution in https://github.com/FerretDB/FerretDB/pull/3536
- @sid-js made their first contribution in https://github.com/FerretDB/FerretDB/pull/3532
- @codenoid made their first contribution in https://github.com/FerretDB/FerretDB/pull/3591
- @chanon-mike made their first contribution in https://github.com/FerretDB/FerretDB/pull/3578
- @pvinoda made their first contribution in https://github.com/FerretDB/FerretDB/pull/3593

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/55?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.12.1...v1.13.0).

## [v1.12.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.12.1) (2023-10-10)

### What's Changed

#### New PostgreSQL backend

The new PostgreSQL backend is ready for testing.
Enable it with `--postgresql-new` flag or `FERRETDB_POSTGRESQL_NEW=true` environment variable.
The next FerretDB version will enable it by default.

#### Docker images changes

Production Docker images use `scratch` as a base Docker image.
The only file present in the image is a FerretDB binary (with root TLS certificates embedded).

#### `arm64` binaries

In addition to `linux/arm64` Docker images, we now provide `linux/arm64` binaries and `.deb` / `.rpm` packages.

### New Features 🎉

- Build `arm64` binaries and packages by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3477
- Implement metrics collection by @Mihai22125 in https://github.com/FerretDB/FerretDB/pull/3430
- Implement `RenameCollection` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3440
- Implement `InsertAll` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3419
- Implement `DeleteAll` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3441
- Implement `DropDatabase` and `Status` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3451
- Implement `UpdateAll` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3449
- Implement `ListCollections`, `CreateCollection` and `DropCollection` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3444
- Implement `explain` by @noisersup in https://github.com/FerretDB/FerretDB/pull/3465
- Implement `database.Stats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3464
- Implement `collection.Stats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3478
- Implement `CreateIndexes`, `DropIndexes`, `ListIndexes` by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3468
- Implement filter pushdown by @noisersup in https://github.com/FerretDB/FerretDB/pull/3482
- Add info about indexes to `dbStats` response by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3534

### Fixed Bugs 🐛

- Verify that client metadata not being mutated by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/3194
- Relax restrictions when \_id is not the first field in projection by @princejha95 in https://github.com/FerretDB/FerretDB/pull/3491
- Fix `_id` restriction in aggregation `$project` stage by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3508

### Enhancements 🛠

- Implement validation for `createIndexes` and `dropIndexes` commands for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3373
- Use `Ping` for checking connection by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3519

### Documentation 📄

- Update Writing Guide by @Fashander in https://github.com/FerretDB/FerretDB/pull/3424
- Add blog post on Using MajorM as MongoDB GUI for FerretDB by @Fashander in https://github.com/FerretDB/FerretDB/pull/3387
- Add release blog post for FerretDB v1.11.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3439
- Fix RSS feed issue with images by @Fashander in https://github.com/FerretDB/FerretDB/pull/3417
- Republish Hacktobest blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3429
- Add blog post on Enmeshed by @Fashander in https://github.com/FerretDB/FerretDB/pull/3448
- Add `$project` and `$unset` to aggregation stages section by @Akhaled19 in https://github.com/FerretDB/FerretDB/pull/3450
- Add operation mode definition to Glossary by @rohitkbc in https://github.com/FerretDB/FerretDB/pull/3472
- Improve definitions for aggregation stages by @Fashander in https://github.com/FerretDB/FerretDB/pull/3499
- Add TODOs for capped collections by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3461
- Fix blog post formatting by @Fashander in https://github.com/FerretDB/FerretDB/pull/3515
- Fix typo in contributing documentation by @jrmanes in https://github.com/FerretDB/FerretDB/pull/3507
- Add mermaid diagrams by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3524
- Update documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3530

### Other Changes 🤖

- Add CI configurations by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3423
- Fix CI configuration and add TODOs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3436
- Add SAP HANA backend stub by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3433
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3437
- Remove extra function by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3447
- Fix fluky `TestRenameCollectionCompat` tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3438
- Mark pushdown results based on tested backend by @noisersup in https://github.com/FerretDB/FerretDB/pull/3446
- Run tests with `envtool tests run` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3453
- Remove unused expected failures by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3455
- Tweak SQLite backend tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3460
- Add tests for backends contract by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3456
- Speed-up Docker image building by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3470
- Fix running a subset of tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3479
- Add a workaround for Docker build failures by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3480
- Add stubs for `Collection.Compact` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3485
- Process collection name param using `collection` tag by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3476
- Revive logic of lower cased key for collection name by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3494
- Add `RecordID` to `types.Document` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3495
- Extract `ReservedPrefix` constant by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3497
- Fix stress tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3502
- Fix concurrent Docker builds by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3503
- Store indexes metadata by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3434
- Tweak tests timeouts by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3514
- Bump Go version by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3510
- Disallow importing handlers code from backends by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3512
- Disable `auto_vacuum` for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3496
- Fix index name generation by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3511
- Fix unit tests for indexes by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3531
- Make new PostgreSQL backend tests pass by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3522

### New Contributors

- @Mihai22125 made their first contribution in https://github.com/FerretDB/FerretDB/pull/3430
- @Akhaled19 made their first contribution in https://github.com/FerretDB/FerretDB/pull/3450
- @rohitkbc made their first contribution in https://github.com/FerretDB/FerretDB/pull/3472
- @princejha95 made their first contribution in https://github.com/FerretDB/FerretDB/pull/3491
- @jrmanes made their first contribution in https://github.com/FerretDB/FerretDB/pull/3507

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/54?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.11.0...v1.12.1).

## [v1.11.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.11.0) (2023-09-25)

### Fixed Bugs 🐛

- Fix `collStats` to return correct count of documents for `SQLite` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3363
- Fix metadata updates for `dropIndexes` by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3358

### Enhancements 🛠

- Return statistics of indexes for `collStats` and `dbStats` for SQLite backend by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3361

### Documentation 📄

- Improve blog format by @Fashander in https://github.com/FerretDB/FerretDB/pull/3359
- Add a blog post for v1.10 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3346
- Add docs for migrating to MongoDB from FerretDB by @Fashander in https://github.com/FerretDB/FerretDB/pull/3374
- Mention SQLite in docs by @ptrfarkas in https://github.com/FerretDB/FerretDB/pull/3408

### Other Changes 🤖

- Add test for inserting different data types by @noisersup in https://github.com/FerretDB/FerretDB/pull/3345
- Recreate test directory by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3364
- Use consistent spelling by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3365
- Use filter and insert more documents in `BenchmarkReplaceSettingsDocument` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3343
- Replace deprecated Jaeger exporter by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3368
- Remove the need to close `conninfo` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3376
- Add small tweaks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3377
- Add CI configuration for SQLite without pushdown by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3381
- Enforce valid `types` usage by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3384
- Reorder codebase in SQLite registry by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3382
- Store `PostgreSQL` metadata by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3356
- Add `TODO`s by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3412
- Run new PostgreSQL backend tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3407
- Implement `Query` in new `PostgreSQL` backend by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3411

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/52?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.10.1...v1.11.0).

## [v1.10.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.10.1) (2023-09-14)

### What's Changed

With this release, the SQLite backend support is officially out of beta,
on par with our PostgreSQL backend, and fully supported!

### New Features 🎉

- Implement `aggregate` for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3256
- Implement `collStats` for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3295
- Implement `createIndexes` for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3304
- Implement `dbStats` for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3270
- Implement `distinct` for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3265
- Implement `dropIndexes` for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3329
- Implement `explain` command for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3264
- Implement `findAndModify` for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3302
- Implement `getLog` for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3279
- Implement `listDatabases` for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3269
- Implement `listIndexes` for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3301
- Implement `renameCollection` for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3321
- Implement `serverStatus` and `dataSize` commands for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3316
- Support `_id` implicit filter for `ObjectID` in SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3330
- Support `$bit` bitwise update operator by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3266
- Support `ordered` `insert`s for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3223

### Enhancements 🛠

- Make `delete`s atomic for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3291
- Make `update`s atomic for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3296
- Do not change `search_path` parameter by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3299

### Documentation 📄

- Cleanup `$bit` update operator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3315
- Document how to test for compatibility by @b1ron in https://github.com/FerretDB/FerretDB/pull/3268
- Update blog writing guide documentation by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3209
- Update category links in writing guide by @Fashander in https://github.com/FerretDB/FerretDB/pull/3323
- Update deb.md - minor grammar correction by @athkishore in https://github.com/FerretDB/FerretDB/pull/3289
- Update the writing guide by @Fashander in https://github.com/FerretDB/FerretDB/pull/3311

### Other Changes 🤖

- Add ability to freeze `*types.Document` and `*types.Array` by @KrishnaSindhur in https://github.com/FerretDB/FerretDB/pull/3253
- Add backend decorators and OpLog stub by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3303
- Add backend interface for `collStats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3294
- Add backend interface for `dbStats` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3267
- Add more tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3336
- Add new PostgreSQL backend stub by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3319
- Add tests for accessing aggregation variable `$$ROOT` field by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3254
- Add tests for validation bug by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3286
- Add transactions to `fsql` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3278
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3284
- Clean-up `*types.Timestamp` a bit by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3305
- Do not `ConsumeValues` in the `$group` aggregation stage by @adetunjii in https://github.com/FerretDB/FerretDB/pull/3344
- Expand architecture docs, add comments by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3288
- Fix params handling for `dropIndexes` implementation for SQLite by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3350
- Make registry return full collection info by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3292
- Remove `Database.Close` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3327
- Remove duplicated `$expr` tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3255
- Return correct response if unique index violation happened on SQLite backend by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3353
- Simplify and deprecate `commonerrors.WriteErrors` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3258
- Skip tests for `enable` `setFreeMonitoring` for MongoDB by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3318
- Tweak MongoDB initialization process by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3307
- Update TODO comments by @noisersup in https://github.com/FerretDB/FerretDB/pull/3262
- Use `pkgsite` instead of `godoc` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3326
- Use Go 1.21 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3324

### New Contributors

- @athkishore made their first contribution in https://github.com/FerretDB/FerretDB/pull/3289

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/51?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.9.0...v1.10.1).

## [v1.9.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.9.0) (2023-08-28)

### Enhancements 🛠

- Add more metrics for `*sql.DB` by @slavabobik in https://github.com/FerretDB/FerretDB/pull/3230

### Documentation 📄

- Add blog post for FerretDB v1.8.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3198
- Fix typos in documentation by @pratikmota in https://github.com/FerretDB/FerretDB/pull/3217
- Make the writing guide accessible but unlisted by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3221
- Add blogpost on Leafcloud by @Fashander in https://github.com/FerretDB/FerretDB/pull/3153
- Add Postgres Ibiza event blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3210
- Add Civo Navigate event blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3201

### Other Changes 🤖

- Configure repo settings with files by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3208
- Update `go-hdb` to v1.4.1 by @aenkya in https://github.com/FerretDB/FerretDB/pull/3213
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3215
- Add another stress test for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3195
- Improve building with test coverage information by @durgakiran in https://github.com/FerretDB/FerretDB/pull/3059
- Fix concurrent SQLite tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3222
- Refactor aggregation operators by @noisersup in https://github.com/FerretDB/FerretDB/pull/3188
- Add stubs for `renameCollection` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3233
- Update issue links by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3234
- Add stubs for `explain` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3236
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3248
- Add linter for issue comments by @KrishnaSindhur in https://github.com/FerretDB/FerretDB/pull/3154
- Simplify `commonerrors` package by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3227
- Publish Docker images on quay.io by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3250
- Refactor aggregation accumulators by @noisersup in https://github.com/FerretDB/FerretDB/pull/3203
- Add new PostgreSQL backend stub by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3251
- Cleanup SQLite tests by @noisersup in https://github.com/FerretDB/FerretDB/pull/3246

### New Contributors

- @aenkya made their first contribution in https://github.com/FerretDB/FerretDB/pull/3213
- @pratikmota made their first contribution in https://github.com/FerretDB/FerretDB/pull/3217
- @durgakiran made their first contribution in https://github.com/FerretDB/FerretDB/pull/3059
- @slavabobik made their first contribution in https://github.com/FerretDB/FerretDB/pull/3230

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/50?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.8.0...v1.9.0).

## [v1.8.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.8.0) (2023-08-14)

### New Features 🎉

- Implement `$group` stage `_id` expression by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3138
- Implement `$expr` evaluation query operator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3163

### Fixed Bugs 🐛

- Do not return immutable `_id` error from `findAndModify` for upserting same `_id` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3171

### Enhancements 🛠

- Cache SQLite tables metadata by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3124
- Use lock for SQLite metadata by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3146

### Other Changes 🤖

- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3142
- Improve MongoDB/FerretDB error checking in tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3143
- Expect some `aggregate` and `insert` tests to fail for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3147
- Make administration command integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3152
- Bump deps, including Go by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3160
- Make aggregate stats integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3151
- Simplify tests a bit by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3149
- Make `distinct` and `explain` command integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3159
- Use one implementation for finding path values by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3087
- Make aggregate documents compat tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3150
- Make `query` integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3182
- Make `findAndModify` integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3173
- Make index integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3185
- Add tests for `$$ROOT` aggregation expression variable by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3180
- Make `getMore` integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3174
- Make `update` integration tests pass for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/3184
- Add tests for `$$ROOT` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3187

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/49?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.7.0...v1.8.0).

## [v1.7.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.7.0) (2023-07-31)

### New Features 🎉

- Implement `$sum` aggregation standard operator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3063

### Fixed Bugs 🐛

- Fix `PLAIN` auth with C# driver by @b1ron in https://github.com/FerretDB/FerretDB/pull/3012

### Enhancements 🛠

- Add validating max nested document/array depth by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2882
- Validate database and collection names for SQLite handler by @noisersup in https://github.com/FerretDB/FerretDB/pull/2868
- Add basic metrics, logging and tracing for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3123
- Tweak and document SQLite URI parameters by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3128

### Documentation 📄

- Add blog post for FerretDB v1.6.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3058
- Update changelog by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3072
- Update blog post for FerretDB v1.6.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/3073
- Tweak documentation and blog by @Fashander in https://github.com/FerretDB/FerretDB/pull/2992
- Add blog post on "Community matters: fireside chat with Artem Ervits, CockroachDB" by @Fashander in https://github.com/FerretDB/FerretDB/pull/3066
- Update Blog Post by @Fashander in https://github.com/FerretDB/FerretDB/pull/3086
- Update tags formatting in writing guide by @Fashander in https://github.com/FerretDB/FerretDB/pull/3097
- Add blog post on "Using Mingo with FerretDB" by @Fashander in https://github.com/FerretDB/FerretDB/pull/3074
- Simplify `checkdocs` linter by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3104
- Update MongoDB comparision blog post by @ptrfarkas in https://github.com/FerretDB/FerretDB/pull/3117
- Update MongoDB comparision blog post by @ptrfarkas in https://github.com/FerretDB/FerretDB/pull/3119
- Add blog post on Grafana Monitoring for FerretDB by @Fashander in https://github.com/FerretDB/FerretDB/pull/3106

### Other Changes 🤖

- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3064
- Mark some tests as failing for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3051
- Improve sjson package fuzzing by @quasilyte in https://github.com/FerretDB/FerretDB/pull/3071
- Merges fuzztool into envtool by @Aditya1404Sal in https://github.com/FerretDB/FerretDB/pull/2645
- Do not import `commonerrors` in tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3081
- Remove dead code by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3093
- Allow to change SQLite URI in tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3092
- Replace test doubles with constants by @noisersup in https://github.com/FerretDB/FerretDB/pull/3024
- Improve `checkdocs` linter by @KrishnaSindhur in https://github.com/FerretDB/FerretDB/pull/3095
- Add daily progress principle to `PROCESS.md` by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/3098
- Support `_id` aggregation operators for `$group` stage by @noisersup in https://github.com/FerretDB/FerretDB/pull/3096
- Bump the tools group in /tools with 1 update by @dependabot in https://github.com/FerretDB/FerretDB/pull/3109
- Backport v1.6.1 fixes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3107
- Support recursive operator calls for `$sum` aggregation accumulator by @noisersup in https://github.com/FerretDB/FerretDB/pull/3116

### New Contributors

- @Aditya1404Sal made their first contribution in https://github.com/FerretDB/FerretDB/pull/2645
- @KrishnaSindhur made their first contribution in https://github.com/FerretDB/FerretDB/pull/3095
- @ptrfarkas made their first contribution in https://github.com/FerretDB/FerretDB/pull/3117

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/47?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.6.1...v1.7.0).

## [v1.6.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.6.1) (2023-07-26)

### Fixed Bugs 🐛

- Fix pushdown for `find` with `filter` and `limit` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3114

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/48?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.6.0...v1.6.1).

## [v1.6.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.6.0) (2023-07-17)

### New Features 🎉

- Implement `killCursors` command by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2939
- Implement `ping` command for SQLite by @noisersup in https://github.com/FerretDB/FerretDB/pull/2965
- Implement `getParameter` method for SQLite by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2985

### Fixed Bugs 🐛

- Ignore `lsid` field in all commands by @b1ron in https://github.com/FerretDB/FerretDB/pull/3010
- Allow `$set` operator to update `_id` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3009
- Apply pushdown for `limit` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2993
- Fix `update` with query operator for `upsert` option by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3028

### Enhancements 🛠

- Add integration tests for `maxTimeMS` in `find`, `aggregate` and `getMore` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2953
- Remove double decoding in unmarshalSingleValue by @quasilyte in https://github.com/FerretDB/FerretDB/pull/3018
- Ignore `count.fields` argument by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3048

### Documentation 📄

- Add blog post on FerretDB release v1.5.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/2958
- Mention SQLite in README.md by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2968
- Add blog post about using NoSQLBooster with FerretDB by @Fashander in https://github.com/FerretDB/FerretDB/pull/2962
- Update blog post image by @Fashander in https://github.com/FerretDB/FerretDB/pull/3029
- Add a note about setting the stable API version by @b1ron in https://github.com/FerretDB/FerretDB/pull/3035
- Add blog post on "How to run FerretDB on top of StackGres" by @Fashander in https://github.com/FerretDB/FerretDB/pull/2869
- Fix blog post formatting by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3047
- Update database naming restrictions by @b1ron in https://github.com/FerretDB/FerretDB/pull/3042

### Other Changes 🤖

- Move `find` and `aggregation` cursor integration tests to `getMore` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2952
- Make a copy of the `testing.TB` interface by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2987
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2998
- Remove Tigris from documentation and builds by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2999
- Remove Tigris code by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3001
- Remove Tigris from tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3002
- Crush PNG files to make them smaller by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3020
- Update issue URL by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3021
- Move `testutil.TB` to `testtb.TB` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3022
- Move `logout` to `commoncommands` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3019
- Make `task all` run only unit tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3023
- Update closed issue links by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3027
- Unskip `findAndModify` `$set` integration test for `_id` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3025
- Expect `renameCollection` tests failures by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3007
- Fix `killCursors` edge case by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3030
- Fix error checking in backend contracts by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3031
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3034
- Remove `Type()` interface from aggregation stage by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3045
- Remove fixed issue link and clean up integration test provider setup by @chilagrow in https://github.com/FerretDB/FerretDB/pull/3052
- Prepare v1.6.0 release by @AlekSi in https://github.com/FerretDB/FerretDB/pull/3056

### New Contributors

- @quasilyte made their first contribution in https://github.com/FerretDB/FerretDB/pull/3018

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/46?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.5.0...v1.6.0).

## [v1.5.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.5.0) (2023-07-03)

### What's Changed

This release provides beta-level support for the SQLite backend.
There is some missing functionality, but it is ready for early adopters.

This release provides improved cursor support, enabling commands like `find` and `aggregate` to return large data sets much more effectively.

Tigris data users: Please note that this is the last release of FerretDB which includes support for the Tigris backend.
Starting from FerretDB v1.6.0, Tigris will not be supported.
If you wish to use Tigris, please do not update FerretDB beyond v1.5.0.
This and earlier versions of FerretDB with Tigris support will still be available on GitHub.

### New Features 🎉

- Implement `count` for SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2865
- Enable cursor support for PostgreSQL and SQLite by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2864

### Enhancements 🛠

- Support `find` `singleBatch` and validate `getMore` parameters by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2855
- Support cursors for aggregation pipelines by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2861
- Fix collection name starting with dot validation by @noisersup in https://github.com/FerretDB/FerretDB/pull/2912
- Improve validation for `createIndexes` and `dropIndexes` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2884
- Use cursors in `find` command by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2933

### Documentation 📄

- Add blogpost on FerretDB v1.4.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/2858
- Add blog post on "Meet FerretDB at Percona University in Casablanca and Belgrade" by @Fashander in https://github.com/FerretDB/FerretDB/pull/2870
- Update supported commands by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2876
- Add blog post "FerretDB Demo: Launch and Test a Database in Minutes" by @Fashander in https://github.com/FerretDB/FerretDB/pull/2851
- Fix Github link for Dance repository by @Matthieu68857 in https://github.com/FerretDB/FerretDB/pull/2887
- Add blog post on "How to Configure FerretDB to work on Percona Distribution for PostgreSQL" by @Fashander in https://github.com/FerretDB/FerretDB/pull/2911
- Update incorrect blog post image by @Fashander in https://github.com/FerretDB/FerretDB/pull/2920
- Crush PNG images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2931

### Other Changes 🤖

- Add more validation and tests for `$unset` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2853
- Make it easier to debug GitHub Actions by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2860
- Unify tests for indexes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2866
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2875
- Fix fuzzing corpus collection by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2879
- Add basic tests for iterators by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2880
- Implement basic `insert` support for SAP HANA by @polyal in https://github.com/FerretDB/FerretDB/pull/2732
- Update contributing docs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2828
- Improve `wire` and `sjson` fuzzing by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2883
- Add operators support for `$addFields` by @noisersup in https://github.com/FerretDB/FerretDB/pull/2850
- Unskip test that passes now by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2885
- Tweak contributing guidelines by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2886
- Add handler's metrics registration by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2895
- Clean-up some code and comments by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2904
- Fix cancellation signals propagation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2908
- Bump deps, add permissions monitoring by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2930
- Fix integration tests after bumping deps by @noisersup in https://github.com/FerretDB/FerretDB/pull/2934
- Update benchmark to use cursors by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2932
- Set `minWireVersion` to 0 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2937
- Test `getMore` integration test using one connection pool by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2878
- Add better metrics for connections by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2938
- Use cursors with iterator in `aggregate` command by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2929
- Implement proper response for `createIndexes` by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2936
- Re-implement `DELETE` for SQLite backend by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2907
- Validate database names for SQLite handler by @noisersup in https://github.com/FerretDB/FerretDB/pull/2924
- Add `insert` documents type validation by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2946
- Convert SQLite directory to URI by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2922
- Do not break fuzzing initialization by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2951

### New Contributors

- @Matthieu68857 made their first contribution in https://github.com/FerretDB/FerretDB/pull/2887

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/45?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.4.0...v1.5.0).

## [v1.4.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.4.0) (2023-06-19)

### New Features 🎉

- Implement `$type` aggregation operator by @noisersup in https://github.com/FerretDB/FerretDB/pull/2789
- Implement `$unset` aggregation pipeline stage by @shibasisp in https://github.com/FerretDB/FerretDB/pull/2676
- Implement simple `$addFields/$set` aggregation pipeline stages by @shibasisp in https://github.com/FerretDB/FerretDB/pull/2783
- Implement `createIndexes` for unique indexes by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2814

### Documentation 📄

- Add blog post for FerretDB v1.3.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/2791
- Add `release` tag to release blog post by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2792
- Add textlint rules for en dashes and em dashes by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2823
- Add Blog Post on Document Databases by @Fashander in https://github.com/FerretDB/FerretDB/pull/2204
- Add user documentation about unique index creation by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2856

### Other Changes 🤖

- Make `testutil.Logger` easier to use by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2790
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2798
- Refactor SQLite handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2731
- Merge test workflows to fix coverage calculation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2801
- Improve `testDistinctCompat` by @noisersup in https://github.com/FerretDB/FerretDB/pull/2782
- Use iterator in `$sum` aggregation accumulator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2799
- Bump Go to 1.20.5 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2810
- Fix free monitoring tests for MongoDB 6.0.6 by @jeremyphua in https://github.com/FerretDB/FerretDB/pull/2784
- Bump MongoDB to 6.0.6 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2727
- Bump MongoDB Go driver by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2817
- Implement `envtool tests shard` command by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2773
- Check error message in non compat integration tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2806
- Shard integration tests by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2820
- Describe current test naming conventions in the contributing guidelines by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2821
- Add tests for `find`/`getMore` `batchSize` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2825
- Add more test cases for index validation by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2752
- Fix running single test with `task` by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2832
- Refactor `getWholeParamStrict` and `GetScaleParam` functions by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2831
- Prevent tests deadlock when backend is down by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2846
- Fix `unimplemented-non-default` tag usages by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2848
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2849
- Add more tests for `$set` and `$addFields` aggregation stages by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2844
- Improve benchmarks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2833
- Handle `$type` aggregation operator errors properly by @noisersup in https://github.com/FerretDB/FerretDB/pull/2829

### New Contributors

- @shibasisp made their first contribution in https://github.com/FerretDB/FerretDB/pull/2676

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/44?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.3.0...v1.4.0).

## [v1.3.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.3.0) (2023-06-05)

### New Features 🎉

- Implement positional operator in projection by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2688
- Implement `logout` command by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2639

### Fixed Bugs 🐛

- Fix reporting of updates availability by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2653
- Fix `.deb` and `.rpm` package versions by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2725
- Allow query to be type null in `distinct` command by @b1ron in https://github.com/FerretDB/FerretDB/pull/2658
- Fix path collisions for multiple update operators by @noisersup in https://github.com/FerretDB/FerretDB/pull/2713

### Enhancements 🛠

- Fix `_id` formatting in update error messages by @noisersup in https://github.com/FerretDB/FerretDB/pull/2711

### Documentation 📄

- Add release blog post for FerretDB version 1.2.0 by @Fashander in https://github.com/FerretDB/FerretDB/pull/2686
- Update `$project` in Supported Commands by @Fashander in https://github.com/FerretDB/FerretDB/pull/2710
- Add formatter for markdown tables by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2693
- Reformat and lint more documentation files by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2740
- Document aggregation operations by @Fashander in https://github.com/FerretDB/FerretDB/pull/2672
- Improve authentication documentation by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2737

### Other Changes 🤖

- Refactor `gitBinaryMaskParam` function by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2634
- Add `distinct` command errors test by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2687
- Clarify what's left in handling OP_MSG checksum by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2677
- Return a better error for authentication problems by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2703
- Aggregation operators refactor by @noisersup in https://github.com/FerretDB/FerretDB/pull/2664
- Implement `envtool version` command by @jeremyphua in https://github.com/FerretDB/FerretDB/pull/2714
- Make `go test -list=.` work by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2718
- Include Hana in integration tests by @polyal in https://github.com/FerretDB/FerretDB/pull/2715
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2702
- Add `logout` test for all backend by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2726
- Fix telemetry reporter logging by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2707
- Add supported aggregations to the `buildInfo` output by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2716
- Add aggregation operator tests by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2724
- Add more consistency to table tests' field names by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2717
- Don't use `sjson.GetTypeOfValue` where it shouldn't be used by @noisersup in https://github.com/FerretDB/FerretDB/pull/2728
- Unify test file names by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2709
- Make `testFindAndModifyCompat` work with `compatTestCaseResultType` by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2739
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2745
- Call `ListSpecifications` driver's method in tests to check indexes by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2746
- Simplify `CountIterator` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2759
- Check for `nil` values in iterators explicitly by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2758
- Trigger GC to run finalizers by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2771
- Update `golangci-lint` config by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2772
- Remove the need to call `DeepCopy` in some places by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2774
- Clean-up `lazyerrors`, use them in more places by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2770
- Replace document slices with iterators by @noisersup in https://github.com/FerretDB/FerretDB/pull/2730
- Fix `findAndModify` tests for MongoDB 6.0.6 by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2779
- Implement a few command stubs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2777
- Add more handler tests by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2769
- Remove `findAndModify` integration tests with `$` prefixed key for MongoDB 6.0.6 compatibility by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2785

### New Contributors

- @jeremyphua made their first contribution in https://github.com/FerretDB/FerretDB/pull/2714

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/42?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.2.1...v1.3.0).

## [v1.2.1](https://github.com/FerretDB/FerretDB/releases/tag/v1.2.1) (2023-05-24)

### Fixed Bugs 🐛

- Fix reporting of updates availability by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2653

### Other Changes 🤖

- Return a better error for authentication problems by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2703

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/43?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.2.0...v1.2.1).

## [v1.2.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.2.0) (2023-05-22)

### What's Changed

This release includes a highly experimental and unsupported SQLite backend.
It will be improved in future releases.

### Fixed Bugs 🐛

- Fix compatibility with C# driver by @b1ron in https://github.com/FerretDB/FerretDB/pull/2613
- Fix a bug with unset field sorting by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2638
- Return `int64` values for `dbStats` and `collStats` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2642
- Return command error from `findAndModify` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2646
- Fix index creation on nested fields by @wqhhust in https://github.com/FerretDB/FerretDB/pull/2637

### Enhancements 🛠

- Perform `insertMany` in a single transaction by @raeidish in https://github.com/FerretDB/FerretDB/pull/2532
- Relax PostgreSQL connection checks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2602
- Cleanup `insert` command by @noisersup in https://github.com/FerretDB/FerretDB/pull/2609
- Support dot notation in projection by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2536

### Documentation 📄

- Add FerretDB v1.1.0 release blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/2594
- Update blog post image for 1.1.0 by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2601
- Add documentation for `.rpm` packages by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2604
- Fix a typo in a blog post by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2611
- Fix typo on RPM package file name by @christiano in https://github.com/FerretDB/FerretDB/pull/2628
- Update documentation formatting by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2640
- Add blog post on "Meteor and FerretDB" by @Fashander in https://github.com/FerretDB/FerretDB/pull/2654

### Other Changes 🤖

- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2592
- Remove `TODO` comment for closed issue by @adetunjii in https://github.com/FerretDB/FerretDB/pull/2573
- Add experimental integration test flag for pushdown sorting by @noisersup in https://github.com/FerretDB/FerretDB/pull/2595
- Extract handler parameters from corresponding structure by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2513
- Add `shell` subcommands (`mkdir`, `rmdir`) in `envtool` by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2596
- Add basic postcondition checker for errors by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2607
- Add `sqlite` handler stub by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2608
- Make protocol-level crashes easier to understand by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2610
- Simplify `envtool shell` subcommands by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2614
- Cleanup old Docker images by @wqhhust in https://github.com/FerretDB/FerretDB/pull/2533
- Fix exponential backoff minimum duration by @noisersup in https://github.com/FerretDB/FerretDB/pull/2578
- Fix `count`'s `query` parameter by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2622
- Add a README.md file for assertions by @b1ron in https://github.com/FerretDB/FerretDB/pull/2569
- Use `ExtractParameters` in handlers by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2620
- Verify `OP_MSG` message checksum by @adetunjii in https://github.com/FerretDB/FerretDB/pull/2540
- Separate codebase for aggregation `$project` and query `projection` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2631
- Implement `envtool shell read` subcommand by @wqhhust in https://github.com/FerretDB/FerretDB/pull/2626
- Cleanup projection by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2641
- Add common backend interface prototype by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2619
- Add SQLite handler flags by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2651
- Add tests for aggregation expressions with dots in `$group` aggregation stage by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2636
- Implement some SQLite backend commands by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2655
- Fix tests to assert correct error by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2546
- Aggregation expression refactor by @noisersup in https://github.com/FerretDB/FerretDB/pull/2644
- Move common commands to `commoncommands` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2660
- Add basic observability into backend interfaces by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2661
- Implement metadata storage by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2656
- Add `Query` to the common backend interface by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2662
- Implement query request for SQLite backend by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2665
- Add test case for read in envtools by @wqhhust in https://github.com/FerretDB/FerretDB/pull/2657
- Run integration tests for `sqlite` handler by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2666
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2671
- Create SQLite directory if needed by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2673
- Implement SQLite `update` and `delete` commands by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2674

### New Contributors

- @adetunjii made their first contribution in https://github.com/FerretDB/FerretDB/pull/2573
- @christiano made their first contribution in https://github.com/FerretDB/FerretDB/pull/2628

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/41?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.1.0...v1.2.0).

## [v1.1.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.1.0) (2023-05-09)

### New Features 🎉

- Implement projection fields assignment by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2484
- Implement `$project` pipeline aggregation stage by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2383
- Handle `create` and `drop` commands in Hana handler by @polyal in https://github.com/FerretDB/FerretDB/pull/2458
- Implement `renameCollection` command by @b1ron in https://github.com/FerretDB/FerretDB/pull/2343

### Fixed Bugs 🐛

- Fix `findAndModify` for `$exists` query operator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2385
- Fix `SchemaStats` to return correct data by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2426
- Fix `findAndModify` for `$set` operator setting `_id` by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2507
- Fix `update` for conflicting dot notation paths by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2521
- Fix `$` path errors for sort by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2534
- Fix empty projections panic by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2562
- Fix `runCommand`'s inserts of documents without `_id`s by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2574

### Enhancements 🛠

- Validate `scale` param for `dbStats` and `collStats` correctly by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2418
- Allow database name contain uppercase characters by @syasyayas in https://github.com/FerretDB/FerretDB/pull/2504
- Add identifying Arch Linux version in `hostInfo` command by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2525
- Handle absent `os-release` file by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2541
- Improve handling of `os-release` files by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2553

### Documentation 📄

- Document test script by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2353
- Use `draft` instead of `unlisted` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2372
- Make example docker compose file restart on failure by @noisersup in https://github.com/FerretDB/FerretDB/pull/2376
- Document how to get logs by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2355
- Update writing guide by @Fashander in https://github.com/FerretDB/FerretDB/pull/2373
- Add comments to our documentation workflow by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2390
- Add blogpost: Announcing FerretDB 1.0 GA - a truly Open Source MongoDB alternative by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2397
- Update documentation for index options by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2417
- Add query pushdown documentation by @noisersup in https://github.com/FerretDB/FerretDB/pull/2339
- Update README.md to link to SSPL by @cooljeanius in https://github.com/FerretDB/FerretDB/pull/2420
- Improve documentation for Docker by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2396
- Add more detailed PR guides in CONTRIBUTING.md by @AuruTus in https://github.com/FerretDB/FerretDB/pull/2435
- Remove a few double spaces by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2431
- Add image for a future blog post by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2453
- Add blogpost - Using FerretDB with Studio 3T by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2454
- Fix YAML indentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2455
- Update blog post on Using FerretDB with Studio3T by @Fashander in https://github.com/FerretDB/FerretDB/pull/2457
- Document `createIndexes`, `listIndexes`, and `dropIndexes` commands by @Fashander in https://github.com/FerretDB/FerretDB/pull/2488

### Other Changes 🤖

- Allow setting "package" variable with a testing flag by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2357
- Make it easier to use Docker-related Task targets by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2358
- Do not mark released binaries as dirty by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2371
- Make Docker Compose flags compatible by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2377
- Bump dependencies by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2367
- Fix version.txt generation for git tags by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2388
- Fix types order linter by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2391
- Cleanup deprecated errors by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2411
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2408
- Use parallel tests consistently by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2409
- Compress CI artifacts by @noisersup in https://github.com/FerretDB/FerretDB/pull/2424
- Use exponential backoff with jitter by @j0holo in https://github.com/FerretDB/FerretDB/pull/2419
- Add Mergify rules for blog posts by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2434
- Migrate to `pgx/v5` by @craigpastro in https://github.com/FerretDB/FerretDB/pull/2439
- Make it harder to misuse iterators by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2428
- Update PR template by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2441
- Rename testing flag by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2437
- Fix compilation on riscv64 by @afiskon in https://github.com/FerretDB/FerretDB/pull/2456
- Cleanup exponential backoff with jitter by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2443
- Add workaround for CockroachDB issue by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2464
- Implement blog posts previews by @noisersup in https://github.com/FerretDB/FerretDB/pull/2433
- Introduce integration benchmarks by @noisersup in https://github.com/FerretDB/FerretDB/pull/2381
- Add tests to findAndModify on `$exists` operator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2422
- Bump deps by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2479
- Refactor aggregation by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2463
- Tweak documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2452
- Fix query projection for top level fields by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2386
- Handle `envtool` panic on timeout by @syasyayas in https://github.com/FerretDB/FerretDB/pull/2499
- Enable debugging tracing of SQL queries by @craigpastro in https://github.com/FerretDB/FerretDB/pull/2467
- Update blog file names to match with slug by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2497
- Add benchmark for replacing large document by @noisersup in https://github.com/FerretDB/FerretDB/pull/2482
- Add more documentation-related items to definition of done by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2494
- Return unsupported operator error for `$` projection operator by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2512
- Use `update_available` from Beacon by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2496
- Use iterator in aggregation stages by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2480
- Increase timeout for tests by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2508
- Add `InsertMany` benchmark by @raeidish in https://github.com/FerretDB/FerretDB/pull/2518
- Add coveralls.io integration by @noisersup in https://github.com/FerretDB/FerretDB/pull/2483
- Add linter for checking blog posts by @raeidish in https://github.com/FerretDB/FerretDB/pull/2459
- Add a YAML formatter by @wqhhust in https://github.com/FerretDB/FerretDB/pull/2485
- Fix `collStats` for Tigris by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2520
- Small addition to YAML formatter usage by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2524
- Cleanup of blog post linter for slug by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2526
- Pushdown simplest sorting for `find` command by @noisersup in https://github.com/FerretDB/FerretDB/pull/2506
- Move `handlers/pg/pjson` to `handlers/sjson` by @craigpastro in https://github.com/FerretDB/FerretDB/pull/2531
- Check test database name length in compat test setup by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2527
- Document `not ready` issues label by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2544
- Remove version and name assertions in integration tests by @raeidish in https://github.com/FerretDB/FerretDB/pull/2552
- Add helpers for iterators and generators by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2542
- Do various small cleanups by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2561
- Pushdown simplest sorting for `aggregate` command by @noisersup in https://github.com/FerretDB/FerretDB/pull/2530
- Move handlers parameters to common by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2529
- Use our own Prettier Docker image by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2535
- Improve fuzzing with recorded seed data by @fenogentov in https://github.com/FerretDB/FerretDB/pull/2392
- Add proper CLI to `envtool` - `envtool setup` subcommand by @kropidlowsky in https://github.com/FerretDB/FerretDB/pull/2570
- Recover from more errors, close connection less often by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2564
- Tweak issue templates and contributing docs by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2572
- Refactor integration benchmarks by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2537
- Do panic in integration tests if connection can't be established by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2577
- Small refactoring by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2575
- Merge `no ci` label into `not ready` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2580

### New Contributors

- @cooljeanius made their first contribution in https://github.com/FerretDB/FerretDB/pull/2420
- @j0holo made their first contribution in https://github.com/FerretDB/FerretDB/pull/2419
- @AuruTus made their first contribution in https://github.com/FerretDB/FerretDB/pull/2435
- @craigpastro made their first contribution in https://github.com/FerretDB/FerretDB/pull/2439
- @afiskon made their first contribution in https://github.com/FerretDB/FerretDB/pull/2456
- @syasyayas made their first contribution in https://github.com/FerretDB/FerretDB/pull/2499
- @raeidish made their first contribution in https://github.com/FerretDB/FerretDB/pull/2518
- @polyal made their first contribution in https://github.com/FerretDB/FerretDB/pull/2458
- @wqhhust made their first contribution in https://github.com/FerretDB/FerretDB/pull/2485

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/40?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v1.0.0...v1.1.0).

## [v1.0.0](https://github.com/FerretDB/FerretDB/releases/tag/v1.0.0) (2023-04-03)

### What's Changed

We are delighted to announce the release of FerretDB 1.0 GA!

### New Features 🎉

- Support `$sum` accumulator of `$group` aggregation by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2292
- Implement `createIndexes` command by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2244
- Add basic `getMore` command by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2309
- Implement `dropIndexes` command by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2313
- Implement `$limit` aggregation pipeline stage by @noisersup in https://github.com/FerretDB/FerretDB/pull/2270
- Add partial support for `collStats`, `dbStats` and `dataSize` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2322
- Implement `$skip` aggregation pipeline stage by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2310
- Implement `$unwind` aggregation pipeline stage by @noisersup in https://github.com/FerretDB/FerretDB/pull/2294
- Support `count` and `storageStats` fields in `$collStats` aggregation pipeline stage by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2338

### Fixed Bugs 🐛

- Fix dot notation negative index errors by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2246
- Apply `skip` before `limit` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2351

### Documentation 📄

- Update supported command for `$sum` aggregation operator by @chilagrow in https://github.com/FerretDB/FerretDB/pull/2318
- Add supported shells and GUIs images by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2323
- Publish FerretDB v0.9.4 blog post by @Fashander in https://github.com/FerretDB/FerretDB/pull/2268
- Use dashes instead of underscores or spaces by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2329
- Update documentation sidebar by @Fashander in https://github.com/FerretDB/FerretDB/pull/2347
- Update FerretDB descriptions by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2281
- Improve flags documentation by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2331
- Describe supported fields for `$collStats` aggregation stage by @rumyantseva in https://github.com/FerretDB/FerretDB/pull/2352

### Other Changes 🤖

- Use iterators for `sort`, `limit`, `skip`, and `projection` by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2254
- Bump dependencies by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2307
- Improve resource tracking by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2319
- Add tests for `find`'s and `count`'s `skip` argument by @w84thesun in https://github.com/FerretDB/FerretDB/pull/2325
- Close iterator properly by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2333
- Improve large numbers initialization in test data by @noisersup in https://github.com/FerretDB/FerretDB/pull/2324
- Ignore `unique` index option for now by @AlekSi in https://github.com/FerretDB/FerretDB/pull/2350

[All closed issues and pull requests](https://github.com/FerretDB/FerretDB/milestone/39?closed=1).
[All commits](https://github.com/FerretDB/FerretDB/compare/v0.9.4...v1.0.0).

## Older Releases

See https://github.com/FerretDB/FerretDB/blob/v1.0.0/CHANGELOG.md.
