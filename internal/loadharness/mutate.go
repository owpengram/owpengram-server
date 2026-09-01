package loadharness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
)

const OfflineMutationVersion = 1

type OfflineMutationChannelPlan struct {
	GroupPosition int
	Messages      int
}

type OfflineMutationChannelState struct {
	GroupIndex    int   `json:"group_index"`
	MessageIDs    []int `json:"message_ids"`
	LatestPts     int   `json:"latest_pts,omitempty"`
	EditPending   bool  `json:"edit_pending,omitempty"`
	EditDone      bool  `json:"edit_done,omitempty"`
	DeletePending bool  `json:"delete_pending,omitempty"`
	DeleteDone    bool  `json:"delete_done,omitempty"`
	PinPending    bool  `json:"pin_pending,omitempty"`
	PinDone       bool  `json:"pin_done,omitempty"`
}

type OfflineMutationState struct {
	Version            int                           `json:"version"`
	DatasetSHA256      string                        `json:"dataset_sha256"`
	SeedIdentitySHA    string                        `json:"seed_identity_sha256"`
	BaselineStateSHA   string                        `json:"baseline_state_sha256"`
	UpdatedAt          time.Time                     `json:"updated_at"`
	PrivateMessageIDs  []int                         `json:"private_message_ids"`
	AccountObservedPts []int                         `json:"account_observed_pts"`
	Channels           []OfflineMutationChannelState `json:"channels"`
}

type MutateOfflineConfig struct {
	ManifestPath      string
	SessionKeyPath    string
	RSAKeyOverride    string
	DatasetPath       string
	SeedStatePath     string
	ClientStatePath   string
	MutationStatePath string
	Concurrency       int
	OperationTimeout  time.Duration
}

type MutationEvent struct {
	Phase     string
	Completed int
	Total     int
	Account   int
	Err       error
}

type MutationResult struct {
	PrivateMessages int
	ChannelMessages int
	DirtyChannels   int
	Edited          int
	Deleted         int
	Pinned          int
}

func (c MutateOfflineConfig) validate() error {
	if c.ManifestPath == "" || c.SessionKeyPath == "" || c.DatasetPath == "" || c.SeedStatePath == "" || c.ClientStatePath == "" || c.MutationStatePath == "" {
		return errors.New("manifest, session-key, dataset, seed-state, client-state and mutation-state paths are required")
	}
	if c.Concurrency <= 0 || c.Concurrency > 64 {
		return errors.New("offline mutation concurrency must be between 1 and 64")
	}
	if c.OperationTimeout <= 0 {
		return errors.New("offline mutation operation timeout must be positive")
	}
	return nil
}

// MutateOffline creates gaps only after a complete baseline has been locked.
// Message sends use stable random_id values. Mutable channel operations use a
// pending journal and public read-back reconciliation before a resumed run can
// declare them complete.
func MutateOffline(ctx context.Context, cfg MutateOfflineConfig, progress func(MutationEvent)) (*MutationResult, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		return nil, err
	}
	dataset, err := LoadDataset(cfg.DatasetPath)
	if err != nil {
		return nil, err
	}
	targets, err := seedPrimaryTargets(manifest, dataset.Config.Accounts)
	if err != nil {
		return nil, err
	}
	seedState, err := LoadDatasetSeedState(cfg.SeedStatePath, dataset)
	if err != nil {
		return nil, err
	}
	seedJournal := &seedJournal{dataset: dataset, state: seedState}
	if err := seedJournal.assertComplete(); err != nil {
		return nil, fmt.Errorf("offline mutation requires a complete seed: %w", err)
	}
	clientState, err := LoadClientState(cfg.ClientStatePath)
	if err != nil {
		return nil, err
	}
	if err := clientState.Validate(dataset, seedState, targets); err != nil {
		return nil, err
	}
	baselineSHA, err := fileSHA256(cfg.ClientStatePath)
	if err != nil {
		return nil, err
	}
	seedIdentity, err := seedIdentitySHA256(seedState)
	if err != nil {
		return nil, err
	}
	plan := planOfflineMutation(dataset)
	state, err := loadOrCreateOfflineMutationState(cfg.MutationStatePath, dataset, seedIdentity, baselineSHA, plan)
	if err != nil {
		return nil, err
	}
	journal := &mutationJournal{path: cfg.MutationStatePath, dataset: dataset, plan: plan, state: state}
	if err := journal.persist(); err != nil {
		return nil, err
	}
	key, err := LoadSessionKey(cfg.SessionKeyPath)
	if err != nil {
		return nil, err
	}
	publicKey, err := loadManifestPublicKey(cfg.ManifestPath, manifest.Endpoint, cfg.RSAKeyOverride)
	if err != nil {
		return nil, err
	}
	accounts := make([]int, dataset.Config.Accounts)
	for account := range accounts {
		accounts[account] = account
	}

	if err := runSeedAccountPhase(ctx, "mutate-private", accounts, cfg.Concurrency, mutationProgressAdapter("private", progress), func(ctx context.Context, account int) error {
		if journal.privateMessageID(account) != 0 {
			return nil
		}
		return withAuthorizedSeedSession(ctx, SeedConfig{ManifestPath: cfg.ManifestPath, OperationTimeout: cfg.OperationTimeout}, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
			recipient := (account + 1) % dataset.Config.Accounts
			marker := offlinePrivateMarker(dataset, account, recipient)
			updates, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
				return raw.MessagesSendMessage(rpcCtx, &tg.MessagesSendMessageRequest{
					Peer:    &tg.InputPeerUser{UserID: targets[recipient].UserID, AccessHash: targets[recipient].AccessHash},
					Message: marker, RandomID: stableDatasetID(dataset.Config.Seed, "offline-private", dataset.Config.Accounts, account, recipient),
				})
			})
			if err != nil {
				return fmt.Errorf("messages.sendMessage: %w", err)
			}
			observation, err := sentMessageObservation(updates, clientPeerKey{typ: "user", id: targets[recipient].UserID}, marker)
			if err != nil {
				return err
			}
			return journal.commitPrivate(account, observation.ID, observation.Pts)
		})
	}); err != nil {
		return nil, err
	}

	channelTasks := offlineChannelTasks(dataset, plan)
	channelAccounts := make([]int, 0, len(channelTasks))
	for account := range channelTasks {
		channelAccounts = append(channelAccounts, account)
	}
	sort.Ints(channelAccounts)
	if err := runSeedAccountPhase(ctx, "mutate-channel", channelAccounts, cfg.Concurrency, mutationProgressAdapter("channel", progress), func(ctx context.Context, account int) error {
		return withAuthorizedSeedSession(ctx, SeedConfig{ManifestPath: cfg.ManifestPath, OperationTimeout: cfg.OperationTimeout}, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
			for _, task := range channelTasks[account] {
				if journal.channelMessageID(task.PlanPosition, task.MessageIndex) != 0 {
					continue
				}
				channelPlan := plan[task.PlanPosition]
				group := dataset.Groups[channelPlan.GroupPosition]
				channel := seedState.Groups[channelPlan.GroupPosition]
				marker := offlineChannelMarker(dataset, group, task.MessageIndex)
				updates, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
					return raw.MessagesSendMessage(rpcCtx, &tg.MessagesSendMessageRequest{
						Peer:    &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
						Message: marker, RandomID: stableDatasetID(dataset.Config.Seed, "offline-channel", dataset.Config.Accounts, group.Index, task.MessageIndex),
					})
				})
				if err != nil {
					return fmt.Errorf("group %d message %d: %w", group.Index, task.MessageIndex, err)
				}
				observation, err := sentMessageObservation(updates, clientPeerKey{typ: "channel", id: channel.ChannelID}, marker)
				if err != nil {
					return fmt.Errorf("group %d message %d: %w", group.Index, task.MessageIndex, err)
				}
				if err := journal.commitChannelMessage(task.PlanPosition, task.MessageIndex, observation.ID, observation.Pts); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}

	// The first dirty channel deliberately exceeds the page limit and owns the
	// edit, delete and pin events. The creator authors the edited message and can
	// administratively delete/pin the two newest messages in one session.
	if len(plan) == 0 || plan[0].Messages < 120 {
		return nil, errors.New("offline mutation plan has no multi-page channel")
	}
	actionGroup := dataset.Groups[plan[0].GroupPosition]
	if err := runSeedAccountPhase(ctx, "mutate-actions", []int{actionGroup.CreatorAccount}, 1, mutationProgressAdapter("actions", progress), func(ctx context.Context, account int) error {
		return withAuthorizedSeedSession(ctx, SeedConfig{ManifestPath: cfg.ManifestPath, OperationTimeout: cfg.OperationTimeout}, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
			return applyOfflineChannelActions(ctx, cfg.OperationTimeout, dataset, seedState, plan, journal, raw)
		})
	}); err != nil {
		return nil, err
	}
	if err := journal.assertComplete(); err != nil {
		return nil, err
	}
	return offlineMutationResult(plan, state), nil
}

func planOfflineMutation(dataset *Dataset) []OfflineMutationChannelPlan {
	plan := make([]OfflineMutationChannelPlan, 0, 40)
	counts := map[string]int{"hot": 10, "medium": 10, "small": 10, "heavy": 10}
	messages := map[string]int{"hot": 3, "medium": 2, "small": 1, "heavy": 3}
	seen := make(map[string]int)
	for position, group := range dataset.Groups {
		if seen[group.Tier] >= counts[group.Tier] {
			continue
		}
		count := messages[group.Tier]
		switch len(plan) {
		case 0:
			count = 120
		case 1:
			// Exactly one full page complements the first channel's >limit
			// channelDifferenceTooLong snapshot path.
			count = 100
		}
		plan = append(plan, OfflineMutationChannelPlan{GroupPosition: position, Messages: count})
		seen[group.Tier]++
	}
	if len(plan) == 0 && len(dataset.Groups) != 0 {
		plan = append(plan, OfflineMutationChannelPlan{GroupPosition: 0, Messages: 120})
	}
	return plan
}

type offlineChannelTask struct {
	PlanPosition int
	MessageIndex int
}

func offlineChannelTasks(dataset *Dataset, plan []OfflineMutationChannelPlan) map[int][]offlineChannelTask {
	tasks := make(map[int][]offlineChannelTask)
	for planPosition, channelPlan := range plan {
		group := dataset.Groups[channelPlan.GroupPosition]
		for message := 0; message < channelPlan.Messages; message++ {
			account := offlineMutationSender(group, message)
			tasks[account] = append(tasks[account], offlineChannelTask{PlanPosition: planPosition, MessageIndex: message})
		}
	}
	return tasks
}

func offlineMutationSender(group DatasetGroup, message int) int {
	if message < 3 {
		return group.CreatorAccount
	}
	return group.MemberAccounts[message%len(group.MemberAccounts)]
}

func offlinePrivateMarker(dataset *Dataset, sender, recipient int) string {
	return fmt.Sprintf("[%s offline private %04d->%04d]", dataset.RunID, sender, recipient)
}

func offlineChannelMarker(dataset *Dataset, group DatasetGroup, message int) string {
	return fmt.Sprintf("[%s offline channel %04d message %04d]", dataset.RunID, group.Index, message+1)
}

type messageObservation struct {
	ID  int
	Pts int
}

func sentMessageObservation(updates tg.UpdatesClass, peer clientPeerKey, marker string) (messageObservation, error) {
	switch value := updates.(type) {
	case *tg.UpdateShortSentMessage:
		if value.ID > 0 {
			return messageObservation{ID: value.ID, Pts: value.Pts}, nil
		}
	case *tg.UpdateShortMessage:
		if value.ID > 0 && value.Message == marker {
			return messageObservation{ID: value.ID, Pts: value.Pts}, nil
		}
	case *tg.Updates:
		return sentMessageObservationFromUpdates(value.Updates, peer, marker)
	case *tg.UpdatesCombined:
		return sentMessageObservationFromUpdates(value.Updates, peer, marker)
	}
	return messageObservation{}, fmt.Errorf("messages.sendMessage returned %T without marker", updates)
}

func sentMessageObservationFromUpdates(updates []tg.UpdateClass, peer clientPeerKey, marker string) (messageObservation, error) {
	for _, update := range updates {
		var message tg.MessageClass
		pts := 0
		switch value := update.(type) {
		case *tg.UpdateNewMessage:
			message, pts = value.Message, value.Pts
		case *tg.UpdateNewChannelMessage:
			message, pts = value.Message, value.Pts
		default:
			continue
		}
		full, ok := message.(*tg.Message)
		if !ok || full.Message != marker {
			continue
		}
		messagePeer, ok := clientPeerFromTG(full.PeerID)
		if ok && messagePeer == peer && full.ID > 0 {
			return messageObservation{ID: full.ID, Pts: pts}, nil
		}
	}
	return messageObservation{}, errors.New("messages.sendMessage updates omitted expected marker")
}

func applyOfflineChannelActions(
	ctx context.Context,
	timeout time.Duration,
	dataset *Dataset,
	seedState *DatasetSeedState,
	plan []OfflineMutationChannelPlan,
	journal *mutationJournal,
	raw *tg.Client,
) error {
	channelPlan := plan[0]
	group := dataset.Groups[channelPlan.GroupPosition]
	channel := seedState.Groups[channelPlan.GroupPosition]
	state := journal.channel(0)
	deleteIndex, pinIndex := channelPlan.Messages-2, channelPlan.Messages-1
	if state.MessageIDs[0] == 0 || state.MessageIDs[deleteIndex] == 0 || state.MessageIDs[pinIndex] == 0 {
		return errors.New("channel action messages are incomplete")
	}
	peer := &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}
	inputChannel := &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}

	if !state.EditDone {
		if !state.EditPending {
			if err := journal.beginAction(0, "edit"); err != nil {
				return err
			}
		}
		editedMarker := offlineChannelMarker(dataset, group, 0) + " edited"
		updates, err := rpcWithFloodWaitRetry(ctx, timeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
			return raw.MessagesEditMessage(rpcCtx, &tg.MessagesEditMessageRequest{Peer: peer, ID: state.MessageIDs[0], Message: editedMarker})
		})
		pts := maxPtsFromUpdates(updates)
		if err != nil {
			if !tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
				return fmt.Errorf("messages.editMessage pending reconciliation: %w", err)
			}
			matches, verifyErr := channelMessageMatches(ctx, timeout, raw, inputChannel, state.MessageIDs[0], editedMarker)
			if verifyErr != nil || !matches {
				return fmt.Errorf("reconcile messages.editMessage: matched=%v err=%w", matches, verifyErr)
			}
		}
		if err := journal.commitAction(0, "edit", pts); err != nil {
			return err
		}
		state = journal.channel(0)
	}
	if !state.DeleteDone {
		if !state.DeletePending {
			if err := journal.beginAction(0, "delete"); err != nil {
				return err
			}
		}
		affected, err := rpcWithFloodWaitRetry(ctx, timeout, func(rpcCtx context.Context) (*tg.MessagesAffectedMessages, error) {
			return raw.ChannelsDeleteMessages(rpcCtx, &tg.ChannelsDeleteMessagesRequest{Channel: inputChannel, ID: []int{state.MessageIDs[deleteIndex]}})
		})
		pts := 0
		if affected != nil {
			pts = affected.Pts
		}
		if err != nil {
			deleted, verifyErr := channelMessageDeleted(ctx, timeout, raw, inputChannel, state.MessageIDs[deleteIndex])
			if verifyErr != nil || !deleted {
				return fmt.Errorf("reconcile channels.deleteMessages: deleted=%v err=%w (rpc %v)", deleted, verifyErr, err)
			}
		}
		if err := journal.commitAction(0, "delete", pts); err != nil {
			return err
		}
		state = journal.channel(0)
	}
	if !state.PinDone {
		if !state.PinPending {
			if err := journal.beginAction(0, "pin"); err != nil {
				return err
			}
		}
		updates, err := rpcWithFloodWaitRetry(ctx, timeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
			return raw.MessagesUpdatePinnedMessage(rpcCtx, &tg.MessagesUpdatePinnedMessageRequest{Silent: true, Peer: peer, ID: state.MessageIDs[pinIndex]})
		})
		pts := maxPtsFromUpdates(updates)
		if err != nil {
			pinned, verifyErr := channelMessagePinned(ctx, timeout, raw, inputChannel, state.MessageIDs[pinIndex])
			if verifyErr != nil || !pinned {
				return fmt.Errorf("reconcile messages.updatePinnedMessage: pinned=%v err=%w (rpc %v)", pinned, verifyErr, err)
			}
		}
		if err := journal.commitAction(0, "pin", pts); err != nil {
			return err
		}
	}
	return nil
}

func channelMessageMatches(ctx context.Context, timeout time.Duration, raw *tg.Client, channel *tg.InputChannel, messageID int, text string) (bool, error) {
	messages, err := getChannelMessages(ctx, timeout, raw, channel, messageID)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if full, ok := message.(*tg.Message); ok && full.ID == messageID {
			return full.Message == text, nil
		}
	}
	return false, nil
}

func channelMessageDeleted(ctx context.Context, timeout time.Duration, raw *tg.Client, channel *tg.InputChannel, messageID int) (bool, error) {
	messages, err := getChannelMessages(ctx, timeout, raw, channel, messageID)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if full, ok := message.(*tg.Message); ok && full.ID == messageID {
			return false, nil
		}
	}
	return true, nil
}

func getChannelMessages(ctx context.Context, timeout time.Duration, raw *tg.Client, channel *tg.InputChannel, messageID int) ([]tg.MessageClass, error) {
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	response, err := raw.ChannelsGetMessages(rpcCtx, &tg.ChannelsGetMessagesRequest{
		Channel: channel, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	cancel()
	if err != nil {
		return nil, err
	}
	modified, ok := response.AsModified()
	if !ok {
		return nil, fmt.Errorf("channels.getMessages returned %T", response)
	}
	return modified.GetMessages(), nil
}

func channelMessagePinned(ctx context.Context, timeout time.Duration, raw *tg.Client, channel *tg.InputChannel, messageID int) (bool, error) {
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	response, err := raw.ChannelsGetFullChannel(rpcCtx, channel)
	cancel()
	if err != nil {
		return false, err
	}
	full, ok := response.FullChat.(*tg.ChannelFull)
	if !ok {
		return false, fmt.Errorf("channels.getFullChannel returned %T", response.FullChat)
	}
	pinned, ok := full.GetPinnedMsgID()
	return ok && pinned == messageID, nil
}

func maxPtsFromUpdates(updates tg.UpdatesClass) int {
	if updates == nil {
		return 0
	}
	maxPts := 0
	var classes []tg.UpdateClass
	switch value := updates.(type) {
	case *tg.UpdateShortSentMessage:
		return value.Pts
	case *tg.UpdateShortMessage:
		return value.Pts
	case *tg.Updates:
		classes = value.Updates
	case *tg.UpdatesCombined:
		classes = value.Updates
	}
	for _, class := range classes {
		switch value := class.(type) {
		case *tg.UpdateNewMessage:
			maxPts = max(maxPts, value.Pts)
		case *tg.UpdateNewChannelMessage:
			maxPts = max(maxPts, value.Pts)
		case *tg.UpdateEditChannelMessage:
			maxPts = max(maxPts, value.Pts)
		case *tg.UpdateDeleteChannelMessages:
			maxPts = max(maxPts, value.Pts)
		case *tg.UpdatePinnedChannelMessages:
			maxPts = max(maxPts, value.Pts)
		}
	}
	return maxPts
}

func mutationProgressAdapter(phase string, progress func(MutationEvent)) func(SeedEvent) {
	if progress == nil {
		return nil
	}
	return func(event SeedEvent) {
		progress(MutationEvent{Phase: phase, Completed: event.Completed, Total: event.Total, Account: event.Account, Err: event.Err})
	}
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func loadOrCreateOfflineMutationState(
	path string,
	dataset *Dataset,
	seedIdentity, baselineSHA string,
	plan []OfflineMutationChannelPlan,
) (*OfflineMutationState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		state := &OfflineMutationState{
			Version: OfflineMutationVersion, DatasetSHA256: dataset.PlanSHA256,
			SeedIdentitySHA: seedIdentity, BaselineStateSHA: baselineSHA,
			PrivateMessageIDs: make([]int, dataset.Config.Accounts), AccountObservedPts: make([]int, dataset.Config.Accounts),
			Channels: make([]OfflineMutationChannelState, len(plan)),
		}
		for i, channelPlan := range plan {
			state.Channels[i] = OfflineMutationChannelState{
				GroupIndex: dataset.Groups[channelPlan.GroupPosition].Index,
				MessageIDs: make([]int, channelPlan.Messages),
			}
		}
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	var state OfflineMutationState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode offline mutation state: %w", err)
	}
	if err := state.Validate(dataset, seedIdentity, baselineSHA, plan); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *OfflineMutationState) Validate(dataset *Dataset, seedIdentity, baselineSHA string, plan []OfflineMutationChannelPlan) error {
	if s == nil || s.Version != OfflineMutationVersion || s.DatasetSHA256 != dataset.PlanSHA256 || s.SeedIdentitySHA != seedIdentity || s.BaselineStateSHA != baselineSHA {
		return errors.New("offline mutation state does not match baseline dataset")
	}
	if len(s.PrivateMessageIDs) != dataset.Config.Accounts || len(s.AccountObservedPts) != dataset.Config.Accounts || len(s.Channels) != len(plan) {
		return errors.New("offline mutation state dimensions do not match plan")
	}
	for account := range s.PrivateMessageIDs {
		if s.PrivateMessageIDs[account] < 0 || s.AccountObservedPts[account] < 0 {
			return fmt.Errorf("invalid offline private mutation account %d", account)
		}
	}
	for i, channel := range s.Channels {
		if channel.GroupIndex != dataset.Groups[plan[i].GroupPosition].Index || len(channel.MessageIDs) != plan[i].Messages || channel.LatestPts < 0 {
			return fmt.Errorf("invalid offline mutation channel %d", channel.GroupIndex)
		}
		for _, messageID := range channel.MessageIDs {
			if messageID < 0 {
				return fmt.Errorf("offline mutation channel %d has invalid message id", channel.GroupIndex)
			}
		}
		if (channel.EditDone && channel.EditPending) || (channel.DeleteDone && channel.DeletePending) || (channel.PinDone && channel.PinPending) {
			return fmt.Errorf("offline mutation channel %d has completed pending action", channel.GroupIndex)
		}
	}
	return nil
}

type mutationJournal struct {
	mu      sync.Mutex
	path    string
	dataset *Dataset
	plan    []OfflineMutationChannelPlan
	state   *OfflineMutationState
}

func (j *mutationJournal) persistLocked() error {
	if err := j.state.Validate(j.dataset, j.state.SeedIdentitySHA, j.state.BaselineStateSHA, j.plan); err != nil {
		return err
	}
	j.state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(j.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(j.path, append(data, '\n'), 0o600)
}

func (j *mutationJournal) persist() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.persistLocked()
}

func (j *mutationJournal) privateMessageID(account int) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.PrivateMessageIDs[account]
}

func (j *mutationJournal) channelMessageID(planPosition, message int) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.Channels[planPosition].MessageIDs[message]
}

func (j *mutationJournal) channel(planPosition int) OfflineMutationChannelState {
	j.mu.Lock()
	defer j.mu.Unlock()
	state := j.state.Channels[planPosition]
	state.MessageIDs = append([]int(nil), state.MessageIDs...)
	return state
}

func (j *mutationJournal) commitPrivate(account, messageID, pts int) error {
	if messageID <= 0 || pts < 0 {
		return errors.New("invalid private mutation observation")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	oldID, oldPts := j.state.PrivateMessageIDs[account], j.state.AccountObservedPts[account]
	j.state.PrivateMessageIDs[account] = messageID
	j.state.AccountObservedPts[account] = max(oldPts, pts)
	if err := j.persistLocked(); err != nil {
		j.state.PrivateMessageIDs[account], j.state.AccountObservedPts[account] = oldID, oldPts
		return err
	}
	return nil
}

func (j *mutationJournal) commitChannelMessage(planPosition, message, messageID, pts int) error {
	if messageID <= 0 || pts <= 0 {
		return errors.New("invalid channel mutation observation")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	channel := &j.state.Channels[planPosition]
	oldID, oldPts := channel.MessageIDs[message], channel.LatestPts
	channel.MessageIDs[message] = messageID
	channel.LatestPts = max(channel.LatestPts, pts)
	if err := j.persistLocked(); err != nil {
		channel.MessageIDs[message], channel.LatestPts = oldID, oldPts
		return err
	}
	return nil
}

func (j *mutationJournal) beginAction(planPosition int, action string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	channel := &j.state.Channels[planPosition]
	old := *channel
	switch action {
	case "edit":
		channel.EditPending = true
	case "delete":
		channel.DeletePending = true
	case "pin":
		channel.PinPending = true
	default:
		return fmt.Errorf("unknown mutation action %q", action)
	}
	if err := j.persistLocked(); err != nil {
		*channel = old
		return err
	}
	return nil
}

func (j *mutationJournal) commitAction(planPosition int, action string, pts int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	channel := &j.state.Channels[planPosition]
	old := *channel
	switch action {
	case "edit":
		channel.EditPending, channel.EditDone = false, true
	case "delete":
		channel.DeletePending, channel.DeleteDone = false, true
	case "pin":
		channel.PinPending, channel.PinDone = false, true
	default:
		return fmt.Errorf("unknown mutation action %q", action)
	}
	channel.LatestPts = max(channel.LatestPts, pts)
	if err := j.persistLocked(); err != nil {
		*channel = old
		return err
	}
	return nil
}

func (j *mutationJournal) assertComplete() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.state.Validate(j.dataset, j.state.SeedIdentitySHA, j.state.BaselineStateSHA, j.plan); err != nil {
		return err
	}
	for account, messageID := range j.state.PrivateMessageIDs {
		if messageID <= 0 {
			return fmt.Errorf("offline private mutation account %d is incomplete", account)
		}
	}
	for i, channel := range j.state.Channels {
		for message, messageID := range channel.MessageIDs {
			if messageID <= 0 {
				return fmt.Errorf("offline channel %d message %d is incomplete", channel.GroupIndex, message)
			}
		}
		if channel.LatestPts <= 0 {
			return fmt.Errorf("offline channel %d has no observed pts", channel.GroupIndex)
		}
		if i == 0 && (!channel.EditDone || !channel.DeleteDone || !channel.PinDone) {
			return fmt.Errorf("offline channel %d actions are incomplete", channel.GroupIndex)
		}
	}
	return nil
}

func offlineMutationResult(plan []OfflineMutationChannelPlan, state *OfflineMutationState) *MutationResult {
	result := &MutationResult{PrivateMessages: len(state.PrivateMessageIDs), DirtyChannels: len(plan)}
	for _, channel := range state.Channels {
		result.ChannelMessages += len(channel.MessageIDs)
		if channel.EditDone {
			result.Edited++
		}
		if channel.DeleteDone {
			result.Deleted++
		}
		if channel.PinDone {
			result.Pinned++
		}
	}
	return result
}
