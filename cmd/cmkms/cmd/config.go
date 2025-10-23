package cmd

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	cometlog "github.com/cometbft/cometbft/libs/log"
)

type configFile struct {
	ID             string
	RaftAddr       string
	HTTPAddr       string
	Peer           []string
	ValidatorAddrs []string
	ChainID        string
	LogLevel       string
	LogFormat      string
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
	file, err := os.Open(path)
	if err != nil {
		return configFile{}, err
	}
	defer file.Close()

	cfg := configFile{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := parseKV(line)
		if !ok {
			continue
		}

		switch key {
		case "id":
			cfg.ID = value
		case "raft_addr":
			cfg.RaftAddr = value
		case "http_addr":
			cfg.HTTPAddr = value
		case "peer":
			cfg.Peer = parseArray(value)
		case "validator_addr":
			cfg.ValidatorAddrs = parseValidatorAddrs(value)
		case "chain_id":
			cfg.ChainID = value
		case "log_level":
			cfg.LogLevel = value
			cometlog.AllowLevel(value)
			if _, ok := validLogLevels[strings.ToLower(value)]; !ok {
				log.Printf("warning: unknown log level %q, defaulting to info", value)
				cfg.LogLevel = "info"
			}
		case "log_format":
			cfg.LogFormat = value
			if _, ok := validLogFormats[strings.ToLower(value)]; !ok {
				log.Printf("warning: unknown log format %q, defaulting to plain", value)
				cfg.LogFormat = "plain"
			}
		default:
			// ignore unknown keys
		}
	}

	if err := scanner.Err(); err != nil {
		return configFile{}, err
	}

	return cfg, nil
}

func parseKV(line string) (string, string, bool) {
	eq := strings.Index(line, "=")
	if eq == -1 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:eq])
	value := strings.TrimSpace(line[eq+1:])
	value = trimQuotes(value)
	return key, value, true
}

// parseArray handles TOML-like string arrays such as ["foo", "bar"].
func parseArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = trimQuotes(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseValidatorAddrs accepts either a single address or an array syntax.
func parseValidatorAddrs(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		if addrs := parseArray(trimmed); addrs != nil {
			return addrs
		}
		return []string{}
	}
	return []string{trimmed}
}

func trimQuotes(val string) string {
	if len(val) >= 2 {
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			return val[1 : len(val)-1]
		}
	}
	return val
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
