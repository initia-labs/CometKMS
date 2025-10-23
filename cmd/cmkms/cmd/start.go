package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	cometlog "github.com/cometbft/cometbft/libs/log"

	"github.com/initia-labs/CometKMS/pkg/api"
	"github.com/initia-labs/CometKMS/pkg/keyserver"
	raftnode "github.com/initia-labs/CometKMS/pkg/raft"
)

const defaultLeaseTTL = 400 * time.Millisecond

func init() {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the CometKMS signer node",
		RunE:  runStart,
	}

	cmd.Flags().String("id", hostnameFallback(), "unique node identifier")
	cmd.Flags().String("raft-addr", "127.0.0.1:9430", "raft bind address (host:port)")
	cmd.Flags().String("http-addr", ":8080", "http listen address")
	cmd.Flags().Duration("default-ttl", defaultLeaseTTL, "default lease ttl")
	cmd.Flags().StringArray("peer", nil, "peer definition in id=host:port form for bootstrapping")
	cmd.Flags().StringArray("validator-addr", nil, "cometbft priv_validator_laddr to dial (repeatable)")
	cmd.Flags().String("chain-id", "", "cometbft chain id")
	cmd.Flags().String("log-level", "info", "log level: debug, info, error")
	cmd.Flags().String("log-format", "plain", "log format: json or plain")
	cmd.Flags().Bool("allow-unsafe", false, "enable unsafe endpoints (e.g. /raft/peer)")
	rootCmd.AddCommand(cmd)
}

func runStart(cmd *cobra.Command, _ []string) error {
	cfg := Config()

	nodeID := stringOption(cmd, "id", "COMETKMS_ID", cfg.ID, hostnameFallback())
	raftAddr := stringOption(cmd, "raft-addr", "COMETKMS_RAFT_ADDR", cfg.RaftAddr, "127.0.0.1:9430")
	httpAddr := stringOption(cmd, "http-addr", "COMETKMS_HTTP_ADDR", cfg.HTTPAddr, ":8080")
	peerDefs := sliceOption(cmd, "peer", "COMETKMS_PEER", cfg.Peer)
	validatorAddrs := sliceOption(cmd, "validator-addr", "COMETKMS_VALIDATOR_ADDR", cfg.ValidatorAddrs)
	chainID := stringOption(cmd, "chain-id", "COMETKMS_CHAIN_ID", cfg.ChainID, "")
	logLevel := stringOption(cmd, "log-level", "COMETKMS_LOG_LEVEL", cfg.LogLevel, "info")
	logFormat := stringOption(cmd, "log-format", "COMETKMS_LOG_FORMAT", cfg.LogFormat, "plain")
	allowUnsafe := boolOption(cmd, "allow-unsafe", "COMETKMS_ALLOW_UNSAFE", cfg.AllowUnsafe, false)

	if nodeID == "" {
		return fmt.Errorf("node id is required")
	}

	raftDir, privDir, err := ensureNodeDirectories(HomeDir(), nodeID)
	if err != nil {
		return err
	}

	logger, err := buildLogger(logLevel, logFormat)
	if err != nil {
		return err
	}

	peers, hasSelfFirst, err := normalizePeers(nodeID, raftAddr, peerDefs)
	if err != nil {
		return fmt.Errorf("invalid peer definitions: %w", err)
	}
	bootstrap := hasSelfFirst && dirEmpty(raftDir)

	// set up signal handling and a cancellable context
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// create an errgroup with the context
	g, ctx := errgroup.WithContext(ctx)

	node, err := raftnode.NewNode(raftnode.Config{
		ID:          nodeID,
		RaftDir:     raftDir,
		BindAddress: raftAddr,
		Bootstrap:   bootstrap,
		Peers:       peers,
		Logger:      logger,
		LogLevel:    hclog.LevelFromString(logLevel),
		LogFormat:   logFormat,
	})
	if err != nil {
		return fmt.Errorf("failed to start raft node: %w", err)
	}
	if err := node.Start(ctx, g); err != nil {
		return fmt.Errorf("failed to start raft node: %w", err)
	}
	defer func() {
		if err := node.Close(); err != nil {
			logger.Error("raft shutdown error", "err", err)
		}
	}()

	srv := api.NewServer(node, httpAddr, logger.With("component", "api"), allowUnsafe)
	if err := srv.ListenAndServe(ctx, g); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	}

	if len(validatorAddrs) == 0 || chainID == "" {
		return fmt.Errorf("at least one validator-addr and chain-id must be configured")
	}
	if privDir == "" {
		return fmt.Errorf("priv-dir must be configured")
	}

	privKeyPath := filepath.Join(privDir, "priv_validator_key.json")
	privStatePath := filepath.Join(privDir, "priv_validator_state.json")

	cfgKS := keyserver.Config{
		ChainID:        chainID,
		ValidatorAddrs: validatorAddrs,
		PrivKeyPath:    privKeyPath,
		PrivStatePath:  privStatePath,
		SignerID:       nodeID,
	}
	cfgKS.Normalize()

	keySrv, err := keyserver.New(g, cfgKS, node, logger.With("component", "keyserver"))
	if err != nil {
		return fmt.Errorf("failed to configure key server: %w", err)
	}
	if err := keySrv.Start(ctx, g); err != nil {
		return fmt.Errorf("failed to start key server: %w", err)
	}
	defer func() {
		if err := keySrv.Stop(); err != nil {
			logger.Error("keyserver shutdown error", "err", err)
		}
	}()

	logger.Info("cmkms node started", "id", nodeID, "http", httpAddr, "raft", raftAddr)
	<-ctx.Done()
	if err := g.Wait(); err != nil {
		return err
	}
	logger.Info("shutting down")
	return nil
}

// ensureNodeDirectories prepares the on-disk directories needed for a node instance.
func ensureNodeDirectories(home, nodeID string) (string, string, error) {
	raftDir := filepath.Join(home, "data", nodeID)
	privDir := filepath.Join(home, "priv")
	if err := os.MkdirAll(raftDir, 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create raft dir: %w", err)
	}
	if err := os.MkdirAll(privDir, 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create priv dir: %w", err)
	}
	return raftDir, privDir, nil
}

// buildLogger constructs the comet logger according to the supplied level and format.
func buildLogger(level, format string) (cometlog.Logger, error) {
	allowLevel, err := cometlog.AllowLevel(level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}
	if format == "json" {
		return cometlog.NewFilter(cometlog.NewTMJSONLogger(cometlog.NewSyncWriter(os.Stdout)), allowLevel), nil
	}
	return cometlog.NewFilter(cometlog.NewTMLogger(cometlog.NewSyncWriter(os.Stdout)), allowLevel), nil
}

// normalizePeers ensures the local node is present in the peer list and reports if it should bootstrap.
func normalizePeers(nodeID, raftAddr string, entries []string) ([]raftnode.Peer, bool, error) {
	peers, err := parsePeers(entries)
	if err != nil {
		return nil, false, err
	}
	selfFirst := len(peers) == 0
	if len(peers) > 0 && peers[0].ID == nodeID {
		selfFirst = true
		if peers[0].Address == "" {
			peers[0].Address = raftAddr
		}
	}
	peers = ensureSelfPeer(peers, nodeID, raftAddr)
	return peers, selfFirst, nil
}
