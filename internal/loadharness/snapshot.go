package loadharness

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iamxvbaba/td/tg"
)

const ClientStateVersion = 1

type ClientUpdateState struct {
	Pts         int `json:"pts"`
	Qts         int `json:"qts"`
	Date        int `json:"date"`
	Seq         int `json:"seq"`
	UnreadCount int `json:"unread_count"`
}

type ClientDialogState struct {
	PeerType        string `json:"peer_type"`
	PeerID          int64  `json:"peer_id"`
	AccessHash      int64  `json:"access_hash"`
	TopMessage      int    `json:"top_message"`
	TopMessageDate  int    `json:"top_message_date"`
	Pts             int    `json:"pts,omitempty"`
	HasPts          bool   `json:"has_pts,omitempty"`
	ReadInboxMaxID  int    `json:"read_inbox_max_id"`
	ReadOutboxMaxID int    `json:"read_outbox_max_id"`
	UnreadCount     int    `json:"unread_count"`
	UnreadMentions  int    `json:"unread_mentions"`
	UnreadReactions int    `json:"unread_reactions"`
	Pinned          bool   `json:"pinned,omitempty"`
	HasDraft        bool   `json:"has_draft,omitempty"`
	DraftText       string `json:"draft_text,omitempty"`
	DatasetExpected bool   `json:"dataset_expected,omitempty"`
}

type ClientAccountState struct {
	AccountIndex int                 `json:"account_index"`
	UserID       int64               `json:"user_id"`
	State        ClientUpdateState   `json:"state"`
	Dialogs      []ClientDialogState `json:"dialogs"`
}

type ClientState struct {
	Version         int                  `json:"version"`
	DatasetSHA256   string               `json:"dataset_sha256"`
	SeedIdentitySHA string               `json:"seed_identity_sha256"`
	CreatedAt       time.Time            `json:"created_at"`
	Accounts        []ClientAccountState `json:"accounts"`
}

type SnapshotConfig struct {
	ManifestPath     string
	SessionKeyPath   string
	RSAKeyOverride   string
	DatasetPath      string
	SeedStatePath    string
	ClientStatePath  string
	Concurrency      int
	OperationTimeout time.Duration
}

type SnapshotEvent struct {
	Completed int
	Total     int
	Account   int
	Resumed   bool
	Err       error
}

type SnapshotResult struct {
	Accounts int
	Dialogs  int
	Channels int
}

func (c SnapshotConfig) validate() error {
	if c.ManifestPath == "" || c.SessionKeyPath == "" || c.DatasetPath == "" || c.SeedStatePath == "" || c.ClientStatePath == "" {
		return errors.New("manifest, session-key, dataset, seed-state and client-state paths are required")
	}
	if c.Concurrency <= 0 || c.Concurrency > 64 {
		return errors.New("snapshot concurrency must be between 1 and 64")
	}
	if c.OperationTimeout <= 0 {
		return errors.New("snapshot operation timeout must be positive")
	}
	return nil
}

// SnapshotClientState establishes the old client cursors that later offline
// mutations must advance from. Dialogs are read through public paginated RPCs;
// per-account part files make a 1,000-account snapshot safely resumable without
// rewriting the growing aggregate after every account.
func SnapshotClientState(ctx context.Context, cfg SnapshotConfig, progress func(SnapshotEvent)) (*SnapshotResult, error) {
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
		return nil, fmt.Errorf("snapshot requires a complete seed: %w", err)
	}
	seedIdentity, err := seedIdentitySHA256(seedState)
	if err != nil {
		return nil, err
	}
	if existing, loadErr := LoadClientState(cfg.ClientStatePath); loadErr == nil {
		if err := existing.Validate(dataset, seedState, targets); err != nil {
			return nil, fmt.Errorf("existing client state does not match requested dataset: %w", err)
		}
		return clientStateResult(existing), nil
	} else if !os.IsNotExist(loadErr) {
		return nil, loadErr
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
	resumed := make([]bool, len(accounts))
	for _, account := range accounts {
		if _, err := loadClientStatePart(clientStatePartPath(cfg.ClientStatePath, account), dataset, seedState, targets, account); err == nil {
			resumed[account] = true
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	completed := 0
	if err := runSeedAccountPhase(ctx, "snapshot", accounts, cfg.Concurrency, func(event SeedEvent) {
		if event.Err == nil {
			completed++
		}
		if progress != nil {
			progress(SnapshotEvent{Completed: completed, Total: len(accounts), Account: event.Account, Resumed: resumed[event.Account], Err: event.Err})
		}
	}, func(ctx context.Context, account int) error {
		if resumed[account] {
			return nil
		}
		state, err := snapshotAccount(ctx, cfg, manifest, dataset, seedState, targets, key, publicKey, account)
		if err != nil {
			return err
		}
		return writeClientStatePart(clientStatePartPath(cfg.ClientStatePath, account), dataset.PlanSHA256, seedIdentity, state)
	}); err != nil {
		return nil, err
	}

	clientState := &ClientState{
		Version: ClientStateVersion, DatasetSHA256: dataset.PlanSHA256, SeedIdentitySHA: seedIdentity, CreatedAt: time.Now().UTC(),
		Accounts: make([]ClientAccountState, 0, len(accounts)),
	}
	for _, account := range accounts {
		state, err := loadClientStatePart(clientStatePartPath(cfg.ClientStatePath, account), dataset, seedState, targets, account)
		if err != nil {
			return nil, err
		}
		clientState.Accounts = append(clientState.Accounts, *state)
	}
	if err := clientState.Validate(dataset, seedState, targets); err != nil {
		return nil, err
	}
	if err := WriteClientState(cfg.ClientStatePath, clientState); err != nil {
		return nil, err
	}
	return clientStateResult(clientState), nil
}

func snapshotAccount(
	ctx context.Context,
	cfg SnapshotConfig,
	manifest *Manifest,
	dataset *Dataset,
	seedState *DatasetSeedState,
	targets []SessionRecord,
	key [32]byte,
	publicKey *rsa.PublicKey,
	account int,
) (*ClientAccountState, error) {
	var result *ClientAccountState
	err := withAuthorizedSeedSession(ctx, SeedConfig{
		ManifestPath: cfg.ManifestPath, OperationTimeout: cfg.OperationTimeout,
	}, manifest, targets[account], key, publicKey, func(ctx context.Context, raw *tg.Client) error {
		dialogs, err := snapshotDialogs(ctx, cfg.OperationTimeout, raw)
		if err != nil {
			return err
		}
		expected := expectedDatasetPeers(dataset, seedState, targets, account)
		for i := range dialogs {
			_, dialogs[i].DatasetExpected = expected[clientPeerKey{typ: dialogs[i].PeerType, id: dialogs[i].PeerID}]
			if dialogs[i].DatasetExpected {
				delete(expected, clientPeerKey{typ: dialogs[i].PeerType, id: dialogs[i].PeerID})
			}
		}
		if len(expected) != 0 {
			missing := make([]string, 0, min(len(expected), 5))
			for peer := range expected {
				missing = append(missing, fmt.Sprintf("%s:%d", peer.typ, peer.id))
				if len(missing) == 5 {
					break
				}
			}
			sort.Strings(missing)
			return fmt.Errorf("messages.getDialogs omitted %d expected dataset peers (sample %s)", len(expected), strings.Join(missing, ","))
		}
		if err := validateSeededRichDialogs(dataset, seedState, targets, account, dialogs, true); err != nil {
			return fmt.Errorf("rich dialog state: %w", err)
		}
		stateCtx, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
		state, err := raw.UpdatesGetState(stateCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("updates.getState: %w", err)
		}
		result = &ClientAccountState{
			AccountIndex: account, UserID: targets[account].UserID,
			State:   ClientUpdateState{Pts: state.Pts, Qts: state.Qts, Date: state.Date, Seq: state.Seq, UnreadCount: state.UnreadCount},
			Dialogs: dialogs,
		}
		return nil
	})
	if err == nil && result == nil {
		return nil, errors.New("snapshot session ended without producing account state")
	}
	return result, err
}

type clientPeerKey struct {
	typ string
	id  int64
}

func snapshotDialogs(ctx context.Context, timeout time.Duration, raw *tg.Client) ([]ClientDialogState, error) {
	dialogs, _, err := snapshotDialogsObserved(ctx, timeout, raw, snapshotPaginationProfile, nil)
	return dialogs, err
}

func snapshotDialogsObserved(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	profile dialogPaginationProfile,
	observe func(string, time.Time, error),
) ([]ClientDialogState, StartupDialogsCounts, error) {
	if profile.FirstLimit <= 0 || profile.SubsequentLimit <= 0 {
		return nil, StartupDialogsCounts{}, errors.New("invalid dialogs pagination profile")
	}
	dialogsByPeer := make(map[clientPeerKey]ClientDialogState)
	counts := StartupDialogsCounts{}
	start := time.Now()
	pinnedCtx, cancel := context.WithTimeout(ctx, timeout)
	pinned, err := raw.MessagesGetPinnedDialogs(pinnedCtx, 0)
	cancel()
	if observe != nil {
		observe("messages.getPinnedDialogs", start, err)
	}
	counts.PinnedCalls++
	if err != nil {
		return nil, counts, fmt.Errorf("messages.getPinnedDialogs: %w", err)
	}
	if _, err := mergeDialogPage(dialogsByPeer, pinned.Dialogs, pinned.Messages, pinned.Chats, pinned.Users, true); err != nil {
		return nil, counts, err
	}
	pinnedPeers := make(map[clientPeerKey]struct{}, len(dialogsByPeer))
	for peer := range dialogsByPeer {
		pinnedPeers[peer] = struct{}{}
	}

	request := &tg.MessagesGetDialogsRequest{ExcludePinned: true, OffsetPeer: &tg.InputPeerEmpty{}, Limit: profile.limit(0)}
	for page := 0; page < 100; page++ {
		request.Limit = profile.limit(page)
		start := time.Now()
		rpcCtx, cancel := context.WithTimeout(ctx, timeout)
		response, err := raw.MessagesGetDialogs(rpcCtx, request)
		cancel()
		if observe != nil {
			observe("messages.getDialogs", start, err)
			if page == 0 {
				observe("messages.getDialogs.first", start, err)
			} else {
				observe("messages.getDialogs.next", start, err)
			}
		}
		counts.Calls++
		if err != nil {
			return nil, counts, fmt.Errorf("messages.getDialogs page %d: %w", page+1, err)
		}
		var pageDialogs []tg.DialogClass
		var messages []tg.MessageClass
		var chats []tg.ChatClass
		var users []tg.UserClass
		complete := false
		switch value := response.(type) {
		case *tg.MessagesDialogs:
			if observe != nil {
				observe("messages.getDialogs.full", start, nil)
			}
			counts.Full++
			pageDialogs, messages, chats, users, complete = value.Dialogs, value.Messages, value.Chats, value.Users, true
		case *tg.MessagesDialogsSlice:
			if observe != nil {
				observe("messages.getDialogs.slice", start, nil)
			}
			counts.Slice++
			pageDialogs, messages, chats, users = value.Dialogs, value.Messages, value.Chats, value.Users
		case *tg.MessagesDialogsNotModified:
			return nil, counts, errors.New("messages.getDialogs with hash=0 returned dialogsNotModified")
		default:
			return nil, counts, fmt.Errorf("messages.getDialogs returned %T", response)
		}
		last, overlap, err := mergeDialogPageKnownOverlap(dialogsByPeer, pageDialogs, messages, chats, users, false, pinnedPeers)
		counts.PinnedOverlap += overlap
		if err != nil {
			return nil, counts, fmt.Errorf("messages.getDialogs page %d: %w", page+1, err)
		}
		// A dialogsSlice is explicitly non-final. The server may enforce a
		// smaller per-page cap than the client-requested limit (TDesktop asks
		// for 500 after its first page while telesrv currently returns at most
		// 100). Only the full constructor or an empty slice proves completion;
		// treating a short slice as EOF truncates the real TDesktop workload.
		if dialogsPaginationDone(complete, len(pageDialogs)) {
			break
		}
		if last.TopMessage <= 0 || last.TopMessageDate <= 0 {
			return nil, counts, fmt.Errorf("messages.getDialogs page %d has no usable offset", page+1)
		}
		request.OffsetDate = last.TopMessageDate
		request.OffsetID = last.TopMessage
		request.OffsetPeer = clientDialogInputPeer(last)
		if request.OffsetPeer == nil {
			return nil, counts, fmt.Errorf("messages.getDialogs page %d has invalid offset peer", page+1)
		}
		if page == 99 {
			return nil, counts, errors.New("messages.getDialogs exceeded 100 pages")
		}
	}
	result := make([]ClientDialogState, 0, len(dialogsByPeer))
	for _, dialog := range dialogsByPeer {
		result = append(result, dialog)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PeerType != result[j].PeerType {
			return result[i].PeerType < result[j].PeerType
		}
		return result[i].PeerID < result[j].PeerID
	})
	counts.Dialogs = len(result)
	return result, counts, nil
}

func dialogsPaginationDone(complete bool, pageSize int) bool {
	return complete || pageSize == 0
}

func mergeDialogPage(
	destination map[clientPeerKey]ClientDialogState,
	dialogClasses []tg.DialogClass,
	messages []tg.MessageClass,
	chats []tg.ChatClass,
	users []tg.UserClass,
	pinnedPage bool,
) (ClientDialogState, error) {
	last, _, err := mergeDialogPageKnownOverlap(destination, dialogClasses, messages, chats, users, pinnedPage, nil)
	return last, err
}

func mergeDialogPageKnownOverlap(
	destination map[clientPeerKey]ClientDialogState,
	dialogClasses []tg.DialogClass,
	messages []tg.MessageClass,
	chats []tg.ChatClass,
	users []tg.UserClass,
	pinnedPage bool,
	allowedOverlap map[clientPeerKey]struct{},
) (ClientDialogState, int, error) {
	accessHashes := make(map[clientPeerKey]int64, len(chats)+len(users))
	for _, chat := range chats {
		channel, ok := chat.(*tg.Channel)
		if !ok {
			continue
		}
		hash, ok := channel.GetAccessHash()
		if ok {
			accessHashes[clientPeerKey{typ: "channel", id: channel.ID}] = hash
		}
	}
	for _, userClass := range users {
		user, ok := userClass.(*tg.User)
		if !ok {
			continue
		}
		hash, ok := user.GetAccessHash()
		if ok {
			accessHashes[clientPeerKey{typ: "user", id: user.ID}] = hash
		}
	}
	messageDates := make(map[string]int, len(messages))
	for _, messageClass := range messages {
		message, ok := messageClass.AsNotEmpty()
		if !ok {
			continue
		}
		peer, ok := clientPeerFromTG(message.GetPeerID())
		if !ok {
			continue
		}
		messageDates[clientMessageKey(peer, message.GetID())] = message.GetDate()
	}
	var last ClientDialogState
	overlaps := 0
	for _, dialogClass := range dialogClasses {
		dialog, ok := dialogClass.(*tg.Dialog)
		if !ok {
			continue
		}
		peer, ok := clientPeerFromTG(dialog.Peer)
		if !ok {
			continue
		}
		hash := accessHashes[peer]
		if hash == 0 {
			return ClientDialogState{}, overlaps, fmt.Errorf("dialog %s:%d omitted access hash", peer.typ, peer.id)
		}
		pts, hasPts := dialog.GetPts()
		if peer.typ == "channel" && (!hasPts || pts <= 0) {
			return ClientDialogState{}, overlaps, fmt.Errorf("channel dialog %d omitted pts", peer.id)
		}
		date := messageDates[clientMessageKey(peer, dialog.TopMessage)]
		if dialog.TopMessage <= 0 || date <= 0 {
			return ClientDialogState{}, overlaps, fmt.Errorf("dialog %s:%d omitted top message payload", peer.typ, peer.id)
		}
		state := ClientDialogState{
			PeerType: peer.typ, PeerID: peer.id, AccessHash: hash,
			TopMessage: dialog.TopMessage, TopMessageDate: date, Pts: pts, HasPts: hasPts,
			ReadInboxMaxID: dialog.ReadInboxMaxID, ReadOutboxMaxID: dialog.ReadOutboxMaxID,
			UnreadCount: dialog.UnreadCount, UnreadMentions: dialog.UnreadMentionsCount,
			UnreadReactions: dialog.UnreadReactionsCount, Pinned: pinnedPage || dialog.Pinned,
		}
		if draftClass, ok := dialog.GetDraft(); ok {
			if draft, ok := draftClass.(*tg.DraftMessage); ok && draft.Message != "" {
				state.HasDraft, state.DraftText = true, draft.Message
			}
		}
		if existing, exists := destination[peer]; exists {
			if _, allowed := allowedOverlap[peer]; !allowed || !existing.Pinned || !state.Pinned || existing.TopMessage != state.TopMessage || existing.AccessHash != state.AccessHash {
				return ClientDialogState{}, overlaps, fmt.Errorf("duplicate dialog %s:%d", peer.typ, peer.id)
			}
			delete(allowedOverlap, peer)
			if !state.HasDraft && existing.HasDraft {
				state.HasDraft, state.DraftText = existing.HasDraft, existing.DraftText
			}
			overlaps++
		}
		destination[peer] = state
		last = state
	}
	return last, overlaps, nil
}

func clientPeerFromTG(peer tg.PeerClass) (clientPeerKey, bool) {
	switch value := peer.(type) {
	case *tg.PeerUser:
		return clientPeerKey{typ: "user", id: value.UserID}, value.UserID > 0
	case *tg.PeerChannel:
		return clientPeerKey{typ: "channel", id: value.ChannelID}, value.ChannelID > 0
	default:
		return clientPeerKey{}, false
	}
}

func clientMessageKey(peer clientPeerKey, messageID int) string {
	return fmt.Sprintf("%s:%d:%d", peer.typ, peer.id, messageID)
}

func clientDialogInputPeer(dialog ClientDialogState) tg.InputPeerClass {
	switch dialog.PeerType {
	case "user":
		return &tg.InputPeerUser{UserID: dialog.PeerID, AccessHash: dialog.AccessHash}
	case "channel":
		return &tg.InputPeerChannel{ChannelID: dialog.PeerID, AccessHash: dialog.AccessHash}
	default:
		return nil
	}
}

func expectedDatasetPeers(dataset *Dataset, seedState *DatasetSeedState, targets []SessionRecord, account int) map[clientPeerKey]struct{} {
	expected := make(map[clientPeerKey]struct{})
	for _, edge := range dataset.PrivateEdges {
		switch account {
		case edge.SenderAccount:
			expected[clientPeerKey{typ: "user", id: targets[edge.RecipientAccount].UserID}] = struct{}{}
		case edge.RecipientAccount:
			expected[clientPeerKey{typ: "user", id: targets[edge.SenderAccount].UserID}] = struct{}{}
		}
	}
	for position, group := range dataset.Groups {
		memberIndex := sort.SearchInts(group.MemberAccounts, account)
		if memberIndex < len(group.MemberAccounts) && group.MemberAccounts[memberIndex] == account {
			expected[clientPeerKey{typ: "channel", id: seedState.Groups[position].ChannelID}] = struct{}{}
		}
	}
	return expected
}

func clientStatePartPath(clientStatePath string, account int) string {
	return filepath.Join(clientStatePath+".parts", fmt.Sprintf("account-%04d.json", account))
}

type clientStatePart struct {
	Version         int                `json:"version"`
	DatasetSHA256   string             `json:"dataset_sha256"`
	SeedIdentitySHA string             `json:"seed_identity_sha256"`
	Account         ClientAccountState `json:"account"`
}

func writeClientStatePart(path, datasetSHA, seedIdentity string, account *ClientAccountState) error {
	if account == nil {
		return errors.New("cannot write nil client account state")
	}
	part := clientStatePart{Version: ClientStateVersion, DatasetSHA256: datasetSHA, SeedIdentitySHA: seedIdentity, Account: *account}
	data, err := json.MarshalIndent(part, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func loadClientStatePart(path string, dataset *Dataset, seedState *DatasetSeedState, targets []SessionRecord, account int) (*ClientAccountState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var part clientStatePart
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&part); err != nil {
		return nil, fmt.Errorf("decode client state part: %w", err)
	}
	seedIdentity, err := seedIdentitySHA256(seedState)
	if err != nil {
		return nil, err
	}
	target := targets[account]
	if part.Version != ClientStateVersion || part.DatasetSHA256 != dataset.PlanSHA256 || part.SeedIdentitySHA != seedIdentity || part.Account.AccountIndex != target.AccountIndex || part.Account.UserID != target.UserID {
		return nil, errors.New("client state part does not match dataset or account")
	}
	if err := validateClientAccountState(&part.Account); err != nil {
		return nil, err
	}
	if err := validateExpectedDatasetPeers(dataset, seedState, targets, &part.Account); err != nil {
		return nil, err
	}
	return &part.Account, nil
}

func (s *ClientState) Validate(dataset *Dataset, seedState *DatasetSeedState, targets []SessionRecord) error {
	if s == nil || s.Version != ClientStateVersion || dataset == nil || s.DatasetSHA256 != dataset.PlanSHA256 {
		return errors.New("client state does not match dataset")
	}
	seedIdentity, err := seedIdentitySHA256(seedState)
	if err != nil {
		return err
	}
	if s.SeedIdentitySHA != seedIdentity {
		return errors.New("client state does not match seeded channel identities")
	}
	if len(s.Accounts) != dataset.Config.Accounts || len(targets) != dataset.Config.Accounts {
		return errors.New("client state account count does not match dataset")
	}
	for account := range s.Accounts {
		if s.Accounts[account].AccountIndex != account || s.Accounts[account].UserID != targets[account].UserID {
			return fmt.Errorf("client state account %d has wrong identity", account)
		}
		if err := validateClientAccountState(&s.Accounts[account]); err != nil {
			return fmt.Errorf("client state account %d: %w", account, err)
		}
		if err := validateExpectedDatasetPeers(dataset, seedState, targets, &s.Accounts[account]); err != nil {
			return fmt.Errorf("client state account %d: %w", account, err)
		}
	}
	return nil
}

func validateExpectedDatasetPeers(dataset *Dataset, seedState *DatasetSeedState, targets []SessionRecord, account *ClientAccountState) error {
	expected := expectedDatasetPeers(dataset, seedState, targets, account.AccountIndex)
	for _, dialog := range account.Dialogs {
		peer := clientPeerKey{typ: dialog.PeerType, id: dialog.PeerID}
		_, shouldBeExpected := expected[peer]
		if dialog.DatasetExpected != shouldBeExpected {
			return fmt.Errorf("dialog %s:%d has incorrect dataset_expected marker", peer.typ, peer.id)
		}
		if shouldBeExpected {
			delete(expected, peer)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("client state omits %d expected dataset peers", len(expected))
	}
	return nil
}

func seedIdentitySHA256(state *DatasetSeedState) (string, error) {
	if state == nil {
		return "", errors.New("nil seed state")
	}
	type identity struct {
		GroupIndex int   `json:"group_index"`
		ChannelID  int64 `json:"channel_id"`
		AccessHash int64 `json:"access_hash"`
	}
	identities := make([]identity, len(state.Groups))
	for i, group := range state.Groups {
		if group.ChannelID <= 0 || group.AccessHash == 0 {
			return "", fmt.Errorf("group %d has incomplete identity", group.GroupIndex)
		}
		identities[i] = identity{GroupIndex: group.GroupIndex, ChannelID: group.ChannelID, AccessHash: group.AccessHash}
	}
	data, err := json.Marshal(identities)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func validateClientAccountState(account *ClientAccountState) error {
	if account == nil || account.AccountIndex < 0 || account.UserID <= 0 || account.State.Pts < 0 || account.State.Qts < 0 || account.State.Date <= 0 || account.State.Seq < 0 {
		return errors.New("invalid account state")
	}
	seen := make(map[clientPeerKey]struct{}, len(account.Dialogs))
	for _, dialog := range account.Dialogs {
		peer := clientPeerKey{typ: dialog.PeerType, id: dialog.PeerID}
		if (peer.typ != "user" && peer.typ != "channel") || peer.id <= 0 || dialog.AccessHash == 0 || dialog.TopMessage <= 0 || dialog.TopMessageDate <= 0 {
			return fmt.Errorf("invalid dialog %s:%d", peer.typ, peer.id)
		}
		if peer.typ == "channel" && (!dialog.HasPts || dialog.Pts <= 0) {
			return fmt.Errorf("invalid channel pts for %d", peer.id)
		}
		if dialog.HasDraft != (dialog.DraftText != "") {
			return fmt.Errorf("invalid draft state for %s:%d", peer.typ, peer.id)
		}
		if _, exists := seen[peer]; exists {
			return fmt.Errorf("duplicate dialog %s:%d", peer.typ, peer.id)
		}
		seen[peer] = struct{}{}
	}
	return nil
}

func WriteClientState(path string, state *ClientState) error {
	if state == nil || state.Version != ClientStateVersion || state.DatasetSHA256 == "" || state.SeedIdentitySHA == "" {
		return errors.New("invalid client state")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func LoadClientState(path string) (*ClientState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state ClientState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode client state: %w", err)
	}
	if state.Version != ClientStateVersion || state.DatasetSHA256 == "" || state.SeedIdentitySHA == "" {
		return nil, errors.New("invalid client state")
	}
	return &state, nil
}

func clientStateResult(state *ClientState) *SnapshotResult {
	result := &SnapshotResult{Accounts: len(state.Accounts)}
	for _, account := range state.Accounts {
		result.Dialogs += len(account.Dialogs)
		for _, dialog := range account.Dialogs {
			if dialog.PeerType == "channel" {
				result.Channels++
			}
		}
	}
	return result
}
