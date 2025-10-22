package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"go.etcd.io/bbolt"

	cometlog "github.com/cometbft/cometbft/libs/log"

	"github.com/initia-labs/CometKMS/pkg/fsm"
)

// Config captures the parameters required to start a Keystone raft node.
type Config struct {
	ID          string
	RaftDir     string
	BindAddress string
	Bootstrap   bool
	Peers       []Peer
	Logger      cometlog.Logger
	LogLevel    hclog.Level
	LogOutput   io.Writer
	LogFormat   string
}

// Peer represents another raft server in the cluster.
type Peer struct {
	ID      string
	Address string
}

// Node wraps the raft instance and associated FSM.
type Node struct {
	raft        *raft.Raft
	fsm         *fsm.FSMState
	config      Config
	autoTimeout time.Duration
	logger      cometlog.Logger
	transport   *raft.NetworkTransport
}

// NotLeaderError indicates the node handling the request is not the leader.
type NotLeaderError struct {
	LeaderID  string
	LeaderAdr string
}

func (e *NotLeaderError) Error() string {
	if e.LeaderID == "" && e.LeaderAdr == "" {
		return "not leader"
	}
	return fmt.Sprintf("not leader (leader=%s addr=%s)", e.LeaderID, e.LeaderAdr)
}

// NewNode bootstraps a raft node with on-disk storage.
func NewNode(cfg Config) (*Node, error) {
	if cfg.ID == "" {
		return nil, errors.New("raft: node id required")
	}
	if cfg.BindAddress == "" {
		return nil, errors.New("raft: bind address required")
	}
	if cfg.RaftDir == "" {
		cfg.RaftDir = filepath.Join(os.TempDir(), "cmkms", cfg.ID)
	}

	if err := os.MkdirAll(cfg.RaftDir, 0o750); err != nil {
		return nil, fmt.Errorf("raft: mkdir: %w", err)
	}

	if cfg.LogOutput == nil {
		cfg.LogOutput = os.Stderr
	}

	fsmState := fsm.NewFSMState()

	// Configure the underlying Raft instance with periodic snapshots and HCL logging.
	raftConfig := raft.DefaultConfig()
	raftConfig.LocalID = raft.ServerID(cfg.ID)
	raftConfig.Logger = hclog.New(&hclog.LoggerOptions{
		Name:       fmt.Sprintf("raft-%s", cfg.ID),
		Level:      cfg.LogLevel,
		Output:     cfg.LogOutput,
		JSONFormat: cfg.LogFormat == "json",
	})

	logStore, err := raftboltdb.New(raftboltdb.Options{Path: filepath.Join(cfg.RaftDir, "raft-log.bolt"), BoltOptions: &bbolt.Options{Timeout: 1 * time.Second}})
	if err != nil {
		return nil, fmt.Errorf("raft: new log store: %w", err)
	}

	stableStore, err := raftboltdb.New(raftboltdb.Options{Path: filepath.Join(cfg.RaftDir, "raft-stable.bolt"), BoltOptions: &bbolt.Options{Timeout: 1 * time.Second}})
	if err != nil {
		return nil, fmt.Errorf("raft: new stable store: %w", err)
	}

	snapshotStore, err := raft.NewFileSnapshotStore(cfg.RaftDir, 2, cfg.LogOutput)
	if err != nil {
		return nil, fmt.Errorf("raft: snapshot store: %w", err)
	}

	// Use a resolved TCP address so peers can reach this node even if the bind host differs from the advertised host.
	advertise, err := net.ResolveTCPAddr("tcp", cfg.BindAddress)
	if err != nil {
		return nil, fmt.Errorf("raft: resolve advertise addr: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddress, advertise, 5, 10*time.Second, cfg.LogOutput)
	if err != nil {
		return nil, fmt.Errorf("raft: transport: %w", err)
	}

	ra, err := raft.NewRaft(raftConfig, fsmState, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("raft: new raft: %w", err)
	}

	node := &Node{
		raft:        ra,
		fsm:         fsmState,
		config:      cfg,
		autoTimeout: 500 * time.Millisecond,
		logger:      cfg.Logger,
		transport:   transport,
	}

	if cfg.Bootstrap {
		localAddr := transport.LocalAddr()
		configuration := raft.Configuration{Servers: []raft.Server{{ID: raft.ServerID(cfg.ID), Address: localAddr}}}
		seen := map[raft.ServerID]struct{}{raft.ServerID(cfg.ID): {}}
		for _, peer := range cfg.Peers {
			id := raft.ServerID(peer.ID)
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			configuration.Servers = append(configuration.Servers, raft.Server{ID: id, Address: raft.ServerAddress(peer.Address)})
		}
		future := ra.BootstrapCluster(configuration)
		if err := future.Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			return nil, fmt.Errorf("raft: bootstrap: %w", err)
		}
	}

	return node, nil
}

func (n *Node) Start(ctx context.Context, g *errgroup.Group) error {
	if len(n.config.Peers) > 0 {
		g.Go(func() error {
			return n.syncPeers(ctx, n.config.Peers)
		})
	}

	return nil
}

func isSynced(peers []Peer, config raft.Configuration) bool {
	known := make(map[raft.ServerID]int, len(config.Servers))
	for _, srv := range config.Servers {
		known[srv.ID] = 0
	}
	for _, peer := range peers {
		if peer.ID == "" || peer.Address == "" {
			continue
		}
		serverID := raft.ServerID(peer.ID)
		if _, ok := known[serverID]; !ok {
			return false
		}
		known[serverID]++
	}
	for _, count := range known {
		if count == 0 {
			return false
		}
	}
	return true
}

func (n *Node) syncPeers(ctx context.Context, peers []Peer) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}

		// keep check whether all peers are already known
		future := n.raft.GetConfiguration()
		if err := future.Error(); err != nil {
			n.logger.Error("raft: configuration fetch failed: %v", err)
			return fmt.Errorf("configuration fetch: %w", err)
		}
		config := future.Configuration()
		if isSynced(peers, config) {
			break
		}

		// only the leader can modify the cluster configuration
		_, id := n.LeaderInfo()
		if id == "" {
			continue
		}
		if !n.IsLeader() {
			continue
		}

		known := make(map[raft.ServerID]int, len(config.Servers))
		for _, srv := range config.Servers {
			known[srv.ID] = 0
		}
		for _, peer := range peers {
			if peer.ID == "" || peer.Address == "" {
				continue
			}

			serverID := raft.ServerID(peer.ID)
			if _, ok := known[serverID]; ok {
				known[serverID]++
				continue
			}

			future := n.raft.AddVoter(serverID, raft.ServerAddress(peer.Address), 0, n.autoTimeout)
			if err := future.Error(); err != nil {
				if errors.Is(err, raft.ErrNotLeader) {
					n.logger.Error("raft: add voter skipped %s (not leader)", peer.ID)
					break
				}
				n.logger.Error("raft: add voter %s failed: %v", peer.ID, err)
				continue
			}

			n.logger.Info("raft: added voter %s (%s)", peer.ID, peer.Address)
		}
		for serverID, count := range known {
			if count == 0 {
				future := n.raft.RemoveServer(serverID, 0, n.autoTimeout)
				if err := future.Error(); err != nil {
					if errors.Is(err, raft.ErrNotLeader) {
						n.logger.Error("raft: remove server skipped %s (not leader)", serverID)
						break
					}
					n.logger.Error("raft: remove server %s failed: %v", serverID, err)
					continue
				}
				n.logger.Info("raft: removed server %s", serverID)
			}
		}
	}

	n.logger.Info("raft: sync peers done")
	return nil
}

// Close shuts down raft and the transport.
func (n *Node) Close() error {
	// try to transfer leadership
	_ = n.raft.LeadershipTransfer()

	future := n.raft.Shutdown()
	if err := future.Error(); err != nil {
		return err
	}
	return n.transport.Close()
}

// SyncLastSignState replicates the provided signing metadata through Raft so
// every node observes the same height/round/step before attempting a sign. The
// request only succeeds when the caller currently holds leadership.
func (n *Node) SyncLastSignState(lastSign *fsm.LastSignState) (*fsm.LastSignState, error) {
	if err := n.ensureLeader(); err != nil {
		return nil, err
	}

	return n.apply(lastSign)
}

// CurrentState returns a copy of the current last synced state.
func (n *Node) GetLastSignState() *fsm.LastSignState {
	return n.fsm.GetLastSignState()
}

// IsLeader reports whether this node currently holds raft leadership.
func (n *Node) IsLeader() bool {
	return n.raft.State() == raft.Leader
}

// LeaderInfo returns the current raft leader address and ID.
func (n *Node) LeaderInfo() (addr string, id string) {
	addrRaw, idRaw := n.raft.LeaderWithID()
	return string(addrRaw), string(idRaw)
}

// Join adds a new voter to the raft quorum.
func (n *Node) Join(id, address string) error {
	if err := n.ensureLeader(); err != nil {
		return err
	}
	future := n.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(address), 0, n.autoTimeout)
	return future.Error()
}

// VerifyLeader verify whether this raft node has the lease
// by asking to peers.
func (n *Node) VerifyLeader() error {
	future := n.raft.VerifyLeader()
	return future.Error()
}

// EnsureLeader light weight local state checking
func (n *Node) EnsureLeader() error {
	return n.ensureLeader()
}

// TransferLeadershipTo requests a leadership transfer to the provided server.
func (n *Node) TransferLeadershipTo(id, address string) error {
	if err := n.ensureLeader(); err != nil {
		return err
	}
	future := n.raft.LeadershipTransferToServer(raft.ServerID(id), raft.ServerAddress(address))
	return future.Error()
}

func (n *Node) ensureLeader() error {
	if n.raft.State() == raft.Leader {
		return nil
	}
	return n.notLeaderError()
}

func (n *Node) notLeaderError() error {
	addr, id := n.raft.LeaderWithID()
	return &NotLeaderError{LeaderAdr: string(addr), LeaderID: string(id)}
}

func (n *Node) apply(lastSignState *fsm.LastSignState) (*fsm.LastSignState, error) {
	data, err := json.Marshal(lastSignState)
	if err != nil {
		return nil, err
	}

	future := n.raft.Apply(data, n.autoTimeout)
	if err := future.Error(); err != nil {
		if errors.Is(err, raft.ErrNotLeader) {
			return nil, n.notLeaderError()
		}
		return nil, err
	}

	return future.Response().(*fsm.LastSignState), nil
}
