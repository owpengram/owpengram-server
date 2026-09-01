package rpc

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func peerIdentityVersionKey(peer domain.Peer) store.ReadModelKey {
	return store.ReadModelKey{Model: peerIdentityReadModel, PeerType: peer.Type, PeerID: peer.ID}
}

func TestPeerUsernameIdentityCacheVersionsPositiveAndNegativeResults(t *testing.T) {
	positive := domain.Peer{Type: domain.PeerTypeUser, ID: 11}
	negative := domain.Peer{Type: domain.PeerTypeChannel, ID: 22}
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{
		peerIdentityVersionKey(positive): 101,
		peerIdentityVersionKey(negative): 202,
	}}
	registry := newFakeUsernameRegistry()
	registry.byPeer[positive] = []domain.Username{{Username: "first", Active: true, CollectibleID: 1}}
	r := &Router{
		deps:              Deps{Usernames: registry, ReadModelVersions: versions},
		peerIdentityCache: newPeerIdentityCache(8),
	}

	first := r.usernameRegistryMap(context.Background(), []domain.Peer{positive, negative})
	if registry.batchCalls != 1 || len(first[positive]) != 1 {
		t.Fatalf("first load calls=%d result=%+v", registry.batchCalls, first)
	}
	first[positive][0].Username = "caller-mutated"
	second := r.usernameRegistryMap(context.Background(), []domain.Peer{positive, negative})
	if registry.batchCalls != 1 {
		t.Fatalf("positive/negative cache miss: batch calls=%d, want 1", registry.batchCalls)
	}
	if got := second[positive][0].Username; got != "first" {
		t.Fatalf("cached username aliased caller mutation: got %q", got)
	}
	if _, ok := second[negative]; ok {
		t.Fatalf("negative peer unexpectedly projected: %+v", second[negative])
	}

	registry.byPeer[positive] = []domain.Username{{Username: "second", Active: true, CollectibleID: 2}}
	versions.hashes[peerIdentityVersionKey(positive)] = 303
	third := r.usernameRegistryMap(context.Background(), []domain.Peer{positive, negative})
	if registry.batchCalls != 1 || registry.peerCalls != 1 || len(third[positive]) != 1 || third[positive][0].Username != "second" {
		t.Fatalf("version advance did not reload: batch=%d peer=%d result=%+v", registry.batchCalls, registry.peerCalls, third)
	}
}

func TestPeerVerificationIdentityCacheVersionsPositiveAndNegativeResults(t *testing.T) {
	positive := domain.Peer{Type: domain.PeerTypeChannel, ID: 31}
	negative := domain.Peer{Type: domain.PeerTypeUser, ID: 32}
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{
		peerIdentityVersionKey(positive): 401,
		peerIdentityVersionKey(negative): 402,
	}}
	verifications := newFakeBotVerifications()
	verifications.marks[positive] = domain.CustomVerification{Peer: positive, IconDocumentID: 9001}
	r := &Router{
		deps:              Deps{BotVerifications: verifications, ReadModelVersions: versions},
		peerIdentityCache: newPeerIdentityCache(8),
	}

	first := r.botVerificationMap(context.Background(), []domain.Peer{positive, negative})
	if verifications.batchCalls != 1 || first[positive].IconDocumentID != 9001 {
		t.Fatalf("first load calls=%d result=%+v", verifications.batchCalls, first)
	}
	second := r.botVerificationMap(context.Background(), []domain.Peer{positive, negative})
	if verifications.batchCalls != 1 || len(second) != 1 {
		t.Fatalf("positive/negative cache miss: calls=%d result=%+v", verifications.batchCalls, second)
	}

	delete(verifications.marks, positive)
	versions.hashes[peerIdentityVersionKey(positive)] = 403
	third := r.botVerificationMap(context.Background(), []domain.Peer{positive, negative})
	if verifications.batchCalls != 1 || verifications.peerCalls != 1 || len(third) != 0 {
		t.Fatalf("version advance did not reload removal: batch=%d peer=%d result=%+v", verifications.batchCalls, verifications.peerCalls, third)
	}
}

func TestPeerIdentityCachesDoNotTurnReadErrorsIntoNegativeEntries(t *testing.T) {
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 51}
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{
		peerIdentityVersionKey(peer): 501,
	}}
	registry := newFakeUsernameRegistry()
	registry.err = errors.New("username backend unavailable")
	verifications := newFakeBotVerifications()
	verifications.err = errors.New("verification backend unavailable")
	r := &Router{
		deps: Deps{
			Usernames:         registry,
			BotVerifications:  verifications,
			ReadModelVersions: versions,
		},
		peerIdentityCache: newPeerIdentityCache(8),
	}

	if got := r.usernameRegistryMap(context.Background(), []domain.Peer{peer}); len(got) != 0 {
		t.Fatalf("username error result = %+v, want empty overlay", got)
	}
	if got := r.botVerificationMap(context.Background(), []domain.Peer{peer}); len(got) != 0 {
		t.Fatalf("verification error result = %+v, want empty overlay", got)
	}
	registry.err = nil
	registry.byPeer[peer] = []domain.Username{{Username: "recovered", Active: true, CollectibleID: 7}}
	verifications.err = nil
	verifications.marks[peer] = domain.CustomVerification{Peer: peer, IconDocumentID: 9007}

	if got := r.usernameRegistryMap(context.Background(), []domain.Peer{peer}); len(got[peer]) != 1 || got[peer][0].Username != "recovered" {
		t.Fatalf("username backend recovery hidden by negative cache: %+v", got)
	}
	if got := r.botVerificationMap(context.Background(), []domain.Peer{peer}); got[peer].IconDocumentID != 9007 {
		t.Fatalf("verification backend recovery hidden by negative cache: %+v", got)
	}
	if registry.peerCalls != 3 || verifications.peerCalls != 3 {
		t.Fatalf("backend recovery did not retry: usernames=%d verifications=%d", registry.peerCalls, verifications.peerCalls)
	}
}

func TestPeerIdentityCacheCombinesFacetsAndIgnoresBaseProjectionInvalidation(t *testing.T) {
	userPeer := domain.Peer{Type: domain.PeerTypeUser, ID: 61}
	channelPeer := domain.Peer{Type: domain.PeerTypeChannel, ID: 62}
	peers := []domain.Peer{userPeer, channelPeer}
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{
		peerIdentityVersionKey(userPeer):    601,
		peerIdentityVersionKey(channelPeer): 602,
	}}
	registry := newFakeUsernameRegistry()
	registry.byPeer[userPeer] = []domain.Username{{Username: "stable", Active: true}}
	verifications := newFakeBotVerifications()
	verifications.marks[channelPeer] = domain.CustomVerification{Peer: channelPeer, IconDocumentID: 9062}
	r := &Router{
		deps: Deps{
			Usernames:         registry,
			BotVerifications:  verifications,
			ReadModelVersions: versions,
		},
		peerIdentityCache: newPeerIdentityCache(8),
	}

	usernames, marks := r.peerIdentityMaps(context.Background(), peers, true, true)
	if registry.batchCalls != 1 || verifications.batchCalls != 1 || usernames[userPeer][0].Username != "stable" || marks[channelPeer].IconDocumentID != 9062 {
		t.Fatalf("combined load registry=%d verification=%d usernames=%+v marks=%+v", registry.batchCalls, verifications.batchCalls, usernames, marks)
	}
	registry.byPeer[userPeer] = []domain.Username{{Username: "must-not-leak", Active: true}}
	delete(verifications.marks, channelPeer)
	r.InvalidateRPCProjectionReadModelForUser(userPeer.ID)
	r.InvalidateRPCProjectionReadModelForChannel(channelPeer.ID)
	usernames, marks = r.peerIdentityMaps(context.Background(), peers, true, true)
	if registry.batchCalls != 1 || verifications.batchCalls != 1 || usernames[userPeer][0].Username != "stable" || marks[channelPeer].IconDocumentID != 9062 {
		t.Fatalf("base invalidation evicted identity registry=%d verification=%d usernames=%+v marks=%+v", registry.batchCalls, verifications.batchCalls, usernames, marks)
	}

	versions.hashes[peerIdentityVersionKey(userPeer)] = 603
	r.InvalidatePeerIdentityReadModel(userPeer)
	usernames = r.usernameRegistryMap(context.Background(), peers)
	if registry.peerCalls != 1 || usernames[userPeer][0].Username != "must-not-leak" {
		t.Fatalf("exact identity invalidation did not reload user: peerCalls=%d usernames=%+v", registry.peerCalls, usernames)
	}
}
