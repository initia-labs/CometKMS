package keyserver

import (
	"fmt"
	"sync"

	"github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/privval"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/initia-labs/CometKMS/pkg/fsm"
	raftnode "github.com/initia-labs/CometKMS/pkg/raft"
)

// PrivValidator wraps a PrivValidator and blocks signing operations until
// the CometKMS lease is active.
type PrivValidator struct {
	inner *privval.FilePV
	node  *raftnode.Node
	mu    sync.Mutex
}

// syncLastSignStateHook is for tests to coordinate interleavings.
// It should be nil in production.
var syncLastSignStateHook func(point string, state *fsm.LastSignState)

// NewPrivValidator returns a validator that defers to inner once the
// CometKMS lease is available.
func NewPrivValidator(inner *privval.FilePV, node *raftnode.Node) *PrivValidator {
	return &PrivValidator{inner: inner, node: node}
}

func (l *PrivValidator) GetPubKey() (crypto.PubKey, error) {
	return l.inner.GetPubKey()
}

// wrapRaftError wraps an error with "raft error" prefix if non-nil.
func wrapRaftError(err error) error {
	if err != nil {
		return fmt.Errorf("raft error: %w", err)
	}
	return nil
}

func (l *PrivValidator) SignVote(chainID string, vote *cmtproto.Vote) error {
	if err := l.node.VerifyLeader(); err != nil {
		return wrapRaftError(err)
	}
	if err := l.syncLastSignState(); err != nil {
		return wrapRaftError(err)
	}
	l.mu.Lock()
	err := l.inner.SignVote(chainID, vote)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	if err := l.syncLastSignState(); err != nil {
		return wrapRaftError(err)
	}
	return nil
}

func (l *PrivValidator) SignProposal(chainID string, proposal *cmtproto.Proposal) error {
	if err := l.node.VerifyLeader(); err != nil {
		return wrapRaftError(err)
	}
	if err := l.syncLastSignState(); err != nil {
		return wrapRaftError(err)
	}
	l.mu.Lock()
	err := l.inner.SignProposal(chainID, proposal)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	if err := l.syncLastSignState(); err != nil {
		return wrapRaftError(err)
	}
	return nil
}

// syncLastSignState pushes the latest sign state through Raft and refreshes the
// on-disk priv-validator state so leadership changes cannot re-sign old blocks.
func (l *PrivValidator) syncLastSignState() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	lastSignState := fsm.FromFilePV(&l.inner.LastSignState)
	if lastSignState.Equal(l.node.GetLastSignState()) {
		return nil
	}

	if syncLastSignStateHook != nil {
		syncLastSignStateHook("before-raft", lastSignState)
	}

	state, err := l.node.SyncLastSignState(lastSignState)
	if err != nil {
		return err
	}

	if syncLastSignStateHook != nil {
		syncLastSignStateHook("before-write", state)
	}

	state.CopyToFilePV(&l.inner.LastSignState)

	return nil
}
