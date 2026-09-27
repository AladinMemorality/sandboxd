# Minecraft / Playit migration

Status: preparation only. The two Minecraft customer projects remain on Docker.
The claim admission primitive is implemented and tested, but no Playit service
is registered, no template is changed, and no production network access is added.

Both projects run Java/Paper with the Playit v0.15.26 agent. Their dashboard and
watchdog manage the local Java and Playit processes. Preserve that arrangement,
worlds, backups, installed Java, saved Playit credentials and assigned addresses.
Do not create replacement Playit claims as part of migration.

## Transport direction

Keep Playit in the guest, using a scoped transport through the authenticated
Cube reverse channel. Normal HTTP proxy support is insufficient: Playit uses
UDP control and a provider-supplied TCP relay for each incoming game connection.
The guest NIC remains deny-all. Public HTTP CONNECT and private service routing
retain their existing policies.

The host must select exactly the persisted Minecraft app/binding/generation,
with control endpoints and tunnel addresses from that agent's verified provider
inventory. A guest cannot supply this inventory or request an arbitrary relay.
Use a bounded UDP transport only to those control endpoints on port 5525.
Only the host UDP receive loop may observe provider packets for claim issuance.
Guest-uploaded packets must never enter claim admission as provider responses.

`control-plane/internal/egress/playit_claims.go` parses NewClient datagrams and
records one-use SHA-256 token tickets, expiring after 15 seconds. It rejects
unapproved sources/tunnels, private or protected relay addresses, malformed
lengths, trailing bytes, duplicate tokens, sibling identities, stale generations
and cancelled channels. It retains bounded tombstones so provider retransmission
cannot renew consumed or expired tickets. Full ledgers refuse admission rather
than evicting replay protection. Ticket formatting is redacted.

The future dialer must recheck the current binding before dialing and cancel
active UDP/TCP work when that binding or channel is revoked. The ledger alone
does not establish those integration guarantees and does not open connections.
The guest TCP adapter should return a loopback `tokio::net::TcpStream`, preserving
the upstream relay token write, acknowledgement and subsequent stream handling.

The upstream agent also maintains a separate UDP data channel even for TCP-only
Minecraft. Before deployment, implement an explicit TCP-only adapter that avoids
that unused channel, or separately authorize its provider-issued endpoint. Do
not silently route arbitrary UDP or assume all packets use control port 5525.

## Source evidence

Reviewed upstream: `playit-cloud/playit-agent` commit
`e6a7b4e10e6214edf5b00349a107e18b5b8d10f5` (v0.15.26).

- `packages/agent_core/src/playit_agent.rs`: control refresh, TCP and UDP tasks.
- `packages/agent_core/src/agent_control/mod.rs`: API routing inventory and 5525.
- `packages/agent_core/src/network/tcp_tunnel.rs`: relay token/acknowledgement.
- `packages/agent_proto/src/control_feed.rs`: NewClient wire schema and public
  fixture used by the Go tests. No customer packet is included in test data.
- Locked `message-encoding` 0.2.2 crate, SHA-256
  `2ae9f151f04c73831889831a3ba38103c9504edc708bec164299212d0820b048`:
  big-endian u32 feed tag, 4/6 family byte, IP bytes and big-endian u16 port,
  big-endian u64 token length, then u64 server ID and u32 data-center ID.

## Remaining acceptance

1. Implement and test the host transport, persisted app authorization, guest
   adapter and channel cleanup with owned fixtures. Include queue/rate limits,
   cancellation, half-close, relay dial failures and generation replacement.
2. Build the exact pinned agent and Cube template. Verify saved credentials are
   used without new claims and that the existing dashboard/watchdog still work.
3. Add actual Minecraft status/ping plus the preserved public Playit tunnel to
   precommit and postmigration acceptance. A healthy dashboard alone is not
   sufficient. Compare retained world data and archives.
4. Migrate each customer with the current operator locks and generation pins.
   Only after both succeed, activate the Cube-only controller and global Cube
   default. Preserve original Docker sources for rollback.

## Offline verification — September 27, 2026

The complete `go test -race ./internal/egress` suite passed in 33.271 seconds.
Focused Playit tests and a 15-second parser fuzz run passed with 198,564
executions. Tests cover the upstream wire fixture, every truncated prefix,
invalid tags/lengths, source/tunnel/relay rejection, expiry without renewal,
replay, concurrent single consumption, channel cancellation and memory bounds.

These ran in the existing pinned Go builder
`sha256:3d699e4d15d0f8f13c9195c0632a16702b8cbdece2955af1c23b37ae5d55a253`,
with networking disabled and no production state mounts. The first test staging
attempt omitted the local `internal/metrics` dependency and failed before tests;
the complete source staging corrected that. Review stage:
`/opt/baarcha-bench/cube-playit-claims-20260927-01`.
No customer packet, tunnel credential or live game traffic was used.
