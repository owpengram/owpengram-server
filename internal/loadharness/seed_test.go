package loadharness

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/iamxvbaba/td/tg"
)

func TestSeedPrimaryTargetsSelectsPrimaryAndRejectsGap(t *testing.T) {
	manifest := &Manifest{Sessions: []SessionRecord{
		{Index: 2, AccountIndex: 0, DeviceIndex: 1, SessionFile: "extra", UserID: 10, AccessHash: 100},
		{Index: 0, AccountIndex: 0, DeviceIndex: 0, SessionFile: "primary-0", UserID: 10, AccessHash: 100},
		{Index: 1, AccountIndex: 1, DeviceIndex: 0, SessionFile: "primary-1", UserID: 11, AccessHash: 101},
	}}
	targets, err := seedPrimaryTargets(manifest, 2)
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].SessionFile != "primary-0" || targets[1].SessionFile != "primary-1" {
		t.Fatalf("primary targets = %+v", targets)
	}
	manifest.Sessions = append(manifest.Sessions, SessionRecord{
		Index: 3, AccountIndex: 2, DeviceIndex: 0, SessionFile: "primary-2", UserID: 12, AccessHash: 102,
	})
	if targets, err := seedPrimaryTargets(manifest, 2); err != nil || len(targets) != 2 {
		t.Fatalf("manifest superset targets=%d err=%v", len(targets), err)
	}
	manifest.Sessions = manifest.Sessions[:2]
	if _, err := seedPrimaryTargets(manifest, 2); err == nil {
		t.Fatal("manifest account gap passed validation")
	}
}

func TestPlanRichAccountStateUsesDeterministicPrivateAndGroupPeers(t *testing.T) {
	dataset, _, _ := snapshotFixture(t)
	plan, err := planRichAccountState(dataset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PinnedPeerAccount != 1 || plan.ReadPeerAccount != 3 || plan.ReadGroupPosition != 0 || plan.DraftMarker == "" {
		t.Fatalf("rich plan = %+v", plan)
	}
}

func TestValidateSeededRichDialogs(t *testing.T) {
	dataset, seedState, targets := snapshotFixture(t)
	seedState.RichStateByAccount = make([]bool, dataset.Config.Accounts)
	seedState.RichStateByAccount[0] = true
	plan, err := planRichAccountState(dataset, 0)
	if err != nil {
		t.Fatal(err)
	}
	dialogs := []ClientDialogState{
		{PeerType: "user", PeerID: targets[plan.PinnedPeerAccount].UserID, Pinned: true, HasDraft: true, DraftText: plan.DraftMarker},
		{PeerType: "user", PeerID: targets[plan.ReadPeerAccount].UserID, TopMessage: 8, ReadInboxMaxID: 8},
		{PeerType: "channel", PeerID: seedState.Groups[plan.ReadGroupPosition].ChannelID, TopMessage: 9, ReadInboxMaxID: 9},
	}
	if err := validateSeededRichDialogs(dataset, seedState, targets, 0, dialogs, true); err != nil {
		t.Fatal(err)
	}
	dialogs[0].DraftText = "wrong"
	if err := validateSeededRichDialogs(dataset, seedState, targets, 0, dialogs, true); err == nil {
		t.Fatal("wrong draft marker passed rich-state validation")
	}
}

func TestCreatedChannelFromUpdates(t *testing.T) {
	channel := &tg.Channel{ID: 41, AccessHash: 42, Title: "target", Megagroup: true}
	got, err := createdChannelFromUpdates(&tg.Updates{Chats: []tg.ChatClass{
		&tg.Chat{ID: 1, Title: "other"}, channel,
	}}, "target")
	if err != nil {
		t.Fatal(err)
	}
	if got != channel {
		t.Fatalf("created channel = %#v, want target", got)
	}
	if _, err := createdChannelFromUpdates(&tg.UpdateShort{}, "target"); err == nil {
		t.Fatal("unexpected create response passed validation")
	}
	if _, err := createdChannelFromUpdates(&tg.UpdatesCombined{Chats: []tg.ChatClass{
		&tg.Channel{ID: 41, AccessHash: 42, Title: "target"},
	}}, "target"); err == nil {
		t.Fatal("broadcast/non-megagroup response passed validation")
	}
}

func TestDatasetHistoryTasksRotateSenders(t *testing.T) {
	cfg := DefaultDatasetConfig(20)
	cfg.HotGroups, cfg.MediumGroups, cfg.HeavyGroups = 0, 0, 0
	cfg.SmallGroups, cfg.SmallMembers, cfg.SmallHistory = 1, 4, 9
	dataset, err := PlanDataset(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tasks := datasetHistoryTasks(dataset)
	wantCounts := datasetHistoryTaskCounts(dataset)
	gotCounts := make([]int, len(tasks))
	for account := range tasks {
		gotCounts[account] = len(tasks[account])
		for _, task := range tasks[account] {
			group := dataset.Groups[task.GroupPosition]
			if got := group.MemberAccounts[task.MessageIndex%len(group.MemberAccounts)]; got != account {
				t.Fatalf("message %d sender = %d, task account %d", task.MessageIndex, got, account)
			}
		}
	}
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Fatalf("history task counts = %v, want %v", gotCounts, wantCounts)
	}
}

func TestSeedJournalPersistsPendingBoundaries(t *testing.T) {
	dataset, err := PlanDataset(DefaultDatasetConfig(20))
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewDatasetSeedState(dataset)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seed-state.json")
	journal := &seedJournal{path: path, dataset: dataset, state: state}
	if err := journal.persist(); err != nil {
		t.Fatal(err)
	}
	if err := journal.beginCreate(0); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDatasetSeedState(path, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Groups[0].CreatePending || loaded.Groups[0].ChannelID != 0 {
		t.Fatalf("pending create state = %+v", loaded.Groups[0])
	}
	if err := journal.commitChannel(0, 101, 202); err != nil {
		t.Fatal(err)
	}
	if err := journal.beginInvite(0, 7); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadDatasetSeedState(path, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Groups[0].InviteCursor != 0 || loaded.Groups[0].InvitePendingEnd != 7 {
		t.Fatalf("pending invite state = %+v", loaded.Groups[0])
	}
	if err := journal.commitInvite(0, 7); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadDatasetSeedState(path, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Groups[0].InviteCursor != 7 || loaded.Groups[0].InvitePendingEnd != 7 {
		t.Fatalf("committed invite state = %+v", loaded.Groups[0])
	}
}

func TestRunSeedAccountPhaseStopsAfterFailure(t *testing.T) {
	want := errors.New("stop")
	err := runSeedAccountPhase(context.Background(), "test", []int{0, 1, 2}, 1, nil, func(_ context.Context, account int) error {
		if account == 1 {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("phase error = %v, want wrapped stop", err)
	}
}
