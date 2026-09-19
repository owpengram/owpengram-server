package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"telesrv/internal/domain"
)

// AccountRating returns one user's stored composite rating projection.
func (s *Service) AccountRating(ctx context.Context, userID int64) (domain.AccountRating, error) {
	if s == nil || s.rating == nil {
		return domain.AccountRating{}, fmt.Errorf("account rating dependency is not configured")
	}
	return s.rating.Rating(ctx, userID)
}

// AccountRatings is the admin leaderboard read.
func (s *Service) AccountRatings(ctx context.Context, filter domain.AccountRatingFilter) ([]domain.AccountRating, error) {
	if s == nil || s.rating == nil {
		return nil, fmt.Errorf("account rating dependency is not configured")
	}
	return s.rating.List(ctx, filter)
}

// AccountRatingEvents returns the contribution ledger that explains a level.
func (s *Service) AccountRatingEvents(ctx context.Context, userID int64, limit int) ([]domain.AccountRatingEvent, error) {
	if s == nil || s.rating == nil {
		return nil, fmt.Errorf("account rating dependency is not configured")
	}
	return s.rating.Events(ctx, userID, limit)
}

// RecomputeAccountRatingRequest forces one user's composite rating to be
// recomputed from the current contribution signals.
type RecomputeAccountRatingRequest struct {
	CommandMeta
	UserID int64 `json:"user_id,string"`
}

// AdjustAccountRatingRequest moves one user's manual rating component by a
// signed delta. The delta survives recomputes, so it is the operator's
// durable override rather than a one-off nudge.
type AdjustAccountRatingRequest struct {
	CommandMeta
	UserID int64 `json:"user_id,string"`
	Amount int64 `json:"amount,string"`
}

// RecomputeAccountRating rebuilds one user's composite rating from the
// current contribution signals. A dry-run only reports the stored
// projection, so the operator can see what a recompute would start from
// without writing.
func (s *Service) RecomputeAccountRating(ctx context.Context, req RecomputeAccountRatingRequest) (CommandResult, error) {
	if s == nil || s.rating == nil {
		return CommandResult{}, fmt.Errorf("admin account rating dependency is not configured")
	}
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	return s.runCommand(ctx, req.CommandMeta, ActionRecomputeAccountRating, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{"user_id": strconv.FormatInt(req.UserID, 10)}
		previous, err := s.rating.Rating(ctx, req.UserID)
		switch {
		case err == nil:
			details["previous_found"] = true
			details["previous_level"] = previous.Level
			details["previous_stars"] = strconv.FormatInt(previous.Stars, 10)
		case errors.Is(err, domain.ErrAccountRatingNotFound):
			details["previous_found"] = false
		default:
			return CommandResult{Details: details}, accountRatingError(err)
		}
		if req.DryRun {
			return CommandResult{Message: "account rating recompute validated", Details: details}, nil
		}
		rating, err := s.rating.Recompute(ctx, req.UserID)
		if err != nil {
			return CommandResult{Details: details}, accountRatingError(err)
		}
		mergeAccountRatingDetails(details, rating)
		return CommandResult{Message: "account rating recomputed", Details: details}, nil
	})
}

// AdjustAccountRating moves the manual component of one user's rating by a
// signed delta and recomputes the projection so the change is visible at
// once. The command id doubles as the ledger key, so a retried command
// records the adjustment exactly once.
func (s *Service) AdjustAccountRating(ctx context.Context, req AdjustAccountRatingRequest) (CommandResult, error) {
	if s == nil || s.rating == nil {
		return CommandResult{}, fmt.Errorf("admin account rating dependency is not configured")
	}
	if req.UserID <= 0 {
		return CommandResult{}, fmt.Errorf("user_id is required")
	}
	if req.Amount == 0 || req.Amount < -maxAccountRatingAdjustment || req.Amount > maxAccountRatingAdjustment {
		return CommandResult{}, codedError(CodeRatingAdjustmentInvalid, domain.ErrAccountRatingAdjustmentInvalid)
	}
	return s.runCommand(ctx, req.CommandMeta, ActionAdjustAccountRating, req.UserID, domain.Peer{}, req, func() (CommandResult, error) {
		details := map[string]any{
			"user_id": strconv.FormatInt(req.UserID, 10),
			"amount":  strconv.FormatInt(req.Amount, 10),
		}
		previous, err := s.rating.Rating(ctx, req.UserID)
		switch {
		case err == nil:
			details["previous_found"] = true
			details["previous_level"] = previous.Level
			details["previous_stars"] = strconv.FormatInt(previous.Stars, 10)
			details["previous_manual_component"] = strconv.FormatInt(previous.ManualComponent, 10)
		case errors.Is(err, domain.ErrAccountRatingNotFound):
			details["previous_found"] = false
		default:
			return CommandResult{Details: details}, accountRatingError(err)
		}
		if req.DryRun {
			return CommandResult{Message: "account rating adjustment validated", Details: details}, nil
		}
		rating, applied, err := s.rating.Adjust(ctx, domain.AdjustAccountRatingRequest{
			UserID:     req.UserID,
			Amount:     req.Amount,
			Reason:     req.Reason,
			Actor:      req.Actor,
			CommandKey: "admin-rating-adjust:" + req.CommandID,
		})
		if err != nil {
			return CommandResult{Details: details}, accountRatingError(err)
		}
		details["applied"] = applied
		mergeAccountRatingDetails(details, rating)
		message := "account rating adjusted"
		if !applied {
			message = "account rating adjustment replayed"
		}
		return CommandResult{Message: message, Details: details}, nil
	})
}

// mergeAccountRatingDetails records the computed projection in command
// details. Every score component crosses the JSON boundary as a decimal
// string so an audit entry reproduces the exact int64 the store holds.
func mergeAccountRatingDetails(details map[string]any, rating domain.AccountRating) {
	details["level"] = rating.Level
	details["stars"] = strconv.FormatInt(rating.Stars, 10)
	details["current_level_stars"] = strconv.FormatInt(rating.CurrentLevelStars, 10)
	details["has_next_level"] = rating.HasNextLevel
	if rating.HasNextLevel {
		details["next_level_stars"] = strconv.FormatInt(rating.NextLevelStars, 10)
	}
	details["stars_component"] = strconv.FormatInt(rating.StarsComponent, 10)
	details["activity_component"] = strconv.FormatInt(rating.ActivityComponent, 10)
	details["penalty_component"] = strconv.FormatInt(rating.PenaltyComponent, 10)
	details["manual_component"] = strconv.FormatInt(rating.ManualComponent, 10)
	details["pending_stars"] = strconv.FormatInt(rating.PendingStars, 10)
	if !rating.PendingDate.IsZero() {
		details["pending_date"] = rating.PendingDate.UTC().Format(time.RFC3339)
	}
	details["version"] = strconv.FormatInt(rating.Version, 10)
}

// AccountRatingErrorCode maps an account-rating domain error onto its stable
// admin code. An unmapped error returns "".
func AccountRatingErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, domain.ErrAccountRatingNotFound):
		return CodeRatingNotFound
	case errors.Is(err, domain.ErrAccountRatingAdjustmentInvalid):
		return CodeRatingAdjustmentInvalid
	case errors.Is(err, domain.ErrAccountRatingWeightsInvalid):
		return CodeRatingWeightsInvalid
	default:
		return ""
	}
}

// accountRatingError prefixes a recognised domain error with its stable
// code, so the journalled command result and the operator both see
// "CODE: message" instead of a bare Go string. Unrecognised errors are
// returned untouched: inventing a code for an unknown failure would be
// worse than reporting it verbatim.
func accountRatingError(err error) error {
	if code := AccountRatingErrorCode(err); code != "" {
		return codedError(code, err)
	}
	return err
}
