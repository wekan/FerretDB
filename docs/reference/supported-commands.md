---
sidebar_position: 1
description: This is a list of all supported commands in FerretDB
---

# Supported commands

<!--
Use ⚠️ for commands and arguments that are implemented with major limitations,
or (safely) ignored.
Use ❌ for commands and arguments that are not implemented at all.
-->

:::note This build (FerretDB v1, SQLite)

This is the FerretDB v1 (SQLite) fork. On top of upstream FerretDB v1.24.2 it adds,
among other things: a single-node replica-set handshake with automatic oplog
creation (`replSetInitiate`, `replSetGetStatus`, `replSetGetConfig`, replica-set
`hello`/`isMaster` fields) so MongoDB-wire clients can tail changes instead of
polling; the aggregation operators `$eq`, `$ne`, `$or`, `$ifNull`,
`$anyElementTrue`, `$objectToArray`, `$map` and the equality-join `$lookup` stage;
and the `throttle` self-throttling extension command (see the end of this page).
The authoritative, dated list is in the CHANGELOG; the ROADMAP has the full
comparison with upstream. FerretDB v1 (SQLite) is a general-purpose MongoDB 7
replacement, comparable to FerretDB v2 (PostgreSQL).

:::

## Query commands

| Command         | Argument                   | Status | Comments                                                  |
| --------------- | -------------------------- | ------ | --------------------------------------------------------- |
| `delete`        |                            | ✅     | Basic command is fully supported                          |
|                 | `deletes`                  | ✅     |                                                           |
|                 | `comment`                  | ⚠️     |                                                           |
|                 | `let`                      | ⚠️     | Unimplemented                                             |
|                 | `ordered`                  | ✅     |                                                           |
|                 | `writeConcern`             | ⚠️     | Ignored                                                   |
|                 | `q`                        | ✅     |                                                           |
|                 | `limit`                    | ✅     |                                                           |
|                 | `collation`                | ❌     | Unimplemented                                             |
|                 | `hint`                     | ⚠️     | Ignored                                                   |
| `find`          |                            | ✅     | Basic command is fully supported                          |
|                 | `filter`                   | ✅     |                                                           |
|                 | `sort`                     | ✅     |                                                           |
|                 | `projection`               | ✅     | Basic projections with fields are supported               |
|                 | `hint`                     | ⚠️     | Ignored                                                   |
|                 | `skip`                     | ⚠️     |                                                           |
|                 | `limit`                    | ✅     |                                                           |
|                 | `batchSize`                | ✅     |                                                           |
|                 | `singleBatch`              | ✅     |                                                           |
|                 | `comment`                  | ⚠️     |                                                           |
|                 | `maxTimeMS`                | ✅     |                                                           |
|                 | `readConcern`              | ⚠️     | Ignored                                                   |
|                 | `max`                      | ⚠️     | Ignored                                                   |
|                 | `min`                      | ⚠️     | Ignored                                                   |
|                 | `returnKey`                | ❌     | Unimplemented                                             |
|                 | `showRecordId`             | ✅     |                                                           |
|                 | `tailable`                 | ✅     |                                                           |
|                 | `oplogReplay`              | ⚠️     | Ignored                                                   |
|                 | `noCursorTimeout`          | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/4035) |
|                 | `awaitData`                | ✅     |                                                           |
|                 | `allowPartialResults`      | ❌     | Unimplemented                                             |
|                 | `collation`                | ❌     | Unimplemented                                             |
|                 | `allowDiskUse`             | ⚠️     | Ignored                                                   |
|                 | `let`                      | ❌     | Unimplemented                                             |
| `findAndModify` |                            | ✅     | Basic command is fully supported                          |
|                 | `query`                    | ✅     |                                                           |
|                 | `sort`                     | ✅     |                                                           |
|                 | `remove`                   | ✅     |                                                           |
|                 | `update`                   | ✅     |                                                           |
|                 | `new`                      | ✅     |                                                           |
|                 | `upsert`                   | ✅     |                                                           |
|                 | `bypassDocumentValidation` | ⚠️     | Ignored                                                   |
|                 | `writeConcern`             | ⚠️     | Ignored                                                   |
|                 | `maxTimeMS`                | ✅     |                                                           |
|                 | `collation`                | ❌     | Unimplemented                                             |
|                 | `arrayFilters`             | ❌     | Unimplemented                                             |
|                 | `hint`                     | ⚠️     | Ignored                                                   |
|                 | `comment`                  | ⚠️     |                                                           |
|                 | `let`                      | ⚠️     | Unimplemented                                             |
| `getMore`       |                            | ✅     | Basic command is fully supported                          |
|                 | `batchSize`                | ✅     |                                                           |
|                 | `maxTimeMS`                | ✅     |                                                           |
|                 | `comment`                  | ⚠️     | Unimplemented                                             |
| `insert`        |                            | ✅     | Basic command is fully supported                          |
|                 | `documents`                | ✅     |                                                           |
|                 | `ordered`                  | ✅     |                                                           |
|                 | `bypassDocumentValidation` | ⚠️     | Ignored                                                   |
|                 | `comment`                  | ⚠️     | Ignored                                                   |
| `update`        |                            | ✅     | Basic command is fully supported                          |
|                 | `updates`                  | ✅     |                                                           |
|                 | `ordered`                  | ⚠️     | Ignored                                                   |
|                 | `writeConcern`             | ⚠️     | Ignored                                                   |
|                 | `bypassDocumentValidation` | ⚠️     | Ignored                                                   |
|                 | `comment`                  | ⚠️     |                                                           |
|                 | `let`                      | ⚠️     | Unimplemented                                             |
|                 | `q`                        | ✅     |                                                           |
|                 | `u`                        | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/2742) |
|                 | `c`                        | ⚠️     | Unimplemented                                             |
|                 | `upsert`                   | ✅     |                                                           |
|                 | `multi`                    | ✅     |                                                           |
|                 | `collation`                | ❌     | Unimplemented                                             |
|                 | `arrayFilters`             | ⚠️     | Unimplemented                                             |
|                 | `hint`                     | ⚠️     | Ignored                                                   |

### Update Operators

The following operators and modifiers are available in the `update` and `findAndModify` commands.

| Operator          | Modifier    | Status | Comments                                                 |
| ----------------- | ----------- | ------ | -------------------------------------------------------- |
| `$currentDate`    |             | ✅     |                                                          |
| `$inc`            |             | ✅     |                                                          |
| `$min`            |             | ✅     |                                                          |
| `$max`            |             | ✅     |                                                          |
| `$mul`            |             | ✅     |                                                          |
| `$rename`         |             | ✅     |                                                          |
| `$set`            |             | ✅     |                                                          |
| `$setOnInsert`    |             | ✅     |                                                          |
| `$unset`          |             | ✅     |                                                          |
| `$`               |             | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/822) |
| `$[]`             |             | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/823) |
| `$[<identifier>]` |             | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/824) |
| `$addToSet`       |             | ✅️    |                                                          |
| `$pop`            |             | ✅     |                                                          |
| `$pull`           |             | ✅     |                                                          |
| `$push`           |             | ✅️    |                                                          |
| `$pullAll`        |             | ✅️    |                                                          |
|                   | `$each`     | ✅️    |                                                          |
|                   | `$position` | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/829) |
|                   | `$slice`    | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/830) |
|                   | `$sort`     | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/831) |
|                   | `$bit`      | ✅️    |                                                          |

### Projection Operators

The following operators are available in the `find` command `projection` argument.

| Operator     | Status | Comments                                                  |
| ------------ | ------ | --------------------------------------------------------- |
| `$`          | ✅️    |                                                           |
| `$elemMatch` | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1710) |
| `$meta`      | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1712) |
| `$slice`     | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1711) |

## Query Plan Cache Commands

| Command                 | Argument     | Status | Comments                                                  |
| ----------------------- | ------------ | ------ | --------------------------------------------------------- |
| `planCacheClear`        |              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1502) |
|                         | `query`      | ⚠️     |                                                           |
|                         | `projection` | ⚠️     |                                                           |
|                         | `sort`       | ⚠️     |                                                           |
|                         | `comment`    | ⚠️     |                                                           |
| `planCacheClearFilters` |              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1503) |
|                         | `query`      | ⚠️     |                                                           |
|                         | `sort`       | ⚠️     |                                                           |
|                         | `projection` | ⚠️     |                                                           |
|                         | `collation`  | ❌     | Unimplemented                                             |
|                         | `comment`    | ⚠️     |                                                           |
| `planCacheListFilters`  |              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1504) |
|                         | `comment`    | ⚠️     |                                                           |
| `planCacheSetFilter`    |              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1505) |
|                         | `query`      | ⚠️     |                                                           |
|                         | `sort`       | ⚠️     |                                                           |
|                         | `projection` | ⚠️     |                                                           |
|                         | `collation`  | ❌     | Unimplemented                                             |
|                         | `indexes`    | ⚠️     |                                                           |
|                         | `comment`    | ⚠️     |                                                           |

## Free Monitoring Commands

| Command                   | Argument            | Status | Comments                                                   |
| ------------------------- | ------------------- | ------ | ---------------------------------------------------------- |
| `setFreeMonitoring`       |                     | ✅     | [Telemetry reporting](../telemetry.md)                     |
|                           | `action: "enable"`  | ✅     | [`--telemetry=enable`](../telemetry.md#enable-telemetry)   |
|                           | `action: "disable"` | ✅     | [`--telemetry=disable`](../telemetry.md#disable-telemetry) |
| `getFreeMonitoringStatus` |                     | ✅     |                                                            |

## Database Operations

### User Management Commands

| Command                    | Argument                         | Status | Comments                                                  |
| -------------------------- | -------------------------------- | ------ | --------------------------------------------------------- |
| `createUser`               |                                  | ✅     |                                                           |
|                            | `pwd`                            | ⚠️     |                                                           |
|                            | `customData`                     | ⚠️     |                                                           |
|                            | `roles`                          | ⚠️     |                                                           |
|                            | `digestPassword`                 | ⚠️     |                                                           |
|                            | `writeConcern`                   | ⚠️     |                                                           |
|                            | `authenticationRestrictions`     | ⚠️     |                                                           |
|                            | `mechanisms`                     | ⚠️     |                                                           |
|                            | `digestPassword`                 | ⚠️     |                                                           |
|                            | `comment`                        | ⚠️     |                                                           |
| `dropAllUsersFromDatabase` |                                  | ✅     |                                                           |
|                            | `writeConcern`                   | ⚠️     |                                                           |
|                            | `comment`                        | ⚠️     |                                                           |
| `dropUser`                 |                                  | ✅     |                                                           |
|                            | `writeConcern`                   | ⚠️     |                                                           |
|                            | `comment`                        | ⚠️     |                                                           |
| `grantRolesToUser`         |                                  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1494) |
|                            | `writeConcern`                   | ⚠️     |                                                           |
|                            | `comment`                        | ⚠️     |                                                           |
| `revokeRolesFromUser`      |                                  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1495) |
|                            | `roles`                          | ⚠️     |                                                           |
|                            | `writeConcern`                   | ⚠️     |                                                           |
|                            | `comment`                        | ⚠️     |                                                           |
| `updateUser`               |                                  | ✅     |                                                           |
|                            | `pwd`                            |        |                                                           |
|                            | `customData`                     |        |                                                           |
|                            | `roles`                          |        |                                                           |
|                            | `digestPassword`                 |        |                                                           |
|                            | `writeConcern`                   |        |                                                           |
|                            | `authenticationRestrictions`     |        |                                                           |
|                            | `mechanisms`                     |        |                                                           |
|                            | `digestPassword`                 |        |                                                           |
|                            | `comment`                        |        |                                                           |
| `usersInfo`                |                                  | ✅     |                                                           |
|                            | `showCredentials`                | ✅     |                                                           |
|                            | `showCustomData`                 | ⚠️     |                                                           |
|                            | `showPrivileges`                 | ⚠️     |                                                           |
|                            | `showAuthenticationRestrictions` | ⚠️     |                                                           |
|                            | `filter`                         | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/4141) |
|                            | `comment`                        | ⚠️     |                                                           |

### Authentication Commands

| Command        | Argument | Status | Comments                                                  |
| -------------- | -------- | ------ | --------------------------------------------------------- |
| `authenticate` |          | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1731) |
| `getnonce`     |          | ❌     | Deprecated                                                |
| `logout`       |          | ✅     |                                                           |
| `saslStart`    |          | ✅     |                                                           |

### Role Management Commands

| Command                    | Argument                     | Status | Comments                                                  |
| -------------------------- | ---------------------------- | ------ | --------------------------------------------------------- |
| `createRole`               |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1528) |
|                            | `privileges`                 | ⚠️     |                                                           |
|                            | `roles`                      | ⚠️     |                                                           |
|                            | `authenticationRestrictions` | ⚠️     |                                                           |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `dropRole`                 |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1529) |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `dropAllRolesFromDatabase` |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1530) |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `grantPrivilegesToRole`    |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1531) |
|                            | `privileges`                 | ⚠️     |                                                           |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `grantRolesToRole`         |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1532) |
|                            | `roles`                      | ⚠️     |                                                           |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `invalidateUserCache`      |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1533) |
| `revokePrivilegesFromRole` |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1534) |
|                            | `privileges`                 | ⚠️     |                                                           |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `revokeRolesFromRole`      |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1535) |
|                            | `roles`                      | ⚠️     |                                                           |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `rolesInfo`                |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1536) |
|                            | `showPrivileges`             | ⚠️     |                                                           |
|                            | `showBuiltinRoles`           | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |
| `updateRole`               |                              | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1537) |
|                            | `privileges`                 | ⚠️     |                                                           |
|                            | `roles`                      | ⚠️     |                                                           |
|                            | `authenticationRestrictions` | ⚠️     |                                                           |
|                            | `writeConcern`               | ⚠️     |                                                           |
|                            | `comment`                    | ⚠️     |                                                           |

### Replication Commands

FerretDB v1 (SQLite) presents itself as a single-node, always-primary replica set
of one — enough for a MongoDB-wire client's oplog tailing (e.g. Meteor's OpLog
mode), not real multi-node replication or elections. See the CHANGELOG for details.

| Command             | Argument | Status | Comments                                                     |
| ------------------- | -------- | ------ | ------------------------------------------------------------ |
| `replSetInitiate`   |          | ✅     | Single-node RS; auto-creates the capped `local.oplog.rs`     |
| `replSetGetStatus`  |          | ✅     | Single-member primary status for `rs.status()`               |
| `replSetGetConfig`  |          | ✅     | Single-member config for `rs.conf()`                         |

## Session Commands

Related [issue](https://github.com/FerretDB/FerretDB/issues/8).

Related [issue](https://github.com/FerretDB/FerretDB/issues/153).

| Command                    | Argument       | Status | Comments                                                  |
| -------------------------- | -------------- | ------ | --------------------------------------------------------- |
| `abortTransaction`         |                | ⚠️     | Compatibility no-op (accepted, ignored) |
|                            | `txnNumber`    | ⚠️     |                                                           |
|                            | `writeConcern` | ⚠️     |                                                           |
|                            | `autocommit`   | ⚠️     |                                                           |
|                            | `comment`      | ⚠️     |                                                           |
| `commitTransaction`        |                | ⚠️     | Compatibility no-op (accepted, ignored) |
|                            | `txnNumber`    | ⚠️     |                                                           |
|                            | `writeConcern` | ⚠️     |                                                           |
|                            | `autocommit`   | ⚠️     |                                                           |
|                            | `comment`      | ⚠️     |                                                           |
| `endSessions`              |                | ⚠️     | Compatibility no-op (accepted, ignored) |
| `killAllSessions`          |                | ⚠️     | Compatibility no-op (accepted, ignored) |
| `killAllSessionsByPattern` |                | ⚠️     | Compatibility no-op (accepted, ignored) |
| `killSessions`             |                | ⚠️     | Compatibility no-op (accepted, ignored) |
| `refreshSessions`          |                | ⚠️     | Compatibility no-op (accepted, ignored) |
| `startSession`             |                | ⚠️     | Compatibility no-op (accepted, ignored) |

## Aggregation pipelines

Related [issue](https://github.com/FerretDB/FerretDB/issues/1917).

| Command     | Argument | Status | Comments |
| ----------- | -------- | ------ | -------- |
| `aggregate` |          | ✅️    |          |
| `count`     |          | ✅     |          |
| `distinct`  |          | ✅     |          |

### Aggregation pipeline stages

| Stage                | Status | Comments                                                  |
| -------------------- | ------ | --------------------------------------------------------- |
| `$addFields`         | ✅     | Supported |
| `$bucket`            | ✅     | Supported |
| `$bucketAuto`        | ✅     | Supported |
| `$changeStream`      | ❌     | Not implemented |
| `$changeStream`      | ❌     | Not implemented |
| `$collStats`         | ✅     | Supported |
| `$count`             | ✅    | Supported |
| `$currentOp`         | ❌     | Not implemented |
| `$densify`           | ❌     | Not implemented |
| `$documents`         | ❌     | Not implemented |
| `$documents`         | ❌     | Not implemented |
| `$facet`             | ✅     | Supported |
| `$fill`              | ❌     | Not implemented |
| `$geoNear`           | ❌     | Not implemented |
| `$graphLookup`       | ❌     | Not implemented |
| `$group`             | ⚠️    | Supported; accumulators limited to `$sum` and `$count` |
| `$indexStats`        | ❌     | Not implemented |
| `$limit`             | ✅    | Supported |
| `$listLocalSessions` | ❌     | Not implemented |
| `$listSessions`      | ❌     | Not implemented |
| `$lookup`            | ✅     | Equality join (from/localField/foreignField/as); pipeline form unsupported |
| `$match`             | ✅     | Supported |
| `$merge`             | ❌     | Not implemented |
| `$out`               | ❌     | Not implemented |
| `$planCacheStats`    | ❌     | Not implemented |
| `$project`           | ✅     | Supported |
| `$redact`            | ❌     | Not implemented |
| `$replaceRoot`       | ✅     | Supported |
| `$replaceWith`       | ✅     | Supported |
| `$sample`            | ✅     | Supported |
| `$search`            | ❌     | Not implemented |
| `$searchMeta`        | ❌     | Not implemented |
| `$set`               | ✅     | Supported |
| `$setWindowFields`   | ✅     | Supported |
| `$skip`              | ✅    | Supported |
| `$sort`              | ✅    | Supported |
| `$sortByCount`       | ✅     | Supported |
| `$unionWith`         | ✅     | Supported |
| `$unset`             | ✅    | Supported |
| `$unwind`            | ✅    | Supported |

### Aggregation pipeline operators

| Operator                  | Status | Comments                                                  |
| ------------------------- | ------ | --------------------------------------------------------- |
| `$abs`                    | ✅     | Supported |
| `$accumulator`            | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$acos`                   | ✅     | Supported |
| `$acosh`                  | ✅     | Supported |
| `$add` (arithmetic)       | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1453) |
| `$add` (date)             | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1460) |
| `$addToSet`               | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1468) |
| `$allElementsTrue`        | ✅     | Supported |
| `$and`                    | ✅     | Supported |
| `$anyElementTrue`         | ✅     | Added in FerretDB v1 |
| `$arrayElemAt`            | ✅     | Supported |
| `$arrayToObject`          | ✅     | Supported |
| `$asin`                   | ✅     | Supported |
| `$asinh`                  | ✅     | Supported |
| `$atan`                   | ✅     | Supported |
| `$atan2`                  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1465) |
| `$atanh`                  | ✅     | Supported |
| `$avg`                    | ✅     | Supported |
| `$binarySize`             | ✅     | Supported |
| `$bottom`                 | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$bottomN`                | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$bsonSize`               | ✅     | Supported |
| `$ceil`                   | ✅     | Supported |
| `$cmp`                    | ✅     | Supported |
| `$concat`                 | ✅     | Supported |
| `$concatArrays`           | ✅     | Supported |
| `$cond`                   | ✅     | Supported |
| `$convert`                | ✅     | Supported |
| `$cos`                    | ✅     | Supported |
| `$cosh`                   | ✅     | Supported |
| `$count`                  | ✅    | Supported |
| `$covariancePop`          | ❌     | Not implemented |
| `$covarianceSamp`         | ❌     | Not implemented |
| `$dateAdd`                | ✅     | Supported |
| `$dateDiff`               | ✅     | Supported |
| `$dateFromParts`          | ✅     | Supported |
| `$dateFromString`         | ✅     | Supported |
| `$dateSubtract`           | ✅     | Supported |
| `$dateToParts`            | ✅     | Supported |
| `$dateToString`           | ✅     | Supported |
| `$dateTrunc`              | ✅     | Supported |
| `$dayOfMonth`             | ✅     | Supported |
| `$dayOfWeek`              | ✅     | Supported |
| `$dayOfYear`              | ✅     | Supported |
| `$degreesToRadians`       | ✅     | Supported |
| `$denseRank`              | ❌     | Not implemented |
| `$derivative`             | ❌     | Not implemented |
| `$divide`                 | ✅     | Supported |
| `$documentNumber`         | ❌     | Not implemented |
| `$eq`                     | ✅     | Added in FerretDB v1 |
| `$exp`                    | ✅     | Supported |
| `$expMovingAvg`           | ❌     | Not implemented |
| `$filter`                 | ✅     | Supported |
| `$first` (accumulator)    | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$first` (array operator) | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1454) |
| `$firstN`                 | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$floor`                  | ✅     | Supported |
| `$function`               | ✅     | Supported |
| `$getField`               | ✅     | Supported |
| `$gt`                     | ✅     | Supported |
| `$gte`                    | ✅     | Supported |
| `$hour`                   | ✅     | Supported |
| `$ifNull`                 | ✅     | Added in FerretDB v1 |
| `$in`                     | ✅     | Supported |
| `$indexOfArray`           | ✅     | Supported |
| `$indexOfBytes`           | ✅     | Supported |
| `$indexOfCP`              | ✅     | Supported |
| `$integral`               | ❌     | Not implemented |
| `$isArray`                | ✅     | Supported |
| `$isNumber`               | ✅     | Supported |
| `$isoDayOfWeek`           | ✅     | Supported |
| `$isoWeek`                | ✅     | Supported |
| `$isoWeekYear`            | ✅     | Supported |
| `$last` (accumulator)     | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$last` (array operator)  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1454) |
| `$lastN`                  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$let`                    | ✅     | Supported |
| `$linearFill`             | ❌     | Not implemented |
| `$literal`                | ✅     | Supported |
| `$ln`                     | ✅     | Supported |
| `$locf`                   | ❌     | Not implemented |
| `$log`                    | ✅     | Supported |
| `$log10`                  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1453) |
| `$lt`                     | ✅     | Supported |
| `$lte`                    | ✅     | Supported |
| `$ltrim`                  | ✅     | Supported |
| `$map`                    | ✅     | Added in FerretDB v1 |
| `$max`                    | ✅     | Supported |
| `$maxN`                   | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$mergeObjects`           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$meta`                   | ❌     | Not implemented |
| `$millisecond`            | ✅     | Supported |
| `$min`                    | ✅     | Supported |
| `$minN`                   | ❌     | Not implemented |
| `$minute`                 | ✅     | Supported |
| `$mod`                    | ✅     | Supported |
| `$month`                  | ✅     | Supported |
| `$multiply`               | ✅     | Supported |
| `$ne`                     | ✅     | Added in FerretDB v1 |
| `$not`                    | ✅     | Supported |
| `$objectToArray`          | ✅     | Added in FerretDB v1 |
| `$or`                     | ✅     | Added in FerretDB v1 |
| `$pow`                    | ✅     | Supported |
| `$push`                   | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$radiansToDegrees`       | ✅     | Supported |
| `$rand`                   | ✅     | Supported |
| `$range`                  | ✅     | Supported |
| `$rank`                   | ❌     | Not implemented |
| `$reduce`                 | ✅     | Supported |
| `$regexFind`              | ❌     | Not implemented |
| `$regexFindAll`           | ❌     | Not implemented |
| `$regexMatch`             | ✅     | Supported |
| `$replaceAll`             | ✅     | Supported |
| `$replaceOne`             | ✅     | Supported |
| `$reverseArray`           | ✅     | Supported |
| `$round`                  | ✅     | Supported |
| `$rtrim`                  | ✅     | Supported |
| `$sampleRate`             | ❌     | Not implemented |
| `$second`                 | ✅     | Supported |
| `$setDifference`          | ✅     | Supported |
| `$setEquals`              | ✅     | Supported |
| `$setField`               | ✅     | Supported |
| `$setIntersection`        | ✅     | Supported |
| `$setIsSubset`            | ✅     | Supported |
| `$setUnion`               | ✅     | Supported |
| `$shift`                  | ❌     | Not implemented |
| `$sin`                    | ✅     | Supported |
| `$sinh`                   | ✅     | Supported |
| `$size`                   | ✅     | Supported |
| `$slice`                  | ✅     | Supported |
| `$sortArray`              | ✅     | Supported |
| `$split`                  | ✅     | Supported |
| `$sqrt`                   | ✅     | Supported |
| `$stdDevPop`              | ❌     | Not implemented |
| `$stdDevSamp`             | ❌     | Not implemented |
| `$strcasecmp`             | ✅     | Supported |
| `$strLenBytes`            | ✅     | Supported |
| `$strLenCP`               | ✅     | Supported |
| `$substr`                 | ✅     | Supported |
| `$substrBytes`            | ✅     | Supported |
| `$substrCP`               | ✅     | Supported |
| `$subtract` (arithmetic)  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1453) |
| `$subtract` (date)        | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1460) |
| `$sum` (accumulator)      | ✅️    |                                                           |
| `$sum` (operator)         | ✅️    |                                                           |
| `$switch`                 | ✅     | Supported |
| `$tan`                    | ✅     | Supported |
| `$tanh`                   | ✅     | Supported |
| `$toBool`                 | ✅     | Supported |
| `$toDate`                 | ✅     | Supported |
| `$toDecimal`              | ❌     | Not implemented |
| `$toDouble`               | ✅     | Supported |
| `$toInt`                  | ✅     | Supported |
| `$toLong`                 | ✅     | Supported |
| `$toLower`                | ✅     | Supported |
| `$toObjectId`             | ✅     | Supported |
| `$top`                    | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$topN`                   | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1467) |
| `$toString`               | ✅     | Supported |
| `$toUpper`                | ✅     | Supported |
| `$trim`                   | ✅     | Supported |
| `$trunc`                  | ✅     | Supported |
| `$tsIncrement`            | ❌     | Not implemented |
| `$tsSecond`               | ❌     | Not implemented |
| `$type`                   | ✅     | Supported |
| `$unsetField`             | ✅     | Supported |
| `$week`                   | ✅     | Supported |
| `$year`                   | ✅     | Supported |
| `$zip`                    | ✅     | Supported |

## Administration commands

| Command                           | Argument / Option              | Property                  | Status | Comments                                                  |
| --------------------------------- | ------------------------------ | ------------------------- | ------ | --------------------------------------------------------- |
| `cloneCollectionAsCapped`         |                                |                           | ❌     |                                                           |
|                                   | `toCollection`                 |                           | ⚠️     |                                                           |
|                                   | `size`                         |                           | ⚠️     |                                                           |
|                                   | `writeConcern`                 |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `collMod`                         |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1510) |
|                                   | `index`                        |                           | ⚠️     |                                                           |
|                                   |                                | `keyPattern`              | ⚠️     |                                                           |
|                                   |                                | `name`                    | ⚠️     |                                                           |
|                                   |                                | `expireAfterSeconds`      | ⚠️     |                                                           |
|                                   |                                | `hidden`                  | ⚠️     |                                                           |
|                                   |                                | `prepareUnique`           | ⚠️     |                                                           |
|                                   |                                | `unique`                  | ⚠️     |                                                           |
|                                   | `validator`                    |                           | ⚠️     |                                                           |
|                                   |                                | `validationLevel`         | ⚠️     |                                                           |
|                                   |                                | `validationAction`        | ⚠️     |                                                           |
|                                   | `viewOn` (Views)               |                           | ⚠️     |                                                           |
|                                   | `pipeline` (Views)             |                           | ⚠️     |                                                           |
|                                   | `cappedSize`                   |                           | ⚠️     |                                                           |
|                                   | `cappedMax`                    |                           | ⚠️     |                                                           |
|                                   | `changeStreamPreAndPostImages` |                           | ⚠️     |                                                           |
| `compact`                         |                                |                           | ✅     |                                                           |
|                                   | `force`                        |                           | ✅     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `compactStructuredEncryptionData` |                                |                           | ❌     |                                                           |
|                                   | `compactionTokens`             |                           | ⚠️     |                                                           |
| `convertToCapped`                 |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/3457) |
|                                   | `size`                         |                           | ⚠️     |                                                           |
|                                   | `writeConcern`                 |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `create`                          |                                |                           | ✅     |                                                           |
|                                   | `capped`                       |                           | ✅️    |                                                           |
|                                   | `timeseries`                   |                           | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/177)  |
|                                   |                                | `timeField`               | ⚠️     |                                                           |
|                                   |                                | `metaField`               | ⚠️     |                                                           |
|                                   |                                | `granularity`             | ⚠️     |                                                           |
|                                   | `expireAfterSeconds`           |                           | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/2415) |
|                                   | `clusteredIndex`               |                           | ⚠️     |                                                           |
|                                   | `changeStreamPreAndPostImages` |                           | ⚠️     |                                                           |
|                                   | `autoIndexId`                  |                           | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/3922) |
|                                   | `size`                         |                           | ✅️    |                                                           |
|                                   | `max`                          |                           | ✅     |                                                           |
|                                   | `storageEngine`                |                           | ⚠️     | Ignored                                                   |
|                                   | `validator`                    |                           | ⚠️     | Not implemented in PostgreSQL                             |
|                                   | `validationLevel`              |                           | ⚠️     | Unimplemented                                             |
|                                   | `validationAction`             |                           | ⚠️     | Unimplemented                                             |
|                                   | `indexOptionDefaults`          |                           | ⚠️     | Ignored                                                   |
|                                   | `viewOn`                       |                           | ⚠️     | Unimplemented                                             |
|                                   | `pipeline`                     |                           | ⚠️     | Unimplemented                                             |
|                                   | `collation`                    |                           | ❌     | Unimplemented                                             |
|                                   | `writeConcern`                 |                           | ⚠️     | Ignored                                                   |
|                                   | `encryptedFields`              |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `createIndexes`                   |                                |                           | ✅     |                                                           |
|                                   | `indexes`                      |                           | ✅     |                                                           |
|                                   |                                | `key`                     | ✅     |                                                           |
|                                   |                                | `name`                    | ✅️    |                                                           |
|                                   |                                | `unique`                  | ✅     |                                                           |
|                                   |                                | `partialFilterExpression` | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/2448) |
|                                   |                                | `sparse`                  | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/2448) |
|                                   |                                | `expireAfterSeconds`      | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/2415) |
|                                   |                                | `hidden`                  | ❌     | Unimplemented                                             |
|                                   |                                | `storageEngine`           | ❌     | Unimplemented                                             |
|                                   |                                | `weights`                 | ❌     | Unimplemented                                             |
|                                   |                                | `default_language`        | ❌     | Unimplemented                                             |
|                                   |                                | `language_override`       | ❌     | Unimplemented                                             |
|                                   |                                | `textIndexVersion`        | ❌     | Unimplemented                                             |
|                                   |                                | `2dsphereIndexVersion`    | ❌     | Unimplemented                                             |
|                                   |                                | `bits`                    | ❌     | Unimplemented                                             |
|                                   |                                | `min`                     | ❌     | Unimplemented                                             |
|                                   |                                | `max`                     | ❌     | Unimplemented                                             |
|                                   |                                | `bucketSize`              | ❌     | Unimplemented                                             |
|                                   |                                | `collation`               | ❌     | Unimplemented                                             |
|                                   |                                | `wildcardProjection`      | ❌     | Unimplemented                                             |
|                                   | `writeConcern`                 |                           | ⚠️     |                                                           |
|                                   | `commitQuorum`                 |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `currentOp`                       |                                |                           | ✅     | Basic command supported (empty `inprog`)                  |
|                                   | `$ownOps`                      |                           | ⚠️     |                                                           |
|                                   | `$all`                         |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `drop`                            |                                |                           | ✅     |                                                           |
|                                   | `writeConcern`                 |                           | ⚠️     | Ignored                                                   |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `dropDatabase`                    |                                |                           | ✅     |                                                           |
|                                   | `writeConcern`                 |                           | ⚠️     | Ignored                                                   |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `dropConnections`                 |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1511) |
|                                   | `hostAndPort`                  |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `dropIndexes`                     |                                |                           | ✅     |                                                           |
|                                   | `index`                        |                           | ✅     |                                                           |
|                                   | `writeConcern`                 |                           | ⚠️     | Ignored                                                   |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `filemd5`                         |                                |                           | ❌     |                                                           |
| `fsync`                           |                                |                           | ❌     |                                                           |
| `fsyncUnlock`                     |                                |                           | ❌     |                                                           |
|                                   | `lock`                         |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `getDefaultRWConcern`             |                                |                           | ❌     |                                                           |
|                                   | `inMemory`                     |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `getClusterParameter`             |                                |                           | ❌     |                                                           |
| `getParameter`                    |                                |                           | ❌     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `killCursors`                     |                                |                           | ✅     |                                                           |
|                                   | `cursors`                      |                           | ✅     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `killOp`                          |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1515) |
|                                   | `op`                           |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `listCollections`                 |                                |                           | ✅     |                                                           |
|                                   | `filter`                       |                           | ✅     |                                                           |
|                                   | `nameOnly`                     |                           | ✅     |                                                           |
|                                   | `authorizedCollections`        |                           | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/3770) |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `listDatabases`                   |                                |                           | ✅     |                                                           |
|                                   | `filter`                       |                           | ✅     |                                                           |
|                                   | `nameOnly`                     |                           | ✅     |                                                           |
|                                   | `authorizedDatabases`          |                           | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/3769) |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `listIndexes`                     |                                |                           | ✅     |                                                           |
|                                   | `cursor.batchSize`             |                           | ⚠️     | Ignored                                                   |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `logRotate`                       |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1959) |
|                                   | `<target>`                     |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `reIndex`                         |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1516) |
| `renameCollection`                |                                |                           | ✅     |                                                           |
|                                   | `to`                           |                           | ✅     | [Issue](https://github.com/FerretDB/FerretDB/issues/2563) |
|                                   | `dropTarget`                   |                           | ⚠️     | [Issue](https://github.com/FerretDB/FerretDB/issues/2565) |
|                                   | `writeConcern`                 |                           | ⚠️     | Ignored                                                   |
|                                   | `comment`                      |                           | ⚠️     | Ignored                                                   |
| `rotateCertificates`              |                                |                           | ❌     |                                                           |
| `setFeatureCompatibilityVersion`  |                                |                           | ❌     |                                                           |
| `setIndexCommitQuorum`            |                                |                           | ❌     |                                                           |
|                                   | `setIndexCommitQuorum`         |                           | ⚠️     |                                                           |
|                                   | `indexNames`                   |                           | ⚠️     |                                                           |
|                                   | `commitQuorum`                 |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `setParameter`                    |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1518) |
| `setDefaultRWConcern`             |                                |                           | ❌     |                                                           |
|                                   | `defaultReadConcern`           |                           | ⚠️     |                                                           |
|                                   | `defaultWriteConcern`          |                           | ⚠️     |                                                           |
|                                   | `writeConcern`                 |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |
| `shutdown`                        |                                |                           | ❌     | [Issue](https://github.com/FerretDB/FerretDB/issues/1519) |
|                                   | `force`                        |                           | ⚠️     |                                                           |
|                                   | `timeoutSecs`                  |                           | ⚠️     |                                                           |
|                                   | `comment`                      |                           | ⚠️     |                                                           |

## Diagnostic commands

| Command              | Argument               | Status | Comments                         |
| -------------------- | ---------------------- | ------ | -------------------------------- |
| `buildInfo`          |                        | ✅     | Basic command is fully supported |
| `collStats`          |                        | ✅     | Basic command is fully supported |
|                      | `collStats`            | ✅     |                                  |
|                      | `scale`                | ✅     |                                  |
| `connPoolStats`      |                        | ❌     | Unimplemented                    |
| `connectionStatus`   |                        | ✅     | Basic command is fully supported |
|                      | `showPrivileges`       | ✅     |                                  |
| `dataSize`           |                        | ✅     | Basic command is fully supported |
|                      | `keyPattern`           | ⚠️     | Unimplemented                    |
|                      | `min`                  | ⚠️     | Unimplemented                    |
|                      | `max`                  | ⚠️     | Unimplemented                    |
|                      | `estimate`             | ⚠️     | Ignored                          |
| `dbHash`             |                        | ❌     | Unimplemented                    |
|                      | `collection`           | ⚠️     |                                  |
| `dbStats`            |                        | ✅     | Basic command is fully supported |
|                      | `scale`                | ✅     |                                  |
|                      | `freeStorage`          | ⚠️     | Unimplemented                    |
| `driverOIDTest`      |                        | ⚠️     | Unimplemented                    |
| `explain`            |                        | ✅     | Basic command is fully supported |
|                      | `verbosity`            | ⚠️     | Ignored                          |
|                      | `comment`              | ⚠️     | Unimplemented                    |
| `features`           |                        | ❌     | Unimplemented                    |
| `getCmdLineOpts`     |                        | ✅     | Basic command is fully supported |
| `getLog`             |                        | ✅     | Basic command is fully supported |
| `hostInfo`           |                        | ✅     | Basic command is fully supported |
| `_isSelf`            |                        | ❌     | Unimplemented                    |
| `listCommands`       |                        | ✅     | Basic command is fully supported |
| `lockInfo`           |                        | ❌     | Unimplemented                    |
| `netstat`            |                        | ❌     | Unimplemented                    |
| `ping`               |                        | ✅     | Basic command is fully supported |
| `profile`            |                        | ❌     | Unimplemented                    |
|                      | `slowms`               | ⚠️     |                                  |
|                      | `sampleRate`           | ⚠️     |                                  |
|                      | `filter`               | ⚠️     |                                  |
| `serverStatus`       |                        | ✅     | Basic command is fully supported |
| `shardConnPoolStats` |                        | ❌     | Unimplemented                    |
| `throttle`           |                        | ✅     | FerretDB v1 extension (see below)|
|                      | `slowDownMs`           | ✅     | Pause per command, 0–1000, def 5 |
|                      | `durationMs`           | ✅     | Window length, 0–300000, def 2000|
| `top`                |                        | ❌     | Unimplemented                    |
| `validate`           |                        | ✅     | Basic command is fully supported |
|                      | `full`                 | ⚠️     |                                  |
|                      | `repair`               | ⚠️     |                                  |
|                      | `metadata`             | ⚠️     |                                  |
|                      | `checkBSONConformance` | ⚠️     |                                  |
| `validateDBMetadata` |                        | ❌     | Unimplemented                    |
|                      | `apiParameters`        | ⚠️     |                                  |
|                      | `db`                   | ⚠️     |                                  |
|                      | `collections`          | ⚠️     |                                  |
| `whatsmyuri`         |                        | ✅     | Basic command is fully supported |

## FerretDB v1 extension commands

These commands are specific to this FerretDB v1 (SQLite) build and are not part of
the MongoDB command set.

### `throttle`

A self-throttle for host-CPU pressure. A client that shares the host with FerretDB
can call `throttle` to (1) learn how busy FerretDB is and (2) ask it to slow down
when the host CPU is high (a client cannot pause FerretDB's internal work from the
outside). While the throttle is active, every command sleeps `slowDownMs` before it
runs, which lowers FerretDB's CPU use and yields time to other software on the host.
The throttle **self-expires** after `durationMs`, so a crashed or disconnected client
can never leave FerretDB permanently slow. The throttle command itself and the cheap
health/handshake commands (`hello`/`isMaster`/`ping`) are never throttled.

A client can drive the loop by calling `throttle` repeatedly with an **increasing**
`slowDownMs` (e.g. doubling) while the host CPU stays high, and backing off once
enough CPU is free. `commandsProcessed` and `operationsSummary` let the client see
how busy FerretDB is and what it has been doing between calls.

**FerretDB also self-regulates.** A client that shares the host may be too starved of
CPU to measure the load or even send the request. So a background loop in FerretDB
samples the host's total CPU (from `/proc/stat`) every few seconds and, when it is too
high, adds its OWN increasing delay before each command until CPU returns below a
target — then backs off. The delay actually applied per command is the **max** of the
client-requested `slowDownMs` and FerretDB's self-regulated delay, so the two
cooperate and FerretDB never monopolizes the host even with no client. Self-regulation
is tunable with `FERRETDB_CPU_SELF_REGULATE` (default on), `FERRETDB_CPU_HIGH_PERCENT`,
`FERRETDB_CPU_TARGET_PERCENT`, `FERRETDB_CPU_SLOWDOWN_MS`, `FERRETDB_CPU_SLOWDOWN_MAX_MS`
and `FERRETDB_CPU_INTERVAL_MS`; it is a no-op where `/proc/stat` is unreadable.

Parameters (all optional):

| Field        | Type | Default | Range        | Meaning                                   |
| ------------ | ---- | ------- | ------------ | ----------------------------------------- |
| `slowDownMs` | int  | 5       | 0–1000       | Pause before each command while active    |
| `durationMs` | int  | 2000    | 0–300000     | How long the throttle stays active        |

Response fields:

| Field               | Meaning                                                        |
| ------------------- | -------------------------------------------------------------- |
| `commandsProcessed` | Running count of commands handled (an activity/"how busy" signal) |
| `operationsSummary` | The busiest commands so far, e.g. `find=12000, update=340, …`  |
| `throttled`         | Whether the client-requested throttle is currently active      |
| `slowDownMs`        | The client-requested pause (clamped) now in effect             |
| `durationMs`        | The requested window length                                    |
| `untilUnixNano`     | Deadline after which the client throttle self-expires          |
| `autoSlowDownMs`    | FerretDB's own self-regulated delay (independent of the client) |
| `hostCpuPercent`    | FerretDB's last measured host CPU%                             |

Example:

```js
db.runCommand({ throttle: 1, slowDownMs: 5, durationMs: 2000 })
// { ok: 1, commandsProcessed: 128374, throttled: true, slowDownMs: 5, ... }

db.runCommand({ throttle: 1, durationMs: 0 }) // resume full speed immediately
```
