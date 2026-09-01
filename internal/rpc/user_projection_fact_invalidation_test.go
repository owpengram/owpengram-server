package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

type recordingUserProjectionFactInvalidator struct {
	freezes []int64
	phones  []int64
}

func (r *recordingUserProjectionFactInvalidator) InvalidateAccountFreezeFact(userID int64) {
	r.freezes = append(r.freezes, userID)
}

func (r *recordingUserProjectionFactInvalidator) InvalidateCollectiblePhoneFact(userID int64) {
	r.phones = append(r.phones, userID)
}

func TestAdminUserFactHooksInvalidateBeforeProjectionRefresh(t *testing.T) {
	facts := &recordingUserProjectionFactInvalidator{}
	router := New(Config{}, Deps{UserProjectionFacts: facts}, zaptest.NewLogger(t), clock.System)
	if err := router.NotifyAccountFreezeChanged(context.Background(), domain.AccountFreeze{UserID: 77, Frozen: true}); err != nil {
		t.Fatalf("NotifyAccountFreezeChanged: %v", err)
	}
	if err := router.NotifyUserChanged(context.Background(), domain.User{ID: 88}); err != nil {
		t.Fatalf("NotifyUserChanged: %v", err)
	}
	if len(facts.freezes) != 1 || facts.freezes[0] != 77 {
		t.Fatalf("freeze invalidations = %v, want [77]", facts.freezes)
	}
	if len(facts.phones) != 1 || facts.phones[0] != 88 {
		t.Fatalf("phone invalidations = %v, want [88]", facts.phones)
	}
}
