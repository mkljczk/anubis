package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	// ErrMiningNoWallet is returned when mining is enabled without a payout wallet.
	ErrMiningNoWallet = errors.New("config.Mining: wallet_address must be set when mining is enabled")
	// ErrMiningNoPool is returned when mining is enabled without a pool endpoint.
	ErrMiningNoPool = errors.New("config.Mining: pool must be set when mining is enabled (host:port)")
	// ErrMiningBadPool is returned when the pool endpoint is not a valid host:port.
	ErrMiningBadPool = errors.New("config.Mining: pool must be in host:port form")
	// ErrMiningBadShares is returned when required_shares is below 1.
	ErrMiningBadShares = errors.New("config.Mining: required_shares must be >= 1")
	// ErrMiningBadThrottle is returned when throttle is outside [0.0, 1.0].
	ErrMiningBadThrottle = errors.New("config.Mining: throttle must be between 0.0 and 1.0")
)

// Mining configures the opt-in, consent-gated Monero (RandomX) proof-of-work
// challenge.
//
// When enabled and referenced by a bot rule using the "monero" challenge
// algorithm, matching visitors are shown a consent screen that explains, in
// plain language, that the site is funded by mining Monero in their browser.
// Only if the visitor explicitly clicks "Accept" does any computation begin.
// The browser then mines to WalletAddress via Pool until RequiredShares shares
// are accepted by the pool, at which point access is granted. Visitors who
// decline are not allowed through (a "mine or refuse access" toll).
//
// This is deliberately transparent: covert in-browser mining (cryptojacking)
// is malware. The consent screen and the refuse option are mandatory parts of
// this feature and must not be removed.
type Mining struct {
	// Enabled turns the mining subsystem on. When false, "monero" challenge
	// rules fail closed and the subsystem registers no routes.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// WalletAddress is the Monero address that mined shares are credited to.
	WalletAddress string `json:"wallet_address,omitempty" yaml:"wallet_address,omitempty"`

	// Pool is the mining pool's stratum endpoint in host:port form.
	Pool string `json:"pool,omitempty" yaml:"pool,omitempty"`

	// PoolUsesTLS dials the pool over TLS when true.
	PoolUsesTLS bool `json:"pool_uses_tls,omitempty" yaml:"pool_uses_tls,omitempty"`

	// Password is the pool worker password. Most pools accept "x".
	Password string `json:"password,omitempty" yaml:"password,omitempty"`

	// RigID is an optional rig identifier reported to the pool.
	RigID string `json:"rig_id,omitempty" yaml:"rig_id,omitempty"`

	// RequiredShares is the number of pool-accepted shares a visitor must
	// contribute before access is granted.
	RequiredShares int `json:"required_shares,omitempty" yaml:"required_shares,omitempty"`

	// Threads is the number of web workers the browser miner spawns. When 0
	// the client picks a sensible default based on hardware concurrency.
	Threads int `json:"threads,omitempty" yaml:"threads,omitempty"`

	// Throttle is the fraction of time each worker spends idle between hashing
	// batches, in [0.0, 1.0]. 0.0 mines as fast as possible; 0.5 mines at
	// roughly half speed to leave the visitor's machine responsive.
	Throttle *float64 `json:"throttle,omitempty" yaml:"throttle,omitempty"`

	// WASMURL is the URL the browser loads the RandomX hashing core from. The
	// module must export a default async factory returning an object with
	// init(seedHash: Uint8Array): Promise<void> and hash(input: Uint8Array):
	// Uint8Array. See docs/docs/admin/configuration/mining.mdx.
	WASMURL string `json:"wasm_url,omitempty" yaml:"wasm_url,omitempty"`
}

// SetDefaults fills in unset optional values with their defaults.
func (m *Mining) SetDefaults() {
	if m.Password == "" {
		m.Password = "x"
	}
	if m.RequiredShares == 0 {
		m.RequiredShares = 8
	}
	if m.Throttle == nil {
		def := 0.5
		m.Throttle = &def
	}
}

func (m *Mining) Valid() error {
	if m == nil || !m.Enabled {
		return nil
	}

	var errs []error

	if strings.TrimSpace(m.WalletAddress) == "" {
		errs = append(errs, ErrMiningNoWallet)
	}

	if strings.TrimSpace(m.Pool) == "" {
		errs = append(errs, ErrMiningNoPool)
	} else if _, _, err := net.SplitHostPort(m.Pool); err != nil {
		errs = append(errs, fmt.Errorf("%w: %q: %w", ErrMiningBadPool, m.Pool, err))
	}

	if m.RequiredShares < 1 {
		errs = append(errs, fmt.Errorf("%w, got: %d", ErrMiningBadShares, m.RequiredShares))
	}

	if m.Throttle != nil && (*m.Throttle < 0.0 || *m.Throttle > 1.0) {
		errs = append(errs, fmt.Errorf("%w, got: %f", ErrMiningBadThrottle, *m.Throttle))
	}

	if len(errs) != 0 {
		return fmt.Errorf("config: mining configuration is not valid:\n%w", errors.Join(errs...))
	}

	return nil
}
