package store

import (
	"errors"
	"fmt"

	"telesrv/internal/domain"
)

var ErrDeliveryOutboxRequired = errors.New("durable delivery boundary required")

// DeliveryEffect is the account-scoped durable update intent built from an
// immutable aggregate snapshot. Main persists it into user_update_events and
// dispatch_outbox in the aggregate transaction.
type DeliveryEffect struct {
	Kind             DeliveryEffectKind
	TargetUserID     int64
	ExcludeAuthKeyID [8]byte
	ExcludeSessionID int64
	Event            domain.UpdateEvent
}

type DeliveryEffectKind uint8

const (
	DeliveryEffectInvalid DeliveryEffectKind = iota
	DeliveryEffectAccountPTS
)

type DeliveryEffectsBuilder[T any] func(T) ([]DeliveryEffect, error)

func AccountPTSDeliveryEffect(userID int64, event domain.UpdateEvent, excludeAuthKeyID [8]byte, excludeSessionID int64) DeliveryEffect {
	return DeliveryEffect{
		Kind: DeliveryEffectAccountPTS, TargetUserID: userID, Event: event,
		ExcludeAuthKeyID: excludeAuthKeyID, ExcludeSessionID: excludeSessionID,
	}
}

func (e DeliveryEffect) Validate() error {
	if e.Kind != DeliveryEffectAccountPTS || e.TargetUserID <= 0 {
		return fmt.Errorf("invalid account PTS delivery effect")
	}
	if (e.ExcludeAuthKeyID == ([8]byte{})) != (e.ExcludeSessionID == 0) {
		return fmt.Errorf("delivery effect exclusion requires both raw auth key and session id")
	}
	if e.Event.PtsCount <= 0 || e.Event.Pts != 0 {
		return fmt.Errorf("account PTS delivery effect requires unallocated positive pts_count")
	}
	return nil
}
