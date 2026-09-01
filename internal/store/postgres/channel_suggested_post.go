package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
)

const suggestedPostSettlementAge = 24 * 60 * 60

// A claim is durable queue metadata, not a business transition. It prevents
// two dispatcher instances that selected the same due key from processing it
// concurrently. A crashed worker only delays that key; it does not block any
// sibling aggregate, and the bounded failure backoff may shorten the lease.
const suggestedPostLifecycleClaimSeconds = 5 * 60

type persistedSuggestedPostApproval struct {
	monoforumID, parentID, actorID, payerID                                                       int64
	messageID, scheduleDate, approvalServiceID, publishedMessageID, settlementDue, finalServiceID int
	state                                                                                         domain.SuggestedPostLifecycleState
	price                                                                                         *domain.SuggestedPostPrice
}

func (s *ChannelStore) ToggleSuggestedPostApproval(ctx context.Context, req domain.ToggleSuggestedPostApprovalRequest) (domain.ToggleSuggestedPostApprovalResult, error) {
	if req.UserID == 0 || req.MonoforumID == 0 || req.MessageID <= 0 || (!req.Reject && strings.TrimSpace(req.RejectComment) != "") {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
	}
	if req.Date == 0 {
		req.Date = nowUnix()
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return domain.ToggleSuggestedPostApprovalResult{}, fmt.Errorf("toggle suggested post: db does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, fmt.Errorf("begin toggle suggested post: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	mono, err := getChannelByID(ctx, tx, req.MonoforumID)
	if err != nil {
		if errors.Is(err, domain.ErrChannelInvalid) {
			return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
		}
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	if mono.Deleted || !mono.Monoforum || mono.LinkedMonoforumID == 0 {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
	}
	parent, err := getChannelByID(ctx, tx, mono.LinkedMonoforumID)
	if err != nil {
		if errors.Is(err, domain.ErrChannelInvalid) {
			return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
		}
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	if parent.Deleted || !parent.Broadcast || parent.LinkedMonoforumID != mono.ID {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
	}
	// All lifecycle/toggle paths lock an existing approval before its original
	// message. A first command has no row to lock, so it rechecks after taking
	// the message lock; this preserves single creation without a gap lock.
	existing, found, err := loadSuggestedPostApprovalTx(ctx, tx, mono.ID, req.MessageID, true)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM channel_messages WHERE channel_id=$1 AND id=$2 FOR UPDATE`, mono.ID, req.MessageID); err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	if !found {
		existing, found, err = loadSuggestedPostApprovalTx(ctx, tx, mono.ID, req.MessageID, true)
		if err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
	}
	original, err := s.getChannelMessage(ctx, tx, mono.ID, req.MessageID)
	if err != nil {
		if errors.Is(err, domain.ErrMessageIDInvalid) {
			return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
		}
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	if original.Deleted || original.SavedPeer.Type != domain.PeerTypeUser || original.SavedPeer.ID == 0 || original.SuggestedPost == nil {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
	}
	fromSubscriber := original.From.Type == domain.PeerTypeUser
	manager := domain.ChannelMember{}
	if fromSubscriber {
		manager, err = s.getChannelMember(ctx, tx, parent.ID, req.UserID)
		if err != nil {
			if errors.Is(err, domain.ErrChannelPrivate) {
				return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostApprovalForbidden
			}
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
		if !manager.CanManageDirectMessages() || (!req.Reject && !manager.CanPostChannelMessages()) {
			return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostApprovalForbidden
		}
	} else if req.UserID != original.SavedPeer.ID {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostApprovalForbidden
	}

	if found && existing.state != domain.SuggestedPostStateBalanceLow {
		result, err := s.loadSuggestedPostResultTx(ctx, tx, existing, true)
		if err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
		committed = true
		return result, nil
	}
	if original.SuggestedPost.Accepted || original.SuggestedPost.Rejected {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostAlreadyHandled
	}
	price := cloneSuggestedPricePG(original.SuggestedPost.Price)
	if price != nil && price.Kind == domain.SuggestedPostPriceStars && price.Nanos != 0 {
		return domain.ToggleSuggestedPostApprovalResult{}, domain.ErrSuggestedPostInvalid
	}
	scheduleDate := original.SuggestedPost.ScheduleDate
	if req.ScheduleDate > 0 {
		scheduleDate = req.ScheduleDate
	}
	if !req.Reject {
		effectiveDate, scheduleErr := domain.EffectiveSuggestedPostPublishDate(scheduleDate, req.Date)
		if scheduleErr != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, scheduleErr
		}
		scheduleDate = effectiveDate
	}
	recipients, err := monoforumManagerRecipientsTx(ctx, tx, parent.ID, original.SavedPeer.ID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	result := domain.ToggleSuggestedPostApprovalResult{Monoforum: mono, Parent: parent, SavedPeer: original.SavedPeer, Recipients: recipients}

	if req.Reject {
		original.SuggestedPost.Accepted, original.SuggestedPost.Rejected = false, true
		original, result.OriginalEvent, err = s.persistSuggestedPostEditTx(ctx, tx, original, req.UserID, req.Date)
		if err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
		result.OriginalMessage = original
		result.ServiceMessage, result.ServiceEvent, err = s.insertSuggestedPostServiceTx(ctx, tx, mono, parent, req.UserID, fromSubscriber && manager.CanManageDirectMessages(), original.SavedPeer, original.ID, req.Date, domain.ChannelMessageAction{
			Type: domain.ChannelActionSuggestedPostApproval, SuggestedPostRejected: true,
			SuggestedPostRejectComment: strings.TrimSpace(req.RejectComment), SuggestedPostPrice: price,
		})
		if err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
		result.State = domain.SuggestedPostStateRejected
		if err := upsertSuggestedPostApprovalTx(ctx, tx, persistedSuggestedPostApproval{monoforumID: mono.ID, messageID: original.ID, parentID: parent.ID, actorID: req.UserID, payerID: original.SavedPeer.ID, state: result.State, price: price, scheduleDate: scheduleDate, approvalServiceID: result.ServiceMessage.ID}, req.Date); err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, err
		}
	} else {
		// telesrv has no Stars economy: a suggested post is approved for free
		// regardless of any price attached to it, so the balance-check/collect
		// step and its "balance too low" retry state are skipped entirely.
		{
			effectivePublishDate := scheduleDate
			original.SuggestedPost.Accepted, original.SuggestedPost.Rejected, original.SuggestedPost.ScheduleDate = true, false, effectivePublishDate
			original, result.OriginalEvent, err = s.persistSuggestedPostEditTx(ctx, tx, original, req.UserID, req.Date)
			if err != nil {
				return domain.ToggleSuggestedPostApprovalResult{}, err
			}
			result.OriginalMessage = original
			result.ServiceMessage, result.ServiceEvent, err = s.insertSuggestedPostServiceTx(ctx, tx, mono, parent, req.UserID, fromSubscriber && manager.CanManageDirectMessages(), original.SavedPeer, original.ID, req.Date, domain.ChannelMessageAction{Type: domain.ChannelActionSuggestedPostApproval, SuggestedPostScheduleDate: effectivePublishDate, SuggestedPostPrice: price})
			if err != nil {
				return domain.ToggleSuggestedPostApprovalResult{}, err
			}
			record := persistedSuggestedPostApproval{monoforumID: mono.ID, messageID: original.ID, parentID: parent.ID, actorID: req.UserID, payerID: original.SavedPeer.ID, state: domain.SuggestedPostStateScheduled, price: price, scheduleDate: effectivePublishDate, approvalServiceID: result.ServiceMessage.ID}
			if effectivePublishDate <= req.Date {
				published, publishErr := s.publishSuggestedPostTx(ctx, tx, parent, original, req.UserID, req.Date)
				if publishErr != nil {
					return domain.ToggleSuggestedPostApprovalResult{}, publishErr
				}
				result.Published = &published
				record.publishedMessageID = published.Message.ID
				// telesrv has no Stars economy: there is nothing to settle after
				// publish (no charge was ever collected), so a published post
				// goes straight to Completed regardless of any attached price.
				record.state = domain.SuggestedPostStateCompleted
			}
			result.State = record.state
			if err := upsertSuggestedPostApprovalTx(ctx, tx, record, req.Date); err != nil {
				return domain.ToggleSuggestedPostApprovalResult{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, fmt.Errorf("commit toggle suggested post: %w", err)
	}
	committed = true
	result.Monoforum, err = getChannelByID(ctx, s.db, mono.ID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, fmt.Errorf("reload suggested post monoforum after commit: %w", err)
	}
	result.Parent, err = getChannelByID(ctx, s.db, parent.ID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, fmt.Errorf("reload suggested post parent after commit: %w", err)
	}
	return result, nil
}

func (s *ChannelStore) persistSuggestedPostEditTx(ctx context.Context, tx pgx.Tx, msg domain.ChannelMessage, actor int64, date int) (domain.ChannelMessage, domain.ChannelUpdateEvent, error) {
	pts, err := s.reserveChannelPts(ctx, tx, msg.ChannelID)
	if err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	encoded, err := marshalJSON(msg.SuggestedPost, "{}")
	if err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	msg.Pts = pts
	if _, err := tx.Exec(ctx, `UPDATE channel_messages SET suggested_post=$3, pts=$4, updated_at=now() WHERE channel_id=$1 AND id=$2 AND NOT deleted`, msg.ChannelID, msg.ID, encoded, pts); err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, fmt.Errorf("update suggested post message: %w", err)
	}
	event := domain.ChannelUpdateEvent{ChannelID: msg.ChannelID, Type: domain.ChannelUpdateEditMessage, Pts: pts, PtsCount: 1, Date: date, Message: msg, SenderUserID: actor}
	if err := insertChannelEventTx(ctx, tx, event); err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	return msg, event, nil
}

func (s *ChannelStore) insertSuggestedPostServiceTx(ctx context.Context, tx pgx.Tx, mono, parent domain.Channel, actor int64, fromChannel bool, saved domain.Peer, replyID, date int, action domain.ChannelMessageAction) (domain.ChannelMessage, domain.ChannelUpdateEvent, error) {
	msgID, err := s.msgIDs.NextChannelMessageID(ctx, mono.ID)
	if err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	pts, err := s.reserveChannelPts(ctx, tx, mono.ID)
	if err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	from := domain.Peer{Type: domain.PeerTypeUser, ID: actor}
	if fromChannel {
		from = domain.Peer{Type: domain.PeerTypeChannel, ID: parent.ID}
	}
	msg := domain.ChannelMessage{ChannelID: mono.ID, ID: msgID, SenderUserID: actor, From: from, SavedPeer: saved, Date: date, ReplyTo: &domain.MessageReply{Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: mono.ID}, MessageID: replyID}, Action: &action, Pts: pts}
	event := domain.ChannelUpdateEvent{ChannelID: mono.ID, Type: domain.ChannelUpdateNewMessage, Pts: pts, PtsCount: 1, Date: date, Message: msg, SenderUserID: actor}
	if err := insertChannelMessageTx(ctx, tx, msg); err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	if err := insertChannelEventTx(ctx, tx, event); err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE channels SET top_message_id=$2, pts=$3, updated_at=now() WHERE id=$1`, mono.ID, msgID, pts); err != nil {
		return domain.ChannelMessage{}, domain.ChannelUpdateEvent{}, err
	}
	return msg, event, nil
}

func (s *ChannelStore) publishSuggestedPostTx(ctx context.Context, tx pgx.Tx, parent domain.Channel, original domain.ChannelMessage, actor int64, date int) (domain.SendChannelMessageResult, error) {
	msgID, err := s.msgIDs.NextChannelMessageID(ctx, parent.ID)
	if err != nil {
		return domain.SendChannelMessageResult{}, err
	}
	pts, err := s.reserveChannelPts(ctx, tx, parent.ID)
	if err != nil {
		return domain.SendChannelMessageResult{}, err
	}
	msg := original
	msg.ChannelID, msg.ID, msg.RandomID, msg.SenderUserID = parent.ID, msgID, 0, actor
	msg.From, msg.SendAs, msg.SavedPeer, msg.Date, msg.EditDate, msg.Post = domain.Peer{Type: domain.PeerTypeChannel, ID: parent.ID}, nil, domain.Peer{}, date, 0, true
	msg.ReplyTo, msg.PaidMessageStars, msg.Pts, msg.Deleted = nil, 0, pts, false
	event := domain.ChannelUpdateEvent{ChannelID: parent.ID, Type: domain.ChannelUpdateNewMessage, Pts: pts, PtsCount: 1, Date: date, Message: msg, SenderUserID: actor}
	if err := insertChannelMessageTx(ctx, tx, msg); err != nil {
		return domain.SendChannelMessageResult{}, err
	}
	if err := insertChannelEventTx(ctx, tx, event); err != nil {
		return domain.SendChannelMessageResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE channels SET top_message_id=$2, pts=$3, updated_at=now() WHERE id=$1`, parent.ID, msgID, pts); err != nil {
		return domain.SendChannelMessageResult{}, err
	}
	recipients, err := s.listActiveChannelMemberIDs(ctx, tx, parent.ID, 0)
	if err != nil {
		return domain.SendChannelMessageResult{}, err
	}
	parent.TopMessageID, parent.Pts = msgID, pts
	return domain.SendChannelMessageResult{Channel: parent, Message: msg, Event: event, Recipients: recipients}, nil
}


func monoforumManagerRecipientsTx(ctx context.Context, tx pgx.Tx, parentID, subscriberID int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM channel_members WHERE channel_id=$1 AND status='active' AND (role='creator' OR (role='admin' AND COALESCE((admin_rights->>'ManageDirectMessages')::boolean,false))) ORDER BY user_id`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{subscriberID}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return uniqueChannelUserIDs(ids, 0), rows.Err()
}

func upsertSuggestedPostApprovalTx(ctx context.Context, tx pgx.Tx, row persistedSuggestedPostApproval, date int) error {
	kind, amount, nanos := "", int64(0), 0
	if row.price != nil {
		kind, amount, nanos = string(row.price.Kind), row.price.Amount, row.price.Nanos
	}
	nextAttemptAt := 0
	switch row.state {
	case domain.SuggestedPostStateScheduled:
		nextAttemptAt = row.scheduleDate
	case domain.SuggestedPostStatePublished:
		nextAttemptAt = row.settlementDue
	}
	_, err := tx.Exec(ctx, `INSERT INTO suggested_post_approvals(monoforum_id,suggestion_message_id,parent_channel_id,actor_user_id,payer_user_id,state,price_kind,price_amount,price_nanos,schedule_date,approval_service_message_id,published_message_id,settlement_due,final_service_message_id,next_attempt_at,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)
ON CONFLICT(monoforum_id,suggestion_message_id) DO UPDATE SET actor_user_id=EXCLUDED.actor_user_id,state=EXCLUDED.state,price_kind=EXCLUDED.price_kind,price_amount=EXCLUDED.price_amount,price_nanos=EXCLUDED.price_nanos,schedule_date=EXCLUDED.schedule_date,approval_service_message_id=EXCLUDED.approval_service_message_id,published_message_id=EXCLUDED.published_message_id,settlement_due=EXCLUDED.settlement_due,final_service_message_id=EXCLUDED.final_service_message_id,lifecycle_attempts=0,next_attempt_at=EXCLUDED.next_attempt_at,last_lifecycle_error='',updated_at=EXCLUDED.updated_at`,
		row.monoforumID, row.messageID, row.parentID, row.actorID, row.payerID, string(row.state), kind, amount, nanos, row.scheduleDate, row.approvalServiceID, row.publishedMessageID, row.settlementDue, row.finalServiceID, nextAttemptAt, date)
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM suggested_post_lifecycle_wakeups
WHERE monoforum_id=$1 AND suggestion_message_id=$2`, row.monoforumID, row.messageID)
	}
	return err
}

func loadSuggestedPostApprovalTx(ctx context.Context, tx pgx.Tx, monoID int64, messageID int, lock bool) (persistedSuggestedPostApproval, bool, error) {
	q := `SELECT parent_channel_id,actor_user_id,payer_user_id,state,price_kind,price_amount,price_nanos,schedule_date,approval_service_message_id,published_message_id,settlement_due,final_service_message_id FROM suggested_post_approvals WHERE monoforum_id=$1 AND suggestion_message_id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	var row persistedSuggestedPostApproval
	row.monoforumID, row.messageID = monoID, messageID
	var state, kind string
	var amount int64
	var nanos int
	err := tx.QueryRow(ctx, q, monoID, messageID).Scan(&row.parentID, &row.actorID, &row.payerID, &state, &kind, &amount, &nanos, &row.scheduleDate, &row.approvalServiceID, &row.publishedMessageID, &row.settlementDue, &row.finalServiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	row.state = domain.SuggestedPostLifecycleState(state)
	if kind != "" {
		row.price = &domain.SuggestedPostPrice{Kind: domain.SuggestedPostPriceKind(kind), Amount: amount, Nanos: nanos}
	}
	return row, true, nil
}

func (s *ChannelStore) loadSuggestedPostResultTx(ctx context.Context, tx pgx.Tx, row persistedSuggestedPostApproval, duplicate bool) (domain.ToggleSuggestedPostApprovalResult, error) {
	mono, err := getChannelByID(ctx, tx, row.monoforumID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	parent, err := getChannelByID(ctx, tx, row.parentID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	original, err := s.getChannelMessage(ctx, tx, row.monoforumID, row.messageID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	recipients, err := monoforumManagerRecipientsTx(ctx, tx, row.parentID, row.payerID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, err
	}
	result := domain.ToggleSuggestedPostApprovalResult{Monoforum: mono, Parent: parent, SavedPeer: domain.Peer{Type: domain.PeerTypeUser, ID: row.payerID}, State: row.state, OriginalMessage: original, Recipients: recipients, Duplicate: duplicate}
	if original.SuggestedPost != nil && (original.SuggestedPost.Accepted || original.SuggestedPost.Rejected) && original.Pts > 0 {
		eventDate, senderUserID := original.Date, row.actorID
		// The event row is the exact durable replay source. Retention may have
		// pruned an old event, in which case the message snapshot still provides
		// a safe replay with its original date and lifecycle actor.
		if err := tx.QueryRow(ctx, `SELECT date,sender_user_id FROM channel_update_events WHERE channel_id=$1 AND pts=$2 AND event_type='edit_channel_message'`, row.monoforumID, original.Pts).Scan(&eventDate, &senderUserID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.ToggleSuggestedPostApprovalResult{}, fmt.Errorf("load suggested post edit event: %w", err)
		}
		result.OriginalEvent = domain.ChannelUpdateEvent{ChannelID: row.monoforumID, Type: domain.ChannelUpdateEditMessage, Pts: original.Pts, PtsCount: 1, Date: eventDate, Message: original, SenderUserID: senderUserID}
	}
	if row.approvalServiceID > 0 {
		result.ServiceMessage, err = s.getChannelMessage(ctx, tx, row.monoforumID, row.approvalServiceID)
		if err != nil {
			return result, err
		}
		result.ServiceEvent = domain.ChannelUpdateEvent{ChannelID: row.monoforumID, Type: domain.ChannelUpdateNewMessage, Pts: result.ServiceMessage.Pts, PtsCount: 1, Date: result.ServiceMessage.Date, Message: result.ServiceMessage, SenderUserID: row.actorID}
	}
	if row.publishedMessageID > 0 {
		msg, e := s.getChannelMessage(ctx, tx, row.parentID, row.publishedMessageID)
		if e != nil {
			return result, e
		}
		event := domain.ChannelUpdateEvent{ChannelID: row.parentID, Type: domain.ChannelUpdateNewMessage, Pts: msg.Pts, PtsCount: 1, Date: msg.Date, Message: msg, SenderUserID: row.actorID}
		result.Published = &domain.SendChannelMessageResult{Channel: parent, Message: msg, Event: event}
	}
	return result, nil
}

func cloneSuggestedPricePG(in *domain.SuggestedPostPrice) *domain.SuggestedPostPrice {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func (s *ChannelStore) ProcessSuggestedPostLifecycle(ctx context.Context, req domain.SuggestedPostLifecycleRequest) ([]domain.ToggleSuggestedPostApprovalResult, error) {
	if req.Now == 0 {
		req.Now = nowUnix()
	}
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 100
	}
	rows, err := s.db.Query(ctx, `
WITH timed AS MATERIALIZED (
    SELECT monoforum_id,suggestion_message_id,next_attempt_at AS due_at
    FROM suggested_post_approvals
    WHERE state IN ('scheduled','published') AND next_attempt_at <= $1
    ORDER BY next_attempt_at,monoforum_id,suggestion_message_id
    LIMIT $2
), woken AS MATERIALIZED (
    SELECT w.monoforum_id,w.suggestion_message_id,w.created_at AS due_at
    FROM suggested_post_lifecycle_wakeups w
    JOIN suggested_post_approvals a
      ON a.monoforum_id=w.monoforum_id AND a.suggestion_message_id=w.suggestion_message_id
    WHERE a.state IN ('scheduled','published')
    ORDER BY w.created_at,w.monoforum_id,w.suggestion_message_id
    LIMIT $2
), due AS (
    SELECT * FROM timed
    UNION ALL
    SELECT * FROM woken
)
SELECT monoforum_id,suggestion_message_id
FROM due
GROUP BY monoforum_id,suggestion_message_id
ORDER BY MIN(due_at),monoforum_id,suggestion_message_id
LIMIT $2`, req.Now, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("list due suggested posts: %w", err)
	}
	type key struct {
		mono    int64
		message int
	}
	keys := make([]key, 0, req.Limit)
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.mono, &k.message); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]domain.ToggleSuggestedPostApprovalResult, 0, len(keys))
	var failures []error
	for _, k := range keys {
		claimed, err := s.claimSuggestedPostLifecycle(ctx, k.mono, k.message, req.Now)
		if err != nil {
			failures = append(failures, fmt.Errorf("claim suggested post lifecycle %d/%d: %w", k.mono, k.message, err))
			continue
		}
		if !claimed {
			continue
		}
		result, changed, err := s.processSuggestedPostLifecycleOne(ctx, k.mono, k.message, req.Now)
		if changed {
			// A post-commit reload can fail after the durable transition already
			// succeeded. Preserve that result so its committed updates still reach
			// the fanout layer; only pre-commit failures need retry metadata.
			out = append(out, result)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("suggested post lifecycle %d/%d: %w", k.mono, k.message, err))
			if !changed {
				if recordErr := s.recordSuggestedPostLifecycleFailure(ctx, k.mono, k.message, req.Now, err); recordErr != nil {
					failures = append(failures, fmt.Errorf("record suggested post lifecycle failure %d/%d: %w", k.mono, k.message, recordErr))
				}
			}
			continue
		}
	}
	return out, errors.Join(failures...)
}

func (s *ChannelStore) claimSuggestedPostLifecycle(ctx context.Context, monoforumID int64, messageID, now int) (bool, error) {
	if monoforumID <= 0 || messageID <= 0 || now <= 0 {
		return false, domain.ErrSuggestedPostInvalid
	}
	var claimed bool
	err := s.db.QueryRow(ctx, `WITH claimed AS (
    UPDATE suggested_post_approvals a
    SET next_attempt_at=$3+$4,
        updated_at=GREATEST(a.updated_at,$3)
    WHERE a.monoforum_id=$1 AND a.suggestion_message_id=$2
      AND a.state IN ('scheduled','published')
      AND (a.next_attempt_at <= $3 OR EXISTS (
          SELECT 1 FROM suggested_post_lifecycle_wakeups w
          WHERE w.monoforum_id=a.monoforum_id AND w.suggestion_message_id=a.suggestion_message_id))
    RETURNING a.monoforum_id,a.suggestion_message_id
), cleared AS (
    DELETE FROM suggested_post_lifecycle_wakeups w
    USING claimed c
    WHERE w.monoforum_id=c.monoforum_id AND w.suggestion_message_id=c.suggestion_message_id
)
SELECT EXISTS(SELECT 1 FROM claimed)`, monoforumID, messageID, now, suggestedPostLifecycleClaimSeconds).Scan(&claimed)
	return claimed, err
}

func (s *ChannelStore) recordSuggestedPostLifecycleFailure(ctx context.Context, monoforumID int64, messageID, now int, cause error) error {
	if monoforumID <= 0 || messageID <= 0 || now <= 0 || cause == nil {
		return domain.ErrSuggestedPostInvalid
	}
	lastError := []rune(strings.TrimSpace(cause.Error()))
	if len(lastError) > 512 {
		lastError = lastError[:512]
	}
	_, err := s.db.Exec(ctx, `UPDATE suggested_post_approvals
SET lifecycle_attempts=LEAST(lifecycle_attempts+1,1000000),
    next_attempt_at=$3+LEAST(300,5*(LEAST(lifecycle_attempts,59)+1)),
    last_lifecycle_error=$4,
    updated_at=GREATEST(updated_at,$3)
WHERE monoforum_id=$1 AND suggestion_message_id=$2 AND state IN ('scheduled','published')`,
		monoforumID, messageID, now, string(lastError))
	return err
}

func (s *ChannelStore) processSuggestedPostLifecycleOne(ctx context.Context, monoID int64, messageID, now int) (domain.ToggleSuggestedPostApprovalResult, bool, error) {
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return domain.ToggleSuggestedPostApprovalResult{}, false, fmt.Errorf("suggested post lifecycle: db does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	row, found, err := loadSuggestedPostApprovalTx(ctx, tx, monoID, messageID, true)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, false, err
	}
	if !found {
		return domain.ToggleSuggestedPostApprovalResult{}, false, fmt.Errorf("suggested post lifecycle invariant: approval row disappeared for monoforum %d message %d", monoID, messageID)
	}
	if row.state != domain.SuggestedPostStateScheduled && row.state != domain.SuggestedPostStatePublished {
		if err := tx.Commit(ctx); err != nil {
			return domain.ToggleSuggestedPostApprovalResult{}, false, err
		}
		committed = true
		return domain.ToggleSuggestedPostApprovalResult{}, false, nil
	}
	mono, err := getChannelByID(ctx, tx, row.monoforumID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, false, err
	}
	parent, err := getChannelByID(ctx, tx, row.parentID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, false, err
	}
	// Serialize a scheduled publish with deletion of the original suggestion.
	// The delete trigger only reads the approval row and writes a wakeup, so
	// taking approval -> message locks here does not introduce a reverse edge.
	original, err := getSuggestedPostMessageForShare(ctx, tx, row.monoforumID, row.messageID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, false, err
	}
	recipients, err := monoforumManagerRecipientsTx(ctx, tx, parent.ID, row.payerID)
	if err != nil {
		return domain.ToggleSuggestedPostApprovalResult{}, false, err
	}
	result := domain.ToggleSuggestedPostApprovalResult{Monoforum: mono, Parent: parent, SavedPeer: domain.Peer{Type: domain.PeerTypeUser, ID: row.payerID}, State: row.state, Recipients: recipients}
	changed := false
	// telesrv has no Stars economy: nothing was ever charged, so a deleted
	// scheduled post is simply dropped (Refunded is the closest existing
	// terminal state, reused here so downstream event handling stays uniform)
	// and a published post is Completed immediately -- there is no settlement
	// window and no refund path to run.
	if row.state == domain.SuggestedPostStateScheduled && original.Deleted {
		row.state, result.State, changed = domain.SuggestedPostStateRefunded, domain.SuggestedPostStateRefunded, true
	}
	if row.state == domain.SuggestedPostStateScheduled && row.scheduleDate <= now {
		published, err := s.publishSuggestedPostTx(ctx, tx, parent, original, row.actorID, now)
		if err != nil {
			return result, false, err
		}
		result.Published = &published
		row.publishedMessageID = published.Message.ID
		row.state = domain.SuggestedPostStateCompleted
		result.State = row.state
		changed = true
	}
	if !changed {
		nextAttemptAt := row.scheduleDate
		if row.state == domain.SuggestedPostStatePublished {
			nextAttemptAt = row.settlementDue
		}
		if _, err := tx.Exec(ctx, `UPDATE suggested_post_approvals
SET lifecycle_attempts=0,next_attempt_at=$3,last_lifecycle_error='',updated_at=GREATEST(updated_at,$4)
WHERE monoforum_id=$1 AND suggestion_message_id=$2`, row.monoforumID, row.messageID, nextAttemptAt, now); err != nil {
			return result, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return result, false, err
		}
		committed = true
		return result, false, nil
	}
	if err := upsertSuggestedPostApprovalTx(ctx, tx, row, now); err != nil {
		return result, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, false, err
	}
	committed = true
	reloadedMonoforum, err := getChannelByID(ctx, s.db, row.monoforumID)
	if err != nil {
		return result, true, fmt.Errorf("reload lifecycle monoforum after commit: %w", err)
	}
	result.Monoforum = reloadedMonoforum
	reloadedParent, err := getChannelByID(ctx, s.db, row.parentID)
	if err != nil {
		return result, true, fmt.Errorf("reload lifecycle parent after commit: %w", err)
	}
	result.Parent = reloadedParent
	return result, true, nil
}

func getSuggestedPostMessageForShare(ctx context.Context, tx pgx.Tx, channelID int64, messageID int) (domain.ChannelMessage, error) {
	if channelID <= 0 || messageID <= 0 {
		return domain.ChannelMessage{}, domain.ErrMessageIDInvalid
	}
	msg, err := scanChannelMessage(tx.QueryRow(ctx, `SELECT `+channelMessageColumns+`
FROM channel_messages
WHERE channel_id=$1 AND id=$2
FOR SHARE`, channelID, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChannelMessage{}, domain.ErrMessageIDInvalid
	}
	return msg, err
}

func (r persistedSuggestedPostApproval) savedPeer() domain.Peer {
	return domain.Peer{Type: domain.PeerTypeUser, ID: r.payerID}
}


