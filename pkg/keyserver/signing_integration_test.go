package keyserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cometlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/privval"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"golang.org/x/sync/errgroup"

	"github.com/initia-labs/CometKMS/pkg/fsm"
	raftnode "github.com/initia-labs/CometKMS/pkg/raft"
)

const testChainID = "chain-test"

type clusterNode struct {
	id            string
	node          *raftnode.Node
	address       string
	nodeGroup     *errgroup.Group
	nodeCancel    context.CancelFunc
	server        *Server
	serverInitGrp *errgroup.Group
	serverGroup   *errgroup.Group
	serverCancel  context.CancelFunc
	shutdown      bool
}

func TestKeyserverClusterHandlesSigningAndPreventsDoubleSign(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	peers := makeTestPeers(t, 3)
	cluster := startRaftCluster(t, ctx, peers)
	t.Cleanup(func() {
		for _, node := range cluster {
			closeClusterNode(t, node)
		}
	})

	baseKeyDir := t.TempDir()
	baseKeyPath := filepath.Join(baseKeyDir, "priv_key.json")
	baseStatePath := filepath.Join(baseKeyDir, "priv_state.json")
	basePV := privval.GenFilePV(baseKeyPath, baseStatePath)
	basePV.Save()

	validatorAddr, validatorEndpoint, signerClient := startValidator(t, testChainID)
	t.Cleanup(func() {
		if err := signerClient.Close(); err != nil {
			t.Logf("signer client close: %v", err)
		}
		validatorEndpoint.Stop()
	})

	startKeyservers(t, ctx, cluster, baseKeyPath, baseStatePath, []string{validatorAddr}, testChainID)

	leader := waitForLeaderNode(t, cluster, 10*time.Second)
	waitForValidatorConnection(t, signerClient, 10*time.Second)

	validatorAddress := ed25519.GenPrivKey().PubKey().Address().Bytes()

	initialTemplate := makeVote(10, 0x01, validatorAddress)
	if _, err := signVoteWithRetry(t, signerClient, testChainID, initialTemplate, 5*time.Second); err != nil {
		t.Fatalf("sign vote: %v", err)
	}
	initialState := leader.node.GetLastSignState()
	if initialState == nil {
		t.Fatal("leader did not store sign state")
	}
	waitForClusterSignState(t, cluster, initialState, 5*time.Second)

	conflicting := makeVote(10, 0xFF, validatorAddress)
	err := signerClient.SignVote(testChainID, cloneVote(conflicting))
	var remoteErr *privval.RemoteSignerError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("expected remote signer error, got %v", err)
	}
	waitForClusterSignState(t, cluster, initialState, 5*time.Second)

	closeClusterNode(t, leader)

	newLeader := waitForLeaderNode(t, cluster, 10*time.Second)
	waitForValidatorConnection(t, signerClient, 10*time.Second)

	newTemplate := makeVote(12, 0x02, validatorAddress)
	if _, err := signVoteWithRetry(t, signerClient, testChainID, newTemplate, 5*time.Second); err != nil {
		t.Fatalf("sign vote after leadership change: %v", err)
	}
	latestState := newLeader.node.GetLastSignState()
	if latestState == nil {
		t.Fatal("new leader did not store sign state")
	}
	waitForClusterSignState(t, cluster, latestState, 5*time.Second)

	conflicting = makeVote(12, 0x03, validatorAddress)
	err = signerClient.SignVote(testChainID, cloneVote(conflicting))
	if !errors.As(err, &remoteErr) {
		t.Fatalf("expected remote signer error after leadership change, got %v", err)
	}
	waitForClusterSignState(t, cluster, latestState, 5*time.Second)
}

func startRaftCluster(t *testing.T, ctx context.Context, peers []raftnode.Peer) []*clusterNode {
	t.Helper()

	baseDir := t.TempDir()
	cluster := make([]*clusterNode, 0, len(peers))
	for i, peer := range peers {
		cfg := raftnode.Config{
			ID:          peer.ID,
			RaftDir:     filepath.Join(baseDir, peer.ID),
			BindAddress: peer.Address,
			Bootstrap:   i == 0,
			Peers:       peers,
			Logger:      cometlog.NewNopLogger(),
			LogLevel:    hclog.Off,
			LogOutput:   io.Discard,
		}

		node, err := raftnode.NewNode(cfg)
		if err != nil {
			t.Fatalf("new raft node %s: %v", cfg.ID, err)
		}
		grp := new(errgroup.Group)
		nodeCtx, nodeCancel := context.WithCancel(ctx)
		if err := node.Start(nodeCtx, grp); err != nil {
			node.Close()
			t.Fatalf("start raft node %s: %v", cfg.ID, err)
		}

		cluster = append(cluster, &clusterNode{
			id:         cfg.ID,
			node:       node,
			address:    peer.Address,
			nodeGroup:  grp,
			nodeCancel: nodeCancel,
		})
	}

	return cluster
}

func TestKeyserverConcurrentSigningWithLeaderChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	peers := makeTestPeers(t, 3)
	cluster := startRaftCluster(t, ctx, peers)
	t.Cleanup(func() {
		for _, node := range cluster {
			closeClusterNode(t, node)
		}
	})

	baseKeyDir := t.TempDir()
	baseKeyPath := filepath.Join(baseKeyDir, "priv_key.json")
	baseStatePath := filepath.Join(baseKeyDir, "priv_state.json")
	keeperPV := privval.GenFilePV(baseKeyPath, baseStatePath)
	keeperPV.Save()

	validatorAddr, validatorEndpoint, signerClient := startValidator(t, testChainID)
	t.Cleanup(func() {
		if err := signerClient.Close(); err != nil {
			t.Logf("signer client close: %v", err)
		}
		validatorEndpoint.Stop()
	})

	startKeyservers(t, ctx, cluster, baseKeyPath, baseStatePath, []string{validatorAddr}, testChainID)

	if _, err := waitForLeaderNodeCtx(cluster, 10*time.Second); err != nil {
		t.Fatalf("leader setup: %v", err)
	}
	waitForValidatorConnection(t, signerClient, 10*time.Second)

	validatorAddress := ed25519.GenPrivKey().PubKey().Address().Bytes()
	const maxHeight = int64(20)
	var signedHeight atomic.Int64
	stopCh := make(chan struct{})
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	rnd := rand.New(rand.NewSource(42))

	// Sequential signing goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for height := int64(1); height <= maxHeight; height++ {
			template := makeVote(height, byte(height%0x80+1), validatorAddress)
			if _, err := waitForLeaderNodeCtx(cluster, 5*time.Second); err != nil {
				errCh <- fmt.Errorf("leader wait: %w", err)
				return
			}
			if _, err := signVoteWithRetry(t, signerClient, testChainID, template, 5*time.Second); err != nil {
				errCh <- fmt.Errorf("sign vote at height %d: %w", height, err)
				return
			}
			signedHeight.Store(height)
			waitForClusterSignHeight(t, cluster, height, 5*time.Second)
			if rnd.Float64() < 0.4 {
				if err := transferRandomLeadership(cluster, rnd, 5*time.Second); err != nil {
					if !isTemporaryLeadershipError(err) {
						errCh <- fmt.Errorf("leadership transfer: %w", err)
						return
					}
				}
			}
		}
		close(stopCh)
	}()

	// Stale signing attacker goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
			}

			height := signedHeight.Load()
			if height == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			conflicting := makeVote(height, 0xF0, validatorAddress)
			err := signerClient.SignVote(testChainID, cloneVote(conflicting))
			if err == nil {
				errCh <- fmt.Errorf("conflicting signature succeeded at height %d", height)
				return
			}
			var remoteErr *privval.RemoteSignerError
			if !errors.As(err, &remoteErr) && !isTemporarySignError(err) {
				errCh <- fmt.Errorf("unexpected stale sign error: %v", err)
				return
			}
			waitForClusterSignHeight(t, cluster, height, 5*time.Second)
			time.Sleep(25 * time.Millisecond)
		}
	}()

	// Wait for completion or error
	select {
	case err := <-errCh:
		t.Fatalf("concurrent signing failure: %v", err)
	case <-stopCh:
	}

	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatalf("post wait failure: %v", err)
	default:
	}

	finalLeader := waitForLeaderNode(t, cluster, 5*time.Second)
	state := finalLeader.node.GetLastSignState()
	if state == nil || state.Height != maxHeight {
		t.Fatalf("expected final height %d, got %+v", maxHeight, state)
	}
	waitForClusterSignHeight(t, cluster, maxHeight, 5*time.Second)
}

func TestKeyserverMultipleValidatorsRejectConflictingVotes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	peers := makeTestPeers(t, 3)
	cluster := startRaftCluster(t, ctx, peers)
	t.Cleanup(func() {
		for _, node := range cluster {
			closeClusterNode(t, node)
		}
	})

	baseKeyDir := t.TempDir()
	baseKeyPath := filepath.Join(baseKeyDir, "priv_key.json")
	baseStatePath := filepath.Join(baseKeyDir, "priv_state.json")
	basePV := privval.GenFilePV(baseKeyPath, baseStatePath)
	basePV.Save()

	validatorAddrA, endpointA, signerA := startValidator(t, testChainID)
	validatorAddrB, endpointB, signerB := startValidator(t, testChainID)
	t.Cleanup(func() {
		if err := signerA.Close(); err != nil {
			t.Logf("signer A close: %v", err)
		}
		if err := signerB.Close(); err != nil {
			t.Logf("signer B close: %v", err)
		}
		endpointA.Stop()
		endpointB.Stop()
	})

	startKeyservers(t, ctx, cluster, baseKeyPath, baseStatePath, []string{validatorAddrA, validatorAddrB}, testChainID)

	leader := waitForLeaderNode(t, cluster, 10*time.Second)
	waitForValidatorConnection(t, signerA, 10*time.Second)
	waitForValidatorConnection(t, signerB, 10*time.Second)

	validatorAddress := ed25519.GenPrivKey().PubKey().Address().Bytes()

	initialTemplate := makeVote(30, 0x21, validatorAddress)
	if _, err := signVoteWithRetry(t, signerA, testChainID, initialTemplate, 5*time.Second); err != nil {
		t.Fatalf("initial sign vote: %v", err)
	}
	currentState := leader.node.GetLastSignState()
	if currentState == nil {
		t.Fatal("leader did not store initial sign state")
	}
	waitForClusterSignState(t, cluster, currentState, 5*time.Second)

	rnd := rand.New(rand.NewSource(1337))

	type signResult struct {
		name string
		vote *cmtproto.Vote
		err  error
	}

	const attempts = 20
	for i := 0; i < attempts; i++ {
		delta := int64(rnd.Intn(3) + 1)
		targetHeight := currentState.Height + delta
		voteA := makeVote(targetHeight, byte(rnd.Intn(0x40)+0x10), validatorAddress)
		voteB := makeVote(targetHeight, byte(rnd.Intn(0x40)+0x50), validatorAddress)

		results := make(chan signResult, 2)
		var wg sync.WaitGroup
		wg.Add(2)

		callerA := func() {
			defer wg.Done()
			time.Sleep(time.Duration(rnd.Intn(30)) * time.Millisecond)
			attempt := cloneVote(voteA)
			err := signerA.SignVote(testChainID, attempt)
			results <- signResult{name: "validatorA", vote: attempt, err: err}
		}

		callerB := func() {
			defer wg.Done()
			time.Sleep(time.Duration(rnd.Intn(30)) * time.Millisecond)
			attempt := cloneVote(voteB)
			err := signerB.SignVote(testChainID, attempt)
			results <- signResult{name: "validatorB", vote: attempt, err: err}
		}

		if rnd.Intn(2) == 0 {
			go callerA()
			go callerB()
		} else {
			go callerB()
			go callerA()
		}

		wg.Wait()
		close(results)

		var success *signResult
		failures := make([]signResult, 0, 2)
		for res := range results {
			if res.err == nil {
				if success != nil {
					t.Fatalf("expected exactly one successful signature per height, got additional success from %s", res.name)
				}
				copy := res
				success = &copy
				continue
			}
			failures = append(failures, res)
			var remoteErr *privval.RemoteSignerError
			if !errors.As(res.err, &remoteErr) {
				t.Fatalf("expected remote signer error from %s, got %v", res.name, res.err)
			}
			if !strings.Contains(remoteErr.Description, "conflicting") {
				t.Fatalf("unexpected error from %s: %v", res.name, res.err)
			}
		}

		if success == nil {
			t.Fatal("expected one validator to receive a signature successfully")
		}
		if len(failures) != 1 {
			t.Fatalf("expected one conflicting signature to fail, got %d", len(failures))
		}
		if len(success.vote.Signature) == 0 {
			t.Fatalf("successful vote from %s missing signature", success.name)
		}
		if len(failures[0].vote.Signature) != 0 {
			t.Fatalf("failed vote from %s unexpectedly received a signature", failures[0].name)
		}

		waitForClusterSignHeight(t, cluster, targetHeight, 5*time.Second)
		leader = waitForLeaderNode(t, cluster, 5*time.Second)
		state := leader.node.GetLastSignState()
		if state == nil || state.Height != targetHeight {
			t.Fatalf("expected cluster height %d, got %+v", targetHeight, state)
		}

		expectedSignBytes := types.VoteSignBytes(testChainID, success.vote)
		if !bytes.Equal(expectedSignBytes, state.SignBytes) {
			t.Fatalf("leader sign bytes mismatch at height %d: expected %X, got %X", targetHeight, expectedSignBytes, []byte(state.SignBytes))
		}

		currentState = state
	}
}

func startKeyservers(t *testing.T, ctx context.Context, cluster []*clusterNode, baseKeyPath, baseStatePath string, validatorAddrs []string, chainID string) {
	t.Helper()

	keysDir := t.TempDir()
	validatorURIs := make([]string, 0, len(validatorAddrs))
	for _, addr := range validatorAddrs {
		validatorURIs = append(validatorURIs, fmt.Sprintf("tcp://%s", addr))
	}

	for _, node := range cluster {
		if node.shutdown {
			continue
		}

		nodeKeyDir := filepath.Join(keysDir, node.id)
		if err := os.MkdirAll(nodeKeyDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", nodeKeyDir, err)
		}

		privKeyPath := filepath.Join(nodeKeyDir, "priv_key.json")
		privStatePath := filepath.Join(nodeKeyDir, "priv_state.json")
		copyFile(t, baseKeyPath, privKeyPath)
		copyFile(t, baseStatePath, privStatePath)

		cfg := Config{
			ChainID:        chainID,
			ValidatorAddrs: validatorURIs,
			PrivKeyPath:    privKeyPath,
			PrivStatePath:  privStatePath,
			SignerID:       node.id,
		}
		cfg.Normalize()

		setupGrp := new(errgroup.Group)
		server, err := New(setupGrp, cfg, node.node, cometlog.NewNopLogger())
		if err != nil {
			t.Fatalf("new keyserver %s: %v", node.id, err)
		}

		serverGrp := new(errgroup.Group)
		serverCtx, serverCancel := context.WithCancel(ctx)
		if err := server.Start(serverCtx, serverGrp); err != nil {
			server.Stop()
			t.Fatalf("start keyserver %s: %v", node.id, err)
		}

		node.server = server
		node.serverInitGrp = setupGrp
		node.serverGroup = serverGrp
		node.serverCancel = serverCancel
	}
}

func closeClusterNode(t *testing.T, node *clusterNode) {
	if node == nil || node.shutdown {
		return
	}

	node.shutdown = true
	if node.serverCancel != nil {
		node.serverCancel()
	}
	if node.server != nil {
		if err := node.server.Stop(); err != nil {
			t.Logf("keyserver %s stop: %v", node.id, err)
		}
	}
	waitGroup(t, "keyserver", node.id, node.serverGroup)
	waitGroup(t, "keyserver-init", node.id, node.serverInitGrp)

	if node.nodeCancel != nil {
		node.nodeCancel()
	}
	if node.node != nil {
		if err := node.node.Close(); err != nil {
			t.Logf("raft node %s close: %v", node.id, err)
		}
	}
	waitGroup(t, "raft", node.id, node.nodeGroup)
}

func waitForLeaderNode(t *testing.T, cluster []*clusterNode, timeout time.Duration) *clusterNode {
	t.Helper()
	node, err := waitForLeaderNodeCtx(cluster, timeout)
	if err != nil {
		t.Fatalf("no leader elected within %s: %v", timeout, err)
	}
	return node
}

func waitForLeaderNodeCtx(cluster []*clusterNode, timeout time.Duration) (*clusterNode, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if leader, err := findLeaderNode(cluster); err == nil {
			return leader, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("no leader within %s", timeout)
}

func findLeaderNode(cluster []*clusterNode) (*clusterNode, error) {
	for _, node := range cluster {
		if node.shutdown {
			continue
		}
		if err := node.node.EnsureLeader(); err != nil {
			continue
		}
		_, leaderID := node.node.LeaderInfo()
		if leaderID == node.id {
			return node, nil
		}
	}
	return nil, fmt.Errorf("leader not found")
}

func waitForClusterSignState(t *testing.T, cluster []*clusterNode, expected *fsm.LastSignState, timeout time.Duration) {
	t.Helper()
	for _, node := range cluster {
		if node.shutdown {
			continue
		}
		waitForNodeSignState(t, node, expected, timeout)
	}
}

func waitForNodeSignState(t *testing.T, node *clusterNode, expected *fsm.LastSignState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := node.node.GetLastSignState()
		if statesEqual(state, expected) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node %s did not observe sign state %+v", node.id, expected)
}

func waitForClusterSignHeight(t *testing.T, cluster []*clusterNode, height int64, timeout time.Duration) {
	t.Helper()
	for _, node := range cluster {
		if node.shutdown {
			continue
		}
		waitForNodeSignHeight(t, node, height, timeout)
	}
}

func waitForNodeSignHeight(t *testing.T, node *clusterNode, height int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := node.node.GetLastSignState()
		if state != nil && state.Height >= height {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node %s did not reach height %d", node.id, height)
}

func statesEqual(a, b *fsm.LastSignState) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Height == b.Height && a.Round == b.Round && a.Step == b.Step
}

func startValidator(t *testing.T, chainID string) (string, *privval.SignerListenerEndpoint, *privval.SignerClient) {
	t.Helper()

	addr := allocateAddress(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen validator: %v", err)
	}
	tcpListener := privval.NewTCPListener(ln, ed25519.GenPrivKey())
	endpoint := privval.NewSignerListenerEndpoint(cometlog.NewNopLogger(), tcpListener)
	client, err := privval.NewSignerClient(endpoint, chainID)
	if err != nil {
		t.Fatalf("new signer client: %v", err)
	}

	return addr, endpoint, client
}

func waitForValidatorConnection(t *testing.T, client *privval.SignerClient, timeout time.Duration) {
	t.Helper()
	if err := client.WaitForConnection(timeout); err != nil {
		t.Fatalf("validator connection timeout: %v", err)
	}
	if !client.IsConnected() {
		t.Fatal("validator not connected")
	}
}

func signVoteWithRetry(t *testing.T, client *privval.SignerClient, chainID string, template *cmtproto.Vote, timeout time.Duration) (*cmtproto.Vote, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		vote := cloneVote(template)
		err := client.SignVote(chainID, vote)
		if err == nil {
			return vote, nil
		}
		if !isTemporarySignError(err) {
			return nil, err
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("sign vote timeout")
	}
	return nil, lastErr
}

func cloneVote(src *cmtproto.Vote) *cmtproto.Vote {
	if src == nil {
		return nil
	}
	copy := *src
	copy.ValidatorAddress = append([]byte(nil), src.ValidatorAddress...)
	copy.BlockID.Hash = append([]byte(nil), src.BlockID.Hash...)
	copy.BlockID.PartSetHeader.Hash = append([]byte(nil), src.BlockID.PartSetHeader.Hash...)
	return &copy
}

func isTemporarySignError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, privval.ErrNoConnection) || errors.Is(err, io.EOF) {
		return true
	}
	var remoteErr *privval.RemoteSignerError
	if errors.As(err, &remoteErr) {
		return strings.Contains(remoteErr.Description, "raft error")
	}
	return true
}

func makeVote(height int64, tag byte, validatorAddress []byte) *cmtproto.Vote {
	blockHash := make([]byte, 32)
	partHash := make([]byte, 32)
	for i := range blockHash {
		blockHash[i] = tag ^ byte(i+1)
	}
	for i := range partHash {
		partHash[i] = tag ^ byte(0x80+i)
	}

	addrCopy := make([]byte, len(validatorAddress))
	copy(addrCopy, validatorAddress)

	return &cmtproto.Vote{
		Type:             cmtproto.PrevoteType,
		Height:           height,
		Round:            0,
		Timestamp:        time.Now(),
		ValidatorAddress: addrCopy,
		ValidatorIndex:   0,
		BlockID: cmtproto.BlockID{
			Hash:          blockHash,
			PartSetHeader: cmtproto.PartSetHeader{Total: 1, Hash: partHash},
		},
	}
}

func makeTestPeers(t *testing.T, count int) []raftnode.Peer {
	t.Helper()
	peers := make([]raftnode.Peer, 0, count)
	for i := 0; i < count; i++ {
		peers = append(peers, raftnode.Peer{ID: fmt.Sprintf("node-%d", i), Address: allocateAddress(t)})
	}
	return peers
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func transferRandomLeadership(cluster []*clusterNode, rnd *rand.Rand, timeout time.Duration) error {
	leader, err := waitForLeaderNodeCtx(cluster, timeout)
	if err != nil {
		return err
	}
	followers := make([]*clusterNode, 0, len(cluster))
	for _, node := range cluster {
		if node.shutdown || node.id == leader.id {
			continue
		}
		followers = append(followers, node)
	}
	if len(followers) == 0 {
		return nil
	}
	target := followers[rnd.Intn(len(followers))]
	if err := leader.node.TransferLeadershipTo(target.id, target.address); err != nil {
		return err
	}
	_, err = waitForLeaderNodeCtx(cluster, timeout)
	return err
}

func isTemporaryLeadershipError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, raft.ErrLeadershipTransferInProgress) {
		return true
	}
	var notLeader *raftnode.NotLeaderError
	return errors.As(err, &notLeader)
}

func waitGroup(t *testing.T, name, id string, grp *errgroup.Group) {
	if grp == nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- grp.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("%s %s: %v", name, id, err)
		}
	case <-time.After(5 * time.Second):
		t.Logf("%s %s: wait timeout", name, id)
	}
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
