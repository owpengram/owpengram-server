package postgres

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func peerIdentityHash(t *testing.T, peer domain.Peer) int64 {
	t.Helper()
	pool := testPool(t)
	var hash int64
	if err := pool.QueryRow(context.Background(), `
SELECT hash
FROM read_model_versions
WHERE model = 'peer_identity'
  AND owner_user_id = 0
  AND peer_type = $1
  AND peer_id = $2`, string(peer.Type), peer.ID).Scan(&hash); err != nil {
		t.Fatalf("read peer identity hash for %+v: %v", peer, err)
	}
	return hash
}

func TestPeerIdentityReadModelTokenCreatedWithPeer(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	user, err := NewUserStore(pool).Create(ctx, domain.User{
		AccessHash: 991, Phone: "+1998" + suffix + "01", FirstName: "IdentitySeed",
	})
	if err != nil {
		t.Fatal(err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	if hash := peerIdentityHash(t, domain.Peer{Type: domain.PeerTypeUser, ID: user.ID}); hash == 0 {
		t.Fatal("new user peer_identity hash is zero")
	}
	created, err := NewChannelStore(pool).CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: user.ID, Title: "Identity Seed " + suffix, Megagroup: true, Date: 1701000300,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID = created.Channel.ID
	if hash := peerIdentityHash(t, domain.Peer{Type: domain.PeerTypeChannel, ID: channelID}); hash == 0 {
		t.Fatal("new channel peer_identity hash is zero")
	}
}

func TestPeerIdentityReadModelBumpsForUsernameRegistryMutation(t *testing.T) {
	pool := testPool(t)
	seed := time.Now().UnixNano() & 0x3fffffff
	peer := collectibleTestUser(t, pool, 6_100_000_000+seed, "")
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM read_model_versions
WHERE model='peer_identity' AND owner_user_id=0 AND peer_type=$1 AND peer_id=$2`, peer.Type, peer.ID)

	setEditableUsername(t, pool, peer, "identityone")
	first := peerIdentityHash(t, peer)
	if _, err := pool.Exec(ctx, `UPDATE peer_usernames
SET active=false, updated_at=now()
WHERE peer_type=$1 AND peer_id=$2`, peer.Type, peer.ID); err != nil {
		t.Fatalf("update peer username: %v", err)
	}
	second := peerIdentityHash(t, peer)
	if first == second {
		t.Fatalf("peer identity hash did not change on username update: %d", first)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM peer_usernames WHERE peer_type=$1 AND peer_id=$2`, peer.Type, peer.ID); err != nil {
		t.Fatalf("delete peer username: %v", err)
	}
	third := peerIdentityHash(t, peer)
	if second == third {
		t.Fatalf("peer identity hash did not change on username delete: %d", second)
	}
}

func TestPeerIdentityReadModelBumpsForCustomVerificationMutation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	verifierID := botVerificationTestUser(t, pool)
	targetID := botVerificationTestUser(t, pool)
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: targetID}
	store := NewBotVerificationStore(pool)
	icon := botVerificationTestIcon(t, pool, store, "peer identity", verifierID)
	botVerificationTestVerifier(t, store, verifierID, icon.DocumentID)
	_, _ = pool.Exec(ctx, `DELETE FROM read_model_versions
WHERE model='peer_identity' AND owner_user_id=0 AND peer_type=$1 AND peer_id=$2`, peer.Type, peer.ID)

	if _, _, err := store.GrantCustomVerification(ctx, domain.CustomVerification{
		VerifierBotID:  verifierID,
		Peer:           peer,
		IconDocumentID: icon.DocumentID,
		Description:    "first",
	}); err != nil {
		t.Fatalf("grant custom verification: %v", err)
	}
	first := peerIdentityHash(t, peer)
	if _, _, err := store.GrantCustomVerification(ctx, domain.CustomVerification{
		VerifierBotID:  verifierID,
		Peer:           peer,
		IconDocumentID: icon.DocumentID,
		Description:    "second",
	}); err != nil {
		t.Fatalf("update custom verification: %v", err)
	}
	second := peerIdentityHash(t, peer)
	if first == second {
		t.Fatalf("peer identity hash did not change on verification update: %d", first)
	}
	if changed, err := store.RevokeCustomVerification(ctx, verifierID, peer); err != nil || !changed {
		t.Fatalf("revoke custom verification: changed=%v err=%v", changed, err)
	}
	third := peerIdentityHash(t, peer)
	if second == third {
		t.Fatalf("peer identity hash did not change on verification delete: %d", second)
	}
}
