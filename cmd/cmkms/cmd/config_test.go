package cmd

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestReadConfigParsesValues(t *testing.T) {
	oldOutput := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	file := filepath.Join(t.TempDir(), "config.toml")
	content := `id = "node-1"
raft_addr = "127.0.0.1:1000"
http_addr = "0.0.0.0:9000"
peer = ["a", "b"]
validator_addr = ["tcp://validator"]
chain_id = "chain"
log_level = "verbose"
log_format = "fancy"
`
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := readConfig(file)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.ID != "node-1" || cfg.RaftAddr != "127.0.0.1:1000" || cfg.HTTPAddr != "0.0.0.0:9000" {
		t.Fatalf("unexpected addresses: %+v", cfg)
	}
	if !slicesEqual(cfg.Peer, []string{"a", "b"}) {
		t.Fatalf("unexpected peers: %v", cfg.Peer)
	}
	if !slicesEqual(cfg.ValidatorAddrs, []string{"tcp://validator"}) {
		t.Fatalf("unexpected validator addrs: %v", cfg.ValidatorAddrs)
	}
	if cfg.ChainID != "chain" {
		t.Fatalf("expected chain id, got %q", cfg.ChainID)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("invalid log level should default to info, got %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "plain" {
		t.Fatalf("invalid format should default to plain, got %q", cfg.LogFormat)
	}
}

func TestEnsureConfigCreatesDefaults(t *testing.T) {
	origConfPath := confPath
	origFileCfg := fileCfg
	t.Cleanup(func() {
		confPath = origConfPath
		fileCfg = origFileCfg
	})

	temp := t.TempDir()
	home := filepath.Join(temp, "relative-home")

	if err := ensureConfig(&home); err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}

	if !filepath.IsAbs(home) {
		t.Fatalf("expected home to be absolute, got %q", home)
	}

	expectedConf := filepath.Join(home, "config.toml")
	if confPath != expectedConf {
		t.Fatalf("confPath %q, expected %q", confPath, expectedConf)
	}

	data, err := os.ReadFile(expectedConf)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("default config file was empty")
	}
	if fileCfg.ID != "node0" {
		t.Fatalf("expected default ID node0, got %q", fileCfg.ID)
	}
	if !slicesEqual(fileCfg.ValidatorAddrs, []string{"tcp://127.0.0.1:8080"}) {
		t.Fatalf("unexpected default validator addrs: %v", fileCfg.ValidatorAddrs)
	}
}

func TestWriteDefaultConfigDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeDefaultConfig(path); err != nil {
		t.Fatalf("writeDefaultConfig initial: %v", err)
	}

	custom := []byte("custom")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatalf("write custom: %v", err)
	}

	if err := writeDefaultConfig(path); err != nil {
		t.Fatalf("writeDefaultConfig second: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != string(custom) {
		t.Fatal("default config overwrote existing file")
	}
}

func TestExpandPath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	rel := "some/path"
	if got := expandPath(rel); got != filepath.Join(cwd, rel) {
		t.Fatalf("expected %q, got %q", filepath.Join(cwd, rel), got)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := expandPath("~/config"); got != filepath.Join(home, "config") {
		t.Fatalf("expected home expansion to %q, got %q", filepath.Join(home, "config"), got)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
