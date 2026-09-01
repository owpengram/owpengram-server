package loadharness

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/iamxvbaba/td/tg"
)

type richAccountStatePlan struct {
	PinnedPeerAccount int
	ReadPeerAccount   int
	ReadGroupPosition int
	DraftMarker       string
}

func planRichAccountState(dataset *Dataset, account int) (richAccountStatePlan, error) {
	if dataset == nil || account < 0 || account >= dataset.Config.Accounts {
		return richAccountStatePlan{}, errors.New("invalid rich-state account")
	}
	if dataset.Config.PrivateFanout < 1 {
		return richAccountStatePlan{}, errors.New("rich startup dataset requires at least one private peer per account")
	}
	groupPosition := -1
	for position, group := range dataset.Groups {
		if group.HistoryMessages > 0 && datasetGroupHasAccount(group, account) {
			groupPosition = position
			break
		}
	}
	if groupPosition < 0 {
		return richAccountStatePlan{}, errors.New("rich startup dataset requires a non-empty supergroup per account")
	}
	return richAccountStatePlan{
		PinnedPeerAccount: (account + 1) % dataset.Config.Accounts,
		ReadPeerAccount:   (account - 1 + dataset.Config.Accounts) % dataset.Config.Accounts,
		ReadGroupPosition: groupPosition,
		DraftMarker:       fmt.Sprintf("[%s draft account %04d]", dataset.RunID, account),
	}, nil
}

// seedRichAccountState creates synchronized dialog state exclusively through
// public MTProto RPCs. Every mutation is absolute and then read back through
// messages.getPeerDialogs before the resumable journal is committed.
func seedRichAccountState(
	ctx context.Context,
	cfg SeedConfig,
	manifest *Manifest,
	dataset *Dataset,
	journal *seedJournal,
	targets []SessionRecord,
	key [32]byte,
	publicKey *rsa.PublicKey,
	account int,
) error {
	if journal.richStateComplete(account) {
		return nil
	}
	plan, err := planRichAccountState(dataset, account)
	if err != nil {
		return err
	}
	return withAuthorizedSeedSession(ctx, cfg, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
		pinnedTarget := targets[plan.PinnedPeerAccount]
		readTarget := targets[plan.ReadPeerAccount]
		pinnedPeer := &tg.InputPeerUser{UserID: pinnedTarget.UserID, AccessHash: pinnedTarget.AccessHash}
		readPeer := &tg.InputPeerUser{UserID: readTarget.UserID, AccessHash: readTarget.AccessHash}
		groupIdentity := journal.group(plan.ReadGroupPosition)
		if groupIdentity.ChannelID <= 0 || groupIdentity.AccessHash == 0 {
			return errors.New("rich-state group identity is incomplete")
		}
		channelPeer := &tg.InputPeerChannel{ChannelID: groupIdentity.ChannelID, AccessHash: groupIdentity.AccessHash}

		before, err := getRichStateDialogs(ctx, cfg.OperationTimeout, raw, pinnedPeer, readPeer, channelPeer)
		if err != nil {
			return fmt.Errorf("read rich-state cursors: %w", err)
		}
		readPrivate, ok := before[clientPeerKey{typ: "user", id: readTarget.UserID}]
		if !ok || readPrivate.TopMessage <= 0 {
			return errors.New("read private peer omitted its top message")
		}
		readChannel, ok := before[clientPeerKey{typ: "channel", id: groupIdentity.ChannelID}]
		if !ok || readChannel.TopMessage <= 0 {
			return errors.New("read channel omitted its top message")
		}

		pinned, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (bool, error) {
			return raw.MessagesToggleDialogPin(rpcCtx, &tg.MessagesToggleDialogPinRequest{
				Pinned: true, Peer: &tg.InputDialogPeer{Peer: pinnedPeer},
			})
		})
		if err != nil || !pinned {
			return rpcBooleanError("messages.toggleDialogPin", pinned, err)
		}
		saved, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (bool, error) {
			return raw.MessagesSaveDraft(rpcCtx, &tg.MessagesSaveDraftRequest{Peer: pinnedPeer, Message: plan.DraftMarker})
		})
		if err != nil || !saved {
			return rpcBooleanError("messages.saveDraft", saved, err)
		}
		if _, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (*tg.MessagesAffectedMessages, error) {
			return raw.MessagesReadHistory(rpcCtx, &tg.MessagesReadHistoryRequest{Peer: readPeer, MaxID: readPrivate.TopMessage})
		}); err != nil {
			return fmt.Errorf("messages.readHistory: %w", err)
		}
		channelRead, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (bool, error) {
			return raw.ChannelsReadHistory(rpcCtx, &tg.ChannelsReadHistoryRequest{
				Channel: &tg.InputChannel{ChannelID: groupIdentity.ChannelID, AccessHash: groupIdentity.AccessHash},
				MaxID:   readChannel.TopMessage,
			})
		})
		if err != nil || !channelRead {
			return rpcBooleanError("channels.readHistory", channelRead, err)
		}

		after, err := getRichStateDialogs(ctx, cfg.OperationTimeout, raw, pinnedPeer, readPeer, channelPeer)
		if err != nil {
			return fmt.Errorf("verify rich-state dialogs: %w", err)
		}
		pinnedDialog, ok := after[clientPeerKey{typ: "user", id: pinnedTarget.UserID}]
		if !ok || !pinnedDialog.Pinned || !pinnedDialog.HasDraft || pinnedDialog.DraftText != plan.DraftMarker {
			return errors.New("pinned private dialog or exact draft was not persisted")
		}
		readPrivate = after[clientPeerKey{typ: "user", id: readTarget.UserID}]
		if readPrivate.ReadInboxMaxID < before[clientPeerKey{typ: "user", id: readTarget.UserID}].TopMessage || readPrivate.UnreadCount != 0 {
			return errors.New("private read boundary did not converge")
		}
		readChannel = after[clientPeerKey{typ: "channel", id: groupIdentity.ChannelID}]
		if readChannel.ReadInboxMaxID < before[clientPeerKey{typ: "channel", id: groupIdentity.ChannelID}].TopMessage || readChannel.UnreadCount != 0 {
			return errors.New("channel read boundary did not converge")
		}
		return journal.setRichStateComplete(account)
	})
}

func rpcBooleanError(operation string, result bool, err error) error {
	if err != nil {
		return fmt.Errorf("%s result=%v: %w", operation, result, err)
	}
	return fmt.Errorf("%s returned false", operation)
}

func getRichStateDialogs(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	peers ...tg.InputPeerClass,
) (map[clientPeerKey]ClientDialogState, error) {
	requests := make([]tg.InputDialogPeerClass, 0, len(peers))
	seen := make(map[clientPeerKey]struct{}, len(peers))
	for _, peer := range peers {
		key, ok := clientPeerFromInput(peer)
		if !ok {
			return nil, errors.New("invalid rich-state input peer")
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		requests = append(requests, &tg.InputDialogPeer{Peer: peer})
	}
	response, err := rpcWithFloodWaitRetry(ctx, timeout, func(rpcCtx context.Context) (*tg.MessagesPeerDialogs, error) {
		return raw.MessagesGetPeerDialogs(rpcCtx, requests)
	})
	if err != nil {
		return nil, err
	}
	dialogs := make(map[clientPeerKey]ClientDialogState, len(response.Dialogs))
	if _, err := mergeDialogPage(dialogs, response.Dialogs, response.Messages, response.Chats, response.Users, false); err != nil {
		return nil, err
	}
	if len(dialogs) != len(requests) {
		return nil, fmt.Errorf("messages.getPeerDialogs returned %d/%d dialogs", len(dialogs), len(requests))
	}
	return dialogs, nil
}

func clientPeerFromInput(peer tg.InputPeerClass) (clientPeerKey, bool) {
	switch value := peer.(type) {
	case *tg.InputPeerUser:
		return clientPeerKey{typ: "user", id: value.UserID}, value.UserID > 0 && value.AccessHash != 0
	case *tg.InputPeerChannel:
		return clientPeerKey{typ: "channel", id: value.ChannelID}, value.ChannelID > 0 && value.AccessHash != 0
	default:
		return clientPeerKey{}, false
	}
}

func validateSeededRichDialogs(
	dataset *Dataset,
	seedState *DatasetSeedState,
	targets []SessionRecord,
	account int,
	dialogs []ClientDialogState,
	requireReadBoundaries bool,
) error {
	if len(seedState.RichStateByAccount) == 0 {
		return nil
	}
	if len(seedState.RichStateByAccount) != dataset.Config.Accounts || !seedState.RichStateByAccount[account] {
		return errors.New("rich-state seed is incomplete")
	}
	plan, err := planRichAccountState(dataset, account)
	if err != nil {
		return err
	}
	byPeer := make(map[clientPeerKey]ClientDialogState, len(dialogs))
	for _, dialog := range dialogs {
		byPeer[clientPeerKey{typ: dialog.PeerType, id: dialog.PeerID}] = dialog
	}
	pinned := byPeer[clientPeerKey{typ: "user", id: targets[plan.PinnedPeerAccount].UserID}]
	if !pinned.Pinned || !pinned.HasDraft || pinned.DraftText != plan.DraftMarker {
		return fmt.Errorf("seeded pinned dialog state mismatch: present=%v pinned=%v has_draft=%v draft_matches=%v got_draft=%q want_draft=%q",
			pinned.PeerID != 0, pinned.Pinned, pinned.HasDraft, pinned.DraftText == plan.DraftMarker, pinned.DraftText, plan.DraftMarker)
	}
	if !requireReadBoundaries {
		return nil
	}
	readPrivate := byPeer[clientPeerKey{typ: "user", id: targets[plan.ReadPeerAccount].UserID}]
	if readPrivate.TopMessage <= 0 || readPrivate.ReadInboxMaxID < readPrivate.TopMessage || readPrivate.UnreadCount != 0 {
		return errors.New("seeded private read boundary is stale")
	}
	group := seedState.Groups[plan.ReadGroupPosition]
	readChannel := byPeer[clientPeerKey{typ: "channel", id: group.ChannelID}]
	if readChannel.TopMessage <= 0 || readChannel.ReadInboxMaxID < readChannel.TopMessage || readChannel.UnreadCount != 0 {
		return errors.New("seeded channel read boundary is stale")
	}
	return nil
}
