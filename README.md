# CometKMS

CometKMS is a lightweight key management service designed to keep a single CometBFT signer active while coordinating multiple hot-standby replicas. It provides a simple HTTP API backed by a Raft quorum so only one signer answers CometBFT-style privval requests at any moment.

## Features

- Raft-backed lease FSM that replicates height/round/step metadata after every signature to prevent double-signs during leadership changes.
- Lightweight HTTP endpoints for health checks and monitoring.
- Static Raft peer configuration with sub-second lease failover and leader-driven sign-state updates.
- Built-in CometBFT remote signer that only dials validators while the lease is active and keeps priv-validator state in sync cluster-wide.

## Getting Started

Initialize your node with:

```bash
cmkms init --home ~/.cmkms
```

This command scaffolds the home directory and writes `config.toml`, `data`, and `priv` dirs if they are missing.

Edit `~/.cmkms/config.toml` (or supply flags/env vars) to point at your validator:

```bash
# ~/.cmkms/config.toml
validator_addr = "tcp://127.0.0.1:8080"
chain_id = "your-chain-id"
# You can provide multiple addresses with an array, e.g.
# validator_addr = ["tcp://127.0.0.1:8080", "unix:///tmp/privval.sock"]
```

Place `priv_validator_key.json` and `priv_validator_state.json` in `$HOME/.cmkms/priv/` (or the `--home` directory you choose). You can override the home directory with `--home` when running multiple instances on the same host. Configure all Raft peers (including the local node) via the `peer` array in `config.toml` or `--peer id=host:port` flags.

```bash
# build the daemon
cd ~/Workspace/cometkms
go build ./cmd/cmkms

# start the first node (the data directory is empty so it bootstraps automatically)
./cmkms start \
  --id node-0 \
  --raft-addr 10.0.0.10:9430 \
  --http-addr :8080 \
  --validator-addr tcp://127.0.0.1:8080 \
  --validator-addr unix:///tmp/privval.sock \
  --chain-id testnet-1 \
  --peer node-0=10.0.0.10:9430 \
  --peer node-1=10.0.0.11:9430

# join a second node by pointing it at the leader's HTTP endpoint
./cmkms start \
  --home ~/.cmkms-node1 \
  --id node-1 \
  --raft-addr 10.0.0.11:9430 \
  --http-addr :8081 \
  --validator-addr tcp://127.0.0.1:8081 \
  --validator-addr unix:///tmp/privval-1.sock \
  --chain-id testnet-1 \
  --peer node-0=10.0.0.10:9430 \
  --peer node-1=10.0.0.11:9430
```

Each node should list the full peer set in the same order. The node whose local
data directory is empty (for example `$HOME/.cmkms/data/node-0`) will
bootstrap the Raft cluster the first time it starts; once state exists, all
nodes simply join using the shared peer list.

Once the cluster is running the Raft leader automatically coordinates signing.
Followers stay hot-standby by applying replicated sign-state updates and will
take over after a leadership change without manual lease management.

Operational endpoints remain available for monitoring:

```bash
curl http://10.0.0.10:8080/healthz           # liveness probe
curl http://10.0.0.10:8080/status            # observe current signer
```

## Development

Run unit tests with a temporary Go build cache to avoid macOS sandbox restrictions:

```bash
make test
```

CometKMS targets Go 1.24.3+. Contributions and feedback are welcome.
