package users

import (
	"context"
	"errors"
	"sort"
	"testing"

	privacyapp "telesrv/internal/app/privacy"
	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type countingSparseBaseUserStore struct {
	store.UserStore
	byIDsCalls int
	byIDs      []int64
}

func (s *countingSparseBaseUserStore) ByIDs(ctx context.Context, ids []int64) ([]domain.User, error) {
	s.byIDsCalls++
	s.byIDs = append([]int64(nil), ids...)
	return s.UserStore.ByIDs(ctx, ids)
}

type countingSparsePhotoProvider struct {
	profile       map[int64]domain.ProfilePhotoRef
	profileCalls  int
	fallbackCalls int
}

func (p *countingSparsePhotoProvider) CurrentProfilePhotos(ctx context.Context, ownerType domain.PeerType, ids []int64) (map[int64]domain.ProfilePhotoRef, error) {
	return p.CurrentProfilePhotosKind(ctx, ownerType, ids, domain.ProfilePhotoKindProfile)
}

func (p *countingSparsePhotoProvider) CurrentProfilePhotosKind(_ context.Context, _ domain.PeerType, ids []int64, kind domain.ProfilePhotoKind) (map[int64]domain.ProfilePhotoRef, error) {
	if kind == domain.ProfilePhotoKindFallback {
		p.fallbackCalls++
		return map[int64]domain.ProfilePhotoRef{}, nil
	}
	p.profileCalls++
	out := make(map[int64]domain.ProfilePhotoRef, len(ids))
	for _, id := range ids {
		if ref, ok := p.profile[id]; ok {
			out[id] = ref
		}
	}
	return out, nil
}

func TestByIDsForViewerUserIDsLoadsUnionOnceAndPreservesViewerSemantics(t *testing.T) {
	ctx := context.Background()
	base := memory.NewUserStore()
	viewerA, _ := base.Create(ctx, domain.User{Phone: "15550001", FirstName: "Viewer A"})
	viewerB, _ := base.Create(ctx, domain.User{Phone: "15550002", FirstName: "Viewer B"})
	ownerA, _ := base.Create(ctx, domain.User{Phone: "15550101", FirstName: "Owner A"})
	ownerB, _ := base.Create(ctx, domain.User{Phone: "15550102", FirstName: "Owner B"})
	contacts := memory.NewContactStore()
	if _, err := contacts.Upsert(ctx, viewerA.ID, domain.ContactInput{ContactUserID: ownerA.ID, FirstName: "Alias A", Phone: "local-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := contacts.Upsert(ctx, viewerB.ID, domain.ContactInput{ContactUserID: ownerB.ID, FirstName: "Alias B"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := contacts.SetPersonalPhoto(ctx, viewerA.ID, ownerA.ID, 9901, 1); err != nil || !found {
		t.Fatalf("SetPersonalPhoto: found=%v err=%v", found, err)
	}
	rules := memory.NewPrivacyStore()
	privacy := privacyapp.NewService(rules, contacts)
	if _, err := privacy.SetRules(ctx, ownerA.ID, domain.PrivacyKeyPhoneNumber, []domain.PrivacyRule{{Kind: domain.PrivacyRuleDisallowAll}}); err != nil {
		t.Fatal(err)
	}
	if _, err := privacy.SetRules(ctx, ownerB.ID, domain.PrivacyKeyPhoneNumber, []domain.PrivacyRule{{Kind: domain.PrivacyRuleAllowAll}}); err != nil {
		t.Fatal(err)
	}
	countingUsers := &countingSparseBaseUserStore{UserStore: base}
	photos := &countingSparsePhotoProvider{profile: map[int64]domain.ProfilePhotoRef{
		viewerA.ID: {PhotoID: 9801, DCID: 2}, viewerB.ID: {PhotoID: 9802, DCID: 2},
		ownerA.ID: {PhotoID: 9811, DCID: 2}, ownerB.ID: {PhotoID: 9812, DCID: 2},
	}}
	svc := NewService(countingUsers, WithContactStore(contacts), WithPrivacyEvaluator(privacy), WithPhotoProvider(photos))
	got, err := svc.ByIDsForViewerUserIDs(ctx, map[int64][]int64{
		viewerA.ID: {ownerA.ID, viewerA.ID},
		viewerB.ID: {ownerB.ID, viewerB.ID},
	})
	if err != nil {
		t.Fatalf("ByIDsForViewerUserIDs: %v", err)
	}
	if countingUsers.byIDsCalls != 1 {
		t.Fatalf("base ByIDs calls = %d, want one union load", countingUsers.byIDsCalls)
	}
	sort.Slice(countingUsers.byIDs, func(i, j int) bool { return countingUsers.byIDs[i] < countingUsers.byIDs[j] })
	wantIDs := []int64{viewerA.ID, viewerB.ID, ownerA.ID, ownerB.ID}
	sort.Slice(wantIDs, func(i, j int) bool { return wantIDs[i] < wantIDs[j] })
	if len(countingUsers.byIDs) != len(wantIDs) {
		t.Fatalf("base ids = %v, want %v", countingUsers.byIDs, wantIDs)
	}
	for i := range wantIDs {
		if countingUsers.byIDs[i] != wantIDs[i] {
			t.Fatalf("base ids = %v, want %v", countingUsers.byIDs, wantIDs)
		}
	}
	if photos.profileCalls != 1 || photos.fallbackCalls != 1 {
		t.Fatalf("photo reads = profile %d fallback %d, want one each", photos.profileCalls, photos.fallbackCalls)
	}
	a := got[viewerA.ID][0]
	if a.ID != ownerA.ID || a.FirstName != "Alias A" || a.Phone != "local-a" || a.PhotoID != 9901 || !a.PhotoPersonal {
		t.Fatalf("viewer A owner projection = %+v", a)
	}
	selfA := got[viewerA.ID][1]
	if selfA.ID != viewerA.ID || selfA.FirstName != "Viewer A" || selfA.Phone != "15550001" || selfA.PhotoID != 9801 {
		t.Fatalf("viewer A self projection = %+v", selfA)
	}
	b := got[viewerB.ID][0]
	if b.ID != ownerB.ID || b.FirstName != "Alias B" || b.Phone != "15550102" || b.PhotoID != 9812 || b.PhotoPersonal {
		t.Fatalf("viewer B owner projection = %+v", b)
	}
	selfB := got[viewerB.ID][1]
	if selfB.ID != viewerB.ID || selfB.FirstName != "Viewer B" || selfB.Phone != "15550002" || selfB.PhotoID != 9802 {
		t.Fatalf("viewer B self projection = %+v", selfB)
	}
}

func TestByIDsForViewerUserIDsRejectsMissingReferencedUserAndPairOverflow(t *testing.T) {
	svc := NewService(memory.NewUserStore())
	if _, err := svc.ByIDsForViewerUserIDs(context.Background(), map[int64][]int64{
		1001: {2001},
	}); !errors.Is(err, ErrBatchUserMissing) {
		t.Fatalf("missing referenced user err = %v, want ErrBatchUserMissing", err)
	}
	if !sparseViewerProjectionPairsAllowed(maxBatchViewerProjectionCells-1, 1) {
		t.Fatal("sparse pair admission rejected the exact boundary")
	}
	if sparseViewerProjectionPairsAllowed(maxBatchViewerProjectionCells, 1) {
		t.Fatal("sparse pair admission accepted a batch above the boundary")
	}
}
