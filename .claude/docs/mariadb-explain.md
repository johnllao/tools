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

## Comprehensive troubleshooting guide

Each section below covers a common EXPLAIN pattern, why it happens, and the recommended fix — ordered by frequency in real-world production databases.

---

### 1. `type = ALL` — Full Table Scan

**How to spot it:**
```
+----+-------------+-------+------+---------------+------+---------+------+--------+-------------+
| id | select_type | table | type | possible_keys | key  | key_len | ref  | rows   | Extra       |
+----+-------------+-------+------+---------------+------+---------+------+--------+-------------+
|  1 | SIMPLE      | users | ALL  | NULL          | NULL | NULL    | NULL | 500000 | Using where |
+----+-------------+-------+------+---------------+------+---------+------+--------+-------------+
```

**Why it happens:** No `WHERE` clause on an indexed column, or the `WHERE` clause uses a function/expression that makes the index unusable (e.g., `WHERE LOWER(email) = '...'` or `WHERE DATE(created_at) = '2025-01-01'`).

**Impact:** Linear scan of all rows. On 500K rows, every query touches 500K rows. At 1000 QPS, that's 500M row reads/second — disastrous.

**Recommended fixes (in priority order):**

1. **Add a covering index** on the columns used in `WHERE`, `JOIN`, and `ORDER BY`:
   ```sql
   -- Before: SELECT * FROM users WHERE email = 'x@y.com';
   -- After adding:
   CREATE INDEX idx_users_email ON users(email);
   ```

2. **Rewrite to avoid wrapping columns in functions** — make the index usable:
   ```sql
   -- Bad:  WHERE DATE(created_at) = '2025-01-01'
   -- Good: WHERE created_at >= '2025-01-01' AND created_at < '2025-01-02'
   ```

3. **Add a covering index that includes SELECT columns** to get `Using index` (no data file reads):
   ```sql
   CREATE INDEX idx_users_email_name ON users(email, name);
   -- now SELECT email, name FROM users WHERE email = '...' uses the index only
   ```

---

### 2. `Using filesort` — Extra Sort Pass

**How to spot it:**
```
+----+-------------+--------+------+---------------+------+---------+------+-------+-----------------------------+
| id | select_type | table  | type | possible_keys | key  | key_len | ref  | rows  | Extra                       |
+----+-------------+--------+------+---------------+------+---------+------+-------+-----------------------------+
|  1 | SIMPLE      | orders | ALL  | NULL          | NULL | NULL    | NULL | 200000 | Using where; Using filesort |
+----+-------------+--------+------+---------------+------+---------+------+-------+-----------------------------+
```

**Why it happens:** The `ORDER BY` column is not covered by an index (or is covered by a different index than the one used for filtering), so MariaDB must collect all matching rows and sort them in a temporary buffer.

**Impact:** The sort buffer is in-memory up to `sort_buffer_size` (default 2 MB), then spills to disk. Sorting 200K rows with a disk spill can take seconds.

**Recommended fixes:**

1. **Create a composite index that covers both the WHERE and the ORDER BY columns** — the ORDER BY column(s) must come **after** the WHERE equality columns:
   ```sql
   -- Query: SELECT * FROM orders WHERE status = 'pending' ORDER BY created_at DESC LIMIT 20;
   CREATE INDEX idx_orders_status_created ON orders(status, created_at);
   --           equality ------> ^       range/order --> ^
   ```

2. **Ensure the ORDER BY direction matches the index direction** whenever feasible (MariaDB 10.1+ can use ascending indexes for descending scans via backwards index scan, so this is less critical than before).

3. **If you must sort on a computed expression**, consider a generated/persistent column and index it:
   ```sql
   ALTER TABLE orders ADD COLUMN total_value INT GENERATED ALWAYS AS (qty * unit_price) PERSISTENT;
   CREATE INDEX idx_orders_total ON orders(total_value);
   ```

4. **For large offsets**, use the deferred join / "seek method" pattern instead of `LIMIT N OFFSET M`:
   ```sql
   -- Bad:  SELECT * FROM orders ORDER BY id LIMIT 20 OFFSET 100000;
   -- Good: SELECT * FROM orders WHERE id > 100000 ORDER BY id LIMIT 20;
   ```

---

### 3. `Using temporary` — Implicit Temporary Table

**How to spot it:**
```
+----+-------------+--------+------+---------------+------+---------+------+-------+---------------------------------+
| id | select_type | table  | type | possible_keys | key  | key_len | ref  | rows  | Extra                           |
+----+-------------+--------+------+---------------+------+---------+------+-------+---------------------------------+
|  1 | SIMPLE      | orders | ALL  | NULL          | NULL | NULL    | NULL | 200000 | Using where; Using temporary; Using filesort |
+----+-------------+--------+------+---------------+------+---------+------+-------+---------------------------------+
```

**Why it happens:** `GROUP BY` columns differ from the index order, or `DISTINCT` is combined with `ORDER BY` on a different column. MariaDB builds a temporary table (in memory or on disk) to hold intermediate results, then sorts them.

**Impact:** Temporary tables are held in memory up to `tmp_table_size` / `max_heap_table_size` (default 16 MB), then converted to InnoDB-on-disk — orders of magnitude slower.

**Recommended fixes:**

1. **Create a composite index matching the `GROUP BY` columns exactly, in order.** If you also `ORDER BY`, make sure the ordering matches:
   ```sql
   -- Query: SELECT status, COUNT(*) FROM orders GROUP BY status ORDER BY status;
   CREATE INDEX idx_orders_status ON orders(status);
   -- Now GROUP BY uses the index — no temp table needed.
   ```

2. **For `GROUP BY` + `ORDER BY` on different columns**, consider whether you can change one to match the other, or add a composite index that satisfies both.

3. **For `DISTINCT`**, a composite index on the distinct columns eliminates the temp table:
   ```sql
   -- SELECT DISTINCT customer_id, product_id FROM orders;
   CREATE INDEX idx_orders_cust_prod ON orders(customer_id, product_id);
   -- Uses the index to skip duplicates — no temp table.
   ```

4. **Increase `tmp_table_size` and `max_heap_table_size`** only if you've already optimized the queries and the temp tables legitimately need more memory. This is a **band-aid, not a fix** — always prefer index optimization first.

---

### 4. `Using join buffer` (Block Nested Loop / Hash Join)

**How to spot it:**
```
+----+-------------+---------+------+---------------+--------+---------+-------------------+------+----------------------------------+
| id | select_type | table   | type | possible_keys | key    | key_len | ref               | rows | Extra                            |
+----+-------------+---------+------+---------------+--------+---------+-------------------+------+----------------------------------+
|  1 | SIMPLE      | orders  | ALL  | NULL          | NULL   | NULL    | NULL              | 200000 | NULL                             |
|  1 | SIMPLE      | items   | ALL  | NULL          | NULL   | NULL    | NULL              | 500000 | Using where; Using join buffer   |
+----+-------------+---------+------+---------------+--------+---------+-------------------+------+----------------------------------+
```

**Why it happens:** The second table in a join has no usable index on the join column, so MariaDB loads the entire table into the join buffer and scans it per row (or per batch) from the driving table.

**Impact:** 200K orders × join-buffer scan of 500K items = 100 billion row comparisons in the worst case. This is the single most common cause of "the database is down" incidents.

**Recommended fixes:**

1. **Add an index on the join column of the scanned table:**
   ```sql
   -- Query: SELECT * FROM orders o JOIN items i ON o.id = i.order_id;
   -- The join column on items is order_id — index it:
   CREATE INDEX idx_items_order_id ON items(order_id);
   ```

2. **If the driving table has a WHERE clause, index that too:**
   ```sql
   -- SELECT * FROM orders o JOIN items i ON o.id = i.order_id WHERE o.status = 'pending';
   -- Index both:
   CREATE INDEX idx_orders_status ON orders(status);       -- driving filter
   CREATE INDEX idx_items_order_id ON items(order_id);     -- join column
   ```

3. **Check the join order** — MariaDB's optimizer usually picks the right driving table, but if statistics are stale, it may not. Use `STRAIGHT_JOIN` as a last resort:
   ```sql
   SELECT STRAIGHT_JOIN ... FROM small_table JOIN large_table ...
   ```

---

### 5. Index chosen but only prefix of composite key used (short `key_len`)

**How to spot it:**
```
+----+-------------+-------+------+---------------------+---------------------+---------+-------+------+-------------+
| id | select_type | table | type | possible_keys       | key                 | key_len | ref   | rows | Extra       |
+----+-------------+-------+------+---------------------+---------------------+---------+-------+------+-------------+
|  1 | SIMPLE      | users | ref  | idx_status_city_age | idx_status_city_age | 12      | const | 5000 | Using where |
+----+-------------+-------+------+---------------------+---------------------+---------+-------+------+-------------+
```

Your composite index is `(status VARCHAR(50), city VARCHAR(100), age INT)`. UTF-8: status = 150 bytes, city = 300 bytes, age = 4 bytes. A `key_len` of 12 means MariaDB is using **only `status`** (which maps to a smaller encoding or a partial prefix). The `WHERE` on `city` and `age` is evaluated as a filter after fetching, not via index seek.

**Why it happens:**
- You're filtering on `status` and `age` but NOT `city` — a composite index can only be used left-to-right. Skipping a column stops index usage at that column.
- Or the column uses a function/expression (`WHERE city LIKE '%York'`) that breaks the index.
- Or the column is compared with `!=`, `NOT IN`, or `OR` across different columns.

**Recommended fixes:**

1. **Reorder the composite index** so the columns used in equality conditions come first, then range conditions, then ORDER BY:
   ```sql
   -- Query: WHERE status = 'active' AND age > 30 ORDER BY created_at
   -- The index idx_status_city_age is useless beyond status because city is skipped.
   -- Better:
   CREATE INDEX idx_status_age_created ON users(status, age, created_at);
   --           equality --> ^       range --> ^   order by --> ^
   ```

2. **If you need to filter on non-adjacent index columns**, create a separate index for that query pattern:
   ```sql
   CREATE INDEX idx_status_age ON users(status, age);
   ```

3. **Avoid `OR` across different columns** — use `UNION` instead:
   ```sql
   -- Bad:  SELECT * FROM users WHERE status = 'active' OR city = 'NYC';
   -- Good:
   SELECT * FROM users WHERE status = 'active'
   UNION
   SELECT * FROM users WHERE city = 'NYC';
   ```

---

### 6. Index exists but optimizer refuses to use it (`key = NULL`, `possible_keys` has entries)

**How to spot it:**
```
+----+-------------+-------+------+------------------+------+---------+------+--------+-------------+
| id | select_type | table | type | possible_keys    | key  | key_len | ref  | rows   | Extra       |
+----+-------------+-------+------+------------------+------+---------+------+--------+-------------+
|  1 | SIMPLE      | users | ALL  | idx_email,idx_id | NULL | NULL    | NULL | 500000 | Using where |
+----+-------------+-------+------+------------------+------+---------+------+--------+-------------+
```

**Why it happens:** The optimizer estimates that a full table scan is cheaper than using the index. Common causes:

- **Low selectivity**: The value you're filtering for appears in a huge percentage of rows (e.g., `WHERE status = 'active'` on a table where 95% of rows are active). The optimizer decides reading the index + the data file costs more than a linear scan.
- **Stale statistics**: `ANALYZE TABLE` hasn't been run after large data changes.
- **Type mismatch**: `WHERE id = '42'` when `id` is `INT` — the implicit cast makes the index unusable. (MariaDB can handle some implicit casts better than MySQL, but it's still risky.)
- **The index doesn't cover all needed columns**, so the optimizer still needs to read from the data file, and the cost of random I/O from index lookups exceeds the cost of sequential I/O from a table scan.

**Recommended fixes:**

1. **Update table statistics:**
   ```sql
   ANALYZE TABLE users PERSISTENT FOR ALL;
   ```

2. **Check and fix type mismatches** in your queries:
   ```sql
   -- Bad:  WHERE id = '42'          (string vs INT column)
   -- Good: WHERE id = 42
   ```

3. **Add a covering index** so the optimizer can satisfy the query entirely from the index (gets `Using index` in Extra):
   ```sql
   -- Query: SELECT email, name FROM users WHERE status = 'active';
   CREATE INDEX idx_users_status_cover ON users(status, email, name);
   ```

4. **Force the index** as a diagnostic tool (not a permanent fix) to see if the optimizer's estimate was wrong:
   ```sql
   SELECT * FROM users FORCE INDEX (idx_email) WHERE email = 'x@y.com';
   ```
   Then run `ANALYZE SELECT ...` with and without the hint and compare `r_rows`. If forcing the index is dramatically faster, the statistics are stale or the optimizer cost model is off.

5. **For genuine low-selectivity columns**, consider whether the query itself can be restructured. If you filter for a common value, maybe you don't need an index — the table scan is actually correct.

---

### 7. Correlated subquery: `DEPENDENT SUBQUERY` with high row count

**How to spot it:**
```
+----+--------------------+----------+------+---------------+------+---------+------+--------+-------------+
| id | select_type        | table    | type | possible_keys | key  | key_len | ref  | rows   | Extra       |
+----+--------------------+----------+------+---------------+------+---------+------+--------+-------------+
|  1 | PRIMARY            | users    | ALL  | NULL          | NULL | NULL    | NULL | 100000 |             |
|  2 | DEPENDENT SUBQUERY | orders   | ALL  | NULL          | NULL | NULL    | NULL | 200000 | Using where |
+----+--------------------+----------+------+---------------+------+---------+------+--------+-------------+
```

**Why it happens:** The subquery references a column from the outer query (`WHERE o.user_id = users.id`), so it must be re-executed for every row from the outer query. Without an index on the correlated column, each execution is a full table scan.

**Impact:** 100K outer rows × 200K inner scan = 20 billion row examinations. This is catastrophic.

**Recommended fixes:**

1. **Add an index on the correlated column:**
   ```sql
   CREATE INDEX idx_orders_user_id ON orders(user_id);
   ```

2. **Rewrite as a JOIN** — the optimizer can often optimize a JOIN more aggressively than a correlated subquery:
   ```sql
   -- Before: SELECT * FROM users WHERE EXISTS (SELECT 1 FROM orders WHERE orders.user_id = users.id);
   -- After:  SELECT DISTINCT users.* FROM users JOIN orders ON users.id = orders.user_id;
   ```

3. **Rewrite as a derived table** that materializes once, then the outer query joins against the materialized result:
   ```sql
   -- Before: SELECT * FROM users u WHERE u.id IN (SELECT user_id FROM orders WHERE ...);
   -- After:
   SELECT * FROM users u
   JOIN (SELECT DISTINCT user_id FROM orders WHERE ...) o ON u.id = o.user_id;
   ```

4. **Consider a semi-join rewrite** — MariaDB's optimizer can internally convert `IN (SELECT ...)` to a semi-join (shown as `Start temporary` / `End temporary` in Extra), but only when it assesses it as cheaper. Making the subquery simpler (no `GROUP BY`, no `LIMIT`, no `UNION`) helps the optimizer choose the semi-join strategy.

---

### 8. `select_type = DERIVED` with large row count and no index

**How to spot it:**
```
+----+-------------+------------+------+---------------+------+---------+------+--------+-------------+
| id | select_type | table      | type | possible_keys | key  | key_len | ref  | rows   | Extra       |
+----+-------------+------------+------+---------------+------+---------+------+--------+-------------+
|  1 | PRIMARY     | <derived2> | ALL  | NULL          | NULL | NULL    | NULL | 100000 |             |
|  2 | DERIVED     | orders     | ALL  | NULL          | NULL | NULL    | NULL | 500000 | Using where |
+----+-------------+------------+------+---------------+------+---------+------+--------+-------------+
```

**Why it happens:** A subquery in the `FROM` clause is materialized into a derived table with no indexes, then the outer query scans it fully.

**Impact:** 500K rows materialized into a temp table, then scanned by the outer query — double cost, no index.

**Recommended fixes:**

1. **Add indexes to the subquery's source tables** so the derived table itself is smaller and built faster.

2. **Rewrite the derived table as a JOIN** so indexes can be used across both tables:
   ```sql
   -- Before:
   SELECT * FROM (SELECT user_id, COUNT(*) AS cnt FROM orders GROUP BY user_id) AS t
   WHERE t.cnt > 10;
   
   -- After (HAVING on the original table, no derived table needed):
   SELECT user_id, COUNT(*) AS cnt FROM orders GROUP BY user_id HAVING cnt > 10;
   ```

3. **If you can't avoid the derived table**, ensure the subquery is as selective as possible (tight WHERE, LIMIT if applicable) to reduce materialization cost.

---

### 9. `Using index condition` (ICP) but not `Using index` — partial coverage

**How to spot it:**
```
+----+-------------+-------+------+------------------+------------------+---------+-------+------+-----------------------+
| id | select_type | table | type | possible_keys    | key              | key_len | ref   | rows | Extra                 |
+----+-------------+-------+------+------------------+------------------+---------+-------+------+-----------------------+
|  1 | SIMPLE      | users | ref  | idx_status_email | idx_status_email | 12      | const | 5000 | Using index condition |
+----+-------------+-------+------+------------------+------------------+---------+-------+------+-----------------------+
```

**Why it happens:** The index is used for lookup (`ref`) and ICP pushes the WHERE condition to the storage engine to filter rows before the server layer sees them — but the final column values still come from the data file, not the index. You have `Using index condition` instead of the ideal `Using index`.

**Impact:** Better than `Using where` (filtering happens at the storage engine level), but still requires data file reads. Not a crisis, but a covering index would be faster.

**Recommended fix:**

1. **Extend the index to cover all SELECT columns** so the query becomes `Using index`:
   ```sql
   -- Query: SELECT id, email, name FROM users WHERE status = 'active';
   -- Current index: (status, email)
   -- Better covering index: (status, email, name, id)
   ```
   This eliminates data-file reads entirely — the index tree alone answers the query.

---

### 10. Implicit type conversion (collation mismatch or charset mismatch)

**How to spot it:** `type = ALL` despite an index existing, and the `WHERE` clause compares a string column to a string literal — but the charset or collation differs.

**Example:**
```sql
-- Table column: name VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci
-- Query: SELECT * FROM users WHERE name = _latin1'John';
-- The index on `name` is skipped because of the charset mismatch.
```

**Recommended fixes:**

1. **Always use the same charset for comparisons** — omit the introducer unless intentional:
   ```sql
   -- Good: WHERE name = 'John'
   ```

2. **Check column and table collations:**
   ```sql
   SHOW FULL COLUMNS FROM users;
   SHOW TABLE STATUS LIKE 'users';
   ```

3. **If you must join tables with different charsets**, add an index on the converted expression, or convert one table's column at write time.

---

### 11. `select_type = UNCACHEABLE SUBQUERY`

**How to spot it:**
```
+----+--------------------+-------+------+---------------+------+---------+------+------+-------------+
| id | select_type        | table | type | possible_keys | key  | key_len | ref  | rows | Extra       |
+----+--------------------+-------+------+---------------+------+---------+------+------+-------------+
|  1 | PRIMARY            | users | ALL  | NULL          | NULL | NULL    | NULL | 1000 |             |
|  2 | UNCACHEABLE SUBQUERY| log   | ALL  | NULL          | NULL | NULL    | NULL | 1000 | Using where |
+----+--------------------+-------+------+---------------+------+---------+------+------+-------------+
```

**Why it happens:** The subquery contains a non-deterministic element (e.g., `NOW()`, `RAND()`, a session variable, or references a NOT-deterministic stored function), so MariaDB cannot cache the subquery result and must re-execute it for every outer row.

**Impact:** Same as `DEPENDENT SUBQUERY` — re-executed N times.

**Recommended fixes:**

1. **Move non-deterministic calls to the outer query** where possible:
   ```sql
   -- Bad:  SELECT * FROM users WHERE created_at > (SELECT MAX(NOW() - INTERVAL 7 DAY) FROM config);
   -- Good: SELECT * FROM users WHERE created_at > NOW() - INTERVAL 7 DAY;
   ```

2. **If the function is truly necessary**, ensure the subquery is indexed and as small as possible.

---

### 12. Large `rows` estimate with low actual count (stale statistics)

**How to diagnose:**
```sql
-- Compare estimate vs reality:
EXPLAIN SELECT * FROM orders WHERE created_at > NOW() - INTERVAL 1 DAY;
-- rows: 500000 (estimate)
ANALYZE SELECT * FROM orders WHERE created_at > NOW() - INTERVAL 1 DAY;
-- r_rows: 50 (actual) ← 10,000x difference!
```

**Why it happens:** InnoDB histogram statistics are stale or not granular enough. The optimizer makes join order and index decisions based on these (wildly wrong) estimates, potentially choosing terrible plans.

**Impact:** The optimizer may choose a full table scan over an index, or pick the wrong driving table in a join — all based on bad data.

**Recommended fixes:**

1. **Update statistics:**
   ```sql
   ANALYZE TABLE orders PERSISTENT FOR ALL;
   ```

2. **Increase histogram buckets** for columns with uneven distribution:
   ```sql
   -- MariaDB 10.4+
   SET histogram_size = 100;  -- default 100; increase to 254 for more precision
   ANALYZE TABLE orders PERSISTENT FOR ALL;
   ```

3. **Enable `innodb_stats_auto_recalc`** so InnoDB updates stats automatically when >10% of rows change (this is the default; check with `SHOW VARIABLES LIKE 'innodb_stats_auto_recalc';`).

4. **For persistent statistics, set `innodb_stats_persistent=ON`** and configure `innodb_stats_auto_recalc=ON` (both are defaults in MariaDB 10.3+).

---

### 13. `Extra: Using where; Using index` but `type = index` (full index scan)

**How to spot it:**
```
+----+-------------+-------+-------+---------------+----------+---------+------+--------+--------------------------+
| id | select_type | table | type  | possible_keys | key      | key_len | ref  | rows   | Extra                    |
+----+-------------+-------+-------+---------------+----------+---------+------+--------+--------------------------+
|  1 | SIMPLE      | users | index | NULL          | idx_name | 302     | NULL | 500000 | Using where; Using index |
+----+-------------+-------+-------+---------------+----------+---------+------+--------+--------------------------+
```

**Why it happens:** `type = index` means a full index scan — MariaDB reads the entire index in order (like a table scan, but over the index instead of the data file). This is slightly better than `ALL` (index is usually smaller), but still touches every row. The `Using where` means rows are filtered after being read.

**Impact:** Still O(n) — just over a narrower data structure. On 500K rows, every query reads 500K index entries.

**Recommended fixes:**

1. **Add a WHERE clause on a column that can use the index as a seek rather than a scan** — convert `index` to `range` or `ref`:
   ```sql
   -- Query: SELECT name FROM users WHERE name LIKE '%son';
   -- The leading wildcard prevents index seek (LIKE '%...').
   -- Consider a FULLTEXT index instead:
   CREATE FULLTEXT INDEX ft_users_name ON users(name);
   SELECT name FROM users WHERE MATCH(name) AGAINST('son');
   ```

2. **If the query truly needs all rows**, ensure the index is covering so you at least avoid data file reads — `Using index` without `Using where` is acceptable for export-style queries.

---

## Quick reference: Fix priority by impact

| Symptom | Priority | Typical fix |
|---|---|---|
| `DEPENDENT SUBQUERY` + `type = ALL` + no index | **CRITICAL** | Index the correlated column; or rewrite as `JOIN` |
| `Using join buffer` + 2nd table `type = ALL` | **CRITICAL** | Index the join column |
| `type = ALL` + large rows on every-query table | **HIGH** | Add `WHERE`-filtered index |
| `Using temporary` + `Using filesort` + large rows | **HIGH** | Composite index for `GROUP BY` + `ORDER BY` |
| `Using filesort` + small rows | **MEDIUM** | Acceptable if rows ≤ 1000; index if more |
| `Using index condition` (no `Using index`) | **MEDIUM** | Extend index to cover `SELECT` columns |
| `DERIVED` + no index | **MEDIUM** | Index the subquery's source table; or rewrite as `JOIN` |
| `UNCACHEABLE SUBQUERY` | **MEDIUM** | Move non-deterministic function to outer query |
| `key_len` shorter than expected | **LOW-MED** | Reorder composite index columns |
| Stale statistics (rows estimate off by >10×) | **LOW-MED** | `ANALYZE TABLE` |
| `type = ALL` (table is <100 rows) | **LOW** | Don't bother — full scan is correct |
| `Using index` (covering, no `Using where`) | **NONE** | Optimal — nothing to fix |
