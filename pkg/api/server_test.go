package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/initia-labs/CometKMS/pkg/fsm"
	"github.com/initia-labs/CometKMS/pkg/raft"
)

type stubLeasingNode struct {
	updateErr error
	calls     []updateCall
}

type updateCall struct {
	id      string
	address string
}

func (s *stubLeasingNode) GetLastSignState() *fsm.LastSignState { return nil }
func (s *stubLeasingNode) LeaderInfo() (string, string)         { return "", "" }
func (s *stubLeasingNode) IsLeader() bool                       { return true }
func (s *stubLeasingNode) UpdatePeer(id, address string) error {
	s.calls = append(s.calls, updateCall{id: id, address: address})
	return s.updateErr
}

func TestHandleUpdatePeer_Success(t *testing.T) {
	node := &stubLeasingNode{}
	server := NewServer(node, ":0", nil, true)

	req := httptest.NewRequest(http.MethodPost, "/raft/peer", bytes.NewBufferString(`{"id":"node-0","address":"host:9430"}`))
	rec := httptest.NewRecorder()

	server.handleUpdatePeer(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
	if len(node.calls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(node.calls))
	}
	call := node.calls[0]
	if call.id != "node-0" || call.address != "host:9430" {
		t.Fatalf("unexpected call args: %+v", call)
	}
}

func TestHandleUpdatePeer_NotLeader(t *testing.T) {
	node := &stubLeasingNode{
		updateErr: &raft.NotLeaderError{LeaderID: "node-1", LeaderAdr: "host:9430"},
	}
	server := NewServer(node, ":0", nil, true)

	req := httptest.NewRequest(http.MethodPost, "/raft/peer", bytes.NewBufferString(`{"id":"node-0","address":"host:9430"}`))
	rec := httptest.NewRecorder()

	server.handleUpdatePeer(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d", http.StatusConflict, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected json content type, got %q", got)
	}
}

func TestHandleUpdatePeer_MethodNotAllowed(t *testing.T) {
	node := &stubLeasingNode{}
	server := NewServer(node, ":0", nil, true)

	req := httptest.NewRequest(http.MethodGet, "/raft/peer", nil)
	rec := httptest.NewRecorder()

	server.handleUpdatePeer(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestHandleUpdatePeer_BadRequest(t *testing.T) {
	node := &stubLeasingNode{}
	server := NewServer(node, ":0", nil, true)

	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{"id":`},
		{name: "missing fields", body: `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/raft/peer", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()

			server.handleUpdatePeer(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}
		})
	}
}

func TestHandleUpdatePeer_InternalError(t *testing.T) {
	node := &stubLeasingNode{updateErr: errors.New("boom")}
	server := NewServer(node, ":0", nil, true)

	req := httptest.NewRequest(http.MethodPost, "/raft/peer", bytes.NewBufferString(`{"id":"node-0","address":"host:9430"}`))
	rec := httptest.NewRecorder()

	server.handleUpdatePeer(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

func TestHandleUpdatePeer_Disabled(t *testing.T) {
	node := &stubLeasingNode{}
	server := NewServer(node, ":0", nil, false)

	req := httptest.NewRequest(http.MethodPost, "/raft/peer", bytes.NewBufferString(`{"id":"node-0","address":"host:9430"}`))
	rec := httptest.NewRecorder()

	server.handleUpdatePeer(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d when endpoint disabled, got %d", http.StatusNotFound, rec.Code)
	}
	if len(node.calls) != 0 {
		t.Fatalf("expected no update calls when disabled, got %d", len(node.calls))
	}
}
