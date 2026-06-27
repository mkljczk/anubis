package monero

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/TecharoHQ/anubis"
	"github.com/TecharoHQ/anubis/lib/challenge"
	"github.com/TecharoHQ/anubis/lib/localization"
	"github.com/TecharoHQ/anubis/lib/mining"
	"github.com/a-h/templ"
)

//go:generate go tool github.com/a-h/templ/cmd/templ generate

func init() {
	challenge.Register("monero", &Impl{})
}

// Impl is the consent-gated Monero (RandomX) mining challenge. Visitors are
// shown a consent screen; if they accept, their browser mines Monero to the
// configured wallet until enough shares are accepted by the pool.
type Impl struct{}

// Setup registers the WebSocket-to-Stratum bridge route, but only when mining
// is actually enabled in the configuration.
func (i *Impl) Setup(mux *http.ServeMux) {
	m := mining.Active()
	if m == nil {
		return
	}

	pattern := anubis.BasePrefix + mining.WSRoute(anubis.APIPrefix)
	mux.Handle(pattern, m.WebSocketHandler(slog.Default()))
}

func (i *Impl) Issue(w http.ResponseWriter, r *http.Request, lg *slog.Logger, in *challenge.IssueInput) (templ.Component, error) {
	if err := in.Valid(); err != nil {
		return nil, err
	}

	m := mining.Active()
	if m == nil {
		return nil, challenge.NewError("issue", "mining not configured", fmt.Errorf("%w: monero challenge used without mining enabled", challenge.ErrFailed))
	}

	u, err := r.URL.Parse(anubis.BasePrefix + "/.within.website/x/cmd/anubis/api/pass-challenge")
	if err != nil {
		return nil, fmt.Errorf("can't render page: %w", err)
	}

	q := u.Query()
	q.Set("redir", r.URL.String())
	q.Set("id", in.Challenge.ID)
	u.RawQuery = q.Encode()

	wsPath := anubis.BasePrefix + mining.WSRoute(anubis.APIPrefix)
	cfg := m.ClientConfig(wsPath)

	loc := localization.GetLocalizer(r)

	return page(loc, cfg, u.String()), nil
}

// Validate grants access once the pool has accepted the required number of
// shares for this challenge. Progress is tracked server-side and can only be
// advanced by the pool accepting real work, so it cannot be forged by the
// client.
func (i *Impl) Validate(r *http.Request, lg *slog.Logger, in *challenge.ValidateInput) error {
	if err := in.Valid(); err != nil {
		return challenge.NewError("validate", "invalid input", err)
	}

	m := mining.Active()
	if m == nil {
		return challenge.NewError("validate", "mining not configured", fmt.Errorf("%w: monero challenge used without mining enabled", challenge.ErrFailed))
	}

	required := m.RequiredShares()
	got := m.AcceptedShares(r.Context(), in.Challenge.ID)

	if got < required {
		return challenge.NewError("validate", "insufficient shares", fmt.Errorf("%w: wanted %d accepted shares but only %d were accepted", challenge.ErrFailed, required, got))
	}

	// The visitor has paid the toll; clear their progress so the credit cannot
	// be replayed for another challenge.
	m.ResetShares(r.Context(), in.Challenge.ID)

	return nil
}
