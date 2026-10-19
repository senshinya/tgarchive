# Video presentation timing implementation plan

> Execute inline with superpowers:executing-plans and test-driven-development.

**Goal:** Detect repeated regressions in decoded frame PTS, generate compatibility copies, and inspect the existing archive.

**Architecture:** Extend the existing ffprobe probe for H.264 MP4 with a bounded frame sample. Keep raw PTS separate from FFmpeg's repaired best-effort timestamps. Existing conversion, atomic installation, original downloads, and frontend fallback remain the integration points. Requeue previously skipped H.264 once using a transactional settings marker; preserve completed copies.

**Constraints:** Normal B frames remain eligible for original playback. Inspect the first video stream, excluding attached pictures. Probe failures remain retryable errors. Sample processes have deadlines. Ignore a few sample boundary frames and require repeated PTS regressions. No private archive media enters git. Deployment uses a versioned release image.

### Task 1: Timestamp detection and conversion regression

Files: `internal/transcode/transcode.go`, `internal/transcode/transcode_test.go`, `internal/transcode/worker_test.go`.

- [x] Add an integration test creating `testsrc2` with B frames and replacing only its `ctts` box type with `free`; assert `Needs(Probe(broken))`, preserve the original's checksum, and assert `!Needs(Probe(Convert(broken)))`.
- [x] Run `go test ./internal/transcode -run TestMissingCompositionTimes -v`; observe the existing probe's false negative.
- [x] Add `InvalidPTS bool` to `Streams`; return true from `Needs` for video with that flag. Probe only otherwise-playable H.264 MP4, using the selected stream index and a bounded sample of raw `frame.pts`. Require at least three backwards steps after trimming four frames at each boundary. Use a 30-second deadline.
- [x] Add coverage for normal reordered frames, missing PTS, duplicate timestamps, isolated boundary regressions, probe errors, and cancellation. Existing codec-only fixtures use keyed fields.
- [x] Run `go test ./internal/transcode -v` and commit the tested change.

### Task 2: One-time archive recheck

Files: `internal/store/compat.go`, `internal/store/compat_test.go`, `internal/transcode/worker.go`, `internal/transcode/worker_test.go`.

- [x] Write a failing store test: initial H.264 `none` becomes unchecked once; successful copies and other codecs remain; failures are retried; a second preparation preserves freshly checked `none` rows.
- [x] Replace startup preparation with a transaction: reset failures; insert settings marker `compat_timing_v1`; when inserted, reset only downloaded H.264 `none` video rows; commit marker and resets together.
- [x] Rename preparation to `PrepareCompatJobs` throughout worker and fakes. Run `go test ./internal/store ./internal/transcode` and commit.

### Task 3: Verification, audit and release preparation

Files: `README.md`, deployment documentation in the server workspace.

- [x] Verify full Go suite and `go vet ./...`; confirm the real synthesized corrupted MP4 regression fails against the old probe and passes with the new probe.
- [x] Finish read-only NAS audit of downloaded video media, recording sample counts, raw PTS regressions, existing compatibility copies, and explicit scan errors.
- [x] Review the whole branch with a fresh reviewer; fix material findings with a failing test first.
- [x] Document current detection/recheck constraints; prepare a versioned release and deployment, honoring existing authorization and release-only policy.
