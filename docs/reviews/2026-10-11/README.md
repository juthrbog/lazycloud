# Deep review, 2026-10-11

This file tracks the findings of a full review of LazyCloud at commit `f107757` on `main`. Six reviewers each read one area: app core, AWS layer, views, UI components, tests and CI, and the market. Most findings have a repro test or a benchmark. The archived repro tests are in [`repro/`](repro/). The market research is in [`market-research.md`](market-research.md).

PR #199 fixed C1 and added this file. Release v0.5.0 contains the fix. v0.4.0 still contains C1.

## How to use this file

Each finding has an ID, a checkbox, and an evidence level. When you fix a finding, tick the box and add the PR number after the title.

The ID prefix gives the type of finding:

- C, H, M, L: critical, high, medium, and low correctness or security bugs.
- P: performance.
- T: tests.
- I: CI, release, and dependencies.
- D: drift between the docs and the code.
- A: architecture changes that fix a group of bugs.

The evidence level tells you how sure the finding is:

- Reproduced: a test in `repro/` shows the bug.
- Traced: a reviewer followed the code path from end to end.
- Measured: a benchmark gives the number.
- Plausible: the code suggests the bug, but nobody ran it.

## Baseline

At `f107757`, `go build`, `go vet`, and `go test -race ./...` pass. A run of 30 shuffled test passes found no flaky tests and no order dependence. golangci-lint v2.14 with gosec reports one issue (L21). govulncheck reports one vulnerability (H9).

| Package | Coverage |
|---|---|
| `internal/app` | 58.6% |
| `internal/aws` | 10.0% |
| `internal/nav` | 100% |
| `internal/registry` | 100% |
| `internal/ui` | 33.1% |
| `internal/views` | 26.8% |
| `internal/config`, `internal/eventlog`, `cmd/seed`, `main` | 0% |
| Total | 27.6% |

## Work plan

Work through the phases in order. Phase 0 contains small fixes that do not depend on each other. Phases 2 to 5 are refactors. Each one fixes a group of bugs at the root.

- [x] Phase 0, small isolated fixes: C1, H9, H7, H8, M14, L21, L22. Done in #199 and in #200.
- [x] Phase 1, SQS peek: H1, M5, M22. H1 is done in #199. M5 and M22 are done in #218, with M7, M9, and L25.
- [x] Phase 2, addressed messages (A1): C2, H3, H4, H5, M12, M13. Done in #201, except the stash parts of M12 (A6).
- [x] Phase 3, table keyed by resource ID (A2): C3, C4, M8, M10, M17, P2. Done in #213, except the AMI part of M10. The manual checks in LocalStack found L23 and L24, and the branch fixes them too.
- [x] Phase 4, input capture and central ReadOnly guard (A3): H2, M27. Done in #214, with T3, T5, D3, D4, and D14.
- [x] Phase 5, AWS services bundle and session switch (A4): H6, M11, L3. Done in #215, with L5 and D8.
- [ ] Phase 6, CI and release: H10, H11, H12, I1 to I9. I1, I3, I4, and I6 are done in #216. I5 (more linters) and I7 (release gaps) are open.
- [ ] Phase 7, the remaining medium findings. M1, M2, M24, M26, and part of M25 are done in #223. M23 is done in #224. M28 is done in #225.
- [ ] Phase 8, generic resource list (A10).
- [ ] Phase 9, product roadmap. See [`market-research.md`](market-research.md).

## Critical

- [x] C1. Shell injection through the EC2 Name tag. Evidence: reproduced. Fixed in #199. #199 also blocks Connect in ReadOnly mode.
  - Where: `internal/aws/ec2.go:72-86` builds the command. `internal/views/ec2_list.go:526-533` builds the label.
  - Problem: `SSMSessionCmd` puts the label inside `printf '<label>' && aws ...` and runs the string with `sh -c`. The label contains the Name tag of the instance. Any principal with `ec2:CreateTags` can set a Name such as `web' ; touch /tmp/x ; echo '`. When an operator presses `o` on that instance, the shell runs the command on the machine of the operator, with the credentials of the operator. A `%` in a name also breaks `printf`. A `//nolint:gosec` comment hid the gosec warning.
  - Related: the code looks up `session-manager-plugin` but not `aws`. Connect works in ReadOnly mode. `sh` does not exist on Windows.
  - Fix: do not build a shell string. Run `aws` directly with `exec.Command`. Print the banner from Go in a small `tea.ExecCommand` wrapper. Remove control characters from the label. Look up `aws` with `exec.LookPath`. Decide if Connect needs ReadWrite mode.

- [x] C2. An S3 delete can run in a different bucket. Evidence: reproduced. Fixed in #201 (routing, plus a bucket guard in `s3DeleteResolvedMsg`).
  - Where: `internal/views/s3_objects.go:247-265` and `:381-388`, `internal/app/app.go:537`.
  - Problem: To delete a folder, the view first lists all keys under the folder. For a large folder, this takes seconds. The result message `s3DeleteResolvedMsg` holds the keys but not the bucket. The app sends each unknown message to the view on top of the stack. If the user opens bucket B before the list finishes, the view for B receives the keys and calls `DeleteObjects` on bucket B. No prompt shows for B. `DeleteObjects` also succeeds for keys that do not exist, so the user sees no sign of the problem. Buckets for staging and production often share a key layout.
  - Fix: put the bucket and prefix in the message, and drop a message that does not match. The general fix is A1. Also test ReadOnly again before the delete starts (A3).

- [x] C3. Table selection follows row position, not the resource. Evidence: reproduced. Fixed in #213. Rows have a key, and the cursor and the selection follow it. The S3 object delete prompt lists the names.
  - Where: `internal/ui/table.go:30` and `:126-151`. Callers: `s3_objects.go:800-858`, `s3_list.go`, `ec2_list.go`.
  - Problem: `ui.Table` stores selections as indexes into its rows and keeps them across `SetRows`. Each insert or reorder moves the selection to a different resource. The delete prompt shows only a count, so the user cannot see the error. Three cases:
    - S3 objects: `buildTable` puts folders first. A later page that brings a new folder moves every object down by one row. Delete then acts on the wrong objects.
    - S3 buckets: select `b-tmp`, then create bucket `0new`. After the refetch, the selection is on `a-prod`.
    - EC2: the refresh after an SSM session keeps the old indexes.
  - Fix: key the selection and the cursor by resource ID inside `ui.Table` (A2). Show the names in delete prompts.

- [x] C4. Bulk bucket delete includes buckets that the filter hides. Evidence: reproduced. Fixed in #213. The filter deselects the rows it hides. The prompt lists the buckets and says that all versions are deleted permanently. The user types the bucket name, or `delete N buckets`. `EmptyAndDeleteBucket` deletes one page of versions at a time. The prompt does not give the number of versions, because that needs a full listing first.
  - Where: `internal/views/s3_list.go:393-409`. The prompt text is at `:345`.
  - Problem: Select `alpha-prod`. Filter to "test" and select `test-1`. Press `x`. The prompt says "Delete 2 buckets?" with no names. After the user types `confirm`, `EmptyAndDeleteBucket("alpha-prod")` runs. That call permanently deletes every object version and every delete marker. The prompt says only "This will delete all objects first". The function also loads all versions into memory before it deletes them. `s3_objects.go:790` already limits deletes to visible rows, but `s3_list` does not.
  - Fix: act only on visible selections. List the bucket names in the prompt. For a bucket delete, make the user type the bucket name, and say that N versions will be permanently deleted. Delete page by page.

## High

- [x] H1. SQS peek hides messages and can move them to the DLQ. Evidence: traced in SDK source, and the request body was captured. Fixed in #199. ReadOnly mode now blocks a peek on a queue with a redrive policy.
  - Where: `internal/aws/sqs.go:195-205`, `internal/views/sqs_messages.go:386-400`, the banner at `sqs_messages.go:647`, and `cmd/seed/sqs.go:282-286`.
  - Problem: `ReceiveMessages` sets `VisibilityTimeout: 0`. The SDK sends this field only when it is not zero (`sqs@v1.42.25/serializers.go:2100`). So the default visibility timeout of the queue applies: 30 seconds by default, up to 12 hours. Each `l` or `r` press hides up to 10 messages from real consumers and adds 1 to their receive count. On a queue with a redrive policy and a low `maxReceiveCount`, a few peeks make the next real receive move the message to the dead-letter queue (DLQ) without processing. On FIFO queues, a peek also locks the message group. Peek works in ReadOnly mode. The banner says "VisibilityTimeout=0", which is false. The seed tool sets the queue default to 0, which hides the bug in LocalStack.
  - Fix: right after the receive, call `ChangeMessageVisibilityBatch` with `VisibilityTimeout` 0 for the received handles. The receive count still goes up, so block peek in ReadOnly mode, or warn when a message is near `maxReceiveCount`. Fix the banner. Remove the workaround in the seed tool.

- [x] H2. Global keys act while the user types in a filter or input. Evidence: reproduced. Fixed in #214. Views implement `nav.InputCapturer`. While a filter or another input is active, the app gives the view every key except `ctrl+c`. The guarded AWS services refuse the copy in the ReadOnly bypass below.
  - Where: `internal/app/app.go:438-492`.
  - Problem: The app handles global keys before the view gets the key. Only `FormView` is exempt. In a filter, or in the S3 copy and move input, these keys misfire:
    - `q` closes the view.
    - `W`, `T`, `P`, `R`, `L`, `:`, and `?` open pickers or overlays.
    - `tab` moves the focus to the panel.
    - `backspace` never deletes a character, because `:486-491` returns even when the history is empty.
  - ReadOnly bypass: in ReadWrite mode, press `c` to copy. Press `W` and choose ReadOnly. Press `enter`. `CopyObject` still runs. Move and Create bucket act the same way.
  - Fix: add an `InputCapturer` interface for views (A3). When the view captures input, skip all global keys except `ctrl+c`. Test ReadOnly again when the action runs.

- [x] H3. Results from the old region or profile appear in the new view. Evidence: reproduced. Fixed in #201.
  - Where: `applyProfile` at `app.go:854-895`, `applyRegion` at `app.go:944-985`, `ec2_list.go:323`.
  - Problem: Open EC2 in one account, and switch the profile or the region before the load ends. The old page is appended to the new list, so one table mixes two accounts. The next-page token from the old account then goes to the new client and fails. No message carries a session ID or a request ID.
  - Fix: add a session generation to the app and to each routed message (A1).

- [x] H4. Async results go only to the view on top. Evidence: reproduced. Fixed in #201.
  - Where: `app.go:537`, `internal/nav/navigator.go:76-84`.
  - Problem: Results for a view that is not on top are lost, or they reach the wrong view:
    - Stop an instance and press `q` before the call returns. Home receives `ec2InstanceMutatedMsg` and drops it. No toast and no event log entry show. The cached EC2 view keeps the "stopping" state and a frozen spinner.
    - A page for `subnet_list:vpc-A` is appended to the list for vpc-B. `S3Versions` has the same problem across keys.
    - A view that is still loading starts a second fetch when the user opens it again. Rows then appear twice, for example "4/4" for 2 instances.
  - Fix: A1.

- [x] H5. Two refreshes in a row duplicate rows. Evidence: reproduced. Fixed in #201 with a load generation in the EC2, VPC, subnet, SQS queue, and S3 object lists.
  - Where: `ec2_list.go:316`, `vpc_list.go:151`, `subnet_list.go:177`, `sqs_queues.go:417`, and `s3_objects.go`.
  - Problem: Press `r` twice, or press `r` during a load. Both fetch chains append to the list. `sqs_queues` duplicates rows even with one page. The bucket and prefix guard in `s3_objects` does not stop this case.
  - Fix: put a load generation in every page message, and drop pages from an old generation (A1).

- [x] H6. A failed AWS client build crashes the app. Evidence: reproduced. Fixed in #215. `aws.NewServices` returns the error and no services. Without a session at startup, the app shows the error on Home and opens no AWS view. A failed switch keeps the old session and shows an error toast. #199 adds a nil guard for SSM Connect only.
  - Where: `app.go:89-99`, `:861-868`, `:951-958`.
  - Problem: When `aws.NewClient` fails, for example with `--profile typo`, the app logs the error and keeps a nil `*Client`. `aws.NewS3Service(nil)` returns a non-nil interface that holds the nil pointer. So `svc == nil` guards such as `s3_objects.go:226` and `ec2_list.go:171` never fire. The first AWS call panics in a command goroutine, and Bubble Tea exits. `applyProfile` already showed a "Profile: x" success toast. SSM Connect dereferences the nil client inside `Update`.
  - Fix: if the build fails, keep the old client, do not apply the change, and show an error toast (A4).

- [x] H7. The S3 versions view panics and shows the wrong version. Evidence: reproduced. Fixed in #199.
  - Where: `internal/views/s3_versions.go:182-192`.
  - Problem: `v.VersionID[:8]` panics when the ID is `"null"`, which an unversioned bucket returns. The app then exits. The handler also indexes `versions` with the sorted cursor position. After a sort, it shows the JSON of a different version.
  - Fix: shorten the ID safely. Find the version by ID, not by row position.

- [x] H8. The default region overrides the region of the profile. Evidence: reproduced. Fixed in #199.
  - Where: `internal/config/config.go:40` and `:134`, `internal/aws/client.go:27`.
  - Problem: `DefaultConfig` sets `Region: "us-east-1"`, and `NewClient` always passes it to `config.WithRegion`. A profile with `region = eu-west-2` in `~/.aws/config` shows us-east-1 resources. `ApplyEnv` reads `AWS_REGION` but not `AWS_DEFAULT_REGION`, and the default then overrides the SDK.
  - Fix: default to an empty region and let the SDK resolve it. Show `client.Config.Region` in the header.

- [x] H9. A dependency has a known vulnerability. Evidence: govulncheck. Fixed in #199. The same change updates `golang.org/x/sys` to v0.44.0 for GO-2026-5024, which the code does not call.
  - Problem: GO-2026-5764 is a panic in the EventStream decoder that allows denial of service. It affects `aws-sdk-go-v2/service/s3 v1.97.1` and `aws/protocol/eventstream v1.7.7`. The code reaches it through S3 calls.
  - Fix: update to `service/s3 >= v1.97.3` and `eventstream >= v1.7.8`.

- [x] H10. CI and releases build with an old Go toolchain. Evidence: govulncheck under go1.26.2, and the first CI run of #199 failed on 19 standard library vulnerabilities. Fixed in #199.
  - Where: all three workflows use `setup-go` with `go-version-file: go.mod`.
  - Problem: This installs exactly `go 1.26.2`. Under that toolchain, govulncheck finds 20 reachable vulnerabilities: 19 in the standard library (fixed in 1.26.6 to 1.26.9) and H9. Release binaries use the same toolchain. A local run shows only H9 when the local Go is newer.
  - Fix: use `go-version: "1.26.x"` with `check-latest: true`, or add a `toolchain` line to `go.mod`.

- [x] H11. No scan runs when the repository is idle. Evidence: GitHub settings and workflow files. Fixed in #202 with a weekly schedule, `workflow_dispatch`, and `dependabot.yml` (I8). The weekly run skips lint and gitleaks, because they use no advisory data. Dependabot alerts and security updates are on in the repository settings.
  - Problem: Security scans run only on push and pull request. The last run was in April 2026. Dependabot alerts and Dependabot security updates are off, and there is no `dependabot.yml`. GitHub also turns off scheduled workflows after 60 days without activity in a public repository.
  - Fix: add a weekly `schedule` and `workflow_dispatch` to `security.yml`. Add `.github/dependabot.yml` (I8). Turn on Dependabot alerts and security updates.

- [x] H12. The latest release is old and vulnerable. Evidence: GitHub releases. Fixed: v0.5.0 was released on 2026-10-11, built with go1.26.9.
  - Problem: v0.4.0 (2026-04-07) contains the vulnerable SDK and was built with Go 1.26.1. 29 commits are not released, including VPC and subnet browsing and the region-switch fix.
  - Fix: after H9 and H10, release v0.5.0.

## Medium

- [x] M1. Fixed in #223. `DeleteObjects` returns a `*DeleteObjectsError` that lists each key that S3 did not delete, with its error code. A batch call that fails does not stop the other batches. The view loads the list again, because S3 deleted the other keys, and the toast names the failed keys. `DeleteObjects` ignores per-key errors. Evidence: reproduced with an httptest server. `internal/aws/s3.go:345-354` uses `Quiet: true`, so the response holds only the failures, and the code never reads `out.Errors`. Keys that were denied or locked are reported as "Deleted N object(s)". Fix: read `out.Errors` as `DeleteObjectVersions` already does, and join them with `errors.Join`.

- [x] M2. Fixed in #223, all five parts. (a) `copySource` percent-encodes every byte outside the unreserved set. (b) `CopyObject` looks for the destination with `HeadObject` and also sends `IfNoneMatch: "*"`. When the destination exists, the view asks before it replaces it, and the user types "confirm". (c) A destination that ends with `/` keeps the object name in that folder. (d) An object over 5 GB is copied with `UploadPartCopy`, and a failed part aborts the upload. (e) The copy sends the storage class of the source. Before the fix, LocalStack showed (a) `NoSuchKey`, (b) a silent overwrite, (c) an object named `archive/`, and (e) STANDARD. S3 copy and move have five problems. Evidence: reproduced for (a). Where: `internal/aws/s3.go:519-543`, `internal/views/s3_objects.go:550-565`.
  - (a) `CopySource` is not URL-encoded. Keys with `%`, `?`, `#`, or `+` fail or copy the wrong object.
  - (b) An existing destination is overwritten without a warning. Use `CopyObjectInput.IfNoneMatch: "*"`.
  - (c) A folder destination such as `archive/` creates an object named `archive/`. `ListObjectsPage` skips `key == prefix` (`s3.go:193`), so the moved data disappears from the UI.
  - (d) Objects over 5 GB fail, because they need `UploadPartCopy`. The copy fails before the delete, so no data is lost.
  - (e) The storage class resets to STANDARD.

- [ ] M3. AWS calls have no timeout and no cancel. Evidence: traced. About 40 call sites in views use `context.Background()`. The default SDK HTTP client has no overall timeout. A stalled connection keeps the spinner running forever. Downloads and bucket empties cannot be stopped, and they continue after navigation or a profile switch. Fix: give each view a context that is cancelled when the view is popped or the session changes. Use `context.WithTimeout` for list and describe calls. Set `config.WithRetryMaxAttempts`.

- [ ] M4. The bucket region lookup is slow and incomplete. Evidence: traced in SDK source. Where: `internal/aws/s3.go:72-104`. `GetBucketLocation` runs before every operation without a cache, which doubles the calls per page. The legacy value `"EU"` is not mapped to `eu-west-1`. The SDK docs say that cross-account calls fail outside the home region of the bucket, and that the API is "no longer a best practice". An unpaginated `ListBuckets` fails for accounts with a bucket quota over 10,000. Fix: use paginated `ListBuckets` (`MaxBuckets`, `ContinuationToken`), which returns `BucketRegion`, or `HeadBucketOutput.BucketRegion`. Cache the result.

- [x] M5. Fixed in #218, as far as SQS allows. `DeleteMessageBatch` returns a `*BatchDeleteError` with each failed handle and its code, also when one call of 10 fails. The rows stay until the result arrives, and only the deleted messages leave the list. On a standard queue, SQS gives no way to detect an old handle, so the prompt says so and the toast says "SQS accepted the delete". SQS delete with an old receipt handle reports success. Evidence: SDK docs. Where: `internal/views/sqs_messages.go:552-583`. On a standard queue, `DeleteMessage` with an old handle succeeds, but the message can stay in the queue. FIFO queues return `ReceiptHandleIsInvalid`. The view removes the rows before the call and does not restore them on failure. It also drops the details of failed entries.

- [x] M6. Fixed in #221: the seed tool stops unless the endpoint host is local or `--allow-remote` is set, and unless `/_localstack/health` returns a LocalStack health report. All its HTTP calls have a timeout. The seed tool has no guard against real AWS. Evidence: traced. Where: `cmd/seed/main.go:18` and `:37`, `internal/aws/client.go:32`. With `-endpoint=""`, for example from an unset variable, the tool uses the normal credential chain and `AWS_PROFILE`. It then creates real VPCs, security groups open to 0.0.0.0/0 on ports 80 and 443, buckets, and queues. The `-wipe` request (`http.Post`) has no timeout. Fix: require an endpoint host of `localhost`, `127.0.0.1`, or `localstack`, or an `--allow-remote` flag. Call `/_localstack/health` first.

- [x] M7. Views report partial failures wrongly. Evidence: reproduced for SQS. Fixed in #218. Queue purge and delete and bucket delete try each item and report "Deleted 2 of 3 queue(s). Failed: name (code)". Only the changed queues are refreshed or removed, and the bucket list loads again. A failed EC2 action refreshes its instances instead of keeping the guessed state.
  - `deleteQueues` returns only `urls[0]` (`sqs_queues.go:684`), so the other deleted queues stay in the list. It stops at the first error and does not say which queues succeeded. Purge acts the same way.
  - `deleteBuckets` keeps only the last error and replaces the whole table with an error screen.
  - `TerminateInstances` can partly succeed. On error, the view keeps the "shutting-down" state and does not refresh (`ec2_list.go:265-273`).
  - Fix: return a result per item, show "Stopped 3/4" with the failed names, and refresh only the changed items.

- [x] M8. The selection marker is written into cell 0. Evidence: reproduced. Fixed in #213: the marker is display only, and views read the row key. `internal/ui/table.go:397` prefixes `"✓ "` to the first cell of each selected row. Each `row[0]` lookup then breaks on a selected row: EC2 details and SSM, S3 Browse and Properties, SQS details, peek, and send, the messages view, and the `y` copy-ID key in the security group, VPC, and subnet views, which copies `"✓ sg-…"`. Fix: draw the marker outside the cell data (A2).

- [x] M9. SQS Send Message uses the cursor queue, but Purge and Delete use the selection. Fixed in #218: Send uses the same target, and the menu offers it only for one queue. Evidence: reproduced. Where: `internal/views/sqs_queues.go:243`.

- [ ] M10. Cross-resource focus lands on the wrong row. Evidence: reproduced. The first two points are fixed in #213: focus uses `SetCursorByKey`, `sqs_queues` handles `FocusResourceMsg`, and a link to the view on top does not push it again. The AMI point is open.
  - `SetCursor(i)` uses the unsorted index. When the list is sorted, the panel shows `sg-a` while the cursor is on `sg-c`. In EC2, a following `m` acts on the cursor row.
  - `sqs_queues` emits focus links to itself, but it has no `FocusResourceMsg` handler. A link also pushes a second copy of the view on the stack.
  - AMI focus does nothing for AMIs that the account does not own. Most instances use such AMIs.

- [x] M11. Cross-navigation history restores a panel into the wrong view. Evidence: reproduced. Fixed in #215. Back traversal pops to the saved depth. `resetNav()` clears the panel, the stash, and the history for a session switch, a theme switch, and `:home`. The stash case of M12 (A6) is still open.
  - `popCrossNavBack` (`app.go:727-741`) pops only one level. For EC2, then a link to VPCs, then Subnets, `backspace` opens the EC2 instance panel over the VPC list.
  - `:home` (`app.go:560-564`) keeps the panel open and keeps the history. `applyTheme` (`app.go:987-1015`) keeps the history. In both cases, `backspace` on Home restores an old resource panel.

- [ ] M12. Panel state leaks across views. Evidence: reproduced. The late `TabbedContentMsg` case is fixed in #201. The stash and `L` cases are open (A6).
  - The app clears the stashed panel only when `panelOpen` is true (`app.go:174`, `:264`, `:443`). Make the terminal narrow, navigate, then make it wide. The old panel opens over the new view.
  - The `L` key pushes the event log with the panel still open (`app.go:470`).
  - `fetchMetadataAndNavigate` returns `TabbedContentMsg` directly (`s3_objects.go:861-882`). A late result opens and focuses a panel over an unrelated view.

- [x] M13. Errors go to the wrong view and stay on screen. Evidence: reproduced. Fixed in #201. `app.go:230-234` sends each `ErrorMsg` to the current view. An S3 `HeadObject` error then hides the EC2 table. The app never clears `m.err` on navigation. `RenderStatusBar` (`statusbar.go:32-40`) shows that error in place of all key hints until `:home`, a session switch, or a restart.

- [x] M14. In quick-confirm mode, Enter means Yes. Fixed in #199. Evidence: traced. Where: `internal/ui/confirm.go:78`. Quick mode is used for "Discard unsaved changes?" and for the theme switch, which clears the navigation stack. The hint shows only `y` and `n`. Fix: make Enter mean No.

- [ ] M15. The content view cursor ignores line wrap. Evidence: reproduced. Where: `internal/ui/contentview.go:413-422` and `:648`. A minified one-line JSON of 38,000 characters wraps to 514 lines, but `G`, `j`, and `ctrl+d` keep the offset at 0, so the user cannot scroll. When a long line above the cursor wraps, the cursor goes off screen.

- [ ] M16. Chroma resets break the cursor and selection highlight. Evidence: reproduced. Chroma ends each token with `\x1b[0m`, which also removes the background. Only the first token and the padding get the highlight. The same resets break the dimming of the unfocused panel (`app.go:1113`). This is the same class of bug as #162. Fix: rewrite `\x1b[0m` to `\x1b[39;22;23;24m` in highlighted output.

- [x] M17. The table filter matches ANSI escape bytes. Evidence: reproduced. Fixed in #213. Where: `internal/ui/table.go:340`. The queries "2", "38", "39", and ";" match every row with a colored state. Fix: match on lowercase text with the escapes removed. Compute it once per `SetRows`. This also removes 23 MB of allocation per keystroke.

- [ ] M18. Form dirty tracking is wrong in both directions. Evidence: reproduced. Where: `internal/ui/form.go:190` and `:210-229`. huh stores a field value only when the focus moves forward. An untouched form with a default value reports dirty, so the user gets a false discard prompt. If the user types in field B, presses `shift+tab` to field A, and presses Esc, the form closes without a prompt, and the input is lost. Fix: keep references to the `huh.Field` objects and compare each `GetValue()` with the start value.

- [ ] M19. The picker has no height limit. Evidence: reproduced. Where: `internal/ui/picker.go:534`. The 23 regions make a dialog of 30 rows. On a terminal with 24 rows, the bottom is cut off, and the cursor on later regions is not visible. Long SSO profile lists make this worse.

- [ ] M20. The app does not support NO_COLOR or light terminals. Evidence: traced, not run. The selected table row (`table.go:53`) and the content cursor use only a background color. With NO_COLOR, no row looks selected. The app sets `isDark` (`app.go:149`) but never reads it. All four themes are dark. The filter text is `#ffffff` (`filter.go:592`), so it is invisible on a white background. Fix: also use reverse or bold, or a gutter marker. Add light theme variants.

- [ ] M21. AWS errors show as raw SDK strings. Evidence: traced. No code uses `smithy.APIError`. Expired SSO sessions and AccessDenied errors show the full SDK text. Fix: map common error codes to hints, for example "run `aws sso login --profile X`".

- [x] M22. SQS queue attribute errors are swallowed, and DLQ detection is wrong. Fixed in #218. `Queue.Err` keeps the error of each queue. The list shows `?` counts and a toast names the queues. The messages view uses `ListDeadLetterSourceQueues`. Evidence: traced and nilerr. `FetchAllQueueAttributes` (`internal/aws/sqs.go:174`) drops `GetQueueAttributes` errors, so a denied or throttled queue shows 0 messages. The messages view decides "is a DLQ" from `RedriveAllowPolicy != nil` (`sqs_messages.go:182`), so most DLQs cannot start a redrive. Fix: return the error per queue. Use `ListDeadLetterSourceQueues`.

- [x] M23. Fixed in #224. `ui.Sanitize` and `ui.SanitizeLines` show each control character as a visible symbol, for example ESC as `␛`, and a C1 control as `�`. The table, the content view and the detail panels, toasts, confirm prompts, pickers, the header, and the event log use them. The views use them for footers, error lines, and the names in confirm prompts. Table cells hold plain text, and the new column `Style` colors a cell, for example a state or an SQS count. As a second guard, `ui.StripUnsafe` removes string sequences and C1 controls from each screen. Before the fix, LocalStack showed these results. A DCS query (`ESC P $ q m ESC \`) in an S3 object preview reached the terminal. A terminal answers this query with input to the app. The C1 control U+009B (8-bit CSI) in an S3 key, an SQS message body, and a VPC tag reached the terminal and turned on reverse video. A carriage return in a preview made a false line. OSC 52 did not reach the terminal. The renderer at this commit drops OSC and CSI sequences, but it passes the string sequences DCS, SOS, PM, and APC, and C1 controls, on unchanged. SQS refuses ESC in a message body, and an XML response cannot hold it, so ESC comes mainly through S3 object content. C1 controls are valid in XML and JSON, so they come through every service. Known limit: the log file is JSON, which escapes C0 controls but not C1 controls. #224 missed the error in the status bar. A line feed in a key split the status bar into two lines. This is fixed in #225. Terminal escape sequences in AWS data reach the terminal. Evidence: reproduced in LocalStack (was: plausible). SQS message bodies, S3 keys, and tags keep their control characters. The ultraviolet renderer keeps unknown escape sequences in cell content, for example OSC 52, which writes to the clipboard. Fix: remove control characters from all AWS strings before display.

- [x] M24. Fixed in #223. EC2 Start and Redrive ask with a y/n prompt (`RequestConfirmMsg.Quick`). `X` deletes every item that the list shows, and the user types `delete N items`. The move overwrite is fixed with M2(b). Redrive and EC2 Start have no prompt. Evidence: traced. Move overwrites the destination without a prompt (M2). `X` ("delete all") does the same as `x`.

- [ ] M25. Partly fixed in #223. A download never replaces a file. It writes to `name (1).ext`, `name (2).ext`, and so on with `O_EXCL` and mode 0600. It removes a partial file after an error, and the toast shows the written path. Progress and cancel are open. They belong with M3. Download overwrites files without a warning. Evidence: traced. `DownloadObject` writes to `~/Downloads/<basename>`, so `a/x.pdf` and `b/x.pdf` collide. It leaves a partial file on error, uses mode 0644, and has no progress and no cancel. Path traversal is not possible, because the code uses `filepath.Base`.

- [x] M26. Fixed in #223. `ListObjectVersions` reads pages until a page holds a sibling key. `ListOwnedAMIs` reads every page. `SearchAMIs` reads pages until it has 100 results or read 20 pages. `ListInstancesPage` sets `MaxResults` to 1,000. In LocalStack, an object with 1,005 versions shows 1,005 versions. The base build shows 1,000. Pagination is incomplete for versions and AMIs. Evidence: traced. `ListObjectVersions` (`s3.go:562`) reads one page with `Prefix=key`, so results stop at 1,000 and include sibling keys. Use `NewListObjectVersionsPaginator`. `SearchAMIs` sets no `Owners`, so it scans all public images, and it reads one filtered page of 100. Such a page can come back almost empty while `NextToken` is set (plausible). `ListOwnedAMIs` is not paginated. `ListInstancesPage` sets no `MaxResults`.

- [x] M27. ReadOnly is not enforced in one place. Evidence: traced. Fixed in #214. The app blocks every `ModeReadWrite` key of the view on top, and the 10 key-press checks of `ui.ReadOnly` in the views are gone. `aws.GuardS3`, `aws.GuardEC2`, and `aws.GuardSQS` refuse the 22 mutating methods when the action runs. `ui.ReadOnly` is now an atomic value behind `ui.IsReadOnly()` and `ui.SetReadOnly()`, because the guards read it from command goroutines. ReadOnly depends on 11 hand-written `if ui.ReadOnly` tests at key press. No prompt or picker callback tests ReadOnly again. `ui.ApplyModeAll` changes copies of the bindings (`keys.go:49`), so disabled bindings still match. Fix: A3.

- [x] M28. Fixed in #225. `ListObjectsPage`, `ListAllKeys`, `ListObjectVersions`, and `firstVersionPage` send `EncodingType: url` and decode each key and prefix with `url.QueryUnescape`, as the AWS CLI does. `ListObjectVersions` sends the key itself as the marker for the next page, not `NextKeyMarker`. It stops with an error when the next page starts at the same version again. The fix also covers deletes. The SDK writes U+FFFD in a request body for a character that XML 1.0 cannot hold. So `DeleteObjects` for a key with ESC deleted a different key, and S3 reported success. `DeleteObjects` and `DeleteObjectVersions` now delete such a key with its own `DeleteObject` call. `EmptyAndDeleteBucket` stops with an error when the first entry of a round comes back, instead of looping. Before the fix, LocalStack showed these results. A bucket with one ESC key did not list, and its bucket delete failed. A key with a CR showed as `␊`, and its preview failed with 404 NotFound. `DeleteObjects` for the ESC key reported success, but the object stayed. After the fix, the list shows the ESC key as `␛` and the CR key as `␍`, and the preview opens. The folder delete, the key delete, and the bucket delete remove the right keys. LocalStack limits: with `EncodingType: url`, LocalStack 2026.9.1 sends `NextKeyMarker` without encoding, so the SDK cannot read a truncated versions page that ends with an ESC key. LocalStack also compares the key marker with the encoded key. So in LocalStack, the versions view of a key that URL encoding changes, for example a key with a space, shows an error after 1,000 versions. S3 does not have these limits. One S3 key with a control character breaks the object list. Evidence: reproduced in LocalStack. `ListObjectsPage` and `ListAllKeys` (`internal/aws/s3.go`) call `ListObjectsV2` without `EncodingType`, so S3 returns the keys in XML 1.0, which cannot hold most C0 controls. With one key that holds ESC, the SDK fails with "XML syntax error on line 2: illegal character code U+001B", and the folder does not list at all. A key with a carriage return comes back with a line feed, because XML changes CR to LF. An action on that key then uses a key that does not exist. `ListObjectVersions` and `firstVersionPage` have the same problem (reproduced), so the versions view and the bucket delete fail too. Fix: send `EncodingType: url` and decode `Key`, `Prefix`, and `CommonPrefixes` with `url.QueryUnescape`.

## Low

- [ ] L1. `pushView` sizes the view with `height-5` (`app.go:595`) and ignores the footer that `chromeHeight` counts. The view draws 22 lines into 20, so the last 2 rows are cut off until the next resize.
- [ ] L2. The navigator cache never removes views (`navigator.go:35-43`). Content viewers get unique IDs, so the cache keeps each one forever, up to 1 MB each. When the ID is already on the stack, the navigator pushes the same pointer twice. This happens for `L` on the event log, VPC and subnet links, and SQS DLQ links.
- [x] L3. Fixed in #215: each `Home.Init()` starts a tick chain with a new generation, and the app calls it each time Home is shown again. The home animation tick has no generation (`home.go:44-55`). `:home` on Home starts a second tick chain, so the animation runs twice as fast. A theme or region reset from a deeper view never restarts the tick, so the animation stops.
- [ ] L4. Configuration gaps: flags can only turn booleans off, so nothing can turn nerd fonts back on. Read-write mode cannot be set in the file. Unknown keys such as `nerd_font` are ignored without a warning. A missing `--config` file is ignored without a warning. `--init-config` says "written" when the file exists. `AWS_ENDPOINT_URL` forces the dummy `test` credentials.
- [x] L5. Fixed in #215: `topLevelViewID()` returns the nearest service view of the registry under the top of the stack. The `topLevelViewID` prefix map (`app.go:1218-1236`) sends a subnet list back to VPCs. The event log, content, and form views fall back to Home.
- [ ] L6. Commands use `time.Sleep` for the EC2 refresh after 2 seconds and for redrive polling. `tea.Tick` is easier to test.
- [ ] L7. `TabbedPanel.SetSize` resizes only the active tab (`tabbedpanel.go:136`). Tabs that were opened before keep the old size.
- [ ] L8. `DistributeWidths` can overflow by one column when it shrinks columns, because of rounding. At width 40, the widest line is 41, so the border wraps the header.
- [ ] L9. In help, each printable key goes to the filter, so `j` and `k` do not scroll (`help.go:303`). A category that is not in the known list gets order 0 and a random position.
- [ ] L10. The legacy `ErrorStyle` (`styles.go:634`) keeps the Catppuccin red from startup. Ten views use it.
- [ ] L11. `ToastManager.View` ignores its `width` argument, so long toasts overflow.
- [ ] L12. The picker, the command bar, and the help filter accept only ASCII 32 to 126. Space arrives as `"space"`, so the input drops it. Paste is ignored.
- [ ] L13. The fallback for `IconShield` is an emoji. `THEMING.md` says not to use emoji.
- [ ] L14. The `Detail` component is not used, and its `View()` draws h+1 lines. Delete it or fix it.
- [ ] L15. `Client.ServiceEndpoint()` is not used.
- [ ] L16. A `Range` request on an empty object returns 416, so the preview of an empty text file can fail. Evidence: plausible.
- [ ] L17. A theme switch rebuilds the whole navigation stack (`app.go:995`) and loses loaded data. The cause is that `NewTable` and `StateColor` put theme colors into the row strings.
- [ ] L18. `resizeViews` drops the commands that views return. All 13 views return nil on `WindowSizeMsg` today, so this has no effect yet.
- [ ] L19. `main.go:90` calls `os.Exit` after a `defer` (gocritic `exitAfterDefer`). The deferred close of the log file does not run.
- [ ] L20. `.gitignore` lists `demo/`, but `demo/s3.tape` is tracked.
- [x] L21. Fixed in #199. `internal/aws/sqs.go:200` uses the deprecated `ReceiveMessageInput.AttributeNames`. The call already sets `MessageSystemAttributeNames`. This is the one golangci-lint issue.
- [x] L22. `go.mod` marks `charm.land/log/v2` as indirect, but `internal/eventlog` imports it. Run `go mod tidy`. Also, 26 files are not gofmt-clean. The `go mod tidy` part is fixed in #199. The gofmt part is fixed in #200.
- [x] L23. Opening a details panel clears the table sort. Evidence: reproduced in LocalStack. Fixed in #213. A panel makes the list narrower. When the narrower width tier has a different number of columns, the view calls `Table.SetColumns`, which set the sort to none. A DLQ link closes and opens the panel again, so the sorted-list part of M10 never applied to SQS. Now the sort stays on the column with the same title, at its new index.
- [x] L24. The confirm prompt shows only the first letter of its placeholder (`> t`). Evidence: reproduced in LocalStack. Fixed in #213. The text input had no width, and bubbles v2 then draws one character of the placeholder. Now the input is as wide as the placeholder.
- [x] L25. Fixed in #218 with `ui.SortKeyInt`. Count columns sort as text. Evidence: reproduced in LocalStack. The SQS queue list sorts Messages as 0, 10, 16, 6, 8. The sort values of Messages, In-Flight, and Delayed (`sqs_queues.go:761`) and of Receives (`sqs_messages.go:665`) are `fmt.Sprintf("%d")`. Fix: pad them with zeros, as `ui.SortKeyBytes` does for sizes.
- [x] L26. Fixed in #221: the seeder sends the messages only to an empty queue. The seeder sends the SQS messages again on each run, so the message counts double. Evidence: reproduced in LocalStack. `docs/DEVELOPMENT.md` says that the seeder is idempotent. Fix: send the seed messages only to a queue that the run creates, or only when the queue is empty.
- [ ] L27. A long toast runs past the right edge of the screen. Evidence: reproduced in LocalStack. `ToastManager.View` (`internal/ui/toast.go:129`) gets the screen width but does not use it, so the end of a long S3 key or download path is cut off. The S3 download toast now writes the home directory as `~`, which is enough for the usual path. Fix: wrap or shorten the toast text to the width.

## Performance

These numbers come from benchmarks on a Ryzen 7840HS. The benchmark file is `repro/ui/zz_bench_test.go.txt`.

| Case | Result |
|---|---|
| Content view, one `j` or `k` press, 1,000 lines | 44 ms, 30 MB |
| Content view, one `j` or `k` press, 10,000 lines | 369 ms, 305 MB |
| Content view, one `j` or `k` press, 1.1 MB JSON | 645 ms, 543 MB |
| Content view `View()` only, 10,000 lines, SoftWrap on | 16 ms |
| Content view `View()` only, 10,000 lines, SoftWrap off | 0.48 ms |
| Table `View()`, 100,000 rows | 0.22 ms |
| Table cursor down, 100,000 rows | 0.5 ms |
| Table `SetRows`, 100,000 rows, no sort | 1.1 ms |
| Table `SetRows`, 100,000 rows, sorted | 102 ms |
| Table filter keystroke, 100,000 rows | 107 ms |
| Table `ToggleSelect`, 100,000 rows | 103 ms |
| Progressive load of 100,000 sorted rows | 4.66 s of CPU on the UI loop |
| Home animation frame, 200×50 | 14.3 ms, 104,000 allocations |

- [ ] P1. The content view runs chroma syntax highlighting on every cursor move. `renderContent` (`contentview.go:571`) runs from every key case (`:433-462`) and from `SetSize` and `SetLinks`. Chroma takes 84% of the time. Opening a tab highlights the content three times. Fix: highlight once and cache the lines, keyed by content, format, and theme. Apply the cursor and selection with `viewport.StyleLineFunc`. Wrap the lines once with `ansi.Wrap` and turn off SoftWrap.
- [x] P2. Fixed in #213. At 100,000 rows, a filter keystroke takes 12 ms (was 113 ms), a toggle 1 ms (was 105 ms), and a paged load with `AppendRows` 0.66 s in total. See `internal/ui/table_bench_test.go`. The table sorts all rows again on each filter keystroke, selection toggle, and page load (`table.go:330-411`). `Filter.Update` also sends `FilterChangedMsg` for arrow and cursor keys (`filter.go:660`). Fix: cache the sorted order. Filter in one pass over it. On a toggle, change only the marker. On an append, sort the new page and merge it.
- [ ] P3. The home animation uses about 28% of one core at 20 frames per second, also when the user is idle (`animation.go:290-308`). It builds a new style and renders it for every cell. The motion happens in `View()`, so the speed depends on how many messages arrive. Fix: precompute the escape codes for each color pair. Move the motion into `Update`. Give each tick chain a generation (L3). Pause on `tea.BlurMsg`.
- [ ] P4. `GetBucketLocation` runs before each S3 call (M4).

## Tests

Coverage by risk: these files have the least coverage for their risk.

| File | Coverage |
|---|---|
| `internal/aws/ec2.go` | 2.9% of 341 statements |
| `internal/aws/s3.go` | 2.6% of 308 statements |
| `internal/aws/sqs.go` | 34%, parsers only |
| `internal/views/sqs_queues.go` | 0% of 514 statements |
| `internal/views/sqs_messages.go` | 0% of 345 statements |
| `internal/config/config.go`, `internal/aws/profiles.go`, `internal/ui/confirm.go`, `internal/ui/form.go`, `internal/ui/picker.go`, `internal/views/eventlog.go` | 0% |

Every S3 mutation handler has 0% coverage: `deleteObjects`, `resolveDeleteKeys`, `collectDeleteEntries`, `copyObject`, `moveObject`, `createBucket`, and `deleteBuckets`. EC2 `terminateInstances` and `app.applyMode` also have 0%.

- [ ] T1. The mocks are weak. `MockSQSService` is not used. In 7 of 9 view test helpers, the returned mock is not used. Only 2 tests call `AssertExpectations`, and 1 test calls `AssertCalled`.
- [ ] T2. Tests change the global `ui.ReadOnly` in 24 places, so tests cannot use `t.Parallel()`.
- [x] T3. Fixed in #214: app tests drive a confirmed delete and a copy input after a switch to ReadOnly. The ReadOnly tests look only at the toast on key press. No test drives a prompt callback. The KeyMap tests look at hint metadata, which supports the false idea that `ApplyModeAll` blocks keys.
- [ ] T4. The filter test part is fixed in #221: the test checks the final model, because on a slow runner the output stream does not contain "alpha" in one piece. Only 4 tests use teatest, all for S3. `TestTeatest_S3ListFilter` is weak (plausible): "alpha" also matches the filter prompt, and the diff renderer does not redraw removed rows, so the `!gamma` assertion passes even if filtering breaks.
- [x] T5. Done in #214 in `internal/app/input_test.go` and `internal/aws/readonly_test.go`. Add a table-driven ReadOnly guard test across all views and every ReadWrite key. Assert an error toast and no mock calls.
- [ ] T6. Add SQS view tests with `MockSQSService`: purge, delete queue, batch delete of messages, and redrive. Assert the exact arguments, then call `AssertExpectations`.
- [ ] T7. Add S3 delete resolution tests: folder expansion through `ListAllKeys`, selected rows that the filter hides, and the error path.
- [ ] T8. Add AWS layer tests with an httptest server. `NewClient("", "us-east-1", srv.URL)` uses `BaseEndpoint`, so no code change is necessary. Assert request bodies and headers (`VisibilityTimeout`, `CopySource`, pagination loops) and error handling (`out.Errors`, `Failed`). Cover 1,001 keys in two delete batches.
- [ ] T9. Region precedence tests exist in #199. The rest is open. Add configuration tests: `Load` with a missing file, bad TOML, and a partial file. `ApplyEnv` and `ApplyFlags` precedence with `t.Setenv`. `WriteDefault` must not overwrite and must use mode 0600. Region resolution from the profile.
- [ ] T10. Add LocalStack integration tests behind a build tag. Do not set the queue `VisibilityTimeout` to 0 there, or H1 stays hidden.

## CI, release, and dependencies

- [x] I1. Fixed in #216: CI runs `go test -race -shuffle=on -coverprofile=cover.out ./...` and prints the total coverage. CI runs tests without `-race`. Use `go test -race -shuffle=on -coverprofile=cover.out ./...`.
- [x] I2. CI has no format check. Fixed in #200 with the gofmt formatter only. Add `formatters: { enable: [gofmt, goimports] }` to `.golangci.yml`.
- [x] I3. Fixed in #216: the release job runs vet, the race tests, and govulncheck before GoReleaser, pins GoReleaser to `~> v2`, and attests the archives with `actions/attest-build-provenance`. `release.yml` publishes on any `v*` tag without tests. It uses `goreleaser-action@v6` with `version: latest`. Add a `go test` step, pin `version: "~> v2"`, and add `id-token: write` and `attestations: write` for `actions/attest-build-provenance`.
- [x] I4. Fixed in #216: every action is pinned to a commit SHA with a version comment, golangci-lint is pinned to v2.14.0, govulncheck is a tool in `go.mod` (v1.8.0, with an `allow` rule in `dependabot.yml`, because go.mod lists tools as indirect), and only the trivy job has `security-events: write`. Action pinning is not consistent. `checkout@v6`, `setup-go@v6`, `upload-sarif@v4`, and `goreleaser-action@v6` use tags, not SHAs. golangci-lint uses `version: latest`. Pin it to `v2.14.0`. govulncheck installs `@latest`. Pin it with `go get -tool golang.org/x/vuln/cmd/govulncheck@vX` and run `go tool govulncheck`. `security-events: write` applies to the whole workflow. Move it to the trivy job.
- [ ] I5. The lint configuration is light. Add these linters (hit counts with v2.14): `nilerr` (1), `noctx` (3), `exhaustive` with `default-signifies-exhaustive: true` (2), `errorlint` (0), `bodyclose` (0), `contextcheck` (0), `nolintlint` (0), `testifylint` (35, auto-fix), `gocritic` (28), `unparam` (7), and `modernize` (23, auto-fix). Do not add `revive` (192, mostly the "exported" rule), `goconst` (186), `wrapcheck` (101), or `perfsprint` (33). Also read all 14 `//nolint` comments again. One of them hid C1.
- [x] I6. Fixed in #216: `task lint` stops when golangci-lint is not `GOLANGCI_LINT_VERSION`, and the new tasks are `test:race`, `cover`, and `vuln`. `task lint` uses the golangci-lint version that is installed on the machine. There are no `test:race`, `vuln`, or `cover` tasks.
- [ ] I7. Release gaps: no Windows build (it compiles, but SSM needs a guard). No Homebrew tap (#41). `go install` prints the version "dev", so add a `debug.ReadBuildInfo()` fallback and document `go install`. The changelog filters (`^docs:` and others) match nothing, because commits do not use these prefixes. Use `changelog: use: github` with groups. No SBOM, no cosign signature, no provenance, no `-trimpath`, no `mod_timestamp`. The README demo GIF is commented out.
- [x] I8. Add `.github/dependabot.yml`. Done in #202. It updates Go modules (with `aws` and `charm` groups) and actions (one group) every week, and the LocalStack image every month. Version updates for Go modules and actions wait 7 days after a release (`cooldown`). Security updates do not wait.

- [x] I9. 16 of 17 direct dependencies are out of date. Only `huh` is current. Fixed in #210 with the Go updates that Dependabot proposed on 2026-10-12. Some newer releases in the table, for example bubbletea v2.1.0, are less than 7 days old. Dependabot proposes them when the cooldown ends. bubbletea v2.1.0 and the later updates came in #220.

  | Module | Current | Latest |
  |---|---|---|
  | `aws-sdk-go-v2/service/s3` | v1.97.1 | v1.114.3 |
  | `aws-sdk-go-v2` | v1.41.5 | v1.47.3 |
  | `aws-sdk-go-v2/service/ec2` | v1.295.0 | v1.338.4 |
  | `aws-sdk-go-v2/service/sqs` | v1.42.25 | v1.52.4 |
  | `charm.land/bubbletea/v2` | v2.0.2 | v2.1.0 |
  | `charm.land/bubbles/v2` | v2.1.0 | v2.2.1 |
  | `charm.land/lipgloss/v2` | v2.0.2 | v2.0.6 |
  | `pelletier/go-toml/v2` | v2.2.4 | v2.4.3 |
  | `stretchr/testify` | v1.11.1 | v1.12.1 |
  | `golang.org/x/sync` | v0.20.0 | v0.24.0 |

  bubbletea v2.1.0 fixes a deadlock in the shutdown signal goroutine and adds a fallback for a terminal size of 0x0. The LocalStack image was pinned to `2026.03.0`. #203 updated it to `2026.9.1`.

## Docs drift

- [ ] D1. `README.md:163`, `TESTING.md:8` and `:279`, and `DEVELOPMENT.md:111` describe integration tests. No file has a `//go:build integration` tag, so `task test:integration` runs the unit tests again.
- [ ] D2. `ARCHITECTURE.md:169-203` describes a "Context-Aware Action System" with `ActionProvider` and `Actions()`. This code does not exist. The real mechanism is `HintBinding` and `KeyMap()`.
- [x] D3. Fixed in #214. `KEYBINDINGS.md:50` and `:58` say that disabled ReadWrite bindings never match. This is false. The only guards are the `if ui.ReadOnly` tests in each view (M27).
- [x] D4. Fixed in #214. `KEYBINDINGS.md:59` says that the help overlay filters by mode. It shows all hints with an `[RW]` badge (`help.go:160-172`).
- [ ] D5. `README.md:107` says that `W` toggles the mode. It opens a picker (`keys.go:82`).
- [ ] D6. The README keybinding table says that `s` sorts. On Home, `s` opens the service picker (`keys.go:93`). The table does not list `L`, `T`, `P`, `R`, or `backspace`.
- [ ] D7. The VPC, subnet, and security group views are not in `README.md:39`, `services/aws/ec2.md`, or `ARCHITECTURE.md:131-158`. The navigation tree in `ARCHITECTURE.md` lists Volumes and Key Pairs, which do not exist.
- [x] D8. Fixed in #215. `ARCHITECTURE.md:212` says that profile and region changes call `ClearCache()`. They replace the navigator (`app.go:874`, `:964`).
- [ ] D9. `services/aws/ec2.md:136` says that `?` searches public AMIs. The key is `p` (`ami_list.go:48`).
- [ ] D10. `TESTING.md:146` says that EC2 has 8 methods and S3 has 17. The interfaces have 21 and 18. `TESTING.md:189` names `ec2InstancesLoadedMsg`. The real type is `ec2PageLoadedMsg`.
- [ ] D11. `DEVELOPMENT.md:7` says Go 1.22 or later. `go.mod` needs 1.26.2. `DEVELOPMENT.md:57` does not list `sqs` in the seed `--service` values.
- [ ] D12. `UI_COMPONENTS.md` and `THEMING.md` have small errors: glyphs (`▀` and `▄`, fallback icons), the hint separator, toast timeouts, and log colors.
- [ ] D13. `BUBBLETEA.md` is the original bootstrap spec, not a tutorial. It shows an old tree without SQS, a seed script that was never built, and a type named `EC2ListView`. Delete it. `CHARM.md` is a general layout reference for v2.0.x. Move it to `docs/reference/` with an "as of" note.
- [x] D14. Fixed in #214: `ui.DefaultTableKeyMap()` binds a half page to `ctrl+d` and `ctrl+u` only. The docs list `d` and `u` as half-page keys. The bubbles table binds them, but they collide with Describe and Presign.
- [x] D15. Fixed in #223: `services/aws/ec2.md` says that `?` opens the public AMI search. The key is `p`. The page also said that EC2 Start runs without a prompt.

## Architecture

`internal/app/app.go` holds the router, the layout, the view factory, the panel state machine, the cross-navigation history, the session and client lifecycle, and the dispatch for pickers and prompts by string ID. To add one service, a developer changes 6 places in `app.go`: a field, `New`, `applyProfile`, `applyRegion`, `resolveView`, and `topLevelViewID`. Issue #194 happened because two of these were missed.

Three gaps cause most of the bugs above:

1. Messages have no address. Each async result goes to the view on top of the stack.
2. The table identifies rows by position, and it draws the selection marker by changing the data.
3. ReadOnly depends on per-view tests at key press, and global keys run before the view.

- [x] A1. Addressed messages. Done in #201, with view identity in place of a session generation (each session builds a new navigator). Wrap command results in `msg.Routed{ViewID, Gen, Msg}` with a helper such as `nav.Tag(viewID, gen, cmd)`. Keep a session generation on the model, and increase it on each profile or region switch. Drop results from an old generation. Deliver other results to the cached view, also when it is not on top. Open panels and overlays only when the source view is current. Add a load generation per view for refresh. Fixes C2, H3, H4, H5, M12, and M13.
- [x] A2. Table keyed by resource ID. Done in #213 with `ui.Row`, `SetRows`, `AppendRows`, `CursorKey`, `SetCursorByKey`, `SelectedKeys`, and `TargetKeys`. The configurable table KeyMap is not done. It belongs with the input work in A3. Add `SetRowsKeyed`, `SelectedKeys`, `SetCursorByKey`, and `AppendRows`. Draw the marker outside the cell data. Allow only visible selections. Cache the sorted order and the search text. Make the table KeyMap configurable. Fixes C3, C4, M8, M10, M17, and P2.
- [x] A3. Input capture and a central ReadOnly guard. Done in #214, with ReadOnly guards on the AWS services. The table KeyMap from A2 is configurable with `Table.SetKeyMap`. Add an `InputCapturer` interface that replaces the `FormView` special case. Test ReadOnly again when an action runs. Consider ReadOnly decorators for the AWS services. Fixes H2 and M27.
- [x] A4. AWS services bundle. Done in #215 with `aws.Services`, `aws.NewServices`, `switchSession()`, and `resetNav()`, and it closes #195. There is no session generation: as in A1, each session builds a new navigator, so view identity does the same job. Add `aws.Services` and one `switchSession(profile, region)` function (#195). Build all services from one client and handle the error. Increase the session generation. Clear the panel, the stash, the history, and `err` in one `resetNav()`, which also calls `home.Init()`. Fixes H6, M11, and L3.
- [ ] A5. Data-driven routes. Use `map[viewID]struct{Factory func(*aws.Services, params) nav.View; Root string}`. This replaces `resolveView`, `topLevelViewID`, and the sync test. Fixes L5.
- [ ] A6. Panel controller. Move open, close, stash, and history into a `panelController` type. Key the stash and the history by owner view ID and depth. Back traversal pops to `entry.navDepth`.
- [ ] A7. Overlay stack. Define an `Overlay` interface (`Visible`, `Update`, `View`, `SetSize`) and keep overlays in a stack. Use typed replies with a `ReplyTo` view ID in place of string IDs. Return a typed picker result in place of the values -1, -2, and `"_clear"`.
- [ ] A8. Theme context. Pass the theme, styles, ReadOnly, and nerd font state into components, and resolve styles at render time. A theme switch then does not rebuild the views (L17), and tests can run in parallel (T2).
- [ ] A9. Shared query input. Use one helper for unicode, space, and paste (L12), and one fuzzy matcher. `fuzzyMatch` and `cmdFuzzyMatch` are the same function.
- [ ] A10. Generic resource list. About 2,400 lines of shared structure are copied across 7 views: ec2 501, sqs_queues 524, ami 351, s3_list 334, subnet 241, vpc 239, sg 220. The code that is specific to each view is only 60 to 305 lines per file. After renaming, the `Update` functions of the VPC and subnet views differ in 13 of about 120 lines. A `ResourceList[T]` makes a new list view about 100 to 150 lines. Do this after A1 to A3, so that each fix exists in one place.

  ```go
  type ListSpec[T any] struct {
    ID, Title, Noun string
    Key     func(T) string
    Columns func(ui.WidthTier) []ui.Column
    Row     func(T, ui.WidthTier) (cells, sortKeys table.Row)
    Fetch   func(ctx context.Context, tok *string) ([]T, *string, error)
    Refresh func(ctx context.Context, ids []string) ([]T, error) // optional
    Detail  func(T) msg.TabbedContentMsg
    Actions []Action[T]   // Label, Destructive, Allowed(T), Run(ctx, []T) []error
    Keys    []ExtraKey[T] // EC2 'o', SQS 'p', VPC 'n', AMI search
  }
  ```

  The base type owns the keymap, filter, sort, spinner, footer, error view, width tiers, paging with view and generation guards, selection by ID, deferred focus, selective refresh, copy ID, `InputActive()`, ReadOnly at key press and at run time, prompts that list names, and per-item results. Each view supplies columns, rows, detail tabs, fetch functions, actions, and extra keys. Migration order: port sg, vpc, and subnet first. Then add actions and port ec2 and sqs_queues. Then port AMI, with search as a second fetch. Keep s3_objects and sqs_messages custom, but reuse the helpers. Write one conformance test suite that runs against every spec.
- [ ] A11. Use one `contentSize()` function for `pushView` and `resizeViews` (L1). Remove content views from the cache when they close (L2).

## ReadOnly coverage

This table lists every mutating action at `f107757`, with the changes from #199. Since Phase 4 (in #214), the app blocks each ReadWrite key centrally, and the guarded AWS services refuse each mutating call in ReadOnly mode when the action runs. The "ReadOnly guard" and "Guard runs again at commit" columns show the state before Phase 4. The security group, VPC, subnet, AMI, and versions views have no mutations.

| View | Action | ReadOnly guard | Guard runs again at commit | Prompt | Prompt shows targets |
|---|---|---|---|---|---|
| ec2_list | Start | `m` key (`:480`) | No | None | Not applicable |
| ec2_list | Stop, Reboot, Terminate | `m` key | No | Type `confirm` | Count only |
| ec2_list | SSM Connect (`o`) | `o` key (since #199) | Not applicable | None | Opens a shell on the instance |
| s3_list | Create bucket | `n` key (`:321`) | No, and H2 bypasses it | None | Not applicable |
| s3_list | Empty and delete buckets | `x` key (`:335`) | No | Type `confirm` | Name for 1, else count. Includes hidden rows (C4) |
| s3_objects | Delete objects and folders | `x`, `X` (`:649`) | No | Type `confirm` | Counts. Folder contents are not shown |
| s3_objects | Copy, Move | `c`, `m` (`:691`, `:706`) | No, and H2 bypasses it | None | Overwrites the destination |
| s3_objects | Download | None | Not applicable | None | Overwrites `~/Downloads/<base>` |
| sqs_queues | Send message | `m` (`:568`) | The form blocks global keys | Form | Uses the cursor queue (M9) |
| sqs_queues | Purge, Delete queue | `m` | No | Type `confirm` | Count only |
| sqs_messages | Peek (`l`, `r`) | Blocked on queues with a DLQ (since #199) | Not applicable | None | Adds 1 to the receive count (H1) |
| sqs_messages | Delete messages | `x` (`:442`) | Handles are computed again at commit | Type `confirm` | Count only |
| sqs_messages | Start redrive | `m` (`:459`) | No | None | Not applicable |

## What works well

Keep these patterns when you make the changes above:

- The AWS layer uses interfaces. Each testify mock has a compile-time `var _` assertion, so the mocks match the interfaces. Views never import `app`.
- The registry is the single source for the home grid and the command bar. A sync test keeps it in line with `resolveView`.
- Overlays capture only key messages, so data loads continue behind them.
- `s3PageLoadedMsg` compares the bucket and prefix, and `S3Objects.Init` restarts a partial load. This is the right idea for A1.
- `s3_objects` already limits deletes to visible rows. This is the right idea for C4.
- ReadOnly is the default. Destructive actions need a typed `confirm`. The app writes files with mode 0600 and directories with mode 0750.
- Endpoints use `BaseEndpoint`, not the deprecated resolvers. Path style is on only for LocalStack. Most list calls paginate fully. Batch limits are respected (1,000 for S3 deletes, 10 for SQS), and `DeleteMessageBatch.Failed` is read.
- The FIFO send form handles `MessageGroupId` and `DeduplicationId`, including content-based deduplication.
- The table hands rendering to bubbles, which draws about two times the visible rows. `ansi.Truncate` keeps escape sequences and wide characters whole. `BestFitTier` and `DistributeWidths` handle tiers, weights, and caps, and they have tests.
- `HintBinding` uses one definition for key dispatch, status bar hints, and help.
- Tests drive `Update()` directly and assert on the returned messages. They are fast, they use no sleeps, and 30 shuffled runs found no flaky test.
- The eventlog is guarded by a mutex, the ring buffer is bounded, and reads return copies.
