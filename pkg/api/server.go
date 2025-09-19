package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	cometlog "github.com/cometbft/cometbft/libs/log"
	"golang.org/x/sync/errgroup"

	"github.com/initia-labs/CometKMS/pkg/fsm"
)

// LeasingNode exposes the minimal state used by the HTTP server.
type LeasingNode interface {
	GetLastSignState() *fsm.LastSignState
	LeaderInfo() (addr string, id string)
	IsLeader() bool
}

// Server exposes the Keystone HTTP status API.
type Server struct {
	node   LeasingNode
	addr   string
	logger cometlog.Logger
}

// NewServer constructs an API server bound to addr.
func NewServer(node LeasingNode, addr string, logger cometlog.Logger) *Server {
	if logger == nil {
		logger = cometlog.NewNopLogger()
	}
	return &Server{node: node, addr: addr, logger: logger}
}

// ListenAndServe runs the HTTP server until context cancellation.
func (s *Server) ListenAndServe(ctx context.Context, g *errgroup.Group) error {
	srv := &http.Server{Addr: s.addr, Handler: s.routes()}

	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("http shutdown error", "err", err)
		}
		return nil
	})

	g.Go(func() error {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	isLeader := s.node.IsLeader()
	leaderAddr, leaderID := s.node.LeaderInfo()
	lastSignState := s.node.GetLastSignState()

	resp := StatusResponse{
		IsLeader:      isLeader,
		LeaderAddr:    leaderAddr,
		LeaderID:      leaderID,
		LastSignState: lastSignState,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("failed to write http response", "err", err)
	}
}

// StatusResponse describes the current lease state.
type StatusResponse struct {
	IsLeader      bool               `json:"is_leader"`
	LeaderAddr    string             `json:"leader_addr,omitempty"`
	LeaderID      string             `json:"leader_id,omitempty"`
	LastSignState *fsm.LastSignState `json:"last_sign_state,omitempty"`
}
