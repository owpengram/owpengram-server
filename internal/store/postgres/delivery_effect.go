package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/store"
	"telesrv/internal/store/postgres/sqlcgen"
)

func applyDeliveryEffectsTx(ctx context.Context, tx pgx.Tx, effects []store.DeliveryEffect) ([]store.DeliveryEffect, error) {
	qtx := sqlcgen.New(tx)
	events := NewUpdateEventStore(tx)
	for i := range effects {
		effect := &effects[i]
		if err := effect.Validate(); err != nil {
			return nil, fmt.Errorf("delivery effect %d: %w", i, err)
		}
		event, err := events.appendInTx(ctx, tx, qtx, effect.TargetUserID, effect.Event, true, effect.ExcludeAuthKeyID, effect.ExcludeSessionID, true)
		if err != nil {
			return nil, fmt.Errorf("apply account PTS delivery effect %d: %w", i, err)
		}
		effect.Event = event
	}
	return effects, nil
}
