# Upstream Sync Log

This file tracks manual syncs from the private `telesrv` workspace into the public
`gramsrv` repository. Keep it append-only so future syncs can start from the last
confirmed source checkpoint.

## Current Checkpoint

- Last synced source commit: `c0a0e5b52240ed415d3b43ba77659821887bf50b`
- Last synced target commit: `860e581`
- Sync date: 2026-06-10
- Source path used locally: `D:\work\waikuai\telegram\telesrv`
- Target path used locally: `D:\work\waikuai\telegram\gramsrv-public`

## Sync History

| Date | Source commit | Target commit | Status | Notes |
|---|---|---|---|---|
| 2026-06-09 | `488e409a1898e9c739cc0bd24cb9791636dfd6b3` | `23a2b2a` | synced | Restored sticker path placeholders, media in dialogs/history, media forwarding to channels, and channel history pagination fixes. Source README changes were already covered by the public repo's gramsrv README wording, so no README file changed in the target cherry-pick. |
| 2026-06-09 | `07b2497664bd108dec84f6cfe43715540faf2688` | `6fd690a` | synced | Kept admin/ban participant changes as transient `updateChannelParticipant` pushes instead of durable channel pts events; added memory/Postgres regression coverage. |
| 2026-06-10 | `c65f76f56278f74082c4fa792ed49104d5d33c38` | `d84fa6e` | synced | Aligned private and channel read/update semantics, including content unread state, block-aware private sends/forwards, channel difference nudges, and durable read-content events. |
| 2026-06-10 | `c0a0e5b52240ed415d3b43ba77659821887bf50b` | `860e581` | synced | Added viewer-specific user projection, contact accept/phone sharing behavior, and regression coverage for contact/user/message projections. |

## Next Sync

Start the next batch from source commits after `c0a0e5b52240ed415d3b43ba77659821887bf50b`.
At the time this log was created, newer `telesrv` commits existed after that point and were
intentionally left out because the latest batch was scoped to the next two commits after
`07b2497664bd108dec84f6cfe43715540faf2688`.
