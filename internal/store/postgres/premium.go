package postgres

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

var errPremiumAlreadyPaid = errors.New("premium: already paid")

// premiumPlanCatalogLockID serializes concurrent plan edits so the "at least
// one enabled plan" invariant can't be broken by two operators disabling
// different rows in the same instant.
const premiumPlanCatalogLockID int64 = 0x7072656d706c616e

type PremiumStore struct {
	db    sqlcgen.DBTX
	botID int64
}

func NewPremiumStore(db sqlcgen.DBTX) *PremiumStore {
	return &PremiumStore{db: db, botID: domain.PremiumBotUserID}
}

func (s *PremiumStore) Plans(ctx context.Context) ([]domain.PremiumPlan, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT months,duration_days,amount_stars,enabled,sort_order,label,managed_by,version,
EXTRACT(EPOCH FROM updated_at)::bigint
FROM premium_plans ORDER BY sort_order,months`)
	if err != nil {
		return nil, fmt.Errorf("list premium plans: %w", err)
	}
	defer rows.Close()
	out := make([]domain.PremiumPlan, 0)
	for rows.Next() {
		var plan domain.PremiumPlan
		var updated int64
		if err := rows.Scan(&plan.Months, &plan.DurationDays, &plan.AmountStars, &plan.Enabled,
			&plan.SortOrder, &plan.Label, &plan.ManagedBy, &plan.Version, &updated); err != nil {
			return nil, fmt.Errorf("scan premium plan: %w", err)
		}
		plan.UpdatedAt = int(updated)
		out = append(out, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate premium plans: %w", err)
	}
	return out, nil
}

func (s *PremiumStore) Plan(ctx context.Context, months int) (domain.PremiumPlan, bool, error) {
	if s == nil || s.db == nil || months <= 0 {
		return domain.PremiumPlan{}, false, nil
	}
	var plan domain.PremiumPlan
	var updated int64
	err := s.db.QueryRow(ctx, `SELECT months,duration_days,amount_stars,enabled,sort_order,label,managed_by,version,
EXTRACT(EPOCH FROM updated_at)::bigint FROM premium_plans WHERE months=$1`, months).
		Scan(&plan.Months, &plan.DurationDays, &plan.AmountStars, &plan.Enabled,
			&plan.SortOrder, &plan.Label, &plan.ManagedBy, &plan.Version, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PremiumPlan{}, false, nil
	}
	if err != nil {
		return domain.PremiumPlan{}, false, fmt.Errorf("get premium plan: %w", err)
	}
	plan.UpdatedAt = int(updated)
	return plan, true, nil
}

// UpsertPremiumPlan creates or edits one month option. ExpectedVersion=0
// creates a new row; a positive value updates that exact revision, so a
// stale edit (someone else changed the plan first) is refused rather than
// silently overwritten. Disabling the last enabled plan is refused: the
// storefront must always have at least one purchasable option.
func (s *PremiumStore) UpsertPremiumPlan(ctx context.Context, req domain.PremiumPlanUpsertRequest) (domain.PremiumPlan, error) {
	if s == nil || s.db == nil || !req.Valid() {
		return domain.PremiumPlan{}, domain.ErrPremiumPlanInvalid
	}
	var out domain.PremiumPlan
	err := withTx(ctx, s.db, "upsert premium plan", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, premiumPlanCatalogLockID); err != nil {
			return fmt.Errorf("lock premium plan catalog: %w", err)
		}
		var existingVersion int64
		var existingEnabled bool
		err := tx.QueryRow(ctx, `SELECT version,enabled FROM premium_plans WHERE months=$1 FOR UPDATE`, req.Months).
			Scan(&existingVersion, &existingEnabled)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock premium plan: %w", err)
		}
		var updated int64
		if found {
			if req.ExpectedVersion == 0 || existingVersion != req.ExpectedVersion {
				return domain.ErrPremiumPlanConflict
			}
			if !req.Enabled && existingEnabled {
				var enabledElsewhere int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM premium_plans WHERE enabled AND months<>$1`,
					req.Months).Scan(&enabledElsewhere); err != nil {
					return err
				}
				if enabledElsewhere == 0 {
					return domain.ErrPremiumLastPlan
				}
			}
			err = tx.QueryRow(ctx, `UPDATE premium_plans SET
duration_days=$2,amount_stars=$3,enabled=$4,sort_order=$5,label=$6,managed_by='admin',
version=version+1,updated_at=now()
WHERE months=$1 AND version=$7
RETURNING months,duration_days,amount_stars,enabled,sort_order,label,managed_by,version,
EXTRACT(EPOCH FROM updated_at)::bigint`,
				req.Months, req.DurationDays, req.AmountStars, req.Enabled, req.SortOrder, req.Label, req.ExpectedVersion).
				Scan(&out.Months, &out.DurationDays, &out.AmountStars, &out.Enabled,
					&out.SortOrder, &out.Label, &out.ManagedBy, &out.Version, &updated)
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrPremiumPlanConflict
			}
		} else {
			if req.ExpectedVersion != 0 {
				return domain.ErrPremiumPlanUnavailable
			}
			err = tx.QueryRow(ctx, `INSERT INTO premium_plans
(months,duration_days,amount_stars,enabled,sort_order,label,managed_by,version,updated_at)
VALUES($1,$2,$3,$4,$5,$6,'admin',1,now())
RETURNING months,duration_days,amount_stars,enabled,sort_order,label,managed_by,version,
EXTRACT(EPOCH FROM updated_at)::bigint`,
				req.Months, req.DurationDays, req.AmountStars, req.Enabled, req.SortOrder, req.Label).
				Scan(&out.Months, &out.DurationDays, &out.AmountStars, &out.Enabled,
					&out.SortOrder, &out.Label, &out.ManagedBy, &out.Version, &updated)
		}
		if err != nil {
			return fmt.Errorf("write premium plan: %w", err)
		}
		out.UpdatedAt = int(updated)
		var enabledPlans int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM premium_plans WHERE enabled`).Scan(&enabledPlans); err != nil {
			return err
		}
		if enabledPlans == 0 {
			return domain.ErrPremiumLastPlan
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.PremiumPlan{}, domain.ErrPremiumPlanConflict
		}
		return domain.PremiumPlan{}, err
	}
	return out, nil
}

func (s *PremiumStore) IssuePremiumPaymentForm(ctx context.Context, form domain.PremiumPaymentForm) (domain.PremiumPaymentForm, error) {
	if form.Message.Entities == nil {
		form.Message.Entities = []domain.MessageEntity{}
	}
	if s == nil || s.db == nil || !form.Valid() {
		return domain.PremiumPaymentForm{}, domain.ErrPremiumFormInvalid
	}
	entities, err := json.Marshal(form.Message.Entities)
	if err != nil {
		return domain.PremiumPaymentForm{}, domain.ErrPremiumGiftMessageInvalid
	}
	for attempt := 0; attempt < 8; attempt++ {
		form.ID, err = newPremiumPaymentFormID()
		if err != nil {
			return domain.PremiumPaymentForm{}, err
		}
		idempotencyKey := form.IdempotencyKey
		if idempotencyKey == "" {
			idempotencyKey = fmt.Sprintf("premium-form:%d:%d", form.BuyerUserID, form.ID)
		}
		_, err = s.db.Exec(ctx, `INSERT INTO premium_payment_intents
(form_id,idempotency_key,buyer_user_id,purchase_kind,recipient_user_id,months,duration_days,amount_stars,
plan_version,gift_message,gift_entities,status,issued_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending',to_timestamp($12),to_timestamp($13))`,
			form.ID, idempotencyKey,
			form.BuyerUserID, string(form.Kind), form.RecipientUserID, form.Months,
			form.DurationDays, form.AmountStars, form.PlanVersion, form.Message.Text, entities,
			form.IssuedAt, form.ExpiresAt)
		if err == nil {
			form.IdempotencyKey = idempotencyKey
			return form, nil
		}
		if !isUniqueViolation(err) {
			return domain.PremiumPaymentForm{}, fmt.Errorf("insert premium form: %w", err)
		}
		form.ID = 0
	}
	return domain.PremiumPaymentForm{}, domain.ErrPremiumPlanUnavailable
}

func newPremiumPaymentFormID() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("generate premium form id: %w", err)
	}
	id := int64(binary.LittleEndian.Uint64(raw[:]) & 0x7fffffffffffffff)
	if id == 0 {
		id = 1
	}
	return id, nil
}

func (s *PremiumStore) PurchasePremium(ctx context.Context, req domain.PremiumPurchaseRequest) (domain.PremiumPurchaseResult, error) {
	if s == nil || s.db == nil || req.BuyerUserID <= 0 || req.FormID <= 0 ||
		req.RecipientUserID <= 0 || req.Months <= 0 || req.Date <= 0 || !req.Message.Valid() {
		return domain.PremiumPurchaseResult{}, domain.ErrPremiumFormInvalid
	}
	if replay, found, err := s.loadPremiumPurchaseReplay(ctx, req); err != nil || found {
		return replay, err
	}
	var result domain.PremiumPurchaseResult
	err := withTx(ctx, s.db, "purchase premium", func(tx pgx.Tx) error {
		form, entitlement, user, balance, err := s.settlePremiumPayment(ctx, tx, req)
		if err != nil {
			return err
		}
		result = domain.PremiumPurchaseResult{Form: form, Entitlement: entitlement, User: user, Balance: balance}
		return nil
	})
	if err != nil {
		if errors.Is(err, errPremiumAlreadyPaid) || isUniqueViolation(err) {
			if replay, found, replayErr := s.loadPremiumPurchaseReplay(ctx, req); replayErr != nil || found {
				result.Duplicate = true
				return replay, replayErr
			}
		}
		if errors.Is(err, domain.ErrPremiumFormExpired) {
			_, _ = s.db.Exec(ctx, `UPDATE premium_payment_intents
SET status='expired',updated_at=now()
WHERE buyer_user_id=$1 AND form_id=$2 AND status='pending' AND expires_at<=to_timestamp($3)`,
				req.BuyerUserID, req.FormID, req.Date)
		}
		return domain.PremiumPurchaseResult{}, err
	}
	return result, nil
}

func (s *PremiumStore) settlePremiumPayment(ctx context.Context, tx pgx.Tx, req domain.PremiumPurchaseRequest) (
	domain.PremiumPaymentForm, domain.PremiumEntitlement, domain.User, domain.StarsBalance, error,
) {
	form, status, intentID, err := loadPremiumPaymentForm(ctx, tx, req.BuyerUserID, req.FormID, true)
	if err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, err
	}
	if status == domain.PremiumPaymentPaid {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, errPremiumAlreadyPaid
	}
	if status != domain.PremiumPaymentPending || form.ExpiresAt <= req.Date {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumFormExpired
	}
	if !premiumRequestMatchesForm(req, form) {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumFormInvalid
	}
	var currentPlan domain.PremiumPlan
	if err := tx.QueryRow(ctx, `SELECT months,duration_days,amount_stars,enabled,version
FROM premium_plans WHERE months=$1 FOR SHARE`, form.Months).
		Scan(&currentPlan.Months, &currentPlan.DurationDays, &currentPlan.AmountStars,
			&currentPlan.Enabled, &currentPlan.Version); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumPlanUnavailable
	}
	if !currentPlan.Enabled {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumPlanUnavailable
	}
	if currentPlan.Version != form.PlanVersion || currentPlan.DurationDays != form.DurationDays ||
		currentPlan.AmountStars != form.AmountStars {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumFormAmountChanged
	}
	if err := lockUsersForUpdate(ctx, tx, form.BuyerUserID, form.RecipientUserID); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, err
	}

	var isBot bool
	var deletedAt, premiumUntil *time.Time
	if err := tx.QueryRow(ctx, `SELECT is_bot,deleted_at,premium_expires_at FROM users
WHERE id=$1 FOR UPDATE`, form.RecipientUserID).Scan(&isBot, &deletedAt, &premiumUntil); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumRecipientInvalid
	}
	if isBot || deletedAt != nil || domain.IsSystemUserID(form.RecipientUserID) || form.RecipientUserID == s.botID {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumRecipientInvalid
	}
	var disallowPremiumGifts bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((
SELECT disallow_premium_gifts FROM account_settings WHERE user_id=$1
),false)`, form.RecipientUserID).Scan(&disallowPremiumGifts); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, err
	}
	if form.Kind == domain.PremiumPurchaseGift && disallowPremiumGifts {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumRecipientRestricted
	}
	if form.Kind == domain.PremiumPurchaseGift && form.RecipientUserID == form.BuyerUserID {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumGiftSelf
	}
	if form.Kind == domain.PremiumPurchaseGift && premiumUntil != nil && int(premiumUntil.Unix()) > req.Date {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{},
			domain.PremiumSubscriptionActiveError{Until: int(premiumUntil.Unix())}
	}

	var balance int64
	var granted bool
	err = tx.QueryRow(ctx, `SELECT balance,granted FROM stars_balances WHERE user_id=$1 FOR UPDATE`,
		form.BuyerUserID).Scan(&balance, &granted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && balance < form.AmountStars) {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrStarsInsufficient
	}
	if err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, fmt.Errorf("load premium buyer balance: %w", err)
	}
	var starsTransactionID int64
	if err := tx.QueryRow(ctx, `UPDATE stars_balances SET balance=balance-$2,updated_at=now()
WHERE user_id=$1 RETURNING balance`, form.BuyerUserID, form.AmountStars).Scan(&balance); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, fmt.Errorf("debit premium stars: %w", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO stars_transactions
(user_id,peer_type,peer_id,amount,reason,title,description,date,premium_payment_intent_id,
premium_recipient_user_id,premium_months)
VALUES($1,'user',$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		form.BuyerUserID, s.botID, -form.AmountStars, string(domain.StarsReasonPremium),
		"Telegram Premium", domain.PremiumPurchaseDescription(form.Kind, form.Months, form.RecipientUserID),
		req.Date, intentID, form.RecipientUserID, form.Months).Scan(&starsTransactionID); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, fmt.Errorf("insert premium stars transaction: %w", err)
	}

	startsAt := req.Date
	if premiumUntil != nil && int(premiumUntil.Unix()) > startsAt {
		startsAt = int(premiumUntil.Unix())
	}
	expiresAt := int(time.Unix(int64(startsAt), 0).Add(time.Duration(form.DurationDays) * 24 * time.Hour).Unix())
	source := domain.PremiumEntitlementPurchase
	if form.Kind == domain.PremiumPurchaseGift {
		source = domain.PremiumEntitlementGift
	}
	entitlement := domain.PremiumEntitlement{
		UserID: form.RecipientUserID, Source: source, SourceUserID: form.BuyerUserID,
		PaymentIntentID: intentID, TransactionID: starsTransactionID,
		Months: form.Months, DurationDays: form.DurationDays,
		StartsAt: startsAt, ExpiresAt: expiresAt, Status: domain.PremiumEntitlementActive, CreatedAt: req.Date,
	}
	if err := tx.QueryRow(ctx, `INSERT INTO premium_entitlements
(user_id,source,source_user_id,payment_intent_id,transaction_id,months,duration_days,starts_at,expires_at,status)
VALUES($1,$2,$3,$4,$5,$6,$7,to_timestamp($8),to_timestamp($9),'active') RETURNING id`,
		entitlement.UserID, string(entitlement.Source), entitlement.SourceUserID, intentID,
		starsTransactionID, entitlement.Months, entitlement.DurationDays, startsAt, expiresAt).
		Scan(&entitlement.ID); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, fmt.Errorf("insert premium entitlement: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET premium_expires_at=to_timestamp($2),
premium_updated_at=to_timestamp($3),updated_at=now()
WHERE id=$1`, form.RecipientUserID, expiresAt, req.Date); err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, fmt.Errorf("update premium aggregate: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE premium_payment_intents SET status='paid',paid_at=to_timestamp($2),
stars_transaction_id=$3,updated_at=now() WHERE id=$1 AND status='pending'`,
		intentID, req.Date, starsTransactionID)
	if err != nil {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, errPremiumAlreadyPaid
	}
	user, found, err := NewUserStore(tx).ByID(ctx, form.RecipientUserID)
	if err != nil || !found {
		if err != nil {
			return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, err
		}
		return domain.PremiumPaymentForm{}, domain.PremiumEntitlement{}, domain.User{}, domain.StarsBalance{}, domain.ErrPremiumRecipientInvalid
	}
	return form, entitlement, user, domain.StarsBalance{
		UserID: form.BuyerUserID, Balance: balance, Granted: granted,
	}, nil
}

func loadPremiumPaymentForm(ctx context.Context, db sqlcgen.DBTX, buyerID, formID int64, lock bool) (
	domain.PremiumPaymentForm, domain.PremiumPaymentStatus, int64, error,
) {
	query := `SELECT id,purchase_kind,recipient_user_id,months,duration_days,amount_stars,plan_version,
gift_message,gift_entities,EXTRACT(EPOCH FROM issued_at)::bigint,EXTRACT(EPOCH FROM expires_at)::bigint,status
FROM premium_payment_intents WHERE buyer_user_id=$1 AND form_id=$2`
	if lock {
		query += " FOR UPDATE"
	}
	var (
		intentID          int64
		kind, status      string
		entities          []byte
		issuedAt, expires int64
	)
	form := domain.PremiumPaymentForm{BuyerUserID: buyerID, ID: formID}
	err := db.QueryRow(ctx, query, buyerID, formID).Scan(
		&intentID, &kind, &form.RecipientUserID, &form.Months, &form.DurationDays, &form.AmountStars,
		&form.PlanVersion, &form.Message.Text, &entities, &issuedAt, &expires, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PremiumPaymentForm{}, "", 0, domain.ErrPremiumFormInvalid
	}
	if err != nil {
		return domain.PremiumPaymentForm{}, "", 0, fmt.Errorf("load premium payment form: %w", err)
	}
	if err := json.Unmarshal(entities, &form.Message.Entities); err != nil {
		return domain.PremiumPaymentForm{}, "", 0, domain.ErrPremiumFormInvalid
	}
	form.Kind = domain.PremiumPurchaseKind(kind)
	form.IssuedAt, form.ExpiresAt = int(issuedAt), int(expires)
	return form, domain.PremiumPaymentStatus(status), intentID, nil
}

func premiumRequestMatchesForm(req domain.PremiumPurchaseRequest, form domain.PremiumPaymentForm) bool {
	return req.BuyerUserID == form.BuyerUserID && req.FormID == form.ID &&
		req.Kind == form.Kind && req.RecipientUserID == form.RecipientUserID &&
		req.Months == form.Months && (req.PlanVersion == 0 || req.PlanVersion == form.PlanVersion) &&
		req.Message.Text == form.Message.Text
}

func (s *PremiumStore) loadPremiumPurchaseReplay(ctx context.Context, req domain.PremiumPurchaseRequest) (
	domain.PremiumPurchaseResult, bool, error,
) {
	form, status, intentID, err := loadPremiumPaymentForm(ctx, s.db, req.BuyerUserID, req.FormID, false)
	if errors.Is(err, domain.ErrPremiumFormInvalid) {
		return domain.PremiumPurchaseResult{}, false, nil
	}
	if err != nil {
		return domain.PremiumPurchaseResult{}, false, err
	}
	if status != domain.PremiumPaymentPaid {
		return domain.PremiumPurchaseResult{}, false, nil
	}
	if !premiumRequestMatchesForm(req, form) {
		return domain.PremiumPurchaseResult{}, false, domain.ErrPremiumFormInvalid
	}
	entitlement, found, err := premiumEntitlementByPayment(ctx, s.db, intentID)
	if err != nil || !found {
		if err != nil {
			return domain.PremiumPurchaseResult{}, false, err
		}
		return domain.PremiumPurchaseResult{}, false, domain.ErrPremiumFormInvalid
	}
	balance, err := NewStarsStore(s.db).GetBalance(ctx, req.BuyerUserID)
	if err != nil {
		return domain.PremiumPurchaseResult{}, false, err
	}
	user, found, err := NewUserStore(s.db).ByID(ctx, form.RecipientUserID)
	if err != nil || !found {
		return domain.PremiumPurchaseResult{}, false, err
	}
	return domain.PremiumPurchaseResult{
		Form: form, Entitlement: entitlement, User: user, Balance: balance, Duplicate: true,
	}, true, nil
}

func premiumEntitlementByPayment(ctx context.Context, db sqlcgen.DBTX, paymentID int64) (
	domain.PremiumEntitlement, bool, error,
) {
	var out domain.PremiumEntitlement
	var source, status string
	var starts, expires, created int64
	err := db.QueryRow(ctx, `SELECT id,user_id,source,source_user_id,payment_intent_id,COALESCE(transaction_id,0),months,duration_days,
EXTRACT(EPOCH FROM starts_at)::bigint,EXTRACT(EPOCH FROM expires_at)::bigint,status,command_key,
EXTRACT(EPOCH FROM created_at)::bigint FROM premium_entitlements WHERE payment_intent_id=$1`, paymentID).
		Scan(&out.ID, &out.UserID, &source, &out.SourceUserID, &out.PaymentIntentID,
			&out.TransactionID, &out.Months, &out.DurationDays, &starts, &expires, &status, &out.CommandKey, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PremiumEntitlement{}, false, nil
	}
	if err != nil {
		return domain.PremiumEntitlement{}, false, err
	}
	out.Source, out.Status = domain.PremiumEntitlementSource(source), domain.PremiumEntitlementStatus(status)
	out.StartsAt, out.ExpiresAt, out.CreatedAt = int(starts), int(expires), int(created)
	return out, true, nil
}

func (s *PremiumStore) ActivePremiumEntitlements(ctx context.Context, userID int64, now int) ([]domain.PremiumEntitlement, error) {
	if s == nil || s.db == nil || userID <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT id,user_id,source,source_user_id,COALESCE(payment_intent_id,0),
COALESCE(transaction_id,0),
months,duration_days,EXTRACT(EPOCH FROM starts_at)::bigint,EXTRACT(EPOCH FROM expires_at)::bigint,
status,command_key,EXTRACT(EPOCH FROM created_at)::bigint
FROM premium_entitlements WHERE user_id=$1 AND status='active' AND expires_at>to_timestamp($2)
ORDER BY expires_at,id`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PremiumEntitlement, 0)
	for rows.Next() {
		var item domain.PremiumEntitlement
		var source, status string
		var starts, expires, created int64
		if err := rows.Scan(&item.ID, &item.UserID, &source, &item.SourceUserID, &item.PaymentIntentID,
			&item.TransactionID, &item.Months, &item.DurationDays, &starts, &expires, &status, &item.CommandKey, &created); err != nil {
			return nil, err
		}
		item.Source, item.Status = domain.PremiumEntitlementSource(source), domain.PremiumEntitlementStatus(status)
		item.StartsAt, item.ExpiresAt, item.CreatedAt = int(starts), int(expires), int(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PremiumStore) PremiumEntitlements(ctx context.Context, userID int64, limit int) ([]domain.PremiumEntitlement, error) {
	if s == nil || s.db == nil || userID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT id,user_id,source,source_user_id,COALESCE(payment_intent_id,0),
COALESCE(transaction_id,0),
months,duration_days,EXTRACT(EPOCH FROM starts_at)::bigint,EXTRACT(EPOCH FROM expires_at)::bigint,
status,command_key,EXTRACT(EPOCH FROM created_at)::bigint
FROM premium_entitlements
WHERE user_id=$1 OR source_user_id=$1
ORDER BY created_at DESC,id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PremiumEntitlement, 0)
	for rows.Next() {
		var item domain.PremiumEntitlement
		var source, status string
		var starts, expires, created int64
		if err := rows.Scan(&item.ID, &item.UserID, &source, &item.SourceUserID, &item.PaymentIntentID,
			&item.TransactionID, &item.Months, &item.DurationDays, &starts, &expires, &status,
			&item.CommandKey, &created); err != nil {
			return nil, err
		}
		item.Source, item.Status = domain.PremiumEntitlementSource(source), domain.PremiumEntitlementStatus(status)
		item.StartsAt, item.ExpiresAt, item.CreatedAt = int(starts), int(expires), int(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PremiumStore) PremiumPayment(ctx context.Context, paymentIntentID int64) (domain.PremiumPaymentDetails, bool, error) {
	if s == nil || s.db == nil || paymentIntentID <= 0 {
		return domain.PremiumPaymentDetails{}, false, nil
	}
	var (
		out                   domain.PremiumPaymentDetails
		kind, status          string
		entities              []byte
		issued, expires, paid int64
		refunded              int64
		created, updated      int64
	)
	err := s.db.QueryRow(ctx, `SELECT id,form_id,idempotency_key,buyer_user_id,purchase_kind,recipient_user_id,
months,duration_days,amount_stars,plan_version,gift_message,gift_entities,status,
EXTRACT(EPOCH FROM issued_at)::bigint,EXTRACT(EPOCH FROM expires_at)::bigint,
COALESCE(EXTRACT(EPOCH FROM paid_at),0)::bigint,COALESCE(EXTRACT(EPOCH FROM refunded_at),0)::bigint,
COALESCE(stars_transaction_id,0),
EXTRACT(EPOCH FROM created_at)::bigint,EXTRACT(EPOCH FROM updated_at)::bigint
FROM premium_payment_intents WHERE id=$1`, paymentIntentID).Scan(
		&out.Intent.ID, &out.Intent.FormID, &out.Intent.IdempotencyKey, &out.Intent.BuyerUserID,
		&kind, &out.Intent.RecipientUserID, &out.Intent.Months, &out.Intent.DurationDays,
		&out.Intent.AmountStars, &out.Intent.PlanVersion,
		&out.Intent.Message.Text, &entities, &status,
		&issued, &expires, &paid, &refunded,
		&out.Intent.StarsTransactionID,
		&created, &updated,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PremiumPaymentDetails{}, false, nil
	}
	if err != nil {
		return domain.PremiumPaymentDetails{}, false, err
	}
	if err := json.Unmarshal(entities, &out.Intent.Message.Entities); err != nil {
		return domain.PremiumPaymentDetails{}, false, domain.ErrPremiumFormInvalid
	}
	out.Intent.Kind = domain.PremiumPurchaseKind(kind)
	out.Intent.Status = domain.PremiumPaymentStatus(status)
	out.Intent.IssuedAt, out.Intent.ExpiresAt = int(issued), int(expires)
	out.Intent.PaidAt, out.Intent.RefundedAt = int(paid), int(refunded)
	out.Intent.CreatedAt, out.Intent.UpdatedAt = int(created), int(updated)

	entitlement, found, err := premiumEntitlementByPayment(ctx, s.db, paymentIntentID)
	if err != nil {
		return domain.PremiumPaymentDetails{}, false, err
	}
	if found {
		out.Entitlement = entitlement
	}
	if out.Intent.StarsTransactionID != 0 {
		var peerType, reason string
		err := s.db.QueryRow(ctx, `SELECT user_id,peer_type,peer_id,amount,reason,title,description,date,
COALESCE(premium_payment_intent_id,0),COALESCE(premium_recipient_user_id,0),COALESCE(premium_months,0)
FROM stars_transactions WHERE id=$1`, out.Intent.StarsTransactionID).Scan(
			&out.Transaction.UserID, &peerType, &out.Transaction.Peer.ID, &out.Transaction.Amount,
			&reason, &out.Transaction.Title, &out.Transaction.Description, &out.Transaction.Date,
			&out.Transaction.PaymentID, &out.Transaction.RecipientUserID, &out.Transaction.PremiumMonths,
		)
		if err != nil {
			return domain.PremiumPaymentDetails{}, false, err
		}
		out.Transaction.ID = out.Intent.StarsTransactionID
		out.Transaction.Peer.Type = domain.PeerType(peerType)
		out.Transaction.Reason = domain.StarsTransactionReason(reason)
	}
	return out, true, nil
}

// RefundPremiumPayment credits the buyer's Stars back, marks the recipient's
// entitlement and the payment intent refunded, then recomputes
// users.premium_expires_at by replaying whatever active entitlements remain
// (see compactPremiumEntitlements) -- correct even when the recipient has
// since stacked another purchase or admin grant on top of this one.
func (s *PremiumStore) RefundPremiumPayment(ctx context.Context, req domain.PremiumRefundRequest) (domain.PremiumPurchaseResult, error) {
	if s == nil || s.db == nil || req.PaymentIntentID <= 0 || req.Date <= 0 {
		return domain.PremiumPurchaseResult{}, domain.ErrPremiumPaymentNotFound
	}
	var buyerID, recipientID int64
	if err := s.db.QueryRow(ctx, `SELECT buyer_user_id,recipient_user_id
FROM premium_payment_intents WHERE id=$1`, req.PaymentIntentID).Scan(&buyerID, &recipientID); err != nil {
		return domain.PremiumPurchaseResult{}, domain.ErrPremiumPaymentNotFound
	}
	var result domain.PremiumPurchaseResult
	err := withTx(ctx, s.db, "refund premium payment", func(tx pgx.Tx) error {
		if err := lockUsersForUpdate(ctx, tx, buyerID, recipientID); err != nil {
			return err
		}
		var formID, amount, planVersion int64
		var kind, status string
		var months, durationDays int
		if err := tx.QueryRow(ctx, `SELECT form_id,purchase_kind,months,duration_days,amount_stars,plan_version,status
FROM premium_payment_intents WHERE id=$1 FOR UPDATE`, req.PaymentIntentID).
			Scan(&formID, &kind, &months, &durationDays, &amount, &planVersion, &status); err != nil {
			return domain.ErrPremiumPaymentNotFound
		}
		if domain.PremiumPaymentStatus(status) == domain.PremiumPaymentRefunded {
			return domain.ErrPremiumAlreadyRefunded
		}
		if domain.PremiumPaymentStatus(status) != domain.PremiumPaymentPaid {
			return domain.ErrPremiumPaymentNotFound
		}
		entitlement, found, err := premiumEntitlementByPayment(ctx, tx, req.PaymentIntentID)
		if err != nil || !found {
			return domain.ErrPremiumPaymentNotFound
		}
		var balance int64
		var granted bool
		if err := tx.QueryRow(ctx, `INSERT INTO stars_balances(user_id,balance,updated_at)
VALUES($1,$2,now()) ON CONFLICT(user_id) DO UPDATE
SET balance=stars_balances.balance+EXCLUDED.balance,updated_at=now()
RETURNING balance,granted`, buyerID, amount).Scan(&balance, &granted); err != nil {
			return err
		}
		var refundTransactionID int64
		if err := tx.QueryRow(ctx, `INSERT INTO stars_transactions
(user_id,peer_type,peer_id,amount,reason,title,description,date,premium_recipient_user_id,premium_months)
VALUES($1,'user',$2,$3,$4,'Telegram Premium refund',$5,$6,$7,$8) RETURNING id`,
			buyerID, s.botID, amount, string(domain.StarsReasonPremium), req.Reason,
			req.Date, recipientID, months).Scan(&refundTransactionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE premium_entitlements
SET status='refunded',refunded_at=to_timestamp($2),reason=$3,updated_at=now()
WHERE id=$1`, entitlement.ID, req.Date, req.Reason); err != nil {
			return err
		}
		if err := compactPremiumEntitlements(ctx, tx, recipientID, req.Date); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE premium_payment_intents
SET status='refunded',refunded_at=to_timestamp($2),updated_at=now() WHERE id=$1`,
			req.PaymentIntentID, req.Date); err != nil {
			return err
		}
		user, found, err := NewUserStore(tx).ByID(ctx, recipientID)
		if err != nil || !found {
			return domain.ErrPremiumRecipientInvalid
		}
		entitlement.Status = domain.PremiumEntitlementRefunded
		result = domain.PremiumPurchaseResult{
			Form: domain.PremiumPaymentForm{ID: formID, BuyerUserID: buyerID,
				Kind: domain.PremiumPurchaseKind(kind), RecipientUserID: recipientID,
				Months: months, DurationDays: durationDays, AmountStars: amount, PlanVersion: planVersion},
			Entitlement: entitlement, User: user,
			Balance: domain.StarsBalance{UserID: buyerID, Balance: balance, Granted: granted},
		}
		return nil
	})
	if err != nil {
		return domain.PremiumPurchaseResult{}, err
	}
	return result, nil
}

// compactPremiumEntitlements recomputes users.premium_expires_at from
// scratch by chaining every remaining active entitlement window back to
// back starting at now, so removing one no longer necessarily the most
// recent still leaves the aggregate correct.
func compactPremiumEntitlements(ctx context.Context, tx pgx.Tx, userID int64, now int) error {
	rows, err := tx.Query(ctx, `SELECT id,
EXTRACT(EPOCH FROM starts_at)::bigint,EXTRACT(EPOCH FROM expires_at)::bigint,duration_days
FROM premium_entitlements
WHERE user_id=$1 AND status='active' AND expires_at>to_timestamp($2)
ORDER BY starts_at,id FOR UPDATE`, userID, now)
	if err != nil {
		return err
	}
	type window struct {
		id              int64
		starts, expires int
		durationDays    int
	}
	windows := make([]window, 0)
	for rows.Next() {
		var item window
		var starts, expires int64
		if err := rows.Scan(&item.id, &starts, &expires, &item.durationDays); err != nil {
			rows.Close()
			return err
		}
		item.starts, item.expires = int(starts), int(expires)
		windows = append(windows, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	cursor := now
	for _, item := range windows {
		seconds := item.durationDays * 24 * 60 * 60
		if item.starts <= now {
			seconds = item.expires - now
		}
		if seconds <= 0 {
			continue
		}
		next := cursor + seconds
		if _, err := tx.Exec(ctx, `UPDATE premium_entitlements
SET starts_at=to_timestamp($2),expires_at=to_timestamp($3),updated_at=now() WHERE id=$1`,
			item.id, cursor, next); err != nil {
			return err
		}
		cursor = next
	}
	if cursor <= now {
		_, err = tx.Exec(ctx, `UPDATE users SET premium_expires_at=NULL,
premium_updated_at=to_timestamp($2),updated_at=now() WHERE id=$1`, userID, now)
	} else {
		_, err = tx.Exec(ctx, `UPDATE users SET premium_expires_at=to_timestamp($2),
premium_updated_at=to_timestamp($3),updated_at=now()
WHERE id=$1`, userID, cursor, now)
	}
	return err
}
