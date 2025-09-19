# CometKMS Security Notes

This document describes how CometKMS defends against validator double-signing,
the assumptions those protections depend on, and the circumstances that can
still interrupt signing.

## Double-Signing Protections

- **Leader-gated signing.** Every `SignVote` and `SignProposal` request first
  calls `VerifyLeader()` on the local Raft node. Only the current leader holds
  the signing lease, so any follower or node in the middle of a leadership
  change immediately rejects the request.
- **Shared last-sign state.** Before and after a successful signature the node
  synchronizes the latest height/round/step (H/R/S), signature, and sign-bytes
  through Raft. The replicated FSM only accepts strictly higher H/R/S values,
  so a restarted node cannot re-sign an older block even if its local disk still
  carries stale state.
- **Hot-standby validator endpoints.** The keyserver activates remote signer
  connections only while it is the leader. Followers stay connected for rapid
  failover but never answer requests unless Raft elects them leader and the
  above state sync succeeds.

These behaviors are covered by integration tests in `pkg/keyserver`:

- `TestKeyserverConcurrentSigningWithLeaderChanges` kills and restarts nodes
  while continuously signing new heights. Only the leader signs, and the rest
  of the cluster observes the same H/R/S after each leadership transfer.
- `TestKeyserverMultipleValidatorsRejectConflictingVotes` runs two validator
  clients against a three-node CometKMS cluster. When both validators request a
  signature for the same H/R/S concurrently, only one succeeds; the other
  receives a conflict error, and the replicated state matches the successful
  signature bytes.

## Assumptions and Out-of-Scope Scenarios

- All validator private key material is only accessible through CometKMS. If an
  operator copies the key files elsewhere and signs manually, CometKMS cannot
  prevent double-signs.
- A Raft majority must remain online to elect and verify a leader. Losing
  quorum will block new signatures until the cluster recovers.
- Network partitions, clock skew, or disk corruption beyond what Raft detects
  can cause availability issues. They do not, however, allow conflicting H/R/S
  to be signed as long as the above leader and FSM checks remain intact.

## Availability Trade-offs

If KMS nodes crash or restart frequently, signing may pause while Raft elects a
new leader or while followers catch up on the replicated sign state. This
behavior is intentional: CometKMS favors safety (no double-signing) over liveness
when quorum or leadership is uncertain.

Operators should monitor cluster health and ensure:

- At least a majority of KMS nodes stay online.
- Validator endpoints remain reachable so the current leader can sign promptly.
- Private key files are protected from direct access outside the KMS process.

Under these conditions, CometKMS prevents double-signing even through repeated
node restarts or leadership changes, at the cost of temporary signing pauses
whenever safety cannot be guaranteed.
