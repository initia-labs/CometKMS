package fsm

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/hashicorp/raft"
)

var _ raft.FSM = (*FSMState)(nil)

// FSMState is the replicated lease metadata shared by the cluster.
type FSMState struct {
	mu sync.Mutex

	lastSignState *LastSignState
}

// NewFSMState creates an empty FSMState instance.
func NewFSMState() *FSMState {
	return &FSMState{}
}

// SyncLastSignState updates the last sign state if src is higher than the current state.
func (s *FSMState) SyncLastSignState(src *LastSignState) *LastSignState {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastSignState == nil || s.lastSignState.Less(src) {
		s.lastSignState = src.Clone()
	}
	return s.lastSignState
}

// GetLastSignState returns a copy of the last sign state.
func (s *FSMState) GetLastSignState() *LastSignState {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastSignState == nil {
		return nil
	}

	return s.lastSignState.Clone()
}

// Apply implements raft.FSM.
func (s *FSMState) Apply(log *raft.Log) any {
	var given *LastSignState
	_ = json.Unmarshal(log.Data, &given) // ignore error, will be nil if unmarshal fails

	return s.SyncLastSignState(given)
}

// Restore implements raft.FSM.
func (s *FSMState) Restore(snapshot io.ReadCloser) error {
	defer snapshot.Close()

	var state LastSignState
	if err := json.NewDecoder(snapshot).Decode(&state); err != nil {
		return err
	}

	s.mu.Lock()
	s.lastSignState = &state
	s.mu.Unlock()
	return nil
}

// Snapshot implements raft.FSM.
func (s *FSMState) Snapshot() (raft.FSMSnapshot, error) {
	return s, nil
}

// Persist implements raft.FSMSnapshot.
func (s *FSMState) Persist(sink raft.SnapshotSink) error {
	data, err := json.Marshal(s.lastSignState)
	if err != nil {
		_ = sink.Cancel()
		return err
	}

	if _, err := sink.Write(data); err != nil {
		_ = sink.Cancel()
		return err
	}

	return nil
}

// Release implements raft.FSMSnapshot.
func (s *FSMState) Release() {}
