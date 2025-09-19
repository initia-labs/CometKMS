package endpoint

import (
	"context"
	"time"

	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/privval"
)

// SignerDialerEndpoint dials using its dialer and responds to any signature
// requests using its privVal.
type SignerDialerEndpoint struct {
	*privval.SignerDialerEndpoint

	dialer privval.SocketDialer

	retryWait      time.Duration
	maxConnRetries int
}

const validatorDialRetryWait = 100 * time.Millisecond
const maxValidatorDialRetries = int(^uint(0) >> 1) // effectively unlimited

// NewSignerDialerEndpoint returns a SignerDialerEndpoint that will dial using the given
// dialer and respond to any signature requests over the connection
// using the given privVal.
func NewSignerDialerEndpoint(
	logger log.Logger,
	dialer privval.SocketDialer,
) *SignerDialerEndpoint {
	return &SignerDialerEndpoint{
		SignerDialerEndpoint: privval.NewSignerDialerEndpoint(logger, dialer),
		dialer:               dialer,
		retryWait:            validatorDialRetryWait,
		maxConnRetries:       maxValidatorDialRetries,
	}
}

func (sd *SignerDialerEndpoint) ensureConnection(ctx context.Context) error {
	if sd.IsConnected() {
		return nil
	}

	retries := 0
	for {
		if sd.maxConnRetries > 0 && retries >= sd.maxConnRetries {
			break
		}

		conn, err := sd.dialer()
		if err != nil {
			retries++
			sd.Logger.Debug("SignerDialer: Reconnection failed", "retries", retries, "max", sd.maxConnRetries, "err", err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(sd.retryWait):
			}
			continue
		}

		sd.SetConnection(conn)
		sd.Logger.Debug("SignerDialer: Connection Ready")
		return nil
	}

	sd.Logger.Debug("SignerDialer: Max retries exceeded", "retries", retries, "max", sd.maxConnRetries)
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return privval.ErrNoConnection
}
