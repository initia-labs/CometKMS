package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	cometlog "github.com/cometbft/cometbft/libs/log"
)

type configFile struct {
	ID             string   `toml:"id"`
	RaftAddr       string   `toml:"raft_addr"`
	HTTPAddr       string   `toml:"http_addr"`
	Peer           []string `toml:"peer"`
	ValidatorAddrs []string `toml:"validator_addr"`
	ChainID        string   `toml:"chain_id"`
	LogLevel       string   `toml:"log_level"`
	LogFormat      string   `toml:"log_format"`
	AllowUnsafe    bool     `toml:"allow_unsafe"`
}

// ensureConfig prepares the home directory, ensures a config file exists, and
// caches the parsed configuration for other commands to consume.
func ensureConfig(homePtr *string) error {
	resolved := expandPath(*homePtr)
	if resolved == "" {
		resolved = defaultHomeDir()
	}

	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return fmt.Errorf("failed to create home directory %s: %v", resolved, err)
	}

	*homePtr = resolved
	confPath = filepath.Join(resolved, "config.toml")

	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		if err := writeDefaultConfig(confPath); err != nil {
			return fmt.Errorf("failed to write default config: %v", err)
		}
	}

	cfg, err := readConfig(confPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	fileCfg = cfg
	return nil
}

var validLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"error": {},
}

var validLogFormats = map[string]struct{}{
	"json":  {},
	"plain": {},
}

// readConfig consumes a simplified TOML document into a configFile struct.
func readConfig(path string) (configFile, error) {
	var cfg configFile

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return configFile{}, fmt.Errorf("failed to decode TOML: %w", err)
	}

	// Validate log level
	if cfg.LogLevel != "" {
		if _, ok := validLogLevels[cfg.LogLevel]; !ok {
			fmt.Printf("warning: unknown log level %q, defaulting to info\n", cfg.LogLevel)
			cfg.LogLevel = "info"
		}
		// Validate with CometBFT logger
		if _, err := cometlog.AllowLevel(cfg.LogLevel); err != nil {
			fmt.Printf("warning: invalid log level %q, defaulting to info\n", cfg.LogLevel)
			cfg.LogLevel = "info"
		}
	}

	// Validate log format
	if cfg.LogFormat != "" {
		if _, ok := validLogFormats[cfg.LogFormat]; !ok {
			fmt.Printf("warning: unknown log format %q, defaulting to plain\n", cfg.LogFormat)
			cfg.LogFormat = "plain"
		}
	}

	return cfg, nil
}

// writeDefaultConfig writes the scaffold config file if it does not yet exist.
func writeDefaultConfig(path string) error {
	content := `# CometKMS configuration
# Values can be overridden by flags or COMETKMS_* environment variables.

# Unique identifier for this CometKMS node.
id = "node0"

# TCP address CometKMS uses for its Raft transport.
raft_addr = "127.0.0.1:9430"

# HTTP listen address for status/admin APIs.
http_addr = ":8080"

# Static Raft peers in id=host:port form (comma separated inside the brackets).
# The first entry should describe this node; when its raft data directory is
# empty the node bootstraps the cluster automatically.
peer = []

# CometBFT priv_validator_laddr entries that CometKMS should connect to.
# Provide multiple addresses in an array if desired.
validator_addr = ["tcp://127.0.0.1:8080"]

# Chain ID used when signing requests.
chain_id = ""

# Log level: debug, info, error
log_level = "info"

# Log format: json or plain
log_format = "plain"

# Enable experimental or operationally unsafe APIs (such as /raft/peer).
allow_unsafe = false
`
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// defaultHomeDir provides the default CMKMS home under the user's $HOME.
func defaultHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cmkms")
	}
	return ".cmkms"
}

// expandPath resolves relative paths and tilde prefixes when possible.
func expandPath(path string) string {
	if path == "" {
		return path
	}
	if after, ok := strings.CutPrefix(path, "~"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, after)
		}
	}
	if !filepath.IsAbs(path) {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Join(cwd, path)
		}
	}
	return path
}
