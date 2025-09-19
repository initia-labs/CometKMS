package keyserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cometlog "github.com/cometbft/cometbft/libs/log"
	cmtnet "github.com/cometbft/cometbft/libs/net"
	"github.com/cometbft/cometbft/privval"
	"golang.org/x/sync/errgroup"

	raftnode "github.com/initia-labs/CometKMS/pkg/raft"
)

// Server dials a CometBFT validator and answers privval signing requests
// whenever this Keystone node holds the active lease.
type Server struct {
	g      *errgroup.Group
	cfg    Config
	logger cometlog.Logger

	pv     *PrivValidator
	filePV *privval.FilePV
	node   *raftnode.Node

	mu         sync.Mutex
	validators []*validatorEndpoint
}

// New constructs a key server backed by the provided Raft node.
func New(g *errgroup.Group, cfg Config, node *raftnode.Node, logger cometlog.Logger) (*Server, error) {
	if logger == nil {
		logger = cometlog.NewTMLogger(cometlog.NewSyncWriter(os.Stdout)).With("module", "cmkms-keyserver")
	} else {
		logger = logger.With("module", "cmkms-keyserver")
	}

	if cfg.ChainID == "" {
		return nil, errors.New("keyserver: chain id required")
	}
	if len(cfg.ValidatorAddrs) == 0 {
		return nil, errors.New("keyserver: validator address required")
	}
	if cfg.PrivKeyPath == "" || cfg.PrivStatePath == "" {
		return nil, errors.New("keyserver: priv-val key and state paths required")
	}

	for _, addr := range cfg.ValidatorAddrs {
		protocol, _ := cmtnet.ProtocolAndAddress(addr)
		switch protocol {
		case "unix", "tcp":
		default:
			return nil, fmt.Errorf("keyserver: unsupported validator address protocol %q", protocol)
		}
	}

	filePV := privval.LoadFilePV(cfg.PrivKeyPath, cfg.PrivStatePath)
	wrappedPV := NewPrivValidator(filePV, node)

	srv := &Server{
		g:          g,
		cfg:        cfg,
		logger:     logger,
		pv:         wrappedPV,
		filePV:     filePV,
		node:       node,
		validators: nil,
	}

	return srv, nil
}

// Start launches the Raft lease loop and begins serving privval requests.
func (s *Server) Start(ctx context.Context, g *errgroup.Group) error {
	g.Go(func() error {
		return s.checkLeadership(ctx, g)
	})

	g.Go(func() error {
		<-ctx.Done()
		if err := s.Stop(); err != nil {
			s.logger.Error("keyserver stop error", "err", err)
		}
		return nil
	})

	s.logger.Info("keyserver started", "validators", strings.Join(s.cfg.ValidatorAddrs, ","), "signer_id", s.cfg.SignerID)
	return nil
}

// Stop stops the signer and releases the lease.
func (s *Server) Stop() error {
	s.stopSigner()
	s.logger.Info("keyserver stopped")
	return nil
}

func (s *Server) checkLeadership(ctx context.Context, g *errgroup.Group) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
			if err := s.node.EnsureLeader(); err == nil {
				if err := s.startSigner(ctx, g); err != nil {
					s.logger.Error("failed to activate signer", "err", err)
					return fmt.Errorf("failed to activate signer: %w", err)
				}
			} else {
				s.stopSigner()
			}
		}
	}
}

func (s *Server) startSigner(ctx context.Context, g *errgroup.Group) error {
	s.mu.Lock()
	if len(s.validators) > 0 {
		s.mu.Unlock()
		return nil
	}

	validators := make([]*validatorEndpoint, 0, len(s.cfg.ValidatorAddrs))
	for _, addr := range s.cfg.ValidatorAddrs {
		ve := newValidatorEndpoint(addr, s.newDialer(addr), s.cfg.ChainID, s.pv, s.logger)
		if err := ve.start(ctx, g); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("validator %s: %w", addr, err)
		}
		validators = append(validators, ve)
	}

	s.validators = validators
	s.mu.Unlock()

	s.logger.Info("leader selected", "validators", len(validators))
	return nil
}

func (s *Server) stopSigner() {
	s.mu.Lock()
	validators := s.validators
	s.validators = nil
	s.mu.Unlock()

	for _, v := range validators {
		v.stop()
	}
}

func (s *Server) newDialer(addr string) privval.SocketDialer {
	protocol, address := cmtnet.ProtocolAndAddress(addr)
	switch protocol {
	case "unix":
		return privval.DialUnixFn(address)
	case "tcp":
		return privval.DialTCPFn(address, 3*time.Second, ed25519.GenPrivKey())
	default:
		return func() (net.Conn, error) {
			return nil, fmt.Errorf("unsupported protocol %q", protocol)
		}
	}
}
