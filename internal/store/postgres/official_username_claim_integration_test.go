package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestClaimOfficialUsernameDisplacesOrdinaryUserAtomically(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	users := NewUserStore(pool)

	officialBefore, found, err := users.ByID(ctx, domain.OfficialSystemUserID)
	if err != nil || !found {
		t.Fatalf("load official user: found=%v err=%v", found, err)
	}
	suffix := time.Now().UnixNano()
	target := fmt.Sprintf("brand_%d", suffix)
	holder := createTestUser(t, ctx, users, fmt.Sprintf("+18881%d", suffix), "Brand", "Holder")
	t.Cleanup(func() {
		_, _ = users.ClaimOfficialUsername(ctx, officialBefore.Username)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, holder.ID)
	})
	if _, err := users.UpdateUsername(ctx, holder.ID, target); err != nil {
		t.Fatalf("occupy target username: %v", err)
	}

	claim, err := users.ClaimOfficialUsername(ctx, target)
	if err != nil {
		t.Fatalf("claim official username: %v", err)
	}
	if !claim.Changed || claim.DisplacedUserID != holder.ID || claim.Official.ID != domain.OfficialSystemUserID || claim.Official.Username != target {
		t.Fatalf("claim = %+v, want changed claim displacing %d", claim, holder.ID)
	}
	displaced, found, err := users.ByID(ctx, holder.ID)
	if err != nil || !found || displaced.Username != "" {
		t.Fatalf("displaced user = %+v found=%v err=%v, want empty username", displaced, found, err)
	}
	resolved, found, err := users.ByUsername(ctx, target)
	if err != nil || !found || resolved.ID != domain.OfficialSystemUserID {
		t.Fatalf("resolve claimed username = %+v found=%v err=%v", resolved, found, err)
	}
	owner, found, err := getPeerUsernameOwner(ctx, pool, target, false)
	if err != nil || !found || !owner.matches(peerUsernameTypeUser, domain.OfficialSystemUserID) || !owner.editable || owner.collectible {
		t.Fatalf("registry owner = %+v found=%v err=%v", owner, found, err)
	}

	again, err := users.ClaimOfficialUsername(ctx, target)
	if err != nil {
		t.Fatalf("repeat official username claim: %v", err)
	}
	if again.Changed || again.DisplacedUserID != 0 || again.Official.Username != target {
		t.Fatalf("repeat claim = %+v, want idempotent no-op", again)
	}
}

func TestClaimOfficialUsernameDoesNotDisplaceBot(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	users := NewUserStore(pool)
	suffix := time.Now().UnixNano()
	target := fmt.Sprintf("botbrand_%d", suffix)
	holder := createTestUser(t, ctx, users, fmt.Sprintf("+18882%d", suffix), "Protected", "Bot")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, holder.ID)
	})
	if _, err := users.UpdateUsername(ctx, holder.ID, target); err != nil {
		t.Fatalf("occupy target username: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET is_bot = true WHERE id = $1`, holder.ID); err != nil {
		t.Fatalf("mark protected holder as bot: %v", err)
	}

	if _, err := users.ClaimOfficialUsername(ctx, target); !errors.Is(err, domain.ErrUsernameOccupied) {
		t.Fatalf("claim bot username error = %v, want ErrUsernameOccupied", err)
	}
	protected, found, err := users.ByID(ctx, holder.ID)
	if err != nil || !found || protected.Username != target {
		t.Fatalf("protected holder = %+v found=%v err=%v, want username unchanged", protected, found, err)
	}
}
