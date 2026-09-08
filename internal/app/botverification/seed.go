package botverification

import (
	"context"
	"errors"
	"fmt"

	"telesrv/internal/branding"
	"telesrv/internal/domain"
)

// SeedDefaultVerifier idempotently grants @marksbot (domain.VerifierBotUserID)
// verifier status on first boot, using a custom-emoji icon the ordinary
// sticker-seed import already writes (domain.VerifierBotDefaultIconDocumentID)
// -- so the reference third-party verifier has something to demonstrate right
// away instead of an empty icon catalogue and a bot that can only explain
// itself.
//
// Runs at most once: if a bot_verifier_settings row for @marksbot already
// exists -- granted by this seed on a previous boot, or hand-configured by an
// operator who pointed it at a different icon/company -- it is left
// completely untouched. This must never overwrite an operator's own decision
// about their own verifier.
// Returns true if it actually granted verifier status.
func (s *Service) SeedDefaultVerifier(ctx context.Context) (bool, error) {
	if s == nil || !s.enabled {
		return false, nil
	}
	if _, err := s.VerifierSettings(ctx, domain.VerifierBotUserID); err == nil {
		return false, nil
	} else if !errors.Is(err, domain.ErrVerifierNotFound) {
		return false, err
	}
	icon, err := s.UpsertIcon(ctx, domain.VerificationIcon{
		DocumentID: domain.VerifierBotDefaultIconDocumentID,
		Name:       "Default (bundled)",
		Active:     true,
	})
	if err != nil {
		return false, fmt.Errorf("seed default verifier icon: %w", err)
	}
	if _, err := s.GrantVerifier(ctx, domain.BotVerifierSettings{
		BotID:                      domain.VerifierBotUserID,
		IconDocumentID:             icon.DocumentID,
		CompanyName:                branding.ProductName(),
		DefaultDescription:         "Bundled reference verifier -- auto-granted on first boot.",
		CanModifyCustomDescription: false,
		Enabled:                    true,
		GrantedBy:                  "startup-seed",
		GrantReason:                "Reference verifier auto-granted on first boot so third-party verification has something to demonstrate out of the box.",
	}); err != nil {
		return false, fmt.Errorf("seed default verifier grant: %w", err)
	}
	return true, nil
}
