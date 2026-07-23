---
sidebar_position: 7
hide_table_of_contents: true
---

# Query pushdown

**Query pushdown** is the method of optimizing a query by reducing the amount of data read and processed.
It saves memory space, network bandwidth, and reduces the query execution time by moving some parts
of the query execution closer to the data source.

Initially FerretDB retrieved all data related to queried collection, and applies filters on its own, making
it possible to implement complex logic safely and quickly.
To make this process more efficient, we minimize the amount of incoming data, by applying WHERE clause on SQL queries.

:::info
You can learn more about query pushdown in our [blog post](https://blog.ferretdb.io/ferretdb-fetches-data-query-pushdown/).
:::

## Supported types and operators

The following table shows all operators and types that FerretDB pushdowns on PostgreSQL backend.
If filter uses type and operator, that's marked as pushdown-supported on this list,
FerretDB will prefetch less data, resulting with more performent query.

If your application requires better performance for specific operation,
feel free to share this with us in our [community](/#community)!

:::tip
As query pushdown allows developers to implement query optimizations separately from the features,
the table will be updated frequently.
:::

<!-- markdownlint-capture -->
<!-- markdownlint-disable MD001 MD033 MD051 -->

|        | Object | Array | Double                  | String | Binary | ObjectID | Boolean | Date | Null | Regex | Integer | Timestamp | Long                    |
| ------ | ------ | ----- | ----------------------- | ------ | ------ | -------- | ------- | ---- | ---- | ----- | ------- | --------- | ----------------------- |
| `=`    | ✖️     | ✖️    | ⚠️ <sub>[[1]](#1)</sub> | ✅     | ✖️     | ✅       | ✅      | ✅   | ✖️   | ✖️    | ✅      | ✖️        | ⚠️ <sub>[[1]](#1)</sub> |
| `$eq`  | ✖️     | ✖️    | ⚠️ <sub>[[1]](#1)</sub> | ✅     | ✖️     | ✅       | ✅      | ✅   | ✖️   | ✖️    | ✅      | ✖️        | ⚠️ <sub>[[1]](#1)</sub> |
| `$gt`  | ✖️     | ✖️    | ✅ <sub>[[2]](#2)</sub> | ✖️     | ✖️     | ✖️       | ✖️      | ✅   | ✖️   | ✖️    | ✅      | ✅        | ✅ <sub>[[2]](#2)</sub> |
| `$gte` | ✖️     | ✖️    | ✅ <sub>[[2]](#2)</sub> | ✖️     | ✖️     | ✖️       | ✖️      | ✅   | ✖️   | ✖️    | ✅      | ✅        | ✅ <sub>[[2]](#2)</sub> |
| `$lt`  | ✖️     | ✖️    | ✅ <sub>[[2]](#2)</sub> | ✖️     | ✖️     | ✖️       | ✖️      | ✅   | ✖️   | ✖️    | ✅      | ✅        | ✅ <sub>[[2]](#2)</sub> |
| `$lte` | ✖️     | ✖️    | ✅ <sub>[[2]](#2)</sub> | ✖️     | ✖️     | ✖️       | ✖️      | ✅   | ✖️   | ✖️    | ✅      | ✅        | ✅ <sub>[[2]](#2)</sub> |
| `$in`  | ✖️     | ✖️    | ✅ <sub>[[2]](#2)</sub> | ✅     | ✖️     | ✅       | ✅      | ✅   | ✅   | ✖️    | ✅      | ✖️        | ✅ <sub>[[2]](#2)</sub> |
| `$ne`  | ✖️     | ✖️    | ⚠️ <sub>[[1]](#1)</sub> | ✅     | ✖️     | ✅       | ✅      | ✅   | ✖️   | ✖️    | ✅      | ✖️        | ⚠️ <sub>[[1]](#1)</sub> |
| `$nin` | ✖️     | ✖️    | ✖️                      | ✖️     | ✖️     | ✖️       | ✖️      | ✖️   | ✖️   | ✖️    | ✖️      | ✖️        | ✖️                      |

###### [1] {#1}

Numbers outside the range of the safe IEEE 754 precision (`< -9007199254740991.0, 9007199254740991.0 >`),
will prefetch all numbers larger/smaller than max/min value of the range.

###### [2] {#2}

**WeKan v1 fork (`main-v1`) addition.** This fork pushes down numeric/date/BSON-Timestamp
range filters (`$gt`/`$gte`/`$lt`/`$lte`) and `$in` on the **sqlite, postgresql, mysql and
hana** backends (upstream pushed neither, on PostgreSQL only). It is why an idle Meteor OpLog
tail's `{ts: {$gt: <last>}}` — and a `{field: {$in: [id, null]}}` client filter — resume as
an indexed scan instead of re-decoding the whole (capped) collection on every poll. Each is a
SUPERSET: a range is type-guarded (SQLite `->>` numeric compare; PostgreSQL
`jsonb_typeof = 'number'`; MySQL `JSON_TYPE IN (...)`; HANA numeric compare) so a non-number
value can never crash a strict cast, and `$in` pushes only its safe elements plus a
null-or-missing arm for a `null` element; the in-Go filter re-applies the exact,
type-bracketed comparison in all cases. See the [WeKan compatibility ROADMAP](../ROADMAP.md).

<!-- markdownlint-restore -->
