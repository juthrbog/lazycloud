# S3 Versioned Bucket Delete & Loading Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix two S3 bugs — versioned bucket delete (#94) and keyboard navigation during object loading (#86) — in a single PR.

**Architecture:** Task 1 adds a `DeleteObjectVersions` method and rewrites `EmptyAndDeleteBucket` to use `ListObjectVersions` for full version/delete-marker cleanup. Task 2 removes the loading guard in `s3_objects.go` so the table receives keyboard input while pages are still streaming in. Task 3 adds the mock method and tests. Task 4 is the final lint/build/test gate.

**Tech Stack:** Go, AWS SDK v2 (`s3`, `s3types`), Bubble Tea, testify/mock

---

### Task 1: Add `DeleteObjectVersions` and fix `EmptyAndDeleteBucket`

**Files:**
- Modify: `internal/aws/s3.go:17-35` (interface)
- Modify: `internal/aws/s3.go:370-390` (EmptyAndDeleteBucket)
- Modify: `internal/aws/s3.go` (new method after line 356)

- [ ] **Step 1: Add `DeleteObjectVersions` to the `S3Service` interface**

In `internal/aws/s3.go`, add to the interface (after line 29, the `DeleteObjects` line):

```go
DeleteObjectVersions(ctx context.Context, bucket string, versions []ObjectVersion) error
```

- [ ] **Step 2: Implement `DeleteObjectVersions` on `S3ServiceImpl`**

Add after the `DeleteObjects` method (after line 356):

```go
// DeleteObjectVersions deletes specific object versions and delete markers in batches of up to 1000.
func (svc *S3ServiceImpl) DeleteObjectVersions(ctx context.Context, bucket string, versions []ObjectVersion) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	const batchSize = 1000
	for i := 0; i < len(versions); i += batchSize {
		end := i + batchSize
		if end > len(versions) {
			end = len(versions)
		}
		batch := versions[i:end]

		objects := make([]s3types.ObjectIdentifier, len(batch))
		for j, v := range batch {
			objects[j] = s3types.ObjectIdentifier{
				Key:       aws.String(v.Key),
				VersionId: aws.String(v.VersionID),
			}
		}

		_, err := s3c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 3: Add `listAllVersions` private helper**

The existing `ListObjectVersions` is scoped to a single key (uses `Prefix` filter and exact-match check). We need a bucket-wide paginated version. Add after `DeleteObjectVersions`:

```go
// listAllVersions returns all object versions and delete markers in the bucket, paginating automatically.
func (svc *S3ServiceImpl) listAllVersions(ctx context.Context, bucket string) ([]ObjectVersion, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}

	var all []ObjectVersion
	var keyMarker, versionMarker *string

	for {
		output, err := s3c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucket),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionMarker,
		})
		if err != nil {
			return nil, err
		}

		for _, v := range output.Versions {
			ov := ObjectVersion{
				Key:       aws.ToString(v.Key),
				VersionID: aws.ToString(v.VersionId),
			}
			all = append(all, ov)
		}
		for _, dm := range output.DeleteMarkers {
			ov := ObjectVersion{
				Key:            aws.ToString(dm.Key),
				VersionID:      aws.ToString(dm.VersionId),
				IsDeleteMarker: true,
			}
			all = append(all, ov)
		}

		if !aws.ToBool(output.IsTruncated) {
			break
		}
		keyMarker = output.NextKeyMarker
		versionMarker = output.NextVersionIdMarker
	}

	return all, nil
}
```

- [ ] **Step 4: Rewrite `EmptyAndDeleteBucket`**

Replace the existing `EmptyAndDeleteBucket` (lines 370-390) with:

```go
// EmptyAndDeleteBucket deletes all object versions and delete markers, then deletes the bucket.
func (svc *S3ServiceImpl) EmptyAndDeleteBucket(ctx context.Context, bucket string) error {
	// Delete all object versions and delete markers (handles versioned and non-versioned buckets)
	for {
		versions, err := svc.listAllVersions(ctx, bucket)
		if err != nil {
			return fmt.Errorf("listing versions: %w", err)
		}
		if len(versions) == 0 {
			break
		}
		if err := svc.DeleteObjectVersions(ctx, bucket, versions); err != nil {
			return fmt.Errorf("deleting versions: %w", err)
		}
	}
	return svc.DeleteBucket(ctx, bucket)
}
```

- [ ] **Step 5: Build to verify compilation**

Run: `go build ./...`
Expected: Clean build, no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/aws/s3.go
git commit -m "Fix versioned bucket delete: use ListObjectVersions + DeleteObjectVersions (#94)"
```

---

### Task 2: Unblock keyboard navigation during S3 object loading

**Files:**
- Modify: `internal/views/s3_objects.go:732-740`

- [ ] **Step 1: Remove the loading early return**

Replace lines 732-740 in `internal/views/s3_objects.go`:

```go
	if s.loading {
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(m)
		return s, cmd
	}

	var cmd tea.Cmd
	s.table, cmd = s.table.Update(m)
	return s, cmd
```

With:

```go
	var cmds []tea.Cmd
	if s.loading {
		var spinnerCmd tea.Cmd
		s.spinner, spinnerCmd = s.spinner.Update(m)
		cmds = append(cmds, spinnerCmd)
	}
	var tableCmd tea.Cmd
	s.table, tableCmd = s.table.Update(m)
	cmds = append(cmds, tableCmd)
	return s, tea.Batch(cmds...)
```

This forwards messages to both the spinner (when loading) and the table (always), so arrow keys work immediately on loaded rows.

**Why this is safe:** `buildTable()` is called on every `s3PageLoadedMsg` (line 450), so rows exist in the table as soon as the first page arrives. The Bubble Tea table's `SetRows` clamps the cursor if it exceeds the row count, and since we only append rows, the cursor position is preserved.

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./...`
Expected: Clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/views/s3_objects.go
git commit -m "Allow keyboard navigation while S3 objects are still loading (#86)"
```

---

### Task 3: Update mock and add tests

**Files:**
- Modify: `internal/aws/awstest/mock_s3.go` (add `DeleteObjectVersions` mock)
- Modify: `internal/views/s3_objects_test.go` (add loading navigation test)

- [ ] **Step 1: Add `DeleteObjectVersions` to `MockS3Service`**

Add to `internal/aws/awstest/mock_s3.go` after the `DeleteObjects` method (after line 80):

```go
func (m *MockS3Service) DeleteObjectVersions(ctx context.Context, bucket string, versions []aws.ObjectVersion) error {
	args := m.Called(ctx, bucket, versions)
	return args.Error(0)
}
```

- [ ] **Step 2: Build to verify the mock satisfies the interface**

Run: `go build ./...`
Expected: Clean build. The `var _ aws.S3Service = (*MockS3Service)(nil)` check on line 17 ensures compilation fails if the interface isn't satisfied.

- [ ] **Step 3: Write test for keyboard navigation during loading**

Add to `internal/views/s3_objects_test.go`:

```go
func TestS3Objects_NavigableDuringLoading(t *testing.T) {
	view, _ := newTestS3Objects()

	// Simulate first page loaded with more pages pending
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file1.txt", Size: 1024, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file2.json", Size: 2048, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file3.go", Size: 512, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		hasMorePages: true,
		pageNum:      1,
		token:        nil,
	})

	// View should still be loading
	assert.True(t, view.loading, "view should still be loading after partial page")

	// Table should have rows
	_, total := view.table.RowCount()
	assert.Equal(t, 3, total, "table should have 3 rows from first page")

	// Press down arrow — should move cursor even while loading
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, 1, view.table.SelectedIndex(), "cursor should move to row 1 during loading")

	// Press down again
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, 2, view.table.SelectedIndex(), "cursor should move to row 2 during loading")
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/views/ -run TestS3Objects_NavigableDuringLoading -v`
Expected: PASS

- [ ] **Step 5: Write test for cursor preservation when new page appends**

Add to `internal/views/s3_objects_test.go`:

```go
func TestS3Objects_CursorPreservedOnNewPage(t *testing.T) {
	view, _ := newTestS3Objects()

	// Load first page
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file1.txt", Size: 1024, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file2.json", Size: 2048, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		hasMorePages: true,
		pageNum:      1,
	})

	// Move cursor to row 1
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, 1, view.table.SelectedIndex(), "cursor should be at row 1")

	// Second page arrives — cursor should stay at row 1
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file3.go", Size: 512, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		hasMorePages: false,
		pageNum:      2,
	})

	assert.Equal(t, 1, view.table.SelectedIndex(), "cursor should stay at row 1 after new page loads")
	_, total := view.table.RowCount()
	assert.Equal(t, 3, total, "table should now have 3 rows")
	assert.False(t, view.loading, "loading should be complete")
}
```

- [ ] **Step 6: Run all S3 objects tests**

Run: `go test ./internal/views/ -run TestS3Objects -v`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/aws/awstest/mock_s3.go internal/views/s3_objects_test.go
git commit -m "Add tests for loading navigation and mock for DeleteObjectVersions"
```

---

### Task 4: Final verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`
Expected: All tests PASS.

- [ ] **Step 2: Run linter with gosec**

Run: `golangci-lint run --enable gosec ./...`
Expected: No new warnings or errors.

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: Clean build.
