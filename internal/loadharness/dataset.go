package loadharness

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	DatasetVersion       = 1
	maxDatasetGroups     = 10000
	maxDatasetMembership = 2_000_000
	maxDatasetMessages   = 2_000_000
)

// DatasetConfig describes a deterministic social graph whose durable facts are
// later materialized exclusively through real MTProto RPCs. Planning does not
// contact the server and never embeds auth/session material.
type DatasetConfig struct {
	Accounts      int   `json:"accounts"`
	Seed          int64 `json:"seed"`
	PrivateFanout int   `json:"private_fanout"`
	HotGroups     int   `json:"hot_groups"`
	HotMembers    int   `json:"hot_members"`
	HotHistory    int   `json:"hot_history"`
	MediumGroups  int   `json:"medium_groups"`
	MediumMembers int   `json:"medium_members"`
	MediumHistory int   `json:"medium_history"`
	SmallGroups   int   `json:"small_groups"`
	SmallMembers  int   `json:"small_members"`
	SmallHistory  int   `json:"small_history"`
	HeavyGroups   int   `json:"heavy_groups"`
	HeavyAccounts int   `json:"heavy_accounts"`
	HeavyHistory  int   `json:"heavy_history"`
}

func DefaultDatasetConfig(accounts int) DatasetConfig {
	return DatasetConfig{
		Accounts: accounts, Seed: 20260827, PrivateFanout: min(10, max(accounts-1, 0)),
		HotGroups: 10, HotMembers: accounts, HotHistory: 100,
		MediumGroups: 100, MediumMembers: min(100, accounts), MediumHistory: 30,
		SmallGroups: 200, SmallMembers: min(20, accounts), SmallHistory: 10,
		HeavyGroups: 200, HeavyAccounts: min(100, accounts), HeavyHistory: 30,
	}
}

type DatasetPrivateEdge struct {
	SenderAccount    int    `json:"sender_account"`
	RecipientAccount int    `json:"recipient_account"`
	RandomID         int64  `json:"random_id"`
	Marker           string `json:"marker"`
}

type DatasetGroup struct {
	Index           int    `json:"index"`
	Tier            string `json:"tier"`
	Title           string `json:"title"`
	About           string `json:"about"`
	CreatorAccount  int    `json:"creator_account"`
	MemberAccounts  []int  `json:"member_accounts"`
	HistoryMessages int    `json:"history_messages"`
}

// Dataset is the immutable plan. Real server identities and resumable progress
// live in the separate compact DatasetSeedState so a large plan is not rewritten
// after every reconciled RPC batch.
type Dataset struct {
	Version      int                  `json:"version"`
	CreatedAt    time.Time            `json:"created_at"`
	RunID        string               `json:"run_id"`
	Config       DatasetConfig        `json:"config"`
	PlanSHA256   string               `json:"plan_sha256"`
	PrivateEdges []DatasetPrivateEdge `json:"private_edges"`
	Groups       []DatasetGroup       `json:"groups"`
}

type DatasetSeedGroupState struct {
	GroupIndex       int   `json:"group_index"`
	ChannelID        int64 `json:"channel_id,omitempty"`
	AccessHash       int64 `json:"access_hash,omitempty"`
	CreatePending    bool  `json:"create_pending,omitempty"`
	InviteCursor     int   `json:"invite_cursor,omitempty"`
	InvitePendingEnd int   `json:"invite_pending_end,omitempty"`
}

type DatasetSeedState struct {
	Version              int                     `json:"version"`
	PlanSHA256           string                  `json:"plan_sha256"`
	UpdatedAt            time.Time               `json:"updated_at"`
	PrivateSentByAccount []int                   `json:"private_sent_by_account"`
	HistorySentByAccount []int                   `json:"history_sent_by_account"`
	RichStateByAccount   []bool                  `json:"rich_state_by_account,omitempty"`
	Groups               []DatasetSeedGroupState `json:"groups"`
}

func PlanDataset(cfg DatasetConfig) (*Dataset, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	runID := fmt.Sprintf("rpc-startup-%d-%016x", cfg.Accounts, uint64(cfg.Seed))
	dataset := &Dataset{
		Version: DatasetVersion, CreatedAt: time.Now().UTC(),
		RunID: runID, Config: cfg,
		PrivateEdges: make([]DatasetPrivateEdge, 0, cfg.Accounts*cfg.PrivateFanout),
	}
	for account := 0; account < cfg.Accounts; account++ {
		for offset := 1; offset <= cfg.PrivateFanout; offset++ {
			recipient := (account + offset) % cfg.Accounts
			dataset.PrivateEdges = append(dataset.PrivateEdges, DatasetPrivateEdge{
				SenderAccount: account, RecipientAccount: recipient,
				RandomID: stableDatasetID(cfg.Seed, "private", cfg.Accounts, account, offset),
				Marker:   fmt.Sprintf("[%s private %04d/%02d]", runID, account, offset),
			})
		}
	}
	groupIndex := 0
	appendTier := func(tier string, count, members, history int, memberSet func(int) []int) {
		for i := 0; i < count; i++ {
			groupMembers := memberSet(i)
			creator := groupMembers[i%len(groupMembers)]
			dataset.Groups = append(dataset.Groups, DatasetGroup{
				Index: groupIndex, Tier: tier,
				Title:          fmt.Sprintf("%s %s %04d", runID, tier, i+1),
				About:          fmt.Sprintf("telesrv real-RPC load dataset %s group %d", tier, i+1),
				CreatorAccount: creator, MemberAccounts: groupMembers, HistoryMessages: history,
			})
			groupIndex++
		}
	}
	appendTier("hot", cfg.HotGroups, cfg.HotMembers, cfg.HotHistory, func(i int) []int {
		return cyclicMembers(cfg.Accounts, i*max(cfg.HotMembers, 1), cfg.HotMembers)
	})
	appendTier("medium", cfg.MediumGroups, cfg.MediumMembers, cfg.MediumHistory, func(i int) []int {
		return cyclicMembers(cfg.Accounts, i*max(cfg.MediumMembers, 1), cfg.MediumMembers)
	})
	appendTier("small", cfg.SmallGroups, cfg.SmallMembers, cfg.SmallHistory, func(i int) []int {
		return cyclicMembers(cfg.Accounts, i*max(cfg.SmallMembers, 1), cfg.SmallMembers)
	})
	appendTier("heavy", cfg.HeavyGroups, cfg.HeavyAccounts, cfg.HeavyHistory, func(int) []int {
		members := make([]int, cfg.HeavyAccounts)
		for i := range members {
			members[i] = i
		}
		return members
	})
	planHash, err := dataset.planHash()
	if err != nil {
		return nil, err
	}
	dataset.PlanSHA256 = planHash
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	return dataset, nil
}

func (c DatasetConfig) validate() error {
	if c.Accounts < 2 || c.Accounts > 100000 {
		return errors.New("dataset accounts must be between 2 and 100000")
	}
	if c.PrivateFanout < 0 || c.PrivateFanout >= c.Accounts {
		return errors.New("private fanout must be non-negative and smaller than accounts")
	}
	groups := c.HotGroups + c.MediumGroups + c.SmallGroups + c.HeavyGroups
	if groups <= 0 || groups > maxDatasetGroups {
		return fmt.Errorf("dataset group count must be between 1 and %d", maxDatasetGroups)
	}
	tiers := []struct {
		name                     string
		groups, members, history int
	}{
		{"hot", c.HotGroups, c.HotMembers, c.HotHistory},
		{"medium", c.MediumGroups, c.MediumMembers, c.MediumHistory},
		{"small", c.SmallGroups, c.SmallMembers, c.SmallHistory},
		{"heavy", c.HeavyGroups, c.HeavyAccounts, c.HeavyHistory},
	}
	memberships := 0
	messages := c.Accounts * c.PrivateFanout
	for _, tier := range tiers {
		if tier.groups < 0 || tier.members < 0 || tier.members > c.Accounts || tier.history < 0 {
			return fmt.Errorf("invalid %s dataset tier", tier.name)
		}
		if tier.groups > 0 && tier.members == 0 {
			return fmt.Errorf("%s dataset tier has groups without members", tier.name)
		}
		memberships += tier.groups * tier.members
		messages += tier.groups * tier.history
	}
	if memberships > maxDatasetMembership {
		return fmt.Errorf("dataset memberships %d exceed hard limit %d", memberships, maxDatasetMembership)
	}
	if messages > maxDatasetMessages {
		return fmt.Errorf("dataset messages %d exceed hard limit %d", messages, maxDatasetMessages)
	}
	return nil
}

func (d *Dataset) Validate() error {
	if d == nil || d.Version != DatasetVersion {
		return errors.New("invalid dataset version")
	}
	if strings.TrimSpace(d.RunID) == "" || strings.TrimSpace(d.PlanSHA256) == "" {
		return errors.New("dataset is missing run id or plan hash")
	}
	if err := d.Config.validate(); err != nil {
		return err
	}
	if len(d.PrivateEdges) != d.Config.Accounts*d.Config.PrivateFanout {
		return errors.New("dataset private graph size does not match config")
	}
	seenGroups := make(map[int]struct{}, len(d.Groups))
	for _, group := range d.Groups {
		if group.Index < 0 || group.CreatorAccount < 0 || group.CreatorAccount >= d.Config.Accounts || group.HistoryMessages < 0 {
			return fmt.Errorf("invalid group %d", group.Index)
		}
		if _, exists := seenGroups[group.Index]; exists {
			return fmt.Errorf("duplicate group index %d", group.Index)
		}
		seenGroups[group.Index] = struct{}{}
		members := make(map[int]struct{}, len(group.MemberAccounts))
		for _, account := range group.MemberAccounts {
			if account < 0 || account >= d.Config.Accounts {
				return fmt.Errorf("group %d has invalid account %d", group.Index, account)
			}
			if _, exists := members[account]; exists {
				return fmt.Errorf("group %d has duplicate account %d", group.Index, account)
			}
			members[account] = struct{}{}
		}
		if _, ok := members[group.CreatorAccount]; !ok {
			return fmt.Errorf("group %d creator is not a member", group.Index)
		}
	}
	wantHash, err := d.planHash()
	if err != nil {
		return err
	}
	if wantHash != d.PlanSHA256 {
		return errors.New("dataset immutable plan hash mismatch")
	}
	return nil
}

func WriteDataset(path string, dataset *Dataset) error {
	if err := dataset.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(dataset, "", "  ")
	if err != nil {
		return fmt.Errorf("encode dataset: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func NewDatasetSeedState(dataset *Dataset) (*DatasetSeedState, error) {
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	state := &DatasetSeedState{
		Version: DatasetVersion, PlanSHA256: dataset.PlanSHA256, UpdatedAt: time.Now().UTC(),
		PrivateSentByAccount: make([]int, dataset.Config.Accounts),
		HistorySentByAccount: make([]int, dataset.Config.Accounts),
		RichStateByAccount:   make([]bool, dataset.Config.Accounts),
		Groups:               make([]DatasetSeedGroupState, len(dataset.Groups)),
	}
	for i, group := range dataset.Groups {
		state.Groups[i].GroupIndex = group.Index
	}
	return state, nil
}

func (s *DatasetSeedState) Validate(dataset *Dataset) error {
	if s == nil || s.Version != DatasetVersion || dataset == nil || s.PlanSHA256 != dataset.PlanSHA256 {
		return errors.New("seed state does not match dataset plan")
	}
	if len(s.PrivateSentByAccount) != dataset.Config.Accounts || len(s.HistorySentByAccount) != dataset.Config.Accounts || len(s.Groups) != len(dataset.Groups) {
		return errors.New("seed state dimensions do not match dataset plan")
	}
	// A nil rich-state vector is accepted only for seed journals created before
	// the comprehensive startup workload added this phase. New journals always
	// allocate the full vector and therefore cannot silently skip it.
	if len(s.RichStateByAccount) != 0 && len(s.RichStateByAccount) != dataset.Config.Accounts {
		return errors.New("seed rich-state dimensions do not match dataset plan")
	}
	historyTasks := datasetHistoryTaskCounts(dataset)
	for account := 0; account < dataset.Config.Accounts; account++ {
		if s.PrivateSentByAccount[account] < 0 || s.PrivateSentByAccount[account] > dataset.Config.PrivateFanout {
			return fmt.Errorf("invalid private seed progress for account %d", account)
		}
		if s.HistorySentByAccount[account] < 0 || s.HistorySentByAccount[account] > historyTasks[account] {
			return fmt.Errorf("invalid history seed progress for account %d", account)
		}
	}
	for i, groupState := range s.Groups {
		group := dataset.Groups[i]
		invitees := len(group.MemberAccounts) - 1
		if groupState.GroupIndex != group.Index || groupState.InviteCursor < 0 || groupState.InviteCursor > invitees {
			return fmt.Errorf("invalid seed state for group %d", group.Index)
		}
		if (groupState.ChannelID == 0) != (groupState.AccessHash == 0) {
			return fmt.Errorf("group %d has partial channel identity", group.Index)
		}
		if groupState.ChannelID != 0 && groupState.CreatePending {
			return fmt.Errorf("group %d has both channel identity and pending create", group.Index)
		}
		if groupState.InvitePendingEnd < groupState.InviteCursor || groupState.InvitePendingEnd > invitees {
			return fmt.Errorf("group %d has invalid pending invite range", group.Index)
		}
		if groupState.ChannelID == 0 && (groupState.InviteCursor != 0 || groupState.InvitePendingEnd != 0) {
			return fmt.Errorf("group %d has invite progress without a channel identity", group.Index)
		}
	}
	return nil
}

func WriteDatasetSeedState(path string, dataset *Dataset, state *DatasetSeedState) error {
	if err := state.Validate(dataset); err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode dataset seed state: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func LoadDatasetSeedState(path string, dataset *Dataset) (*DatasetSeedState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewDatasetSeedState(dataset)
	}
	if err != nil {
		return nil, fmt.Errorf("read dataset seed state: %w", err)
	}
	var state DatasetSeedState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode dataset seed state: %w", err)
	}
	if err := state.Validate(dataset); err != nil {
		return nil, err
	}
	return &state, nil
}

func datasetHistoryTaskCounts(dataset *Dataset) []int {
	counts := make([]int, dataset.Config.Accounts)
	for _, group := range dataset.Groups {
		for message := 0; message < group.HistoryMessages; message++ {
			counts[group.MemberAccounts[message%len(group.MemberAccounts)]]++
		}
	}
	return counts
}

func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	var dataset Dataset
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dataset); err != nil {
		return nil, fmt.Errorf("decode dataset: %w", err)
	}
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	return &dataset, nil
}

func (d *Dataset) planHash() (string, error) {
	type immutableDataset struct {
		Version      int                  `json:"version"`
		RunID        string               `json:"run_id"`
		Config       DatasetConfig        `json:"config"`
		PrivateEdges []DatasetPrivateEdge `json:"private_edges"`
		Groups       []struct {
			Index           int    `json:"index"`
			Tier            string `json:"tier"`
			Title           string `json:"title"`
			About           string `json:"about"`
			CreatorAccount  int    `json:"creator_account"`
			MemberAccounts  []int  `json:"member_accounts"`
			HistoryMessages int    `json:"history_messages"`
		} `json:"groups"`
	}
	immutable := immutableDataset{Version: d.Version, RunID: d.RunID, Config: d.Config, PrivateEdges: d.PrivateEdges}
	for _, group := range d.Groups {
		immutable.Groups = append(immutable.Groups, struct {
			Index           int    `json:"index"`
			Tier            string `json:"tier"`
			Title           string `json:"title"`
			About           string `json:"about"`
			CreatorAccount  int    `json:"creator_account"`
			MemberAccounts  []int  `json:"member_accounts"`
			HistoryMessages int    `json:"history_messages"`
		}{group.Index, group.Tier, group.Title, group.About, group.CreatorAccount, group.MemberAccounts, group.HistoryMessages})
	}
	data, err := json.Marshal(immutable)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cyclicMembers(accounts, start, count int) []int {
	if count <= 0 {
		return nil
	}
	members := make([]int, count)
	for i := range members {
		members[i] = (start + i) % accounts
	}
	sort.Ints(members)
	return members
}

func stableDatasetID(seed int64, namespace string, values ...int) int64 {
	h := sha256.New()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(seed))
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(namespace))
	for _, value := range values {
		binary.LittleEndian.PutUint64(buf[:], uint64(value))
		_, _ = h.Write(buf[:])
	}
	sum := h.Sum(nil)
	id := int64(binary.LittleEndian.Uint64(sum[:8]) & ^(uint64(1) << 63))
	if id == 0 {
		return 1
	}
	return id
}
