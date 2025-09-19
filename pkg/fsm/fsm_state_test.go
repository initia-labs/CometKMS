package fsm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/hashicorp/raft"
)

func TestFSMStateSyncAndGet(t *testing.T) {
	state := NewFSMState()
	if state.GetLastSignState() != nil {
		t.Fatal("expected new FSMState to be empty")
	}

	initial := &LastSignState{Height: 10, Round: 1, Step: 1}
	stored := state.SyncLastSignState(initial)
	if stored == initial {
		t.Fatal("expected SyncLastSignState to clone input")
	}
	if stored.Height != initial.Height || stored.Round != initial.Round || stored.Step != initial.Step {
		t.Fatalf("unexpected stored state %+v", stored)
	}

	initial.Height = 1
	if stored.Height != 10 {
		t.Fatalf("stored state mutated after caller modification: %+v", stored)
	}

	if state.GetLastSignState() == nil {
		t.Fatal("expected FSMState to be non-empty after sync")
	}

	copy := state.GetLastSignState()
	if copy == stored {
		t.Fatal("GetLastSignState should return clone")
	}
	copy.Height = 5
	if stored.Height != 10 {
		t.Fatalf("stored state mutated after Get clone modification: %+v", stored)
	}

	lower := &LastSignState{Height: 5, Round: 1, Step: 1}
	state.SyncLastSignState(lower)
	if got := state.GetLastSignState(); got.Height != 10 {
		t.Fatalf("lower state should not overwrite, got %+v", got)
	}

	higher := &LastSignState{Height: 11, Round: 0, Step: 0}
	state.SyncLastSignState(higher)
	got := state.GetLastSignState()
	if got.Height != 11 {
		t.Fatalf("expected higher state to overwrite, got %+v", got)
	}

	state.SyncLastSignState(nil) // should not panic
}

func TestFSMStateApply(t *testing.T) {
	state := NewFSMState()
	entry := &LastSignState{Height: 12, Round: 2, Step: 1}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp := state.Apply(&raft.Log{Data: data})
	result, ok := resp.(*LastSignState)
	if !ok {
		t.Fatalf("expected *LastSignState response, got %T", resp)
	}
	if result.Height != entry.Height {
		t.Fatalf("apply stored %+v", result)
	}

	// invalid payload should simply return current state
	resp = state.Apply(&raft.Log{Data: []byte("not json")})
	if resp == nil {
		t.Fatal("expected response for invalid payload")
	}
}

func TestFSMStateSnapshotAndPersist(t *testing.T) {
	state := NewFSMState()
	current := &LastSignState{Height: 15, Round: 1, Step: 2}
	state.SyncLastSignState(current)

	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	memSink := newMemorySink()
	if err := snapshot.Persist(memSink); err != nil {
		t.Fatalf("persist: %v", err)
	}

	data, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(memSink.Bytes(), data) {
		t.Fatalf("unexpected snapshot bytes: %x vs %x", memSink.Bytes(), data)
	}

	snapshot.Release() // should be no-op

	failing := &failingSink{}
	if err := snapshot.Persist(failing); err == nil {
		t.Fatal("expected persist error")
	}
	if !failing.canceled {
		t.Fatal("expected Persist to cancel sink on failure")
	}
}

func TestFSMStateRestore(t *testing.T) {
	state := NewFSMState()
	expected := &LastSignState{Height: 21, Round: 3, Step: 1}

	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := state.Restore(io.NopCloser(bytes.NewReader(data))); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := state.GetLastSignState()
	if got.Height != expected.Height || got.Round != expected.Round || got.Step != expected.Step {
		t.Fatalf("unexpected restored state %+v", got)
	}
}

type memorySink struct {
	bytes.Buffer
}

func newMemorySink() *memorySink { return &memorySink{} }

func (m *memorySink) Write(p []byte) (int, error) { return m.Buffer.Write(p) }

func (m *memorySink) Close() error { return nil }

func (m *memorySink) Cancel() error { return nil }

func (m *memorySink) ID() string { return "memory" }

type failingSink struct {
	canceled bool
}

func (f *failingSink) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func (f *failingSink) Close() error { return nil }

func (f *failingSink) Cancel() error {
	f.canceled = true
	return nil
}

func (f *failingSink) ID() string { return "failing" }
