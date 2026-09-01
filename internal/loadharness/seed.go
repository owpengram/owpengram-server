package loadharness

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
)

const maxSeedInviteBatch = 200

type SeedConfig struct {
	ManifestPath     string
	SessionKeyPath   string
	RSAKeyOverride   string
	DatasetPath      string
	SeedStatePath    string
	Concurrency      int
	OperationTimeout time.Duration
}

type SeedEvent struct {
	Phase     string
	Completed int
	Total     int
	Account   int
	Err       error
}

type SeedResult struct {
	PrivateMessages   int
	Groups            int
	InvitedMembers    int
	GroupMessages     int
	RichStateAccounts int
}

func (c SeedConfig) validate() error {
	if c.ManifestPath == "" || c.SessionKeyPath == "" || c.DatasetPath == "" || c.SeedStatePath == "" {
		return errors.New("manifest, session-key, dataset and seed-state paths are required")
	}
	if c.Concurrency <= 0 || c.Concurrency > 64 {
		return errors.New("seed concurrency must be between 1 and 64")
	}
	if c.OperationTimeout <= 0 {
		return errors.New("seed operation timeout must be positive")
	}
	return nil
}

// Seed materializes a Dataset only through authenticated MTProto RPCs. It never
// calls a server-internal handler or store. The journal is persisted before any
// non-idempotent channel operation, so an interrupted run can reconcile using
// the same account's public RPC view before it proceeds.
func Seed(ctx context.Context, cfg SeedConfig, progress func(SeedEvent)) (*SeedResult, error) {
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
	key, err := LoadSessionKey(cfg.SessionKeyPath)
	if err != nil {
		return nil, err
	}
	publicKey, err := loadManifestPublicKey(cfg.ManifestPath, manifest.Endpoint, cfg.RSAKeyOverride)
	if err != nil {
		return nil, err
	}
	state, err := LoadDatasetSeedState(cfg.SeedStatePath, dataset)
	if err != nil {
		return nil, err
	}
	journal := &seedJournal{path: cfg.SeedStatePath, dataset: dataset, state: state}
	if err := journal.enableRichState(); err != nil {
		return nil, err
	}
	if err := journal.persist(); err != nil {
		return nil, err
	}

	accounts := make([]int, dataset.Config.Accounts)
	for account := range accounts {
		accounts[account] = account
	}
	if err := runSeedAccountPhase(ctx, "private", accounts, cfg.Concurrency, progress, func(ctx context.Context, account int) error {
		return seedPrivateMessages(ctx, cfg, manifest, dataset, journal, targets, key, publicKey, account)
	}); err != nil {
		return nil, err
	}

	groupsByCreator := make(map[int][]int)
	for position, group := range dataset.Groups {
		groupsByCreator[group.CreatorAccount] = append(groupsByCreator[group.CreatorAccount], position)
	}
	creators := make([]int, 0, len(groupsByCreator))
	for account := range groupsByCreator {
		creators = append(creators, account)
	}
	sort.Ints(creators)
	if err := runSeedAccountPhase(ctx, "groups", creators, cfg.Concurrency, progress, func(ctx context.Context, account int) error {
		return withAuthorizedSeedSession(ctx, cfg, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
			for _, position := range groupsByCreator[account] {
				if err := seedGroup(ctx, cfg, dataset, journal, targets, raw, position); err != nil {
					return fmt.Errorf("group %d: %w", dataset.Groups[position].Index, err)
				}
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}

	historyTasks := datasetHistoryTasks(dataset)
	if err := runSeedAccountPhase(ctx, "group-history", accounts, cfg.Concurrency, progress, func(ctx context.Context, account int) error {
		return seedGroupHistory(ctx, cfg, manifest, dataset, journal, targets, key, publicKey, account, historyTasks[account])
	}); err != nil {
		return nil, err
	}
	if err := runSeedAccountPhase(ctx, "rich-state", accounts, cfg.Concurrency, progress, func(ctx context.Context, account int) error {
		return seedRichAccountState(ctx, cfg, manifest, dataset, journal, targets, key, publicKey, account)
	}); err != nil {
		return nil, err
	}
	if err := journal.assertComplete(); err != nil {
		return nil, err
	}

	result := &SeedResult{PrivateMessages: len(dataset.PrivateEdges), Groups: len(dataset.Groups), RichStateAccounts: dataset.Config.Accounts}
	for _, group := range dataset.Groups {
		result.InvitedMembers += len(group.MemberAccounts) - 1
		result.GroupMessages += group.HistoryMessages
	}
	return result, nil
}

func seedPrimaryTargets(manifest *Manifest, accounts int) ([]SessionRecord, error) {
	targets := primaryTargets(manifest.Sessions)
	if len(targets) < accounts {
		return nil, fmt.Errorf("dataset requires %d primary accounts, manifest has account range 0..%d", accounts, len(targets)-1)
	}
	targets = targets[:accounts]
	for account, target := range targets {
		if target.AccountIndex != account || target.UserID <= 0 || target.AccessHash == 0 || target.SessionFile == "" {
			return nil, fmt.Errorf("manifest has no complete primary session for account %d", account)
		}
	}
	return targets, nil
}

func seedPrivateMessages(
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
	cursor := journal.privateCursor(account)
	if cursor == dataset.Config.PrivateFanout {
		return nil
	}
	start := account * dataset.Config.PrivateFanout
	edges := dataset.PrivateEdges[start : start+dataset.Config.PrivateFanout]
	return withAuthorizedSeedSession(ctx, cfg, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
		for i := cursor; i < len(edges); i++ {
			edge := edges[i]
			target := targets[edge.RecipientAccount]
			_, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
				return raw.MessagesSendMessage(rpcCtx, &tg.MessagesSendMessageRequest{
					Peer:    &tg.InputPeerUser{UserID: target.UserID, AccessHash: target.AccessHash},
					Message: edge.Marker, RandomID: edge.RandomID,
				})
			})
			if err != nil {
				return fmt.Errorf("messages.sendMessage edge %d: %w", i, err)
			}
		}
		return journal.setPrivateCursor(account, len(edges))
	})
}

func seedGroup(
	ctx context.Context,
	cfg SeedConfig,
	dataset *Dataset,
	journal *seedJournal,
	targets []SessionRecord,
	raw *tg.Client,
	position int,
) error {
	group := dataset.Groups[position]
	groupState := journal.group(position)
	if groupState.ChannelID == 0 && groupState.CreatePending {
		channel, found, err := reconcilePendingChannelCreate(ctx, cfg.OperationTimeout, raw, group)
		if err != nil {
			return err
		}
		if found {
			if err := journal.commitChannel(position, channel.ID, channel.AccessHash); err != nil {
				return err
			}
		} else if err := journal.clearCreatePending(position); err != nil {
			return err
		}
		groupState = journal.group(position)
	}
	if groupState.ChannelID == 0 {
		if err := journal.beginCreate(position); err != nil {
			return err
		}
		updates, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
			return raw.ChannelsCreateChannel(rpcCtx, &tg.ChannelsCreateChannelRequest{
				Megagroup: true, Title: group.Title, About: group.About,
			})
		})
		if err != nil {
			return fmt.Errorf("channels.createChannel pending reconciliation: %w", err)
		}
		channel, err := createdChannelFromUpdates(updates, group.Title)
		if err != nil {
			return err
		}
		if err := journal.commitChannel(position, channel.ID, channel.AccessHash); err != nil {
			return err
		}
		groupState = journal.group(position)
	}

	invitees := groupInvitees(group)
	for groupState.InviteCursor < len(invitees) {
		if groupState.InvitePendingEnd > groupState.InviteCursor {
			if err := reconcilePendingInvites(ctx, cfg.OperationTimeout, raw, groupState, invitees, targets); err != nil {
				return err
			}
			if err := journal.commitInvite(position, groupState.InvitePendingEnd); err != nil {
				return err
			}
			groupState = journal.group(position)
			continue
		}
		end := min(groupState.InviteCursor+maxSeedInviteBatch, len(invitees))
		if err := journal.beginInvite(position, end); err != nil {
			return err
		}
		if err := inviteAccounts(ctx, cfg.OperationTimeout, raw, groupState, invitees[groupState.InviteCursor:end], targets); err != nil {
			return fmt.Errorf("channels.inviteToChannel pending reconciliation: %w", err)
		}
		if err := journal.commitInvite(position, end); err != nil {
			return err
		}
		groupState = journal.group(position)
	}
	return nil
}

func reconcilePendingChannelCreate(ctx context.Context, timeout time.Duration, raw *tg.Client, group DatasetGroup) (*tg.Channel, bool, error) {
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	dialogs, err := raw.MessagesGetDialogs(rpcCtx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{}, Limit: 500,
	})
	cancel()
	if err != nil {
		return nil, false, fmt.Errorf("reconcile create with messages.getDialogs: %w", err)
	}
	var chats []tg.ChatClass
	switch value := dialogs.(type) {
	case *tg.MessagesDialogs:
		chats = value.Chats
	case *tg.MessagesDialogsSlice:
		chats = value.Chats
	case *tg.MessagesDialogsNotModified:
		return nil, false, errors.New("reconcile create unexpectedly returned dialogsNotModified")
	default:
		return nil, false, fmt.Errorf("reconcile create messages.getDialogs returned %T", dialogs)
	}
	matches := make([]*tg.Channel, 0, 1)
	for _, chat := range chats {
		channel, ok := chat.(*tg.Channel)
		if !ok || channel.Title != group.Title || !channel.Megagroup {
			continue
		}
		if channel.ID <= 0 || channel.AccessHash == 0 {
			return nil, false, fmt.Errorf("reconciled channel %q has incomplete identity", group.Title)
		}
		matches = append(matches, channel)
	}
	if len(matches) > 1 {
		return nil, false, fmt.Errorf("ambiguous create produced %d channels named %q", len(matches), group.Title)
	}
	if len(matches) == 0 {
		return nil, false, nil
	}
	return matches[0], true, nil
}

func createdChannelFromUpdates(updates tg.UpdatesClass, title string) (*tg.Channel, error) {
	var chats []tg.ChatClass
	switch value := updates.(type) {
	case *tg.Updates:
		chats = value.Chats
	case *tg.UpdatesCombined:
		chats = value.Chats
	default:
		return nil, fmt.Errorf("channels.createChannel returned %T", updates)
	}
	for _, chat := range chats {
		channel, ok := chat.(*tg.Channel)
		if ok && channel.Title == title && channel.Megagroup && channel.ID > 0 && channel.AccessHash != 0 {
			return channel, nil
		}
	}
	return nil, fmt.Errorf("channels.createChannel response omitted supergroup %q", title)
}

func groupInvitees(group DatasetGroup) []int {
	invitees := make([]int, 0, len(group.MemberAccounts)-1)
	for _, account := range group.MemberAccounts {
		if account != group.CreatorAccount {
			invitees = append(invitees, account)
		}
	}
	return invitees
}

func inviteAccounts(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	groupState DatasetSeedGroupState,
	accounts []int,
	targets []SessionRecord,
) error {
	if len(accounts) == 0 || len(accounts) > maxSeedInviteBatch {
		return fmt.Errorf("invalid invite batch size %d", len(accounts))
	}
	users := make([]tg.InputUserClass, 0, len(accounts))
	for _, account := range accounts {
		target := targets[account]
		users = append(users, &tg.InputUser{UserID: target.UserID, AccessHash: target.AccessHash})
	}
	result, err := rpcWithFloodWaitRetry(ctx, timeout, func(rpcCtx context.Context) (*tg.MessagesInvitedUsers, error) {
		return raw.ChannelsInviteToChannel(rpcCtx, &tg.ChannelsInviteToChannelRequest{
			Channel: &tg.InputChannel{ChannelID: groupState.ChannelID, AccessHash: groupState.AccessHash},
			Users:   users,
		})
	})
	if err != nil {
		return err
	}
	if len(result.MissingInvitees) != 0 {
		return fmt.Errorf("server reported %d missing_invitees", len(result.MissingInvitees))
	}
	return nil
}

func reconcilePendingInvites(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	groupState DatasetSeedGroupState,
	invitees []int,
	targets []SessionRecord,
) error {
	pending := invitees[groupState.InviteCursor:groupState.InvitePendingEnd]
	missing := make([]int, 0, len(pending))
	for _, account := range pending {
		target := targets[account]
		rpcCtx, cancel := context.WithTimeout(ctx, timeout)
		_, err := raw.ChannelsGetParticipant(rpcCtx, &tg.ChannelsGetParticipantRequest{
			Channel:     &tg.InputChannel{ChannelID: groupState.ChannelID, AccessHash: groupState.AccessHash},
			Participant: &tg.InputPeerUser{UserID: target.UserID, AccessHash: target.AccessHash},
		})
		cancel()
		switch {
		case err == nil:
		case tgerr.Is(err, "USER_NOT_PARTICIPANT"):
			missing = append(missing, account)
		default:
			return fmt.Errorf("channels.getParticipant account %d: %w", account, err)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return inviteAccounts(ctx, timeout, raw, groupState, missing, targets)
}

type datasetHistoryTask struct {
	GroupPosition int
	MessageIndex  int
}

func datasetHistoryTasks(dataset *Dataset) [][]datasetHistoryTask {
	tasks := make([][]datasetHistoryTask, dataset.Config.Accounts)
	for position, group := range dataset.Groups {
		for message := 0; message < group.HistoryMessages; message++ {
			account := group.MemberAccounts[message%len(group.MemberAccounts)]
			tasks[account] = append(tasks[account], datasetHistoryTask{GroupPosition: position, MessageIndex: message})
		}
	}
	return tasks
}

func seedGroupHistory(
	ctx context.Context,
	cfg SeedConfig,
	manifest *Manifest,
	dataset *Dataset,
	journal *seedJournal,
	targets []SessionRecord,
	key [32]byte,
	publicKey *rsa.PublicKey,
	account int,
	tasks []datasetHistoryTask,
) error {
	cursor := journal.historyCursor(account)
	if cursor == len(tasks) {
		return nil
	}
	return withAuthorizedSeedSession(ctx, cfg, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
		for i := cursor; i < len(tasks); i++ {
			task := tasks[i]
			group := dataset.Groups[task.GroupPosition]
			groupState := journal.group(task.GroupPosition)
			if groupState.ChannelID == 0 || groupState.InviteCursor != len(group.MemberAccounts)-1 {
				return fmt.Errorf("group %d is not fully seeded", group.Index)
			}
			_, err := rpcWithFloodWaitRetry(ctx, cfg.OperationTimeout, func(rpcCtx context.Context) (tg.UpdatesClass, error) {
				return raw.MessagesSendMessage(rpcCtx, &tg.MessagesSendMessageRequest{
					Peer:     &tg.InputPeerChannel{ChannelID: groupState.ChannelID, AccessHash: groupState.AccessHash},
					Message:  fmt.Sprintf("[%s group %04d message %04d sender %04d]", dataset.RunID, group.Index, task.MessageIndex+1, account),
					RandomID: stableDatasetID(dataset.Config.Seed, "group-history", dataset.Config.Accounts, group.Index, task.MessageIndex),
				})
			})
			if err != nil {
				return fmt.Errorf("messages.sendMessage group %d message %d: %w", group.Index, task.MessageIndex, err)
			}
		}
		return journal.setHistoryCursor(account, len(tasks))
	})
}

func withAuthorizedSeedSession(
	ctx context.Context,
	cfg SeedConfig,
	manifest *Manifest,
	record SessionRecord,
	key [32]byte,
	publicKey *rsa.PublicKey,
	work func(context.Context, *tg.Client) error,
) error {
	storage := &EncryptedFileStorage{Path: resolveSessionPath(cfg.ManifestPath, record), Key: key}
	client, err := newClient(manifest.Endpoint, publicKey, storage, clientHooks{})
	if err != nil {
		return err
	}
	return client.Run(ctx, func(ctx context.Context) error {
		statusCtx, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
		status, err := client.Auth().Status(statusCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("authorization status: %w", err)
		}
		if !status.Authorized || status.User == nil || status.User.ID != record.UserID {
			return fmt.Errorf("session account %d is not authorized as expected user", record.AccountIndex)
		}
		return work(ctx, tg.NewClient(client))
	})
}

type seedAccountResult struct {
	account int
	err     error
}

func runSeedAccountPhase(
	ctx context.Context,
	phase string,
	accounts []int,
	concurrency int,
	progress func(SeedEvent),
	work func(context.Context, int) error,
) error {
	phaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan seedAccountResult, len(accounts))
	workers := min(concurrency, len(accounts))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for account := range jobs {
				err := work(phaseCtx, account)
				results <- seedAccountResult{account: account, err: err}
				if err != nil {
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, account := range accounts {
			select {
			case jobs <- account:
			case <-phaseCtx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	completed := 0
	var firstErr error
	for result := range results {
		if result.err == nil {
			completed++
		} else if firstErr == nil {
			firstErr = fmt.Errorf("%s account %d: %w", phase, result.account, result.err)
			cancel()
		}
		if progress != nil {
			progress(SeedEvent{Phase: phase, Completed: completed, Total: len(accounts), Account: result.account, Err: result.err})
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if completed != len(accounts) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s completed %d/%d accounts", phase, completed, len(accounts))
	}
	return nil
}

type seedJournal struct {
	mu      sync.Mutex
	path    string
	dataset *Dataset
	state   *DatasetSeedState
}

func (j *seedJournal) persist() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return WriteDatasetSeedState(j.path, j.dataset, j.state)
}

func (j *seedJournal) privateCursor(account int) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.PrivateSentByAccount[account]
}

func (j *seedJournal) historyCursor(account int) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.HistorySentByAccount[account]
}

func (j *seedJournal) richStateComplete(account int) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.state.RichStateByAccount) == j.dataset.Config.Accounts && j.state.RichStateByAccount[account]
}

func (j *seedJournal) enableRichState() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.state.RichStateByAccount) == j.dataset.Config.Accounts {
		return nil
	}
	if len(j.state.RichStateByAccount) != 0 {
		return errors.New("seed rich-state journal has invalid dimensions")
	}
	j.state.RichStateByAccount = make([]bool, j.dataset.Config.Accounts)
	return WriteDatasetSeedState(j.path, j.dataset, j.state)
}

func (j *seedJournal) group(position int) DatasetSeedGroupState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.Groups[position]
}

func (j *seedJournal) setPrivateCursor(account, cursor int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	old := j.state.PrivateSentByAccount[account]
	j.state.PrivateSentByAccount[account] = cursor
	if err := WriteDatasetSeedState(j.path, j.dataset, j.state); err != nil {
		j.state.PrivateSentByAccount[account] = old
		return err
	}
	return nil
}

func (j *seedJournal) setHistoryCursor(account, cursor int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	old := j.state.HistorySentByAccount[account]
	j.state.HistorySentByAccount[account] = cursor
	if err := WriteDatasetSeedState(j.path, j.dataset, j.state); err != nil {
		j.state.HistorySentByAccount[account] = old
		return err
	}
	return nil
}

func (j *seedJournal) setRichStateComplete(account int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.state.RichStateByAccount) != j.dataset.Config.Accounts {
		return errors.New("seed rich-state journal is not enabled")
	}
	old := j.state.RichStateByAccount[account]
	j.state.RichStateByAccount[account] = true
	if err := WriteDatasetSeedState(j.path, j.dataset, j.state); err != nil {
		j.state.RichStateByAccount[account] = old
		return err
	}
	return nil
}

func (j *seedJournal) updateGroup(position int, update func(*DatasetSeedGroupState)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	old := j.state.Groups[position]
	update(&j.state.Groups[position])
	if err := WriteDatasetSeedState(j.path, j.dataset, j.state); err != nil {
		j.state.Groups[position] = old
		return err
	}
	return nil
}

func (j *seedJournal) beginCreate(position int) error {
	return j.updateGroup(position, func(state *DatasetSeedGroupState) { state.CreatePending = true })
}

func (j *seedJournal) clearCreatePending(position int) error {
	return j.updateGroup(position, func(state *DatasetSeedGroupState) { state.CreatePending = false })
}

func (j *seedJournal) commitChannel(position int, channelID, accessHash int64) error {
	if channelID <= 0 || accessHash == 0 {
		return errors.New("cannot commit incomplete channel identity")
	}
	return j.updateGroup(position, func(state *DatasetSeedGroupState) {
		state.ChannelID = channelID
		state.AccessHash = accessHash
		state.CreatePending = false
	})
}

func (j *seedJournal) beginInvite(position, end int) error {
	return j.updateGroup(position, func(state *DatasetSeedGroupState) { state.InvitePendingEnd = end })
}

func (j *seedJournal) commitInvite(position, end int) error {
	return j.updateGroup(position, func(state *DatasetSeedGroupState) {
		state.InviteCursor = end
		state.InvitePendingEnd = end
	})
}

func (j *seedJournal) assertComplete() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.state.Validate(j.dataset); err != nil {
		return err
	}
	historyCounts := datasetHistoryTaskCounts(j.dataset)
	for account := 0; account < j.dataset.Config.Accounts; account++ {
		if j.state.PrivateSentByAccount[account] != j.dataset.Config.PrivateFanout || j.state.HistorySentByAccount[account] != historyCounts[account] {
			return fmt.Errorf("account %d seed is incomplete", account)
		}
		if len(j.state.RichStateByAccount) != 0 && !j.state.RichStateByAccount[account] {
			return fmt.Errorf("account %d rich state is incomplete", account)
		}
	}
	for position, group := range j.dataset.Groups {
		state := j.state.Groups[position]
		if state.ChannelID == 0 || state.CreatePending || state.InviteCursor != len(group.MemberAccounts)-1 || state.InvitePendingEnd != state.InviteCursor {
			return fmt.Errorf("group %d seed is incomplete", group.Index)
		}
	}
	return nil
}
