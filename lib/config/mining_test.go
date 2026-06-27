package config

import (
	"errors"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestMiningValid(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   *Mining
		err  error
	}{
		{
			name: "nil is valid",
			in:   nil,
			err:  nil,
		},
		{
			name: "disabled skips validation",
			in:   &Mining{Enabled: false},
			err:  nil,
		},
		{
			name: "enabled without wallet",
			in:   &Mining{Enabled: true, Pool: "pool.example:3333", RequiredShares: 1},
			err:  ErrMiningNoWallet,
		},
		{
			name: "enabled without pool",
			in:   &Mining{Enabled: true, WalletAddress: "4abc", RequiredShares: 1},
			err:  ErrMiningNoPool,
		},
		{
			name: "pool missing port",
			in:   &Mining{Enabled: true, WalletAddress: "4abc", Pool: "pool.example", RequiredShares: 1},
			err:  ErrMiningBadPool,
		},
		{
			name: "required shares too low",
			in:   &Mining{Enabled: true, WalletAddress: "4abc", Pool: "pool.example:3333", RequiredShares: 0},
			err:  ErrMiningBadShares,
		},
		{
			name: "throttle out of range",
			in:   &Mining{Enabled: true, WalletAddress: "4abc", Pool: "pool.example:3333", RequiredShares: 1, Throttle: ptr(1.5)},
			err:  ErrMiningBadThrottle,
		},
		{
			name: "fully valid",
			in:   &Mining{Enabled: true, WalletAddress: "4abc", Pool: "pool.example:3333", RequiredShares: 8, Throttle: ptr(0.5)},
			err:  nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Valid()
			if tt.err == nil {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("expected error %v, got: %v", tt.err, err)
			}
		})
	}
}

func TestMiningSetDefaults(t *testing.T) {
	m := &Mining{Enabled: true}
	m.SetDefaults()

	if m.Password != "x" {
		t.Errorf("expected default password %q, got %q", "x", m.Password)
	}
	if m.RequiredShares != 8 {
		t.Errorf("expected default required shares 8, got %d", m.RequiredShares)
	}
	if m.Throttle == nil || *m.Throttle != 0.5 {
		t.Errorf("expected default throttle 0.5, got %v", m.Throttle)
	}
}
