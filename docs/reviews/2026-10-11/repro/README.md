# Repro tests

These tests come from the review on 2026-10-11. They are a snapshot of commit `f107757`. Each `TestRepro*` test passes while its bug exists, so the tests for fixed findings now fail. The tracker in `../README.md` shows which findings are fixed. Each fix adds its own regression test in the package, so do not copy a repro test for a fixed finding back into the code.

The files end in `.go.txt`, so that `go build` and `go vet` ignore them.

## Run a repro test

1. Copy the file into the package folder and remove `.txt`. For example, copy `views/zz_review_repro_test.go.txt` to `internal/views/zz_review_repro_test.go`.
2. Run `go test ./internal/views -run Repro -v`.
3. Delete the copy when you are done.

Do not copy `app/review_repro_test.go.txt` and `app/zz_review_repro_test.go.txt` into `internal/app` at the same time. Both files define the helper `kp`, so the package does not compile.

## Run the benchmarks

Copy `ui/zz_bench_test.go.txt` to `internal/ui/zz_bench_test.go`. Then run `go test ./internal/ui -run '^$' -bench . -benchmem`.

## Map of tests to findings

| Test | Finding |
|---|---|
| `TestRepro_S3DeleteResolvedRoutedToOtherBucket` | C2 |
| `TestRepro_S3ObjectsPrefixPageShiftsSelection`, `TestRepro_S3ListSelectionShiftsAfterCreate`, `TestReproSelectionIndexDrift` | C3 |
| `TestRepro_S3ListDeletesHiddenSelection` | C4 |
| `TestRepro_GlobalKeysStealFilterInput`, `TestRepro_FilterInputHijackedByGlobalKeys`, `TestRepro_S3CopyAfterSwitchToReadOnly` | H2 |
| `TestRepro_StaleRegionPageAppliedToNewView` | H3 |
| `TestRepro_AsyncMsgGoesToTopView`, `TestRepro_MutationResultDropped`, `TestRepro_ReenterWhileLoadingDuplicates`, `TestRepro_SubnetCrossViewPage` | H4 |
| `TestRepro_EC2DoubleRefreshDuplicates`, `TestRepro_SQSDoubleRefreshDuplicates` | H5 |
| `TestRepro_NilClientPanics` | H6 |
| `TestRepro_S3VersionsNullPanics`, `TestRepro_S3VersionsSortedIndex` | H7 |
| `TestRepro_DefaultRegionShadowsProfile` | H8 |
| `TestRepro_SQSMultiDeleteRemovesOnlyFirst` | M7 |
| `TestRepro_CheckmarkBreaksRowLookup` | M8 |
| `TestRepro_SQSSendTargetsCursorNotSelection` | M9 |
| `TestRepro_FocusCursorWrongWhenSorted`, `TestRepro_SQSFocusIgnored` | M10 |
| `TestRepro_CrossNavBackAfterDeeperNav`, `TestRepro_HomeCommandKeepsPanelAndHistory`, `TestRepro_ThemeKeepsHistory` | M11 |
| `TestRepro_StashRestoredOverOtherView`, `TestRepro_EventLogKeepsPanelOpen`, `TestRepro_LatePanelOverOtherView` | M12 |
| `TestRepro_ErrorMsgMisrouted` | M13 |
| `TestReproLongLineScroll`, `TestReproCursorOffscreenAfterWrap` | M15 |
| `TestReproCursorLineBg` | M16 |
| `TestReproFilterMatchesANSI` | M17 |
| `TestReproFormDirtyDefault`, `TestReproFormDirtyShiftTab` | M18 |
| `TestReproPickerHeight` | M19 |
| `TestRepro_PushViewIgnoresFooter` | L1 |
| `TestRepro_SamePointerTwiceOnStack` | L2 |
| `TestRepro_UnknownKeysIgnored`, `TestRepro_MissingExplicitConfig` | L4 |
| `TestReproTableOverflow`, `TestReproDistribute` | L8 |
| `TestReproHelpJK` | L9 |

The AWS layer repros (C1, H1, M1, M2) used a temporary httptest file that was not kept. T8 describes how to rebuild them.
