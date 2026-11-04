package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/owpengram/owpengram-td/bin"
	"github.com/owpengram/owpengram-td/clock"
	"github.com/owpengram/owpengram-td/tg"
	"github.com/owpengram/owpengram-td/tgerr"
	"github.com/owpengram/owpengram-td/tlprofile"
	"go.uber.org/zap/zaptest"

	appchannels "telesrv/internal/app/channels"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

// fakeUsernameRegistry is an in-memory UsernameRegistryService. It enforces the
// same domain rules the real registry does, so the RPC tests exercise the actual
// error mapping rather than hand-rolled sentinels.
type fakeUsernameRegistry struct {
	byPeer       map[domain.Peer][]domain.Username
	collectibles map[string]domain.CollectibleUsername
	// err, when set, fails every read. Used for the degradation tests.
	err error
	// batchCalls / peerCalls count read fan-out so the tests can assert no N+1.
	batchCalls int
	peerCalls  int
}

func newFakeUsernameRegistry() *fakeUsernameRegistry {
	return &fakeUsernameRegistry{
		byPeer:       make(map[domain.Peer][]domain.Username),
		collectibles: make(map[string]domain.CollectibleUsername),
	}
}

func (f *fakeUsernameRegistry) PeerUsernames(_ context.Context, peer domain.Peer) ([]domain.Username, error) {
	f.peerCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.byPeer[peer], nil
}

func (f *fakeUsernameRegistry) UsernamesBatch(_ context.Context, peers []domain.Peer) (map[domain.Peer][]domain.Username, error) {
	f.batchCalls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.Peer][]domain.Username, len(peers))
	for _, peer := range peers {
		if list, ok := f.byPeer[peer]; ok {
			out[peer] = list
		}
	}
	return out, nil
}

func (f *fakeUsernameRegistry) ToggleUsername(_ context.Context, peer domain.Peer, username string, active bool) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	current := f.byPeer[peer]
	if err := domain.ValidateUsernameToggle(current, username, active); err != nil {
		return false, err
	}
	changed := false
	next := append([]domain.Username(nil), current...)
	for i := range next {
		if next[i].Username != domain.NormalizeUsername(username) {
			continue
		}
		if next[i].Active != active {
			next[i].Active = active
			changed = true
		}
	}
	f.byPeer[peer] = next
	return changed, nil
}

func (f *fakeUsernameRegistry) ReorderUsernames(_ context.Context, peer domain.Peer, order []string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	current := f.byPeer[peer]
	next, err := domain.ApplyUsernameReorder(current, order)
	if err != nil {
		return false, err
	}
	// Same contract as both real backends: the renumbering is always persisted,
	// and "changed" is only about what a client can see.
	changed := !domain.SameUsernameOrder(current, next)
	f.byPeer[peer] = next
	return changed, nil
}

func (f *fakeUsernameRegistry) DeactivateAllUsernames(_ context.Context, peer domain.Peer) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	next := append([]domain.Username(nil), f.byPeer[peer]...)
	changed := false
	for i := range next {
		if next[i].Collectible() && next[i].Active {
			next[i].Active = false
			changed = true
		}
	}
	f.byPeer[peer] = next
	return changed, nil
}

func (f *fakeUsernameRegistry) Collectible(_ context.Context, username string) (domain.CollectibleUsername, error) {
	if f.err != nil {
		return domain.CollectibleUsername{}, f.err
	}
	// Usernames are case-insensitive in the registry, as they are in the store.
	asset, ok := f.collectibles[strings.ToLower(domain.NormalizeUsername(username))]
	if !ok {
		return domain.CollectibleUsername{}, domain.ErrCollectibleUsernameNotFound
	}
	return asset, nil
}

var _ UsernameRegistryService = (*fakeUsernameRegistry)(nil)

func TestTgUsernamesFromRegistryFollowsStoredOrder(t *testing.T) {
	// Legacy numbering, which is what every peer that never reordered carries:
	// the editable slot and the first collectible share sort_order 0 and the
	// editable slot wins the tie, so it still projects first.
	assertUsernameVector(t,
		[]domain.Username{
			{Username: "second", Active: false, SortOrder: 1, CollectibleID: 22},
			{Username: "editable_slot", Active: true, Editable: true, SortOrder: 0},
			{Username: "first", Active: true, SortOrder: 0, CollectibleID: 11},
		},
		[]tg.Username{
			{Editable: true, Active: true, Username: "editable_slot"},
			{Editable: false, Active: true, Username: "first"},
			{Editable: false, Active: false, Username: "second"},
		})

	// After a reorder made a collectible primary, stored order wins: clients read
	// usernames[0] as the peer's primary username
	// (core.telegram.org/api/fragment), so the editable slot must be able to
	// leave that position.
	assertUsernameVector(t,
		[]domain.Username{
			{Username: "second", Active: false, SortOrder: 2, CollectibleID: 22},
			{Username: "editable_slot", Active: true, Editable: true, SortOrder: 1},
			{Username: "first", Active: true, SortOrder: 0, CollectibleID: 11},
		},
		[]tg.Username{
			{Editable: false, Active: true, Username: "first"},
			{Editable: true, Active: true, Username: "editable_slot"},
			{Editable: false, Active: false, Username: "second"},
		})
}

func assertUsernameVector(t *testing.T, list []domain.Username, want []tg.Username) {
	t.Helper()
	got := tgUsernamesFromRegistry(list, "editable_slot")
	if len(got) != len(want) {
		t.Fatalf("usernames = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Username != want[i].Username || got[i].Editable != want[i].Editable || got[i].Active != want[i].Active {
			t.Fatalf("usernames[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTgUsernamesFromRegistryDegradesToScalar(t *testing.T) {
	// Empty registry contribution must be byte-identical to the legacy vector.
	if got, want := tgUsernamesFromRegistry(nil, "legacy"), tgUsernames("legacy"); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("empty registry = %+v, want %+v", got, want)
	}
	if got := tgUsernamesFromRegistry(nil, ""); got != nil {
		t.Fatalf("empty registry with empty scalar = %+v, want nil", got)
	}
	// A registry list that normalizes away entirely also degrades rather than
	// emitting an empty vector.
	if got, want := tgUsernamesFromRegistry([]domain.Username{{Username: "  "}}, "legacy"), tgUsernames("legacy"); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("blank registry rows = %+v, want %+v", got, want)
	}
}

type usernameProjectionFixture struct {
	router   *Router
	registry *fakeUsernameRegistry
	owner    domain.User
	friend   domain.User
}

func newUsernameProjectionFixture(t *testing.T, registry UsernameRegistryService) usernameProjectionFixture {
	t.Helper()
	ctx := context.Background()
	userStore := memory.NewUserStore()
	owner, err := userStore.Create(ctx, domain.User{AccessHash: 11, Phone: "15550002001", FirstName: "Owner", Username: "owner_slot"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	friend, err := userStore.Create(ctx, domain.User{AccessHash: 22, Phone: "15550002002", FirstName: "Friend", Username: "friend_slot"})
	if err != nil {
		t.Fatalf("create friend: %v", err)
	}
	fake, _ := registry.(*fakeUsernameRegistry)
	return usernameProjectionFixture{
		router: New(Config{}, Deps{
			Users:     appusers.NewService(userStore),
			Usernames: registry,
		}, zaptest.NewLogger(t), clock.System),
		registry: fake,
		owner:    owner,
		friend:   friend,
	}
}

func usernameStrings(list []tg.Username) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.Username)
	}
	return out
}

type tgUsernamePeer interface {
	SetFlags()
	GetUsername() (string, bool)
	GetUsernames() ([]tg.Username, bool)
}

func assertVectorOnlyUsernames(t *testing.T, stage string, peer tgUsernamePeer, want []string) []tg.Username {
	t.Helper()
	peer.SetFlags()
	if scalar, set := peer.GetUsername(); set || scalar != "" {
		t.Fatalf("%s scalar username = %q (set %v), want absent when usernames vector is present", stage, scalar, set)
	}
	vector, set := peer.GetUsernames()
	if !set || !reflect.DeepEqual(usernameStrings(vector), want) {
		t.Fatalf("%s usernames = %v (set %v), want %v", stage, usernameStrings(vector), set, want)
	}
	return vector
}

func assertScalarOnlyUsername(t *testing.T, stage string, peer tgUsernamePeer, want string) {
	t.Helper()
	peer.SetFlags()
	if vector, set := peer.GetUsernames(); set || len(vector) != 0 {
		t.Fatalf("%s usernames = %+v (set %v), want vector absent", stage, vector, set)
	}
	if scalar, set := peer.GetUsername(); !set || scalar != want {
		t.Fatalf("%s scalar username = %q (set %v), want %q", stage, scalar, set, want)
	}
}

func assertUsernameUpdateEnvelope(t *testing.T, stage string, updates *tg.Updates, want []string) {
	t.Helper()
	assertValue := func(valueStage string, value *tg.Updates) {
		t.Helper()
		if value == nil || len(value.Updates) != 1 || len(value.Users) != 1 {
			t.Fatalf("%s envelope = %+v, want one update and one user", valueStage, value)
		}
		update, ok := value.Updates[0].(*tg.UpdateUserName)
		if !ok {
			t.Fatalf("%s update = %T, want *tg.UpdateUserName", valueStage, value.Updates[0])
		}
		if got := usernameStrings(update.Usernames); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s update usernames = %v, want %v", valueStage, got, want)
		}
		user, ok := value.Users[0].(*tg.User)
		if !ok {
			t.Fatalf("%s companion user = %T, want *tg.User", valueStage, value.Users[0])
		}
		assertVectorOnlyUsernames(t, valueStage+" companion", user, want)
	}
	assertValue(stage, updates)
	for _, profile := range []tlprofile.Profile{
		tlprofile.Profile225,
		tlprofile.Profile226,
		tlprofile.Profile227,
		tlprofile.Profile228,
	} {
		profileStage := fmt.Sprintf("%s Layer %d", stage, profile)
		var wire bin.Buffer
		if err := tlprofile.EncodeObject(profile, updates, &wire); err != nil {
			t.Fatalf("encode %s: %v", profileStage, err)
		}
		decoded, err := tlprofile.DecodeObject(profile, &bin.Buffer{Buf: wire.Copy()}, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("decode %s: %v", profileStage, err)
		}
		decodedUpdates, ok := decoded.(*tg.Updates)
		if !ok {
			t.Fatalf("decoded %s = %T, want *tg.Updates", profileStage, decoded)
		}
		assertValue(profileStage, decodedUpdates)
	}
}

func TestApplyUsernamesFromRegistryKeepsEditableOnlyPeersScalarOnly(t *testing.T) {
	user := &tg.User{ID: 11}
	user.SetUsername("editable_user")
	channel := &tg.Channel{ID: 22}
	channel.SetUsername("editable_channel")
	applyUsernamesFromRegistry(
		[]tg.UserClass{user},
		[]tg.ChatClass{channel},
		map[domain.Peer][]domain.Username{
			{Type: domain.PeerTypeUser, ID: user.ID}: {
				{Username: "editable_user", Editable: true, Active: true},
			},
			{Type: domain.PeerTypeChannel, ID: channel.ID}: {
				{Username: "editable_channel", Editable: true, Active: true},
			},
		},
	)
	assertScalarOnlyUsername(t, "editable-only user", user, "editable_user")
	assertScalarOnlyUsername(t, "editable-only channel", channel, "editable_channel")
}

func TestUsersGetUsersProjectsCollectibleUsernamesInOneBatch(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}] = []domain.Username{
		{Username: "owner_slot", Editable: true, Active: true, SortOrder: 1},
		{Username: "nft4", Active: true, SortOrder: 0, CollectibleID: 7},
	}
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: f.friend.ID}] = []domain.Username{
		{Username: "friend_slot", Editable: true, Active: true},
		{Username: "gem4", Active: false, SortOrder: 1, CollectibleID: 8},
	}
	ctx := WithUserID(context.Background(), f.owner.ID)

	out, err := f.router.onUsersGetUsers(ctx, []tg.InputUserClass{
		&tg.InputUserSelf{},
		&tg.InputUser{UserID: f.friend.ID, AccessHash: f.friend.AccessHash},
	})
	if err != nil {
		t.Fatalf("get users: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("users = %d, want 2", len(out))
	}
	self := out[0].(*tg.User)
	vector := assertVectorOnlyUsernames(t, "self", self, []string{"nft4", "owner_slot"})
	if vector[0].Editable || !vector[0].Active {
		t.Fatalf("primary collectible flags = %+v, want non-editable+active", vector[0])
	}
	if !vector[1].Editable || !vector[1].Active {
		t.Fatalf("editable slot flags = %+v, want editable+active", vector[1])
	}
	friend := out[1].(*tg.User)
	friendVector := assertVectorOnlyUsernames(t, "friend", friend, []string{"friend_slot", "gem4"})
	if friendVector[1].Active {
		t.Fatalf("inactive collectible projected active: %+v", friendVector[1])
	}
	// Two users, one batch read: no N+1.
	if registry.batchCalls != 1 || registry.peerCalls != 0 {
		t.Fatalf("registry reads = batch %d / peer %d, want batch 1 / peer 0", registry.batchCalls, registry.peerCalls)
	}
}

func TestResolveUsernamePreservesCompleteUsernamesThroughLayers225To228(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}] = []domain.Username{
		{Username: "owner_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "owner_collectible_b", Active: true, SortOrder: 1, CollectibleID: 22},
		{Username: "owner_collectible_a", Active: true, SortOrder: 2, CollectibleID: 21},
	}

	resolved, err := f.router.onContactsResolveUsername(
		WithUserID(context.Background(), f.friend.ID),
		&tg.ContactsResolveUsernameRequest{Username: "@owner_slot"},
	)
	if err != nil {
		t.Fatalf("resolve username: %v", err)
	}
	assertResolvedUsernames := func(stage string, value *tg.ContactsResolvedPeer) {
		t.Helper()
		if value == nil || len(value.Users) != 1 {
			t.Fatalf("%s resolved users = %+v, want one user", stage, value)
		}
		user, ok := value.Users[0].(*tg.User)
		if !ok {
			t.Fatalf("%s resolved user = %T, want *tg.User", stage, value.Users[0])
		}
		want := []string{"owner_slot", "owner_collectible_b", "owner_collectible_a"}
		assertVectorOnlyUsernames(t, stage, user, want)
	}
	assertResolvedUsernames("canonical", resolved)

	for _, profile := range []tlprofile.Profile{
		tlprofile.Profile225,
		tlprofile.Profile226,
		tlprofile.Profile227,
		tlprofile.Profile228,
	} {
		stage := fmt.Sprintf("Layer %d", profile)
		var wire bin.Buffer
		if err := tlprofile.EncodeObject(profile, resolved, &wire); err != nil {
			t.Fatalf("encode %s resolved peer: %v", stage, err)
		}
		decoded, err := tlprofile.DecodeObject(profile, &bin.Buffer{Buf: wire.Copy()}, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("decode %s resolved peer: %v", stage, err)
		}
		decodedResolved, ok := decoded.(*tg.ContactsResolvedPeer)
		if !ok {
			t.Fatalf("decoded %s object = %T, want *tg.ContactsResolvedPeer", stage, decoded)
		}
		assertResolvedUsernames(stage, decodedResolved)

		requestBody := encodeExactLayerRPC(t, profile, &tg.ContactsResolveUsernameRequest{Username: "@owner_slot"})
		admitted, err := f.router.AdmitLayer(profile, &requestBody, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("admit %s contacts.resolveUsername: %v", stage, err)
		}
		result, method, err := f.router.DispatchAdmitted(
			WithUserID(context.Background(), f.friend.ID),
			[8]byte{1},
			1,
			1,
			1,
			admitted,
		)
		if err != nil || method != "contacts.resolveUsername" {
			t.Fatalf("dispatch %s method=%q err=%v", stage, method, err)
		}
		var resultWire bin.Buffer
		if err := result.Encode(&resultWire); err != nil {
			t.Fatalf("encode %s method result: %v", stage, err)
		}
		decodedResult, err := tlprofile.DecodeObject(profile, &bin.Buffer{Buf: resultWire.Copy()}, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("decode %s method result: %v", stage, err)
		}
		methodResolved, ok := decodedResult.(*tg.ContactsResolvedPeer)
		if !ok {
			t.Fatalf("decoded %s method result = %T, want *tg.ContactsResolvedPeer", stage, decodedResult)
		}
		assertResolvedUsernames(stage+" method result", methodResolved)
	}
}

func TestAuthLoginTokenSuccessProjectsCompleteSelfUsernames(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}] = []domain.Username{
		{Username: "owner_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "owner_collectible_b", Active: true, SortOrder: 1, CollectibleID: 22},
		{Username: "owner_collectible_a", Active: true, SortOrder: 2, CollectibleID: 21},
	}

	result, err := f.router.authLoginTokenSuccess(context.Background(), domain.Authorization{UserID: f.owner.ID})
	if err != nil {
		t.Fatalf("auth login token success: %v", err)
	}
	success, ok := result.(*tg.AuthLoginTokenSuccess)
	if !ok {
		t.Fatalf("auth login token result = %T, want *tg.AuthLoginTokenSuccess", result)
	}
	authorization, ok := success.Authorization.(*tg.AuthAuthorization)
	if !ok {
		t.Fatalf("authorization = %T, want *tg.AuthAuthorization", success.Authorization)
	}
	self, ok := authorization.User.(*tg.User)
	if !ok {
		t.Fatalf("authorization user = %T, want *tg.User", authorization.User)
	}
	want := []string{"owner_slot", "owner_collectible_b", "owner_collectible_a"}
	assertVectorOnlyUsernames(t, "authorization", self, want)

	for _, profile := range []tlprofile.Profile{
		tlprofile.Profile225,
		tlprofile.Profile226,
		tlprofile.Profile227,
		tlprofile.Profile228,
	} {
		stage := fmt.Sprintf("Layer %d authorization", profile)
		var wire bin.Buffer
		if err := tlprofile.EncodeObject(profile, success, &wire); err != nil {
			t.Fatalf("encode %s: %v", stage, err)
		}
		decoded, err := tlprofile.DecodeObject(profile, &bin.Buffer{Buf: wire.Copy()}, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("decode %s: %v", stage, err)
		}
		decodedSuccess, ok := decoded.(*tg.AuthLoginTokenSuccess)
		if !ok {
			t.Fatalf("decoded %s = %T", stage, decoded)
		}
		decodedAuthorization := decodedSuccess.Authorization.(*tg.AuthAuthorization)
		decodedSelf := decodedAuthorization.User.(*tg.User)
		assertVectorOnlyUsernames(t, stage, decodedSelf, want)
	}
	if registry.peerCalls != 1 || registry.batchCalls != 0 {
		t.Fatalf("authorization username reads = peer:%d batch:%d, want 1/0",
			registry.peerCalls, registry.batchCalls)
	}
}

func TestAccountProfileMutationResponsesKeepVectorOnlyUsernames(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	sessions := &captureSessions{}
	f.router.deps.Sessions = sessions
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}
	registry.byPeer[peer] = []domain.Username{
		{Username: "owner_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "owner_collectible", Active: true, SortOrder: 1, CollectibleID: 21},
	}
	ctx := WithUserID(context.Background(), f.owner.ID)
	profile := &tg.AccountUpdateProfileRequest{}
	profile.SetFirstName("Updated")

	updated, err := f.router.onAccountUpdateProfile(ctx, profile)
	if err != nil {
		t.Fatalf("account.updateProfile: %v", err)
	}
	self, ok := updated.(*tg.User)
	if !ok {
		t.Fatalf("account.updateProfile user = %T, want *tg.User", updated)
	}
	assertVectorOnlyUsernames(t, "account.updateProfile", self, []string{"owner_slot", "owner_collectible"})
	profileEnvelope := sessions.lastUserPush().(*tg.Updates)
	pushed := profileEnvelope.Users[0].(*tg.User)
	if pushed == self {
		t.Fatal("account.updateProfile reused one mutable tg.User for RPC result and push")
	}
	assertUsernameUpdateEnvelope(t, "account.updateProfile push", profileEnvelope, []string{"owner_slot", "owner_collectible"})
	if registry.peerCalls != 1 || registry.batchCalls != 0 {
		t.Fatalf("account.updateProfile username reads = peer:%d batch:%d, want 1/0", registry.peerCalls, registry.batchCalls)
	}

	registry.byPeer[peer] = []domain.Username{
		{Username: "renamed_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "owner_collectible", Active: true, SortOrder: 1, CollectibleID: 21},
	}
	updated, err = f.router.onAccountUpdateUsername(ctx, "renamed_slot")
	if err != nil {
		t.Fatalf("account.updateUsername: %v", err)
	}
	self, ok = updated.(*tg.User)
	if !ok {
		t.Fatalf("account.updateUsername user = %T, want *tg.User", updated)
	}
	assertVectorOnlyUsernames(t, "account.updateUsername", self, []string{"renamed_slot", "owner_collectible"})
	usernameEnvelope := sessions.lastUserPush().(*tg.Updates)
	pushed = usernameEnvelope.Users[0].(*tg.User)
	if pushed == self {
		t.Fatal("account.updateUsername reused one mutable tg.User for RPC result and push")
	}
	assertUsernameUpdateEnvelope(t, "account.updateUsername push", usernameEnvelope, []string{"renamed_slot", "owner_collectible"})
	if registry.peerCalls != 2 || registry.batchCalls != 0 {
		t.Fatalf("profile mutation username reads = peer:%d batch:%d, want 2/0", registry.peerCalls, registry.batchCalls)
	}
}

func TestPremiumStatusProjectionSkipsOfflineUsernameRead(t *testing.T) {
	const userID = int64(1000000888)
	registry := newFakeUsernameRegistry()
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: userID}] = []domain.Username{
		{Username: "premium_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "premium_collectible", Active: true, SortOrder: 1, CollectibleID: 88},
	}
	sessions := &captureSessions{}
	r := New(Config{}, Deps{Sessions: sessions, Usernames: registry}, zaptest.NewLogger(t), clock.System)
	u := domain.User{ID: userID, FirstName: "Premium", Username: "premium_slot"}

	r.pushPremiumStatusUpdate(context.Background(), u)
	if registry.peerCalls != 0 || sessions.lastUserPush() != nil {
		t.Fatalf("offline premium push = registry reads:%d message:%T, want no work", registry.peerCalls, sessions.lastUserPush())
	}

	sessions.onlineUserIDs = []int64{userID}
	r.pushPremiumStatusUpdate(context.Background(), u)
	if registry.peerCalls != 1 {
		t.Fatalf("online premium username reads = %d, want 1", registry.peerCalls)
	}
	updates, ok := sessions.lastUserPush().(*tg.Updates)
	if !ok || len(updates.Users) != 1 {
		t.Fatalf("online premium push = %T %+v", sessions.lastUserPush(), sessions.lastUserPush())
	}
	assertVectorOnlyUsernames(t, "premium status update", updates.Users[0].(*tg.User), []string{"premium_slot", "premium_collectible"})
}

func TestPremiumStatusProjectionBatchesOnlineUsers(t *testing.T) {
	registry := newFakeUsernameRegistry()
	users := []domain.User{
		{ID: 101, FirstName: "One", Username: "one_slot"},
		{ID: 102, FirstName: "Two", Username: "two_slot"},
		{ID: 103, FirstName: "Offline", Username: "offline_slot"},
	}
	for _, u := range users {
		registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: u.ID}] = []domain.Username{
			{Username: u.Username, Editable: true, Active: true, SortOrder: 0},
			{Username: u.Username + "_collectible", Active: true, SortOrder: 1, CollectibleID: u.ID},
		}
	}
	sessions := &captureSessions{onlineUserIDs: []int64{101, 102}}
	r := New(Config{}, Deps{Sessions: sessions, Usernames: registry}, zaptest.NewLogger(t), clock.System)

	r.pushPremiumStatusUpdates(context.Background(), []domain.User{users[0], users[1], users[0], users[2]})

	if registry.batchCalls != 1 || registry.peerCalls != 0 {
		t.Fatalf("premium batch username reads = batch:%d peer:%d, want 1/0", registry.batchCalls, registry.peerCalls)
	}
	if got, want := sessions.pushedUserIDs(), []int64{101, 102}; !reflect.DeepEqual(got, want) {
		t.Fatalf("premium batch pushed users = %v, want %v", got, want)
	}
	updates, ok := sessions.lastUserPush().(*tg.Updates)
	if !ok || len(updates.Users) != 1 {
		t.Fatalf("premium batch last push = %T %+v", sessions.lastUserPush(), sessions.lastUserPush())
	}
	assertVectorOnlyUsernames(t, "premium batch last user", updates.Users[0].(*tg.User), []string{"two_slot", "two_slot_collectible"})
}

func TestMessageEchoProjectsCompleteUsernamesInOneBatch(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}] = []domain.Username{
		{Username: "owner_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "owner_collectible", Active: true, SortOrder: 1, CollectibleID: 21},
	}
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: f.friend.ID}] = []domain.Username{
		{Username: "friend_slot", Editable: true, Active: true, SortOrder: 0},
		{Username: "friend_collectible", Active: true, SortOrder: 1, CollectibleID: 22},
	}

	users := f.router.usersForMessageUpdate(context.Background(), f.owner.ID, domain.Message{
		OwnerUserID: f.owner.ID,
		From:        domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID},
		Peer:        domain.Peer{Type: domain.PeerTypeUser, ID: f.friend.ID},
	})
	if len(users) != 2 {
		t.Fatalf("message echo users = %d, want owner and friend", len(users))
	}
	want := map[int64][]string{
		f.owner.ID:  {"owner_slot", "owner_collectible"},
		f.friend.ID: {"friend_slot", "friend_collectible"},
	}
	for _, item := range users {
		user, ok := item.(*tg.User)
		if !ok {
			t.Fatalf("message echo user = %T, want *tg.User", item)
		}
		assertVectorOnlyUsernames(t, fmt.Sprintf("message echo user %d", user.ID), user, want[user.ID])
	}
	if registry.batchCalls != 1 || registry.peerCalls != 0 {
		t.Fatalf("registry reads = batch %d / peer %d, want one batch read for the response", registry.batchCalls, registry.peerCalls)
	}
}

func TestChannelMessageUpdatesProjectCompleteUsernames(t *testing.T) {
	const (
		viewerUserID = int64(1001)
		senderUserID = int64(1002)
		channelID    = int64(2001)
	)
	registry := newFakeUsernameRegistry()
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: senderUserID}] = []domain.Username{
		{Username: "channel_sender", Editable: true, Active: true, SortOrder: 0},
		{Username: "channel_collectible", Active: true, SortOrder: 1, CollectibleID: 31},
	}
	router := New(Config{}, Deps{
		Users: mapUsersService{users: map[int64]domain.User{
			senderUserID: {ID: senderUserID, FirstName: "Sender", Username: "channel_sender"},
		}},
		Usernames: registry,
	}, zaptest.NewLogger(t), clock.System)
	message := domain.ChannelMessage{
		ID:           41,
		ChannelID:    channelID,
		SenderUserID: senderUserID,
		From:         domain.Peer{Type: domain.PeerTypeUser, ID: senderUserID},
		Date:         1700000500,
		Pts:          9,
	}
	updates := router.channelMessageUpdatesWithPeerCache(context.Background(), viewerUserID, domain.SendChannelMessageResult{
		Channel: domain.Channel{ID: channelID, AccessHash: 22, Title: "group", Megagroup: true, Date: 1700000000},
		Message: message,
		Event: domain.ChannelUpdateEvent{
			ChannelID: channelID,
			Type:      domain.ChannelUpdateNewMessage,
			Pts:       9,
			PtsCount:  1,
			Date:      message.Date,
			Message:   message,
		},
	}, 0, newViewerPeerCache(router))
	if updates == nil || len(updates.Users) != 1 {
		t.Fatalf("channel updates users = %+v, want one sender", updates)
	}
	user := updates.Users[0].(*tg.User)
	assertVectorOnlyUsernames(t, "channel sender", user, []string{"channel_sender", "channel_collectible"})
	if registry.peerCalls != 0 || registry.batchCalls != 1 {
		t.Fatalf("registry reads = peer %d / batch %d, want one batched user+channel read", registry.peerCalls, registry.batchCalls)
	}
}

func TestChannelDifferenceProjectsCompleteUsernames(t *testing.T) {
	const (
		viewerUserID = int64(1001)
		senderUserID = int64(1002)
		channelID    = int64(2001)
	)
	registry := newFakeUsernameRegistry()
	registry.byPeer[domain.Peer{Type: domain.PeerTypeUser, ID: senderUserID}] = []domain.Username{
		{Username: "difference_sender", Editable: true, Active: true, SortOrder: 0},
		{Username: "difference_collectible", Active: true, SortOrder: 1, CollectibleID: 41},
	}
	router := New(Config{}, Deps{Usernames: registry}, zaptest.NewLogger(t), clock.System)
	out := router.tgChannelDifference(context.Background(), viewerUserID, domain.ChannelDifference{
		Final:   true,
		Pts:     9,
		Channel: domain.Channel{ID: channelID, AccessHash: 22, Title: "group", Megagroup: true, Date: 1700000000},
		Self:    domain.ChannelMember{ChannelID: channelID, UserID: viewerUserID, Status: domain.ChannelMemberActive},
		Users: []domain.User{{
			ID:        senderUserID,
			FirstName: "Sender",
			Username:  "difference_sender",
		}},
		NewMessages: []domain.ChannelMessage{{
			ID:           51,
			ChannelID:    channelID,
			SenderUserID: senderUserID,
			From:         domain.Peer{Type: domain.PeerTypeUser, ID: senderUserID},
			Date:         1700000600,
			Pts:          9,
		}},
	})
	diff, ok := out.(*tg.UpdatesChannelDifference)
	if !ok || len(diff.Users) != 1 {
		t.Fatalf("channel difference = %T %+v, want one user", out, out)
	}
	user := diff.Users[0].(*tg.User)
	assertVectorOnlyUsernames(t, "channel difference sender", user, []string{"difference_sender", "difference_collectible"})
	if registry.batchCalls != 1 || registry.peerCalls != 0 {
		t.Fatalf("registry reads = batch %d / peer %d, want one batched user+channel read", registry.batchCalls, registry.peerCalls)
	}
}

func TestUsersGetUsersDegradesWithoutRegistry(t *testing.T) {
	f := newUsernameProjectionFixture(t, nil)
	ctx := WithUserID(context.Background(), f.owner.ID)

	out, err := f.router.onUsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		t.Fatalf("get users: %v", err)
	}
	self := out[0].(*tg.User)
	// Without a collectible registry the official legacy shape is scalar-only.
	assertScalarOnlyUsername(t, "self without registry", self, "owner_slot")
}

func TestUsersGetUsersDegradesWhenRegistryFails(t *testing.T) {
	registry := newFakeUsernameRegistry()
	registry.err = errors.New("registry unavailable")
	f := newUsernameProjectionFixture(t, registry)
	ctx := WithUserID(context.Background(), f.owner.ID)

	out, err := f.router.onUsersGetUsers(ctx, []tg.InputUserClass{
		&tg.InputUserSelf{},
		&tg.InputUser{UserID: f.friend.ID, AccessHash: f.friend.AccessHash},
	})
	if err != nil {
		t.Fatalf("get users must not fail when the registry does: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("users = %d, want 2", len(out))
	}
	for i, want := range []string{"owner_slot", "friend_slot"} {
		user := out[i].(*tg.User)
		assertScalarOnlyUsername(t, fmt.Sprintf("users[%d] with failed registry", i), user, want)
	}
}

func TestFragmentGetCollectibleInfoRPC(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}
	registry.collectibles["nfts"] = domain.CollectibleUsername{
		ID:             7,
		Username:       "nfts",
		Status:         domain.CollectibleUsernameStatusOwned,
		Owner:          peer,
		PurchaseDate:   time.Unix(1700000000, 0),
		Currency:       domain.CollectibleCurrencyUSD,
		Amount:         550000,
		CryptoCurrency: domain.CollectibleCryptoCurrencyTON,
		CryptoAmount:   1200000000,
		URL:            "https://fragment.example/username/nfts",
	}
	registry.byPeer[peer] = []domain.Username{{Username: "nfts", Active: false, CollectibleID: 7}}
	ctx := WithUserID(context.Background(), f.owner.ID)

	info, err := f.router.onFragmentGetCollectibleInfo(ctx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectibleUsername{Username: "@NFTS"},
	})
	if err != nil {
		t.Fatalf("get collectible info: %v", err)
	}
	if info.PurchaseDate != 1700000000 || info.Currency != domain.CollectibleCurrencyUSD || info.Amount != 550000 {
		t.Fatalf("collectible info = %+v, want the stored purchase record", info)
	}
	if info.CryptoCurrency != domain.CollectibleCryptoCurrencyTON || info.CryptoAmount != 1200000000 {
		t.Fatalf("collectible crypto = %s/%d, want TON/1200000000", info.CryptoCurrency, info.CryptoAmount)
	}
	if info.URL != "https://fragment.example/username/nfts" {
		t.Fatalf("collectible url = %q", info.URL)
	}

	// Inactive collectibles are private to their current owner.
	friendCtx := WithUserID(context.Background(), f.friend.ID)
	if _, err := f.router.onFragmentGetCollectibleInfo(friendCtx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectibleUsername{Username: "nfts"},
	}); !tgerr.Is(err, "COLLECTIBLE_NOT_FOUND") {
		t.Fatalf("inactive collectible seen by another user: %v, want COLLECTIBLE_NOT_FOUND", err)
	}
	registry.byPeer[peer][0].Active = true
	if _, err := f.router.onFragmentGetCollectibleInfo(friendCtx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectibleUsername{Username: "nfts"},
	}); err != nil {
		t.Fatalf("active collectible hidden from another user: %v", err)
	}

	// A name with no collectible asset behind it is not occupied as a collectible.
	if _, err := f.router.onFragmentGetCollectibleInfo(ctx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectibleUsername{Username: "owner_slot"},
	}); !tgerr.Is(err, "COLLECTIBLE_NOT_FOUND") {
		t.Fatalf("non-collectible username err = %v, want COLLECTIBLE_NOT_FOUND", err)
	}
	// Syntactically impossible name is rejected before the registry is consulted.
	if _, err := f.router.onFragmentGetCollectibleInfo(ctx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectibleUsername{Username: "a"},
	}); !tgerr.Is(err, "COLLECTIBLE_INVALID") {
		t.Fatalf("malformed username err = %v, want COLLECTIBLE_INVALID", err)
	}
	// Collectible phones do not exist in this server.
	if _, err := f.router.onFragmentGetCollectibleInfo(ctx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectiblePhone{Phone: "15550002001"},
	}); !tgerr.Is(err, "COLLECTIBLE_NOT_FOUND") {
		t.Fatalf("collectible phone err = %v, want COLLECTIBLE_NOT_FOUND", err)
	}
}

func TestFragmentGetCollectibleInfoWithoutRegistry(t *testing.T) {
	f := newUsernameProjectionFixture(t, nil)
	ctx := WithUserID(context.Background(), f.owner.ID)

	if _, err := f.router.onFragmentGetCollectibleInfo(ctx, &tg.FragmentGetCollectibleInfoRequest{
		Collectible: &tg.InputCollectibleUsername{Username: "owner_slot"},
	}); !tgerr.Is(err, "COLLECTIBLE_NOT_FOUND") {
		t.Fatalf("no registry err = %v, want COLLECTIBLE_NOT_FOUND", err)
	}
}

func TestAccountToggleAndReorderUsernamesRPC(t *testing.T) {
	registry := newFakeUsernameRegistry()
	f := newUsernameProjectionFixture(t, registry)
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: f.owner.ID}
	registry.byPeer[peer] = []domain.Username{
		{Username: "owner_slot", Editable: true, Active: true},
		{Username: "alpha", Active: true, SortOrder: 0, CollectibleID: 1},
		{Username: "bravo", Active: true, SortOrder: 1, CollectibleID: 2},
	}
	ctx := WithUserID(context.Background(), f.owner.ID)

	if ok, err := f.router.onAccountToggleUsername(ctx, &tg.AccountToggleUsernameRequest{Username: "alpha", Active: false}); !ok || err != nil {
		t.Fatalf("toggle collectible = %v,%v, want true,nil", ok, err)
	}
	if list := registry.byPeer[peer]; list[1].Active {
		t.Fatalf("alpha still active after toggle: %+v", list)
	}
	// Toggling the same value again changes nothing.
	if ok, err := f.router.onAccountToggleUsername(ctx, &tg.AccountToggleUsernameRequest{Username: "alpha", Active: false}); ok || !tgerr.Is(err, "USERNAME_NOT_MODIFIED") {
		t.Fatalf("idempotent toggle = %v,%v, want false,USERNAME_NOT_MODIFIED", ok, err)
	}
	// The editable slot is not a collectible: account.updateUsername owns it.
	if ok, err := f.router.onAccountToggleUsername(ctx, &tg.AccountToggleUsernameRequest{Username: "owner_slot", Active: false}); ok || !tgerr.Is(err, "USERNAME_INVALID") {
		t.Fatalf("toggle editable slot = %v,%v, want false,USERNAME_INVALID", ok, err)
	}
	// An unknown name is not occupied.
	if ok, err := f.router.onAccountToggleUsername(ctx, &tg.AccountToggleUsernameRequest{Username: "ghost", Active: true}); ok || !tgerr.Is(err, "USERNAME_NOT_OCCUPIED") {
		t.Fatalf("toggle unknown = %v,%v, want false,USERNAME_NOT_OCCUPIED", ok, err)
	}

	// alpha is inactive at this point, so the order does not have to mention it.
	if ok, err := f.router.onAccountReorderUsernames(ctx, &tg.AccountReorderUsernamesRequest{Order: []string{"owner_slot", "bravo"}}); !ok || err != nil {
		t.Fatalf("reorder = %v,%v, want true,nil", ok, err)
	}
	list := domain.SortUsernames(registry.byPeer[peer])
	if len(list) != 3 || !list[0].Editable || list[1].Username != "bravo" || list[2].Username != "alpha" {
		t.Fatalf("order after reorder = %+v, want editable, bravo, alpha", list)
	}
	// The editable slot may lead the order or follow a collectible.
	if ok, err := f.router.onAccountReorderUsernames(ctx, &tg.AccountReorderUsernamesRequest{Order: []string{"bravo", "owner_slot"}}); !ok || err != nil {
		t.Fatalf("reorder with a collectible first = %v,%v, want true,nil", ok, err)
	}
	if list := domain.SortUsernames(registry.byPeer[peer]); list[0].Username != "bravo" || !list[1].Editable {
		t.Fatalf("order after promoting a collectible = %+v, want bravo, editable, alpha", list)
	}
	// An order that omits an active username is still rejected.
	if ok, err := f.router.onAccountReorderUsernames(ctx, &tg.AccountReorderUsernamesRequest{Order: []string{"bravo"}}); ok || !tgerr.Is(err, "ORDER_INVALID") {
		t.Fatalf("partial reorder = %v,%v, want false,ORDER_INVALID", ok, err)
	}
	// So is one naming something the peer does not own.
	if ok, err := f.router.onAccountReorderUsernames(ctx, &tg.AccountReorderUsernamesRequest{Order: []string{"owner_slot", "bravo", "ghost"}}); ok || !tgerr.Is(err, "ORDER_INVALID") {
		t.Fatalf("reorder with an unknown name = %v,%v, want false,ORDER_INVALID", ok, err)
	}
	if ok, err := f.router.onAccountReorderUsernames(ctx, &tg.AccountReorderUsernamesRequest{
		Order: make([]string, domain.MaxPeerCollectibleUsernames+2),
	}); ok || !tgerr.Is(err, "LIMIT_INVALID") {
		t.Fatalf("oversized reorder = %v,%v, want false,LIMIT_INVALID", ok, err)
	}
}

func TestAccountUsernameManagementWithoutRegistry(t *testing.T) {
	f := newUsernameProjectionFixture(t, nil)
	ctx := WithUserID(context.Background(), f.owner.ID)

	if ok, err := f.router.onAccountToggleUsername(ctx, &tg.AccountToggleUsernameRequest{Username: "owner_slot", Active: true}); ok || !tgerr.Is(err, "USERNAME_NOT_MODIFIED") {
		t.Fatalf("toggle without registry = %v,%v, want false,USERNAME_NOT_MODIFIED", ok, err)
	}
	if ok, err := f.router.onAccountReorderUsernames(ctx, &tg.AccountReorderUsernamesRequest{Order: []string{"owner_slot"}}); ok || !tgerr.Is(err, "USERNAME_NOT_MODIFIED") {
		t.Fatalf("reorder without registry = %v,%v, want false,USERNAME_NOT_MODIFIED", ok, err)
	}
}

// TestCollectibleUsernameRPCsAreRegistered proves the three previously missing
// methods reach a handler through the real dispatcher rather than the unregistered
// fallback: an ordinary client calls them by wire constructor, not by Go method.
func TestCollectibleUsernameRPCsAreRegistered(t *testing.T) {
	f := newUsernameProjectionFixture(t, nil)
	ctx := WithUserID(context.Background(), f.owner.ID)
	cases := []struct {
		name string
		req  bin.Encoder
		want string
	}{
		{name: "account.reorderUsernames", req: &tg.AccountReorderUsernamesRequest{Order: []string{"owner_slot"}}, want: "USERNAME_NOT_MODIFIED"},
		{name: "account.toggleUsername", req: &tg.AccountToggleUsernameRequest{Username: "owner_slot", Active: true}, want: "USERNAME_NOT_MODIFIED"},
		{name: "fragment.getCollectibleInfo", req: &tg.FragmentGetCollectibleInfoRequest{Collectible: &tg.InputCollectibleUsername{Username: "owner_slot"}}, want: "COLLECTIBLE_NOT_FOUND"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var in bin.Buffer
			if err := tt.req.Encode(&in); err != nil {
				t.Fatalf("encode request: %v", err)
			}
			if _, err := f.router.Dispatch(ctx, [8]byte{}, 0, &in); !tgerr.Is(err, tt.want) {
				t.Fatalf("dispatch err = %v, want %s", err, tt.want)
			}
		})
	}
}

func newCollectibleChannelFixture(t *testing.T, registry UsernameRegistryService) (*Router, domain.User, *tg.Channel) {
	t.Helper()
	ctx := context.Background()
	userStore := memory.NewUserStore()
	owner, err := userStore.Create(ctx, domain.User{AccessHash: 11, Phone: "15550003001", FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	channelStore := memory.NewChannelStore()
	r := New(Config{}, Deps{
		Users:     appusers.NewService(userStore),
		Channels:  appchannels.NewService(channelStore),
		Usernames: registry,
	}, zaptest.NewLogger(t), clock.System)
	ownerCtx := WithUserID(ctx, owner.ID)
	created, err := r.onChannelsCreateChannel(ownerCtx, &tg.ChannelsCreateChannelRequest{
		Broadcast: true,
		Title:     "Collectible Channel",
		About:     "about",
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channel := created.(*tg.Updates).Chats[0].(*tg.Channel)
	if _, err := r.onChannelsUpdateUsername(ownerCtx, &tg.ChannelsUpdateUsernameRequest{
		Channel:  &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
		Username: "chan_slot",
	}); err != nil {
		t.Fatalf("update channel username: %v", err)
	}
	return r, owner, channel
}

func TestChannelsUsernameManagementUsesRegistry(t *testing.T) {
	registry := newFakeUsernameRegistry()
	r, owner, channel := newCollectibleChannelFixture(t, registry)
	ctx := WithUserID(context.Background(), owner.ID)
	input := &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: channel.ID}
	registry.byPeer[peer] = []domain.Username{
		{Username: "chan_slot", Editable: true, Active: true},
		{Username: "alpha", Active: true, SortOrder: 0, CollectibleID: 31},
		{Username: "bravo", Active: true, SortOrder: 1, CollectibleID: 32},
	}

	if ok, err := r.onChannelsToggleUsername(ctx, &tg.ChannelsToggleUsernameRequest{Channel: input, Username: "alpha", Active: false}); !ok || err != nil {
		t.Fatalf("toggle channel collectible = %v,%v, want true,nil", ok, err)
	}
	if ok, err := r.onChannelsToggleUsername(ctx, &tg.ChannelsToggleUsernameRequest{Channel: input, Username: "alpha", Active: false}); ok || !tgerr.Is(err, "USERNAME_NOT_MODIFIED") {
		t.Fatalf("idempotent channel toggle = %v,%v, want false,USERNAME_NOT_MODIFIED", ok, err)
	}
	if ok, err := r.onChannelsToggleUsername(ctx, &tg.ChannelsToggleUsernameRequest{Channel: input, Username: "chan_slot", Active: false}); ok || !tgerr.Is(err, "USERNAME_INVALID") {
		t.Fatalf("toggle channel editable slot = %v,%v, want false,USERNAME_INVALID", ok, err)
	}
	// alpha was just deactivated, so the order carries only the active names.
	if ok, err := r.onChannelsReorderUsernames(ctx, &tg.ChannelsReorderUsernamesRequest{Channel: input, Order: []string{"chan_slot", "bravo"}}); !ok || err != nil {
		t.Fatalf("reorder channel usernames = %v,%v, want true,nil", ok, err)
	}
	if list := domain.SortUsernames(registry.byPeer[peer]); list[1].Username != "bravo" {
		t.Fatalf("channel order = %+v, want bravo first collectible", list)
	}
	if ok, err := r.onChannelsDeactivateAllUsernames(ctx, input); !ok || err != nil {
		t.Fatalf("deactivate all = %v,%v, want true,nil", ok, err)
	}
	for _, item := range registry.byPeer[peer] {
		if item.Collectible() && item.Active {
			t.Fatalf("collectible still active after deactivateAll: %+v", item)
		}
	}
	// Deactivate-all is idempotent, while reorder follows the documented
	// USERNAME_NOT_MODIFIED result for an unchanged order.
	if ok, err := r.onChannelsDeactivateAllUsernames(ctx, input); !ok || err != nil {
		t.Fatalf("repeat deactivate all = %v,%v, want true,nil", ok, err)
	}
	if ok, err := r.onChannelsReorderUsernames(ctx, &tg.ChannelsReorderUsernamesRequest{Channel: input, Order: []string{"chan_slot"}}); ok || !tgerr.Is(err, "USERNAME_NOT_MODIFIED") {
		t.Fatalf("repeat reorder after deactivateAll = %v,%v, want false,USERNAME_NOT_MODIFIED", ok, err)
	}

	// A non-admin must not reach the registry at all.
	other := WithUserID(context.Background(), owner.ID+9999)
	if _, err := r.onChannelsToggleUsername(other, &tg.ChannelsToggleUsernameRequest{Channel: input, Username: "alpha", Active: true}); err == nil {
		t.Fatalf("toggle as stranger err = nil, want permission failure")
	}
}

func TestChannelsUsernameManagementWithoutRegistryKeepsLegacyAnswers(t *testing.T) {
	r, owner, channel := newCollectibleChannelFixture(t, nil)
	ctx := WithUserID(context.Background(), owner.ID)
	input := &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}

	if ok, err := r.onChannelsToggleUsername(ctx, &tg.ChannelsToggleUsernameRequest{Channel: input, Username: "chan_slot", Active: true}); !ok || err != nil {
		t.Fatalf("toggle without registry = %v,%v, want true,nil", ok, err)
	}
	if ok, err := r.onChannelsReorderUsernames(ctx, &tg.ChannelsReorderUsernamesRequest{Channel: input, Order: []string{"chan_slot"}}); !ok || err != nil {
		t.Fatalf("reorder without registry = %v,%v, want true,nil", ok, err)
	}
	if ok, err := r.onChannelsDeactivateAllUsernames(ctx, input); !ok || err != nil {
		t.Fatalf("deactivate without registry = %v,%v, want true,nil", ok, err)
	}
}

// TestChannelsDeactivateAllUsernamesIsIdempotent covers the report "I created a
// channel without a username and later could not set one": Telegram Desktop
// drives its channel-username editor by calling channels.deactivateAllUsernames
// first and only continuing when it answers true. A channel that owns no
// collectible username at all -- every freshly created channel -- has nothing to
// deactivate, and answering USERNAME_NOT_MODIFIED there aborted the whole flow
// client-side. Deactivating an empty set is success.
func TestChannelsDeactivateAllUsernamesIsIdempotent(t *testing.T) {
	registry := newFakeUsernameRegistry()
	r, owner, channel := newCollectibleChannelFixture(t, registry)
	ctx := WithUserID(context.Background(), owner.ID)
	input := &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: channel.ID}

	// A channel with only its editable slot: no collectible username exists.
	registry.byPeer[peer] = []domain.Username{{Username: "chan_slot", Editable: true, Active: true}}
	for attempt := 0; attempt < 2; attempt++ {
		if ok, err := r.onChannelsDeactivateAllUsernames(ctx, input); !ok || err != nil {
			t.Fatalf("deactivate all with nothing collectible (attempt %d) = %v,%v, want true,nil", attempt, ok, err)
		}
	}
	// The whole visible list is just the editable slot, which is exactly what
	// Telegram Desktop sends here.
	if ok, err := r.onChannelsReorderUsernames(ctx, &tg.ChannelsReorderUsernamesRequest{Channel: input, Order: []string{"chan_slot"}}); ok || !tgerr.Is(err, "USERNAME_NOT_MODIFIED") {
		t.Fatalf("reorder with nothing collectible = %v,%v, want false,USERNAME_NOT_MODIFIED", ok, err)
	}
	// The editable slot is untouched by either call: only collectibles are hidden.
	if len(registry.byPeer[peer]) != 1 || !registry.byPeer[peer][0].Active {
		t.Fatalf("editable slot after bulk calls = %+v, want it left active", registry.byPeer[peer])
	}

	// A channel with no rows at all behaves the same way.
	delete(registry.byPeer, peer)
	if ok, err := r.onChannelsDeactivateAllUsernames(ctx, input); !ok || err != nil {
		t.Fatalf("deactivate all on an empty registry = %v,%v, want true,nil", ok, err)
	}

	// Permission is still checked before the registry is consulted.
	stranger := WithUserID(context.Background(), owner.ID+4242)
	if ok, err := r.onChannelsDeactivateAllUsernames(stranger, input); ok || err == nil {
		t.Fatalf("deactivate all as stranger = %v,%v, want false and a permission failure", ok, err)
	}
}

func TestChannelsGetChannelsProjectsCollectibleUsernames(t *testing.T) {
	registry := newFakeUsernameRegistry()
	r, owner, channel := newCollectibleChannelFixture(t, registry)
	ctx := WithUserID(context.Background(), owner.ID)
	registry.byPeer[domain.Peer{Type: domain.PeerTypeChannel, ID: channel.ID}] = []domain.Username{
		{Username: "chan_slot", Editable: true, Active: true, SortOrder: 1},
		{Username: "chan_nft", Active: true, SortOrder: 0, CollectibleID: 44},
	}

	chats, err := r.onChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
	})
	if err != nil {
		t.Fatalf("get channels: %v", err)
	}
	out := chats.(*tg.MessagesChats).Chats
	if len(out) != 1 {
		t.Fatalf("chats = %d, want 1", len(out))
	}
	assertVectorOnlyUsernames(t, "channel", out[0].(*tg.Channel), []string{"chan_nft", "chan_slot"})
	for _, profile := range []tlprofile.Profile{
		tlprofile.Profile225,
		tlprofile.Profile226,
		tlprofile.Profile227,
		tlprofile.Profile228,
	} {
		stage := fmt.Sprintf("Layer %d channel", profile)
		var wire bin.Buffer
		if err := tlprofile.EncodeObject(profile, chats, &wire); err != nil {
			t.Fatalf("encode %s: %v", stage, err)
		}
		decoded, err := tlprofile.DecodeObject(profile, &bin.Buffer{Buf: wire.Copy()}, tlprofile.Limits{})
		if err != nil {
			t.Fatalf("decode %s: %v", stage, err)
		}
		decodedChats, ok := decoded.(*tg.MessagesChats)
		if !ok || len(decodedChats.Chats) != 1 {
			t.Fatalf("decoded %s = %T %+v", stage, decoded, decoded)
		}
		assertVectorOnlyUsernames(t, stage, decodedChats.Chats[0].(*tg.Channel), []string{"chan_nft", "chan_slot"})
	}
}

func TestChannelsGetChannelsDegradesWithoutRegistry(t *testing.T) {
	r, owner, channel := newCollectibleChannelFixture(t, nil)
	ctx := WithUserID(context.Background(), owner.ID)

	chats, err := r.onChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
	})
	if err != nil {
		t.Fatalf("get channels: %v", err)
	}
	assertScalarOnlyUsername(t, "channel without registry", chats.(*tg.MessagesChats).Chats[0].(*tg.Channel), "chan_slot")
}
