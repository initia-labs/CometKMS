package cmd

import (
	"os"
	"path/filepath"
	"testing"

	raftnode "github.com/initia-labs/CometKMS/pkg/raft"
	"github.com/spf13/cobra"
)

func TestStringOptionPriority(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("addr", "", "")
	cmd.Flags().Set("addr", "flag-value")
	t.Setenv("KEY_ENV", "env-value")

	got := stringOption(cmd, "addr", "KEY_ENV", "config-value", "fallback")
	if got != "flag-value" {
		t.Fatalf("expected flag to win, got %q", got)
	}

	cmd = &cobra.Command{Use: "test"}
	cmd.Flags().String("addr", "", "")
	t.Setenv("KEY_ENV", "env-value")
	got = stringOption(cmd, "addr", "KEY_ENV", "config-value", "fallback")
	if got != "env-value" {
		t.Fatalf("expected env to win, got %q", got)
	}

	t.Setenv("KEY_ENV", "")
	got = stringOption(cmd, "addr", "KEY_ENV", "config-value", "fallback")
	if got != "config-value" {
		t.Fatalf("expected config to win, got %q", got)
	}

	got = stringOption(cmd, "addr", "KEY_ENV", "", "fallback")
	if got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestSliceOptionPriority(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringArray("peer", []string{}, "")
	cmd.Flags().Set("peer", "flag-a")
	cmd.Flags().Set("peer", "flag-b")
	t.Setenv("PEER_ENV", "env-a, env-b , ")

	got := sliceOption(cmd, "peer", "PEER_ENV", []string{"cfg-a"})
	expect := []string{"flag-a", "flag-b"}
	if !slicesEqual(got, expect) {
		t.Fatalf("expected flag slice %v, got %v", expect, got)
	}

	cmd = &cobra.Command{Use: "test"}
	cmd.Flags().StringArray("peer", []string{}, "")
	t.Setenv("PEER_ENV", "env-a, env-b , ")
	got = sliceOption(cmd, "peer", "PEER_ENV", []string{"cfg-a"})
	expect = []string{"env-a", "env-b"}
	if !slicesEqual(got, expect) {
		t.Fatalf("expected env slice %v, got %v", expect, got)
	}

	t.Setenv("PEER_ENV", "")
	got = sliceOption(cmd, "peer", "PEER_ENV", []string{"cfg-a", "cfg-b"})
	expect = []string{"cfg-a", "cfg-b"}
	if !slicesEqual(got, expect) {
		t.Fatalf("expected config slice %v, got %v", expect, got)
	}
}

func TestBoolOptionPriority(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("unsafe", false, "")
	cmd.Flags().Set("unsafe", "true")
	t.Setenv("BOOL_ENV", "false")

	if got := boolOption(cmd, "unsafe", "BOOL_ENV", false, false); !got {
		t.Fatalf("expected flag bool to win")
	}

	cmd = &cobra.Command{Use: "test"}
	cmd.Flags().Bool("unsafe", false, "")
	t.Setenv("BOOL_ENV", "true")
	if got := boolOption(cmd, "unsafe", "BOOL_ENV", false, false); !got {
		t.Fatalf("expected env bool to win")
	}

	t.Setenv("BOOL_ENV", "false")
	if got := boolOption(cmd, "unsafe", "BOOL_ENV", true, false); got {
		t.Fatalf("expected env to trump config and set false")
	}

	t.Setenv("BOOL_ENV", "")
	if got := boolOption(cmd, "unsafe", "BOOL_ENV", true, false); !got {
		t.Fatalf("expected config bool to win")
	}

	if got := boolOption(cmd, "unsafe", "BOOL_ENV", false, true); !got {
		t.Fatalf("expected fallback when nothing else set")
	}
}

func TestParsePeersAndEnsureSelf(t *testing.T) {
	peers, err := parsePeers([]string{"node-1=127.0.0.1:1234"})
	if err != nil {
		t.Fatalf("parsePeers: %v", err)
	}
	if len(peers) != 1 || peers[0].ID != "node-1" {
		t.Fatalf("unexpected peers %+v", peers)
	}

	if _, err := parsePeers([]string{"unset"}); err == nil {
		t.Fatal("expected parsePeers to fail on invalid entry")
	}

	// ensureSelfPeer should append when local id absent
	updated := ensureSelfPeer(peers, "node-0", "127.0.0.1:9000")
	if len(updated) != 2 {
		t.Fatalf("expected self peer appended, got %+v", updated)
	}
	// ensureSelfPeer should no-op when already present
	again := ensureSelfPeer(updated, "node-1", "ignored")
	if len(again) != 2 {
		t.Fatalf("expected no duplicate peers, got %+v", again)
	}
}

func TestParsePeerError(t *testing.T) {
	if _, err := parsePeer("invalid"); err == nil {
		t.Fatal("expected parsePeer to error on invalid input")
	}
	peer, err := parsePeer("node=addr")
	if err != nil {
		t.Fatalf("parsePeer valid: %v", err)
	}
	if peer.ID != "node" || peer.Address != "addr" {
		t.Fatalf("unexpected peer %+v", peer)
	}
}

func TestDirEmpty(t *testing.T) {
	path := t.TempDir()
	if !dirEmpty(path) {
		t.Fatal("expected empty dir to be reported empty")
	}

	file := filepath.Join(path, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if dirEmpty(path) {
		t.Fatal("expected non-empty dir to be reported non-empty")
	}

	if !dirEmpty(filepath.Join(path, "missing")) {
		t.Fatal("expected missing path to be treated as empty")
	}
}

func TestEnsureSelfPeerIdempotent(t *testing.T) {
	peers := []raftnode.Peer{{ID: "node-1", Address: "addr"}}
	updated := ensureSelfPeer(peers, "node-1", "addr")
	if len(updated) != 1 {
		t.Fatalf("expected no change when peer exists, got %+v", updated)
	}
}
