# S3 Versioned Bucket Delete & Object List Loading Navigation

Fixes: #94, #86

## Issue #94: S3 Bucket Delete Fails on Versioned Buckets

### Problem

`EmptyAndDeleteBucket` in `internal/aws/s3.go` uses `ListObjectsPage` (which calls `ListObjectsV2`), returning only current object versions. In versioned buckets, previous versions and delete markers remain, causing `DeleteBucket` to fail with `BucketNotEmpty`.

### Solution

**Approach: Loop `ListObjectVersions` + new `DeleteObjectVersions` method**

Changes to `internal/aws/s3.go`:

1. **New `DeleteObjectVersions(ctx, bucket, []ObjectVersion)` method** — builds S3 `DeleteObjects` input with version IDs. Batches in groups of 1000 (S3's per-request limit). Handles both regular versions and delete markers.

2. **Rewrite `EmptyAndDeleteBucket`** — replace `ListObjectsPage` loop with `ListObjectVersions` loop. For each page of versions (including delete markers), call `DeleteObjectVersions`. Once all versions are deleted, call `DeleteBucket`.

3. **Add `DeleteObjectVersions` to `S3Service` interface** for testability.

Non-versioned buckets still work because `ListObjectVersions` returns current versions too, so a single code path handles both cases.

### Acceptance Criteria

- `EmptyAndDeleteBucket` deletes all object versions and delete markers before deleting the bucket
- Versioned buckets can be successfully deleted via the UI
- Non-versioned buckets continue to work as before

---

## Issue #86: S3 Object List Blocks Keyboard Navigation During Loading

### Problem

In `internal/views/s3_objects.go`, the `Update` method (lines 732-740) early-returns when `s.loading == true`, routing all messages to the spinner only. The table never receives keyboard input during loading, even though `buildTable()` is already being called after each page arrives and rows are present.

### Solution

**Approach: Remove the loading guard, forward messages to both spinner and table**

Changes to `internal/views/s3_objects.go`:

1. **Remove the early return in `Update`** — when `s.loading == true`, pass messages to both the spinner (for animation) and the table (for keyboard navigation), instead of short-circuiting to the spinner only.

2. **Cursor stability** — verify that `buildTable()` preserves cursor position when rows are rebuilt during loading. If the table resets cursor to row 0, save/restore cursor index around `buildTable()` calls while loading.

3. **No visual changes** — spinner continues in bottom-left as before. The only difference is the table is now interactive while loading.

### Acceptance Criteria

- Table is interactive while objects are still loading
- Row selection/highlight is visible before loading completes
- Cursor position is preserved as new rows are appended
- No regression for buckets that load quickly (single batch)
