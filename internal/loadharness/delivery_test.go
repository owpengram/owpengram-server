package loadharness

import (
	"testing"
	"time"

	"github.com/iamxvbaba/td/tg"
)

func TestDeliveryTrackerReconcilesObservationBeforeSendReturn(t *testing.T) {
	tracker := newDeliveryTracker("run")
	marker := tracker.marker(7, 9)

	tracker.observe(marker, 200, deliveryLive)
	tracker.expect(marker, 100, 200)

	report := tracker.report()
	if report.Expected != 1 || report.Delivered != 1 || report.LiveDelivered != 1 || report.Missing != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDeliveryTrackerMeasuresFromSendStart(t *testing.T) {
	tracker := newDeliveryTracker("run")
	marker := tracker.marker(1, 1)
	tracker.begin(marker, 100, 200, time.Now().Add(-25*time.Millisecond))
	tracker.observe(marker, 200, deliveryLive)
	tracker.finish(marker, true)

	report := tracker.report()
	if report.E2EP50MS < 20 || report.E2EP99MS < report.E2EP50MS || report.E2EMaxMS < report.E2EP99MS {
		t.Fatalf("unexpected e2e latency report: %+v", report)
	}
}

func TestDeliveryTrackerSeparatesDifferenceRecoveryAndDuplicates(t *testing.T) {
	tracker := newDeliveryTracker("run")
	liveMarker := tracker.marker(1, 1)
	differenceMarker := tracker.marker(2, 1)
	tracker.expect(liveMarker, 100, 200)
	tracker.expect(differenceMarker, 200, 300)

	tracker.observe(liveMarker, 200, deliveryLive)
	tracker.observe(liveMarker, 200, deliveryLive)
	tracker.observe(liveMarker, 200, deliveryDifference)
	tracker.observe(differenceMarker, 300, deliveryDifference)

	report := tracker.report()
	if report.Delivered != 2 || report.LiveDelivered != 1 || report.DifferenceRecovered != 1 {
		t.Fatalf("unexpected delivery sources: %+v", report)
	}
	if report.DuplicateObservations != 1 {
		t.Fatalf("duplicate observations = %d, want 1", report.DuplicateObservations)
	}
}

func TestObserveUpdatesClassExtractsPrivateMessages(t *testing.T) {
	tracker := newDeliveryTracker("run")
	shortMarker := tracker.marker(1, 1)
	fullMarker := tracker.marker(2, 1)
	tracker.expect(shortMarker, 100, 200)
	tracker.expect(fullMarker, 300, 200)

	observeUpdatesClass(tracker, 200, &tg.UpdateShortMessage{Message: shortMarker}, deliveryLive)
	observeUpdatesClass(tracker, 200, &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewMessage{Message: &tg.Message{Message: fullMarker}},
	}}, deliveryLive)

	report := tracker.report()
	if report.Delivered != 2 || report.Missing != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDeliveryTrackerRejectsForeignAndMalformedMarkers(t *testing.T) {
	tracker := newDeliveryTracker("run")
	tracker.observe("telesrv-load-v3/other/1/1", 200, deliveryLive)
	tracker.observe("telesrv-load-v3/run/not-an-index/1", 200, deliveryLive)

	if report := tracker.report(); report.UnmatchedMarkers != 0 {
		t.Fatalf("foreign marker entered report: %+v", report)
	}
}
