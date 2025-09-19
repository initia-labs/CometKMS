package keyserver

import (
	"context"
	"fmt"
	"sync"

	cometlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/types"
	"golang.org/x/sync/errgroup"

	edp "github.com/initia-labs/CometKMS/pkg/endpoint"
)

type validatorEndpoint struct {
	addr    string
	logger  cometlog.Logger
	dialer  privval.SocketDialer
	chainID string
	pv      types.PrivValidator

	mu       sync.Mutex
	endpoint *edp.SignerDialerEndpoint
	signer   *edp.SignerServer
}

func newValidatorEndpoint(addr string, dialer privval.SocketDialer, chainID string, pv types.PrivValidator, logger cometlog.Logger) *validatorEndpoint {
	if logger == nil {
		logger = cometlog.NewNopLogger()
	}
	return &validatorEndpoint{
		addr:    addr,
		logger:  logger.With("validator", addr),
		dialer:  dialer,
		chainID: chainID,
		pv:      pv,
	}
}

func (v *validatorEndpoint) start(ctx context.Context, g *errgroup.Group) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.endpoint != nil {
		return nil
	}

	endpoint := edp.NewSignerDialerEndpoint(v.logger, v.dialer)
	signer := edp.NewSignerServer(ctx, g, endpoint, v.chainID, v.pv)

	if err := endpoint.Start(); err != nil {
		endpoint.Stop()
		return fmt.Errorf("endpoint start: %w", err)
	}
	if err := signer.Start(); err != nil {
		endpoint.Stop()
		signer.Stop()
		return fmt.Errorf("signer start: %w", err)
	}

	v.endpoint = endpoint
	v.signer = signer
	v.logger.Info("validator connection active")
	return nil
}

func (v *validatorEndpoint) stop() {
	v.mu.Lock()
	endpoint := v.endpoint
	signer := v.signer
	v.endpoint = nil
	v.signer = nil
	v.mu.Unlock()

	if signer != nil && signer.IsRunning() {
		if err := signer.Stop(); err != nil {
			v.logger.Error("validator signer stop failed", "err", err)
		}
	}
	if endpoint != nil && endpoint.IsRunning() {
		if err := endpoint.Stop(); err != nil {
			v.logger.Error("validator endpoint stop failed", "err", err)
		}
	}
}
