package keyserver

import (
	"strings"
)

// Config defines runtime parameters for the CometBFT key server.
type Config struct {
	// ChainID is the CometBFT chain identifier used when signing.
	ChainID string

	// ValidatorAddrs are the priv-validator socket addresses (tcp:// or unix://)
	// exposed by the CometBFT node. They are attempted in order until a
	// connection succeeds.
	ValidatorAddrs []string

	// PrivKeyPath points to a priv_validator_key.json file containing the
	// validator's private key material.
	PrivKeyPath string

	// PrivStatePath stores the last sign state (priv_validator_state.json).
	PrivStatePath string

	// SignerID is used when acquiring the Keystone lease. Defaults to the node
	// identifier when left empty.
	SignerID string
}

// Normalize applies default values to optional fields.
func (c *Config) Normalize() {
	if len(c.ValidatorAddrs) > 0 {
		addrs := make([]string, 0, len(c.ValidatorAddrs))
		for _, addr := range c.ValidatorAddrs {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				addrs = append(addrs, addr)
			}
		}
		c.ValidatorAddrs = addrs
	}
}
