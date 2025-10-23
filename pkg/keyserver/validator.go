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

// NewPrivValidator returns a validator that defers to inner once the
// CometKMS lease is available.
func NewPrivValidator(inner *privval.FilePV, node *raftnode.Node) *PrivValidator {
	return &PrivValidator{inner: inner, node: node}
}

func (l *PrivValidator) GetPubKey() (crypto.PubKey, error) {
	return l.inner.GetPubKey()
}

func (l *PrivValidator) SignVote(chainID string, vote *cmtproto.Vote) error {
	if err := l.node.VerifyLeader(); err != nil {
		return fmt.Errorf("lease unavailable: %w", err)
	}
	if err := l.syncLastSignState(); err != nil {
		return err
	}
	l.mu.Lock()
	err := l.inner.SignVote(chainID, vote)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	return l.syncLastSignState()
}

func (l *PrivValidator) SignProposal(chainID string, proposal *cmtproto.Proposal) error {
	if err := l.node.VerifyLeader(); err != nil {
		return fmt.Errorf("lease unavailable: %w", err)
	}
	if err := l.syncLastSignState(); err != nil {
		return err
	}
	l.mu.Lock()
	err := l.inner.SignProposal(chainID, proposal)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	return l.syncLastSignState()
}

// syncLastSignState pushes the latest sign state through Raft and refreshes the
// on-disk priv-validator state so leadership changes cannot re-sign old blocks.
func (l *PrivValidator) syncLastSignState() error {
	l.mu.Lock()
	lastSignState := fsm.FromFilePV(&l.inner.LastSignState)
	if lastSignState.EqualHRS(l.node.GetLastSignState()) {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	state, err := l.node.SyncLastSignState(lastSignState)
	if err != nil {
		return err
	}

	l.mu.Lock()
	state.CopyToFilePV(&l.inner.LastSignState)
	l.mu.Unlock()

	return nil
}
