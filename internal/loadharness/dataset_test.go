package loadharness

import (
	"path/filepath"
	"testing"
)

func TestPlanDatasetDefaultTopology(t *testing.T) {
	dataset, err := PlanDataset(DefaultDatasetConfig(1000))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(dataset.PrivateEdges), 10000; got != want {
		t.Fatalf("private edges = %d, want %d", got, want)
	}
	if got, want := len(dataset.Groups), 510; got != want {
		t.Fatalf("groups = %d, want %d", got, want)
	}
	memberships := make([]int, dataset.Config.Accounts)
	totalMemberships := 0
	for _, group := range dataset.Groups {
		totalMemberships += len(group.MemberAccounts)
		for _, account := range group.MemberAccounts {
			memberships[account]++
		}
	}
	if totalMemberships != 44000 {
		t.Fatalf("memberships = %d, want 44000", totalMemberships)
	}
	if got, want := memberships[999]+20, 44; got != want {
		t.Fatalf("regular account dialogs = %d, want %d", got, want)
	}
	if got, want := memberships[0]+20, 244; got != want {
		t.Fatalf("heavy account dialogs = %d, want %d", got, want)
	}
	if dataset.PlanSHA256 == "" || dataset.PrivateEdges[0].RandomID == 0 {
		t.Fatal("dataset is missing stable identity")
	}
}

func TestPlanDatasetIsDeterministic(t *testing.T) {
	cfg := DefaultDatasetConfig(100)
	first, err := PlanDataset(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanDataset(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanSHA256 != second.PlanSHA256 {
		t.Fatalf("plan hash changed: %s != %s", first.PlanSHA256, second.PlanSHA256)
	}
	if first.PrivateEdges[37] != second.PrivateEdges[37] {
		t.Fatal("private edge plan changed")
	}
}

func TestDatasetRandomIDsAreNamespacedByScale(t *testing.T) {
	ten, err := PlanDataset(DefaultDatasetConfig(10))
	if err != nil {
		t.Fatal(err)
	}
	hundred, err := PlanDataset(DefaultDatasetConfig(100))
	if err != nil {
		t.Fatal(err)
	}
	if ten.PrivateEdges[0].RandomID == hundred.PrivateEdges[0].RandomID {
		t.Fatal("private random_id collided across dataset scales")
	}
	if stableDatasetID(7, "offline", 10, 0) == stableDatasetID(7, "offline", 100, 0) {
		t.Fatal("offline random_id collided across dataset scales")
	}
}

func TestDatasetRoundTripAndImmutableHash(t *testing.T) {
	dataset, err := PlanDataset(DefaultDatasetConfig(20))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := WriteDataset(path, dataset); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PlanSHA256 != dataset.PlanSHA256 || len(loaded.Groups) != len(dataset.Groups) {
		t.Fatal("dataset round trip changed the plan")
	}
	loaded.Groups[0].Title += " changed"
	if err := loaded.Validate(); err == nil {
		t.Fatal("mutated immutable plan passed validation")
	}
}

func TestDatasetSeedStateRoundTrip(t *testing.T) {
	dataset, err := PlanDataset(DefaultDatasetConfig(20))
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewDatasetSeedState(dataset)
	if err != nil {
		t.Fatal(err)
	}
	state.PrivateSentByAccount[0] = 2
	state.Groups[0].ChannelID = 11
	state.Groups[0].AccessHash = 22
	state.Groups[0].InviteCursor = 3
	state.Groups[0].InvitePendingEnd = 7
	path := filepath.Join(t.TempDir(), "seed-state.json")
	if err := WriteDatasetSeedState(path, dataset, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDatasetSeedState(path, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PrivateSentByAccount[0] != 2 || loaded.Groups[0].ChannelID != 11 || loaded.Groups[0].InviteCursor != 3 || loaded.Groups[0].InvitePendingEnd != 7 {
		t.Fatal("seed state round trip changed progress")
	}
}

func TestDatasetSeedStateAcceptsLegacyMissingRichVector(t *testing.T) {
	dataset, err := PlanDataset(DefaultDatasetConfig(20))
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewDatasetSeedState(dataset)
	if err != nil {
		t.Fatal(err)
	}
	state.RichStateByAccount = nil
	if err := state.Validate(dataset); err != nil {
		t.Fatal(err)
	}
	state.RichStateByAccount = []bool{true}
	if err := state.Validate(dataset); err == nil {
		t.Fatal("partial rich-state vector passed validation")
	}
}

func TestDatasetSeedStateRejectsImpossiblePendingOperation(t *testing.T) {
	dataset, err := PlanDataset(DefaultDatasetConfig(20))
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewDatasetSeedState(dataset)
	if err != nil {
		t.Fatal(err)
	}
	state.Groups[0].CreatePending = true
	state.Groups[0].ChannelID = 11
	state.Groups[0].AccessHash = 22
	if err := state.Validate(dataset); err == nil {
		t.Fatal("channel identity plus pending create passed validation")
	}
	state.Groups[0].CreatePending = false
	state.Groups[0].ChannelID = 0
	state.Groups[0].AccessHash = 0
	state.Groups[0].InvitePendingEnd = 1
	if err := state.Validate(dataset); err == nil {
		t.Fatal("invite progress without channel identity passed validation")
	}
}

func TestDatasetConfigRejectsUnsafeScale(t *testing.T) {
	cfg := DefaultDatasetConfig(1000)
	cfg.HotGroups = maxDatasetGroups + 1
	if _, err := PlanDataset(cfg); err == nil {
		t.Fatal("oversized dataset passed validation")
	}
	cfg = DefaultDatasetConfig(10)
	cfg.PrivateFanout = 10
	if _, err := PlanDataset(cfg); err == nil {
		t.Fatal("self-wrapping private fanout passed validation")
	}
}
