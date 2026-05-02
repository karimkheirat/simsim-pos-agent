// Package pairing orchestrates the pair/unpair/status flows on top of
// the cloud client (internal/cloud) and local secret store
// (internal/config). Pure logic — no console I/O, no flag parsing.
//
// Lifted from cmd/agentctl in M2 sub-task A4 so the orchestration is
// unit-testable with mock cloud + mock secret store.
//
// Concerns that intentionally stay in the CLI layer:
//   - Bootstrap one-shot heartbeat after Pair (presentation; needs the
//     local agent /health probe).
//   - "Force clear?" prompt on Unpair network errors (interactive I/O).
//
// Service.Pair returns once secrets are persisted; Service.Unpair on a
// network error propagates ErrNetwork without clearing local state so
// the CLI can decide what to do.
package pairing

import (
	"context"
	"errors"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// Service holds the dependencies the pairing flows need. Construct with
// real *cloud.Client + real config.SecretStore in production, or with
// httptest-backed and in-memory mocks in tests.
type Service struct {
	Cloud     *cloud.Client
	Secrets   config.SecretStore
	MachineID string
	Version   string
}

// Status reports the agent's local pair state for the `status`
// subcommand. PairedAt is zero when Paired is false.
type Status struct {
	Paired     bool
	TerminalID string
	StoreID    string
	PairedAt   time.Time
}

// Pair exchanges a 6-digit pairing code for a long-lived terminal token
// via the cloud, then persists the resulting secrets locally.
//
//   - On any cloud error (invalid code, rate limit, network, ...): no
//     secrets are saved and the error is returned.
//   - On a save error after a successful cloud exchange: the cloud-side
//     pairing already succeeded but the local persist failed; the error
//     is returned. The cloud will eventually reject the orphaned token
//     when the operator re-pairs (Spec §8.3).
//   - When already paired: succeeds and overwrites the old secrets — the
//     new pair supersedes (cloud already considers the previous token
//     stale once a new /pair completes).
//
// Code-format validation lives in the CLI; Pair forwards whatever code
// it gets to the cloud and lets the cloud reject INVALID_CODE.
func (s *Service) Pair(ctx context.Context, code string) (*cloud.PairResponse, error) {
	resp, err := s.Cloud.Pair(ctx, code, s.Version, s.MachineID)
	if err != nil {
		return nil, err
	}
	if err := s.Secrets.Save(&config.Secrets{
		TerminalID:    resp.TerminalID,
		TerminalToken: resp.TerminalToken,
		StoreID:       resp.StoreID,
		PairedAt:      time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	return resp, nil
}

// Unpair revokes the terminal's token cloud-side and clears local
// secrets. Behavior by cloud response:
//
//   - 200 / nil: secrets cleared, nil returned.
//   - 401 UNAUTHENTICATED: treated as already-revoked; secrets cleared,
//     nil returned (benign — getting back to unpaired state is the goal).
//   - ErrNetwork: secrets are NOT cleared and the error is propagated.
//     The caller (CLI) is responsible for prompting the operator about
//     a force-clear if they want to abandon the cloud-side token.
//   - Other cloud errors: secrets are NOT cleared and the error is
//     propagated.
//
// When no local secrets exist, returns config.ErrNoSecrets without
// touching the cloud.
func (s *Service) Unpair(ctx context.Context) error {
	secrets, err := s.Secrets.Load()
	if err != nil {
		return err
	}

	cloudErr := s.Cloud.Unpair(ctx, secrets.TerminalToken)
	switch {
	case cloudErr == nil:
		// Successful revoke — clear locally.
	case errors.Is(cloudErr, cloud.ErrUnauthenticated):
		// Already revoked server-side — clear locally and report success.
	case errors.Is(cloudErr, cloud.ErrNetwork):
		// CLI decides whether to force-clear via Secrets.Clear() directly.
		return cloudErr
	default:
		// Conservative: any other cloud error leaves secrets in place so
		// the operator can retry. CLI surfaces the error.
		return cloudErr
	}

	return s.Secrets.Clear()
}

// Status reports whether the agent is paired and (when paired) the
// minimal identifiers from the persisted secrets. config.ErrNoSecrets
// translates to Status{Paired: false}; any other Load error is returned.
func (s *Service) Status(ctx context.Context) (*Status, error) {
	secrets, err := s.Secrets.Load()
	if errors.Is(err, config.ErrNoSecrets) {
		return &Status{Paired: false}, nil
	}
	if err != nil {
		return nil, err
	}
	return &Status{
		Paired:     true,
		TerminalID: secrets.TerminalID,
		StoreID:    secrets.StoreID,
		PairedAt:   secrets.PairedAt,
	}, nil
}
