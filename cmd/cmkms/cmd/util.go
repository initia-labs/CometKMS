package cmd

import (
	"fmt"
	"os"
	"strings"

	raftnode "github.com/initia-labs/CometKMS/pkg/raft"
	"github.com/spf13/cobra"
)

// hostnameFallback returns the current hostname or a static identifier when it
// cannot be determined.
func hostnameFallback() string {
	host, err := os.Hostname()
	if err != nil {
		return "cmkms-node"
	}
	return host
}

// The option helpers resolve values in priority order: CLI flag, environment
// variable, config file entry, and finally the provided fallback.
func stringOption(cmd *cobra.Command, flagName, envKey, cfgValue, fallback string) string {
	if cmd.Flags().Changed(flagName) {
		val, _ := cmd.Flags().GetString(flagName)
		if val != "" {
			return val
		}
	}
	if envVal := strings.TrimSpace(os.Getenv(envKey)); envVal != "" {
		return envVal
	}
	if cfgValue != "" {
		return cfgValue
	}
	return fallback
}

// sliceOption resolves string-array options from flag, environment, or config
// sources, preserving the priority order described in stringOption.
func sliceOption(cmd *cobra.Command, flagName, envKey string, cfgValue []string) []string {
	if cmd.Flags().Changed(flagName) {
		vals, _ := cmd.Flags().GetStringArray(flagName)
		return vals
	}
	if envVal := strings.TrimSpace(os.Getenv(envKey)); envVal != "" {
		parts := strings.Split(envVal, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return cfgValue
}

// parsePeers converts id=host:port definitions into raft peers.
func parsePeers(entries []string) ([]raftnode.Peer, error) {
	peers := make([]raftnode.Peer, 0, len(entries))
	for _, entry := range entries {
		peer, err := parsePeer(entry)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

// parsePeer splits an id=addr definition into a raft peer structure.
func parsePeer(entry string) (raftnode.Peer, error) {
	parts := strings.SplitN(entry, "=", 2)
	if len(parts) != 2 {
		return raftnode.Peer{}, fmt.Errorf("peer %q must be in id=addr form", entry)
	}
	return raftnode.Peer{ID: parts[0], Address: parts[1]}, nil
}

// ensureSelfPeer appends the local node to the peer list when it is absent.
func ensureSelfPeer(peers []raftnode.Peer, nodeID, raftAddr string) []raftnode.Peer {
	for _, p := range peers {
		if p.ID == nodeID {
			return peers
		}
	}
	return append(peers, raftnode.Peer{ID: nodeID, Address: raftAddr})
}

// dirEmpty reports true when the directory is empty or cannot be read.
func dirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return true
	}
	return len(entries) == 0
}
