# MariaDB EXPLAIN — Columns Explained

`EXPLAIN` (or `EXPLAIN EXTENDED`, `EXPLAIN FORMAT=JSON`) shows the query execution plan — how MariaDB's optimizer intends to execute a `SELECT` (or `INSERT/UPDATE/DELETE`).

## Core Columns (traditional tabular format)

| Column | Description |
|---|---|
| **`id`** | The step/sequence number in the execution plan. A simple single-table query has `id=1`. For subqueries and unions, each sub-query or derived table gets a higher `id`. All rows with the same `id` run at the same level; higher `id` rows run first. |
| **`select_type`** | The kind of SELECT being performed. Common values: `SIMPLE`, `PRIMARY` (outermost query), `SUBQUERY`, `DERIVED` (subquery in FROM clause), `UNION`, `UNION RESULT`, `DEPENDENT SUBQUERY`, `DEPENDENT UNION`, `MATERIALIZED`, `UNCACHEABLE SUBQUERY`. |
| **`table`** | The table name (or alias) the row refers to. Also shows `<derivedN>`, `<unionM,N>`, or `<subqueryN>` for materialized results. |
| **`type`** | **The join type** — how MariaDB accesses the table. From best to worst: `system` → `const` → `eq_ref` → `ref` → `fulltext` → `ref_or_null` → `index_merge` → `unique_subquery` → `index_subquery` → `range` → `index` → `ALL`. You generally want `const`, `eq_ref`, or `ref`; `ALL` (full table scan) is a red flag for large tables. |
| **`possible_keys`** | Indexes that MariaDB *could* use for this table (including ones ultimately not chosen). If `NULL`, no index is available — you probably need one. |
| **`key`** | The index actually chosen by the optimizer. May be `NULL` if no index is used (usually `type = ALL`). If an index appears in `possible_keys` but not here, the optimizer deemed a different access path cheaper. |
| **`key_len`** | Length (in bytes) of the chosen index key that MariaDB actually uses. For multi-column indexes, this reveals how many prefix columns are being utilized — e.g. on an `INT` (4 bytes) + `VARCHAR(100)` (UTF-8 → up to 400 bytes) index, a `key_len` of 4 means only the first column is used. |
| **`ref`** | The column(s) or constant(s) compared against the index in `key`. Shows `const` for a literal value, `db.table.column` for a join column, or `func` when a function result is used. |
| **`rows`** | **Estimated** number of rows MariaDB thinks it must examine to satisfy this step. An estimate, not exact — the optimizer uses index statistics. Use this to spot the most expensive step in a multi-table join. |
| **`Extra`** | Additional information about how the query is executed. This is a critical column — see the breakdown below. |

## Common `Extra` values

| Extra Value | Meaning |
|---|---|
| `Using index` | **Covering index** — data is read entirely from the index tree; no read from the data file. Desirable. |
| `Using where` | A `WHERE` clause filters rows (either after the storage engine returns them, or pushed down via index condition pushdown). |
| `Using index condition` | **Index Condition Pushdown (ICP)** — the storage engine evaluates parts of the WHERE using index columns *before* handing rows to the server layer. Reduces the rows passed up. |
| `Using temporary` | A temporary table is needed (e.g., `GROUP BY`, `DISTINCT`, `ORDER BY` with different columns than the join). Often a performance concern. |
| `Using filesort` | An extra sort pass is required (not using an index for ordering). May be unavoidable, but on large result sets it's a performance target. |
| `Using join buffer` | Tables in a join without an index are read in full into a join buffer. Often indicates a missing index on the join column. |
| `Using intersect/union/sort_union(...)` | Index merge strategies (intersection or union of multiple indexes). |
| `Impossible WHERE` | The `WHERE` clause is always false — no rows will be examined. |
| `No tables used` | Query has no `FROM` clause (e.g., `SELECT 1+1`). |
| `Distinct` | The optimizer stops scanning after finding the first matching row (when `DISTINCT` or `GROUP BY` can be optimized away). |
| `Start temporary` / `End temporary` | Semi-join duplicate weedout strategy in action. |
| `Using MRR` | **Multi-Range Read** — the optimizer batches index lookups and sorts them by row ID before accessing the table, reducing random I/O. |
| `Using where with pushed condition` | Condition was pushed down to a storage engine (e.g., NDB Cluster). |

## MariaDB-specific additions (vs MySQL)

MariaDB adds:

- **`filtered`** column (shown with `EXPLAIN EXTENDED` or automatically in MariaDB 10.1+): Percentage (0–100) of rows from this table that will pass the `WHERE` clause. Helps the optimizer decide join order — a low `filtered` value on an early table means that table is an effective filter.
- **`used_key_parts`** (MariaDB 10.7+ `EXPLAIN FORMAT=JSON`): Breaks down exactly which columns of a composite index are used for equality, range, etc.
- Enhanced **`EXPLAIN FORMAT=JSON`** output counts `r_loops`, `r_rows`, `r_total_time_ms` when run with `ANALYZE`.

## ANALYZE variants

### `ANALYZE ...`
(MariaDB 10.1+) Runs `EXPLAIN` **and** actually executes the query, replacing estimates with real measurements:

| Additional Column | Description |
|---|---|
| `r_rows` | Actual number of rows read with each iteration |
| `r_filtered` | Actual percentage of rows that passed the filter |
| `r_total_time_ms` | Total time spent reading from this table (in milliseconds) |

### `EXPLAIN FORMAT=JSON`
Returns the plan as a JSON document — structurally richer, with `used_columns`, `attached_condition`, `used_key_parts`, `scan_type`, and cost estimates per step. The JSON format is the most complete, especially for complex plans involving subqueries, semi-joins, or materialization.

## Format options

```sql
EXPLAIN SELECT ...                  -- tabular (default)
EXPLAIN EXTENDED SELECT ...         -- tabular + filtered column
EXPLAIN FORMAT=JSON SELECT ...      -- JSON document
ANALYZE SELECT ...                  -- runs query, shows actual row counts + timing
ANALYZE FORMAT=JSON SELECT ...      -- JSON + actuals
```

## Quick troubleshooting guide

| Symptom | Likely meaning |
|---|---|
| `type = ALL` + many `rows` | Full table scan — add an index |
| `key = NULL` but `possible_keys` has entries | Optimizer chose not to use it — check selectivity or force via `USE INDEX` |
| `Using filesort` + large `rows` | Expensive sort — consider a compound index that covers the `ORDER BY` |
| `Using temporary` + `Using filesort` | Both temp table and sort — typically from `GROUP BY` / `DISTINCT` on non-indexed columns |
| `filtered` is very low (~10 or less) on the first table in a join | This table is an effective filter — probably fine as-is |
| `key_len` is small relative to a composite index width | Only the leading columns of the index are used — check if WHERE covers more columns |
