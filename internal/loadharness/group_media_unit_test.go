package loadharness

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/iamxvbaba/td/tg"
)

func TestGroupMediaChannelNudgeUsesOnlyMatchingChannelAndMaximumPTS(t *testing.T) {
	matchingLow := &tg.UpdateChannelTooLong{ChannelID: 71}
	matchingLow.SetPts(11)
	matchingHigh := &tg.UpdateChannelTooLong{ChannelID: 71}
	matchingHigh.SetPts(19)
	other := &tg.UpdateChannelTooLong{ChannelID: 72}
	other.SetPts(100)
	pts, ok := groupMediaChannelNudge(&tg.Updates{Updates: []tg.UpdateClass{other, matchingLow, matchingHigh}}, 71)
	if !ok || pts != 19 {
		t.Fatalf("matching channel nudge = pts:%d ok:%v, want 19/true", pts, ok)
	}
	withoutPTS := &tg.UpdateChannelTooLong{ChannelID: 71}
	if pts, ok = groupMediaChannelNudge(&tg.UpdateShort{Update: withoutPTS}, 71); !ok || pts != 0 {
		t.Fatalf("optional-pts channel nudge = pts:%d ok:%v, want 0/true", pts, ok)
	}
	if pts, ok = groupMediaChannelNudge(&tg.Updates{Updates: []tg.UpdateClass{other}}, 71); ok || pts != 0 {
		t.Fatalf("unrelated channel nudge = pts:%d ok:%v, want 0/false", pts, ok)
	}
}

func TestGroupMediaChannelLiveUpdateUsesOnlyMatchingChannelAndMaximumPTS(t *testing.T) {
	matchingLow := &tg.UpdateNewChannelMessage{
		Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 71}}, Pts: 11, PtsCount: 1,
	}
	matchingHigh := &tg.UpdateNewChannelMessage{
		Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 71}}, Pts: 19, PtsCount: 1,
	}
	other := &tg.UpdateNewChannelMessage{
		Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 72}}, Pts: 100, PtsCount: 1,
	}
	pts, count := groupMediaChannelLiveUpdate(
		&tg.Updates{Updates: []tg.UpdateClass{other, matchingLow, matchingHigh}}, 71,
	)
	if pts != 19 || count != 2 {
		t.Fatalf("matching channel live updates = pts:%d count:%d, want 19/2", pts, count)
	}
	pts, count = groupMediaChannelLiveUpdate(&tg.UpdateShort{Update: other}, 71)
	if pts != 0 || count != 0 {
		t.Fatalf("unrelated channel live update = pts:%d count:%d, want 0/0", pts, count)
	}
}

func TestGroupMediaDifferencePageRequiresMonotonicPTS(t *testing.T) {
	message := &tg.Message{Message: "marker"}
	pts, final, messages, updates, err := groupMediaDifferencePage(10, &tg.UpdatesChannelDifference{
		Pts: 11, Final: false, NewMessages: []tg.MessageClass{message},
		OtherUpdates: []tg.UpdateClass{&tg.UpdateChannel{ChannelID: 71}},
	})
	if err != nil || pts != 11 || final || len(messages) != 1 || len(updates) != 1 {
		t.Fatalf("full difference page = pts:%d final:%v messages:%d updates:%d err:%v", pts, final, len(messages), len(updates), err)
	}
	pts, final, messages, updates, err = groupMediaDifferencePage(11, &tg.UpdatesChannelDifferenceEmpty{Pts: 11, Final: true})
	if err != nil || pts != 11 || !final || len(messages) != 0 || len(updates) != 0 {
		t.Fatalf("empty difference page = pts:%d final:%v messages:%d updates:%d err:%v", pts, final, len(messages), len(updates), err)
	}
	dialog := &tg.Dialog{}
	dialog.SetPts(15)
	pts, final, messages, updates, err = groupMediaDifferencePage(11, &tg.UpdatesChannelDifferenceTooLong{
		Final: true, Dialog: dialog, Messages: []tg.MessageClass{message},
	})
	if err != nil || pts != 15 || !final || len(messages) != 1 || len(updates) != 0 {
		t.Fatalf("too-long difference page = pts:%d final:%v messages:%d updates:%d err:%v", pts, final, len(messages), len(updates), err)
	}
	if _, _, _, _, err := groupMediaDifferencePage(15, &tg.UpdatesChannelDifference{Pts: 15, Final: false}); err == nil {
		t.Fatal("non-final difference page without PTS progress was accepted")
	}
	if _, _, _, _, err := groupMediaDifferencePage(15, &tg.UpdatesChannelDifferenceEmpty{Pts: 14, Final: true}); err == nil {
		t.Fatal("difference PTS regression was accepted")
	}
	if _, _, _, _, err := groupMediaDifferencePage(15, &tg.UpdatesChannelDifferenceTooLong{Final: false, Dialog: dialog}); err == nil {
		t.Fatal("non-final channelDifferenceTooLong was accepted")
	}
}

func TestGroupMediaDifferenceRequestCoalescesMaximumPTS(t *testing.T) {
	client := &groupMediaClient{differenceWake: make(chan struct{}, 1)}
	client.requestChannelDifference(12)
	client.requestChannelDifference(9)
	client.requestChannelDifference(17)
	client.requestChannelDifference(0)
	if !client.differenceRequested.Load() || !client.differenceForce.Load() || client.differenceTargetPts.Load() != 17 || len(client.differenceWake) != 1 {
		t.Fatalf("coalesced request = requested:%v force:%v pts:%d wakes:%d", client.differenceRequested.Load(), client.differenceForce.Load(), client.differenceTargetPts.Load(), len(client.differenceWake))
	}
}

func TestGroupMediaFanoutCountsOnlyDifferenceRecoveredMessages(t *testing.T) {
	const marker = "telesrv-group-media/run/photo/1"
	message := func(id int64) *tg.Message {
		value := &tg.Message{Message: marker}
		media := &tg.MessageMediaDocument{}
		media.SetDocument(&tg.Document{ID: id, AccessHash: id + 100, FileReference: []byte{byte(id)}, Size: 4096})
		value.SetMedia(media)
		return value
	}
	tracker := &groupMediaFanoutTracker{
		prefix: "telesrv-group-media/run/", members: map[int64]int{101: 0, 102: 1},
		expected: make(map[string]time.Time), committed: make(map[string]bool),
		observations: make(map[string]map[int64]groupMediaObservation),
	}
	tracker.begin(marker)
	tracker.observeDifference(101, []tg.MessageClass{message(1)}, nil)
	tracker.observeDifference(102, nil, []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: message(2)}})
	tracker.finish(marker, true)
	report := tracker.report()
	if report.Messages != 1 || report.Expected != 2 || report.Observed != 2 || report.Missing != 0 || report.Duplicate != 0 {
		t.Fatalf("difference fanout report = %+v", report)
	}
	canonical := []groupMediaTarget{{Kind: "photo", Marker: marker, Size: 4096, SHA256: sha256.Sum256([]byte("canonical"))}}
	for index, userID := range []int64{101, 102} {
		targets, err := tracker.targetsForUser(userID, canonical)
		if err != nil || len(targets) != 1 || targets[0].SHA256 != canonical[0].SHA256 || targets[0].Location == nil {
			t.Fatalf("difference targets for user %d = %+v, %v", userID, targets, err)
		}
		location, ok := targets[0].Location.(*tg.InputDocumentFileLocation)
		if !ok || location.ID != int64(index+1) {
			t.Fatalf("difference target for user %d has location %#v, want document %d", userID, targets[0].Location, index+1)
		}
	}
}
