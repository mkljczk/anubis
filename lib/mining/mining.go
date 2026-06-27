// Package mining implements the opt-in, consent-gated Monero (RandomX)
// proof-of-work challenge for Anubis.
//
// The server side acts as a WebSocket-to-Stratum bridge: browsers speak a small
// JSON protocol to Anubis over a WebSocket, and Anubis relays the underlying
// Monero stratum protocol to a configured mining pool, logging in with the
// operator's wallet. Shares are only counted as accepted when the pool itself
// accepts them, so a visitor cannot fake progress.
//
// Mining never starts without explicit visitor consent. The consent screen and
// its "refuse access" option are part of the challenge template and must not be
// removed; covert in-browser mining is malware.
package mining

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/TecharoHQ/anubis/lib/config"
	"github.com/TecharoHQ/anubis/lib/store"
)

// shareTTL is how long a visitor's accepted-share progress is retained. It must
// comfortably exceed how long a visitor might spend on the consent screen and
// mining before being let through.
const shareTTL = 30 * time.Minute

var active atomic.Pointer[Miner]

// Miner holds the runtime state for the mining subsystem.
type Miner struct {
	cfg   config.Mining
	store store.Interface
}

// Configure installs (or clears) the active mining subsystem. It is safe to
// call with a nil or disabled config, which disables mining entirely.
func Configure(cfg *config.Mining, st store.Interface) {
	if cfg == nil || !cfg.Enabled || st == nil {
		active.Store(nil)
		return
	}

	c := *cfg
	c.SetDefaults()
	active.Store(&Miner{cfg: c, store: st})
}

// Active returns the configured Miner, or nil if mining is disabled.
func Active() *Miner {
	return active.Load()
}

// Config returns a copy of the active mining configuration.
func (m *Miner) Config() config.Mining {
	return m.cfg
}

// RequiredShares is the number of pool-accepted shares a visitor must
// contribute before they are granted access.
func (m *Miner) RequiredShares() int {
	return m.cfg.RequiredShares
}

func sharesKey(id string) string {
	return "mining:shares:" + id
}

// AcceptedShares returns how many shares the visitor identified by the given
// challenge id has had accepted by the pool so far.
func (m *Miner) AcceptedShares(ctx context.Context, id string) int {
	raw, err := m.store.Get(ctx, sharesKey(id))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0
	}
	return n
}

// IncrementShares records one additional pool-accepted share for the visitor
// identified by the given challenge id and returns the new total.
func (m *Miner) IncrementShares(ctx context.Context, id string) (int, error) {
	n := m.AcceptedShares(ctx, id) + 1
	if err := m.store.Set(ctx, sharesKey(id), []byte(strconv.Itoa(n)), shareTTL); err != nil {
		return n, err
	}
	return n, nil
}

// ResetShares clears the accepted-share counter for a challenge id, used once a
// visitor has been granted access.
func (m *Miner) ResetShares(ctx context.Context, id string) {
	_ = m.store.Delete(ctx, sharesKey(id))
}

// ClientConfig is the JSON payload injected into the consent page so the
// browser miner knows how to behave. It deliberately excludes secrets such as
// the pool password; the browser never talks to the pool directly.
type ClientConfig struct {
	RequiredShares int     `json:"requiredShares"`
	Threads        int     `json:"threads"`
	Throttle       float64 `json:"throttle"`
	WASMURL        string  `json:"wasmURL"`
	WSPath         string  `json:"wsPath"`
	Pool           string  `json:"pool"`
}

// ClientConfig returns the configuration injected into the consent page.
func (m *Miner) ClientConfig(wsPath string) ClientConfig {
	throttle := 0.5
	if m.cfg.Throttle != nil {
		throttle = *m.cfg.Throttle
	}

	return ClientConfig{
		RequiredShares: m.cfg.RequiredShares,
		Threads:        m.cfg.Threads,
		Throttle:       throttle,
		WASMURL:        m.cfg.WASMURL,
		WSPath:         wsPath,
		Pool:           m.cfg.Pool,
	}
}
