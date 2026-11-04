package rpc

import (
	"context"
	"testing"

	"github.com/owpengram/owpengram-td/clock"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

type userFullBatchPrivacy struct {
	stubPrivacy
	visible     map[domain.PrivacyKey]bool
	batchCalls  int
	scalarCalls int
	keys        []domain.PrivacyKey
}

func (p *userFullBatchPrivacy) CanSee(_ context.Context, _, _ int64, key domain.PrivacyKey) (bool, error) {
	p.scalarCalls++
	return p.visible[key], nil
}

func (p *userFullBatchPrivacy) CanSeeBatch(_ context.Context, ownerUserIDs []int64, _ int64, keys []domain.PrivacyKey) (map[int64]map[domain.PrivacyKey]bool, error) {
	p.batchCalls++
	p.keys = append([]domain.PrivacyKey(nil), keys...)
	out := make(map[int64]map[domain.PrivacyKey]bool, len(ownerUserIDs))
	for _, ownerID := range ownerUserIDs {
		owner := make(map[domain.PrivacyKey]bool, len(p.visible))
		for key, allowed := range p.visible {
			owner[key] = allowed
		}
		out[ownerID] = owner
	}
	return out, nil
}

func TestBuildUserFullProjectionBatchesPrivacyAndFailsClosedOnMissingKeys(t *testing.T) {
	privacy := &userFullBatchPrivacy{visible: map[domain.PrivacyKey]bool{
		domain.PrivacyKeyAbout:         false,
		domain.PrivacyKeyPhoneCall:     true,
		domain.PrivacyKeyPhoneP2P:      false,
		domain.PrivacyKeyVoiceMessages: false,
		domain.PrivacyKeyBirthday:      true,
		// ProfilePhoto and SavedMusic are deliberately absent: batch omissions
		// must stay denied rather than becoming a privacy bypass.
	}}
	r := New(Config{}, Deps{Privacy: privacy}, zaptest.NewLogger(t), clock.System)
	full, err := r.buildUserFullProjection(context.Background(), 10, domain.User{
		ID: 20, FirstName: "Target", About: "private about",
		Birthday: domain.Birthday{Day: 2, Month: 8, Year: 2000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if privacy.batchCalls != 1 || privacy.scalarCalls != 0 {
		t.Fatalf("privacy calls batch=%d scalar=%d, want 1/0", privacy.batchCalls, privacy.scalarCalls)
	}
	if len(privacy.keys) != 7 {
		t.Fatalf("batch keys=%v, want seven UserFull privacy keys", privacy.keys)
	}
	if full.About != "" || !full.PhoneCallsAvailable || !full.PhoneCallsPrivate || !full.VoiceMessagesForbidden {
		t.Fatalf("privacy projection=%+v", full)
	}
	if _, ok := full.GetBirthday(); !ok {
		t.Fatal("allowed birthday omitted")
	}
	if _, ok := full.GetProfilePhoto(); ok {
		t.Fatal("missing profile-photo visibility defaulted to visible")
	}
	if _, ok := full.GetSavedMusic(); ok {
		t.Fatal("missing saved-music visibility defaulted to visible")
	}
}

func TestBuildUserFullProjectionSelfSkipsPrivacyEvaluation(t *testing.T) {
	privacy := &userFullBatchPrivacy{visible: map[domain.PrivacyKey]bool{}}
	r := New(Config{}, Deps{Privacy: privacy}, zaptest.NewLogger(t), clock.System)
	full, err := r.buildUserFullProjection(context.Background(), 20, domain.User{ID: 20, About: "self"})
	if err != nil {
		t.Fatal(err)
	}
	if privacy.batchCalls != 0 || privacy.scalarCalls != 0 || full.About != "self" {
		t.Fatalf("self projection calls=%d/%d full=%+v", privacy.batchCalls, privacy.scalarCalls, full)
	}
}
