# 计划 1 遗留事项（供计划 2–4 参考）

计划 1 的逐任务审查与终审中被判定为可延后的次要问题，以及执行中做出的裁定。

## 裁定

- Ruling: execute on branch feat/p1-backend-core instead of a git worktree — fresh repo with only docs on main, a branch isolates equally — cost if wrong: none.
- Ruling: started execution without a separate plan-review pause — user said "完成后通过subagent执行" — cost if wrong: plan issues surface during task reviews instead.
- Task 3: Ruling: Important (plan-mandated) "ListBots/GetBotByTgID/SetBotStatus/SetBotAvatar untested in store" — no fix; exercised by later tasks' tests (T12 StartAll + fatal-error status, T13 avatars, T15 duplicate add) — cost if wrong: a store-level regression would surface one layer up instead of in store tests.
- Ruling: TG_API_ID/TG_API_HASH remain in .env — the official telegram-bot-api server needs them at process start; they are app credentials, not bot tokens or user login — cost if wrong: plan 4 would need the app to supervise the Bot API process.
- Task 6: Ruling: Important (plan-mandated) "edited_message for a soft-deleted message rewrites it and relinks media forever" — real; fix: Ingest ignores edits to deleted messages (no rewrite, no relink, offset still advances) — cost if wrong: deleted messages never reflect later edits (intended).
- Task 8: Ruling: Important (plan-mandated) "CleanOlderThan swallows os.Remove errors" — fix: log each non-ErrNotExist failure and return errors.Join of failures — cost if wrong: none material.
- Task 10: Ruling: Important (plan-mandated) "receipt state persisted even when SetReaction/Reply failed" — fix: persist only after the primary transport call succeeds (👀→seen, failure reply→failed, 👌→done); secondary calls only logged — cost if wrong: a permanently failing reply (e.g. message deleted in TG) is re-attempted on each later evaluation of that message (rare, bounded).
- Task 12: Ruling: Minor "removed/disabled bot can be flipped to running/error by a worker" promoted into fix round 1 — Task 15 admin flows depend on it; fix: run() loads bot first and returns quietly when removed or disabled — cost if wrong: none.
- Task 13: Ruling: Important (plan-mandated) "avatar size-selection not exercised by test (fake returns same file_id for both sizes)" — no fix in this task; avatar size is cosmetic and the fix needs a tgtest change; final review may batch it with the picker-ordering minor — cost if wrong: a slightly oversized/undersized avatar.
- Task 16: Ruling: two Important (plan-mandated) findings fixed in round 1 (main.go closes app before HTTP drain; restart test offset assertion satisfiable by a1) + Minor "prove getFile in flight before close" folded in — cost if wrong: none.
- Ruling: edit migration 0001 in place for the new UNIQUE(chat_id, source, origin_chat_id, tg_message_id) — no deployed data exists — cost if wrong: none (nothing deployed).
- Ruling: include minors 1 (receipt call timeout), 2 (LinkHandler returns error), 4 (CSRF backstop), 7 (patchBot bot.status event) in the single fix wave — cheap and they protect plans 2–4 — cost if wrong: small extra diff.
- Ruling: user-facing failure reply keeps the (redacted) error text rather than a generic message — the sender benefits from the reason — cost if wrong: internal detail visible to whitelisted senders only.

## 延后的次要问题

- Task 1: minor (deferred): go.mod says `go 1.26.1` (patch-pinned by go mod init) vs constraint `go 1.26`
- Task 2: minor (deferred): Seal panics on crypto/rand failure (plan-mandated); no GoDoc on exported seal API; no nonce-length boundary test
- Task 3: minor (deferred): withTx drops Rollback error; AdvanceOffset silent on missing bot; scan loops not DRY; SetBotStatus can re-set a removed bot's status (worker could mark removed bot 'running'/'error' — check in final review); UpsertBot keeps avatar_path/update_offset on re-add (intended: same bot keeps cursor)
- Task 4: minor (deferred): fake swallows body decode errors; GetFile/LogOut/GetUserProfilePhotos not unit-tested in tgbot (covered by T8/T9/T13/T15); fake getFile always uses .jpg
- Task 5: minor (deferred): forward_origin "chat" branch untested; compound || assertions; sticker extra/video metadata not asserted
- Task 7: minor (deferred): link detection via LIKE on entities_json (plan-mandated); reply hydration N+1
- Task 6: minor (deferred): MarkMediaRetry/Failed/TooLarge silent on missing id; ChatSenders multi-bot/removed paths untested; collectOrphans deletes row-by-row
- Task 8: minor (deferred): LinkOrCopy copy fallback untested; Registry.Get holds lock across DB call; missing doc comments
- Task 8: minor (deferred): CleanOlderThan drops rmErrs when WalkDir itself errors
- Task 10: minor (deferred): engine-wide mutex coarse; no concurrent Evaluate test
- Task 9: minor (deferred): BotSource ignores os.Remove(local) error silently; no traversal check on filepath.Rel(final); no-source branch untested
- Task 11: minor (deferred): no exported event-type constants (plan 3 typo risk); Bark invalid-config/non-200 branches untested; no-op test asserts nothing
- Task 12: minor (deferred): Start no-op while a cancelled worker still exits (concurrent admin requests); poison update retries forever without escalation; RecordRejected/TryHandle not idempotent across refetch (plan 2 LinkHandler must dedupe); Evaluate skippable on Stop; no tests for 409/RetryAfter/mid-batch store error; status stays 'running' after clean shutdown
- Task 10: minor (deferred): receipt_test.go failThenSucceed struct not gofmt'd (run gofmt -l over repo in final review)
- Task 13: minor (deferred): picker assumes ascending order (seed from [0]); os.Remove(local) error unlogged; chats[0] unchecked in test
- Task 12: minor (deferred): transient 'running' stamp if Stop races right after Clients.Get
- Task 14: minor (deferred): 405 responses are plain text not JSON; retryMedia uses == instead of errors.Is; SSE marshal error unlogged; 400 vs 404 for malformed ids; 500 passthrough of err.Error()
- Task 15: minor (deferred): mixed zh/en error messages
- Task 16: minor (deferred): test cleanup leaks on failure; helpers ignore Unmarshal errors / hard-coded bot id 1; downloader: cancel between Fetch and MarkMediaDone leaves file on disk + row pending (possible orphan under new yyyy/mm) — final review should weigh this
- Task 17: minor (deferred): child orphaned when main log.Fatal's after supervisor start (StartAll error / ListenAndServe error) — consider Pdeathsig or Close before exit; concurrent PUT Save/Apply not atomic; same creds re-save restarts child; managed port hardcoded 8081 vs BOT_API_URL; undecryptable setting blocks startup; tests don't wait Run exit; backoff/old-child-exit/app-level Close-waits untested; PUT body unbounded
