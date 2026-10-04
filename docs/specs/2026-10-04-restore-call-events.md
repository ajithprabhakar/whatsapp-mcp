# Restore WhatsApp call event persistence

## Root cause

The installed bridge and current main source lack the previous calls-table initializer, `MessageStore.StoreCall`, and call event handler. The June 1 pre-subjects backup binary contains them; the pre-icons backup and current binary do not. Maya's existing `check_new_calls` consumer still runs successfully, but its 93-row calls source last advanced on May 31. Message and group ingestion still work. This change restores the omitted producer.

## Acceptance criteria

1. Handle the pinned SDK's `CallOffer`, `CallAccept`, `CallTerminate`, `CallReject`, and individual `CallOfferNotice` events. Store one row per actual `CallID`. Exclude group calls and leave unrelated event handling unchanged. Outgoing observations require an owner `CallCreator` and a distinct remote `From`; do not invent outgoing offers or completed conversations.
2. Recognize both owner PN and LID identities. Prefer an incoming creator's PN only when supplied by the SDK's `CallCreatorAlt`; otherwise retain the LID namespace. Reject missing or self-only bindings. Set media to voice/video only when observed, otherwise unknown. Do not store direction in `call_type` or fabricate duration.
3. Restore the existing schema: `call_id`, `caller_jid`, `call_type`, `timestamp`, `summary`, and `created_at`. Add no columns or live migration. Repeated events preserve owner summaries, earliest observed timestamp, and known media. Unknown media may be upgraded by later evidence. A conflicting remote identity for the same call ID fails closed.
4. Use fabricated SDK events and temporary SQLite in tests. Cover lifecycle replay, out-of-order observations, outgoing calls, LID alternatives, unknown/self/group identities, known-media preservation, summaries, and unrelated messages. Never open the production message/session store or make a test call.
5. Preserve the pinned protocol, message history, group names, and avatars. Review before implementation, then run race-enabled tests, vet, and an isolated build. Merge the reviewed change to the user fork's main branch and record that revision in Maya's main release before deployment. Deployment and service restart remain release-coordinator actions.

## Plan and reuse evidence

Add focused regression tests first. Add `calls.go` for observation normalization and persistence, initialize the existing calls table from `NewMessageStore`, and intercept supported call events before the existing event switch. Review the implemented change and merge through a pull request.

The existing Maya Graphify graph does not cover this external repository; its bounded Call query returned unrelated nodes. LSP was unavailable, so source searches were bounded to this repository and the pinned SDK. `MessageStore` is the existing SQLite writer and `AddEventHandler` is the SDK dispatch point. No repository-local AGENTS.md or CLAUDE.md exists; home CLAUDE.md's lookup requirements were followed.

The pinned SDK is `whatsmeow v0.0.0-20260915134308-320ff7ebf928`. Its event types provide call identity, participant handles, and event timestamps, but no duration. Ordinary Git history contains no `caller_jid` implementation; the backup binary confirms the old schema and store contract. The recovery starts from user fork main `a94414531fa62fa64ff021dc046f75d9172cb936`, which includes the protocol compatibility fix.
