package raft

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"golang.org/x/sync/errgroup"

	cometlog "github.com/cometbft/cometbft/libs/log"

	"github.com/initia-labs/CometKMS/pkg/fsm"
)

type testClusterNode struct {
	id   string
	node *Node
	grp  *errgroup.Group
}

func TestSyncLastSignStateReplicatesAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	peers := makeTestPeers(t, 3)
	cluster := startTestCluster(t, ctx, peers)
	t.Cleanup(func() {
		for _, c := range cluster {
			if err := c.node.Close(); err != nil {
				t.Logf("close %s: %v", c.id, err)
			}
			if c.grp != nil {
				if err := c.grp.Wait(); err != nil {
					t.Logf("group %s: %v", c.id, err)
				}
			}
		}
	})

	leader := waitForLeader(t, cluster, 5*time.Second)
	initial := &fsm.LastSignState{Height: 10, Round: 1, Step: 2}
	state, err := leader.node.SyncLastSignState(initial)
	if err != nil {
		t.Fatalf("leader sync initial: %v", err)
	}
	if state.Height != initial.Height || state.Round != initial.Round || state.Step != initial.Step {
		t.Fatalf("unexpected initial state %+v", state)
	}
	for _, c := range cluster {
		waitForLastSignState(t, c.node, initial.Height, initial.Round, initial.Step, 5*time.Second)
	}

	follower := pickFollower(cluster, leader.id)
	if follower == nil {
		t.Fatalf("expected follower")
	}
	_, err = follower.node.SyncLastSignState(&fsm.LastSignState{Height: 11, Round: 0, Step: 0})
	var notLeaderErr *NotLeaderError
	if !errors.As(err, &notLeaderErr) {
		t.Fatalf("expected not leader error, got %v", err)
	}
	ensureMajorityHasLastSignState(t, cluster, initial.Height, initial.Round, initial.Step)
	for _, c := range cluster {
		waitForLastSignState(t, c.node, initial.Height, initial.Round, initial.Step, 5*time.Second)
	}

	higher := &fsm.LastSignState{Height: 12, Round: 0, Step: 0}
	for range int64(100) {
		higher = &fsm.LastSignState{Height: higher.Height + 1, Round: 0, Step: 0}
		state, err = leader.node.SyncLastSignState(higher)
		if err != nil {
			t.Fatalf("leader sync higher: %v", err)
		}
		if state.Height != higher.Height {
			t.Fatalf("expected height %d, got %d", higher.Height, state.Height)
		}
		ensureMajorityHasLastSignState(t, cluster, higher.Height, 0, 0)
	}

	lower := &fsm.LastSignState{Height: 11, Round: 2, Step: 1}
	state, err = leader.node.SyncLastSignState(lower)
	if err != nil {
		t.Fatalf("leader sync lower: %v", err)
	}
	if state.Height != higher.Height || state.Round != higher.Round || state.Step != higher.Step {
		t.Fatalf("lower sync should keep higher, got %+v", state)
	}
	ensureMajorityHasLastSignState(t, cluster, higher.Height, 0, 0)
	for _, c := range cluster {
		waitForLastSignState(t, c.node, higher.Height, higher.Round, higher.Step, 5*time.Second)
	}
}

func startTestCluster(t *testing.T, ctx context.Context, peers []Peer) []testClusterNode {
	t.Helper()

	baseDir := t.TempDir()
	cluster := make([]testClusterNode, 0, len(peers))
	for i, peer := range peers {
		cfg := Config{
			ID:          peer.ID,
			RaftDir:     filepath.Join(baseDir, peer.ID),
			BindAddress: peer.Address,
			Bootstrap:   i == 0,
			Peers:       peers,
			Logger:      cometlog.NewNopLogger(),
			LogLevel:    hclog.Off,
			LogOutput:   io.Discard,
		}

		node, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("new node %s: %v", cfg.ID, err)
		}
		grp := new(errgroup.Group)
		cluster = append(cluster, testClusterNode{id: cfg.ID, node: node, grp: grp})
	}

	for _, c := range cluster {
		if err := c.node.Start(ctx, c.grp); err != nil {
			t.Fatalf("start node %s: %v", c.id, err)
		}
	}

	return cluster
}

func pickFollower(cluster []testClusterNode, leaderID string) *testClusterNode {
	for i := range cluster {
		if cluster[i].id != leaderID {
			return &cluster[i]
		}
	}
	return nil
}

func waitForLeader(t *testing.T, cluster []testClusterNode, timeout time.Duration) testClusterNode {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, c := range cluster {
			if err := c.node.EnsureLeader(); err != nil {
				continue
			}
			_, leaderID := c.node.LeaderInfo()
			if leaderID == c.id {
				return c
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no leader elected within %s", timeout)
	return testClusterNode{}
}

func waitForLastSignState(t *testing.T, node *Node, height int64, round int32, step int8, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := node.GetLastSignState()
		if state != nil && state.Height == height && state.Round == round && state.Step == step {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node %s did not observe sign state %d/%d/%d", node.config.ID, height, round, step)
}

func ensureMajorityHasLastSignState(t *testing.T, nodes []testClusterNode, height int64, round int32, step int8) {
	t.Helper()
	count := 0
	for _, n := range nodes {
		state := n.node.GetLastSignState()
		if state != nil && state.Height == height && state.Round == round && state.Step == step {
			count++
		}
	}

	if count < len(nodes)/2 {
		t.Fatalf("majority nodes did not observe sign sate %d/%d/%d", height, round, step)
	}
}

func makeTestPeers(t *testing.T, count int) []Peer {
	t.Helper()

	peers := make([]Peer, 0, count)
	for i := 0; i < count; i++ {
		peers = append(peers, Peer{ID: fmt.Sprintf("node-%d", i), Address: allocateAddress(t)})
	}
	return peers
}

func allocateAddress(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}
