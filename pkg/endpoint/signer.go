package endpoint

import (
	"context"
	"errors"
	"io"

	"github.com/cometbft/cometbft/libs/service"
	cmtsync "github.com/cometbft/cometbft/libs/sync"
	"github.com/cometbft/cometbft/privval"
	privvalproto "github.com/cometbft/cometbft/proto/tendermint/privval"
	"github.com/cometbft/cometbft/types"
	"golang.org/x/sync/errgroup"
)

// SignerServer runs the privval RPC loop against a SignerDialerEndpoint.
type SignerServer struct {
	service.BaseService

	// endpoint details for providing the privval service
	chainID  string
	endpoint *SignerDialerEndpoint
	privVal  types.PrivValidator

	handlerMtx               cmtsync.Mutex
	validationRequestHandler privval.ValidationRequestHandlerFunc

	g      *errgroup.Group
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSignerServer creates a signer instance that inherits cancellation from
// the supplied context but can also be stopped independently via BaseService.
func NewSignerServer(ctx context.Context, g *errgroup.Group, endpoint *SignerDialerEndpoint, chainID string, privVal types.PrivValidator) *SignerServer {
	signerCtx, cancel := context.WithCancel(ctx)
	ss := &SignerServer{
		endpoint:                 endpoint,
		chainID:                  chainID,
		privVal:                  privVal,
		validationRequestHandler: privval.DefaultValidationRequestHandler,

		g:      g,
		ctx:    signerCtx,
		cancel: cancel,
	}

	ss.BaseService = *service.NewBaseService(endpoint.Logger, "SignerServer", ss)

	return ss
}

// OnStart implements service.Service.
func (ss *SignerServer) OnStart() error {
	ss.g.Go(func() error {
		return ss.serviceLoop()
	})

	return nil
}

// OnStop implements service.Service.
func (ss *SignerServer) OnStop() {
	if ss.cancel != nil {
		ss.cancel()
	}
	ss.endpoint.Logger.Debug("SignerServer: OnStop calling Close")
	_ = ss.endpoint.Close()
}

// SetRequestHandler override the default function that is used to service requests
func (ss *SignerServer) SetRequestHandler(validationRequestHandler privval.ValidationRequestHandlerFunc) {
	ss.handlerMtx.Lock()
	defer ss.handlerMtx.Unlock()
	ss.validationRequestHandler = validationRequestHandler
}

// servicePendingRequest handles a single privval request, dropping the
// connection when a read error occurs so the dialer can retry.
func (ss *SignerServer) servicePendingRequest() {
	if !ss.IsRunning() {
		return // Ignore error from closing.
	}

	req, err := ss.endpoint.ReadMessage()
	if err != nil {
		if err != io.EOF {
			ss.Logger.Error("SignerServer: HandleMessage", "err", err)
		}

		// we have copied the whole signer and endpoint package from CometBFT due to
		// this part to drop connections more easily for retrying
		ss.Logger.Debug("SignerServer: dropping connection")
		ss.endpoint.DropConnection()
		return
	}

	var res privvalproto.Message
	{
		// limit the scope of the lock
		ss.handlerMtx.Lock()
		defer ss.handlerMtx.Unlock()
		res, err = ss.validationRequestHandler(ss.privVal, req, ss.chainID)
		if err != nil {
			// only log the error; we'll reply with an error in res
			ss.Logger.Error("SignerServer: handleMessage", "err", err)
		}
	}

	err = ss.endpoint.WriteMessage(res)
	if err != nil {
		ss.Logger.Error("SignerServer: writeMessage", "err", err)
	}
}

// serviceLoop keeps the dialer connected and drains requests until the server
// is told to stop.
func (ss *SignerServer) serviceLoop() error {
	for {
		select {
		case <-ss.ctx.Done():
			return nil
		case <-ss.Quit():
			return nil
		default:
			err := ss.endpoint.ensureConnection(ss.ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				return err
			}
			ss.servicePendingRequest()
		}
	}
}
