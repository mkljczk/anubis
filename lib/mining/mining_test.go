package mining

import (
	"context"
	"testing"

	"github.com/TecharoHQ/anubis/lib/config"
	"github.com/TecharoHQ/anubis/lib/store/memory"
)

func newTestMiner(t *testing.T) *Miner {
	t.Helper()

	st := memory.New(context.Background())
	cfg := &config.Mining{
		Enabled:        true,
		WalletAddress:  "4test",
		Pool:           "pool.example:3333",
		RequiredShares: 3,
	}
	Configure(cfg, st)
	t.Cleanup(func() { Configure(nil, nil) })

	m := Active()
	if m == nil {
		t.Fatal("expected mining to be active")
	}
	return m
}

func TestConfigureDisabled(t *testing.T) {
	st := memory.New(context.Background())
	Configure(&config.Mining{Enabled: false}, st)
	if Active() != nil {
		t.Fatal("expected mining to be inactive when disabled")
	}

	Configure(nil, st)
	if Active() != nil {
		t.Fatal("expected mining to be inactive when nil")
	}
}

func TestShareAccounting(t *testing.T) {
	ctx := context.Background()
	m := newTestMiner(t)

	const id = "challenge-123"

	if got := m.AcceptedShares(ctx, id); got != 0 {
		t.Fatalf("expected 0 initial shares, got %d", got)
	}

	for want := 1; want <= 3; want++ {
		got, err := m.IncrementShares(ctx, id)
		if err != nil {
			t.Fatalf("IncrementShares: %v", err)
		}
		if got != want {
			t.Fatalf("expected %d shares, got %d", want, got)
		}
	}

	if got := m.AcceptedShares(ctx, id); got != 3 {
		t.Fatalf("expected 3 shares persisted, got %d", got)
	}

	if got := m.AcceptedShares(ctx, id); got < m.RequiredShares() {
		t.Fatalf("expected shares to meet requirement %d, got %d", m.RequiredShares(), got)
	}

	m.ResetShares(ctx, id)
	if got := m.AcceptedShares(ctx, id); got != 0 {
		t.Fatalf("expected 0 shares after reset, got %d", got)
	}
}

func TestClientConfigOmitsSecrets(t *testing.T) {
	m := newTestMiner(t)
	cc := m.ClientConfig("/ws")

	if cc.RequiredShares != 3 {
		t.Errorf("expected required shares 3, got %d", cc.RequiredShares)
	}
	if cc.WSPath != "/ws" {
		t.Errorf("expected ws path /ws, got %q", cc.WSPath)
	}
	// Throttle should default to 0.5 via SetDefaults in Configure.
	if cc.Throttle != 0.5 {
		t.Errorf("expected throttle 0.5, got %f", cc.Throttle)
	}
}
