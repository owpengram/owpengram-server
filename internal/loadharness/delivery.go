package loadharness

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iamxvbaba/td/tg"
)

const deliveryMarkerPrefix = "telesrv-load-v3"

type deliverySource uint8

const (
	deliveryLive deliverySource = 1 << iota
	deliveryDifference
)

type deliveryExpectation struct {
	senderUserID int64
	targetUserID int64
	startedAt    time.Time
	committed    bool
}

type deliveryObservation struct {
	sources deliverySource
	repeats uint64
	firstAt time.Time
}

type deliveryTracker struct {
	mu           sync.Mutex
	runID        string
	expected     map[string]deliveryExpectation
	observations map[string]map[int64]deliveryObservation
}

type DeliveryReport struct {
	RunID                 string  `json:"run_id"`
	Expected              uint64  `json:"expected"`
	Delivered             uint64  `json:"delivered"`
	Missing               uint64  `json:"missing"`
	LiveDelivered         uint64  `json:"live_delivered"`
	DifferenceRecovered   uint64  `json:"difference_recovered"`
	DuplicateObservations uint64  `json:"duplicate_observations"`
	WrongAccountObserved  uint64  `json:"wrong_account_observed"`
	UnmatchedMarkers      uint64  `json:"unmatched_markers"`
	E2EP50MS              float64 `json:"e2e_p50_ms"`
	E2EP95MS              float64 `json:"e2e_p95_ms"`
	E2EP99MS              float64 `json:"e2e_p99_ms"`
	E2EMaxMS              float64 `json:"e2e_max_ms"`
}

func newDeliveryTracker(runID string) *deliveryTracker {
	return &deliveryTracker{
		runID:        runID,
		expected:     make(map[string]deliveryExpectation),
		observations: make(map[string]map[int64]deliveryObservation),
	}
}

func (t *deliveryTracker) marker(senderIndex int, sequence uint64) string {
	return fmt.Sprintf("%s/%s/%d/%d", deliveryMarkerPrefix, t.runID, senderIndex, sequence)
}

func (t *deliveryTracker) expect(marker string, senderUserID, targetUserID int64) {
	t.begin(marker, senderUserID, targetUserID, time.Now())
	t.finish(marker, true)
}

func (t *deliveryTracker) begin(marker string, senderUserID, targetUserID int64, startedAt time.Time) {
	if t == nil || !t.matches(marker) {
		return
	}
	t.mu.Lock()
	t.expected[marker] = deliveryExpectation{senderUserID: senderUserID, targetUserID: targetUserID, startedAt: startedAt}
	t.mu.Unlock()
}

func (t *deliveryTracker) finish(marker string, success bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	expectation, ok := t.expected[marker]
	if ok && success {
		expectation.committed = true
		t.expected[marker] = expectation
	} else if ok {
		delete(t.expected, marker)
	}
	t.mu.Unlock()
}

func (t *deliveryTracker) observe(marker string, accountUserID int64, source deliverySource) {
	if t == nil || accountUserID <= 0 || !t.matches(marker) {
		return
	}
	t.mu.Lock()
	byAccount := t.observations[marker]
	if byAccount == nil {
		byAccount = make(map[int64]deliveryObservation)
		t.observations[marker] = byAccount
	}
	observation := byAccount[accountUserID]
	if observation.firstAt.IsZero() {
		observation.firstAt = time.Now()
	}
	if observation.sources&source != 0 {
		observation.repeats++
	}
	observation.sources |= source
	byAccount[accountUserID] = observation
	t.mu.Unlock()
}

func (t *deliveryTracker) matches(marker string) bool {
	parts := strings.Split(marker, "/")
	if len(parts) != 4 || parts[0] != deliveryMarkerPrefix || parts[1] != t.runID {
		return false
	}
	if _, err := strconv.Atoi(parts[2]); err != nil {
		return false
	}
	sequence, err := strconv.ParseUint(parts[3], 10, 64)
	return err == nil && sequence > 0
}

func (t *deliveryTracker) report() DeliveryReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	report := DeliveryReport{RunID: t.runID}
	latencies := make([]time.Duration, 0, len(t.expected))
	for marker, expectation := range t.expected {
		if !expectation.committed {
			continue
		}
		report.Expected++
		byAccount := t.observations[marker]
		observation, delivered := byAccount[expectation.targetUserID]
		if delivered {
			report.Delivered++
			switch {
			case observation.sources&deliveryLive != 0:
				report.LiveDelivered++
			case observation.sources&deliveryDifference != 0:
				report.DifferenceRecovered++
			}
			report.DuplicateObservations += observation.repeats
			if !expectation.startedAt.IsZero() && !observation.firstAt.Before(expectation.startedAt) {
				latencies = append(latencies, observation.firstAt.Sub(expectation.startedAt))
			}
		} else {
			report.Missing++
		}
		for accountUserID, other := range byAccount {
			if accountUserID == expectation.targetUserID || accountUserID == expectation.senderUserID {
				continue
			}
			report.WrongAccountObserved++
			report.DuplicateObservations += other.repeats
		}
	}
	for marker := range t.observations {
		if expectation, ok := t.expected[marker]; !ok || !expectation.committed {
			report.UnmatchedMarkers++
		}
	}
	if len(latencies) > 0 {
		report.E2EP50MS = deliveryQuantileMS(latencies, 0.50)
		report.E2EP95MS = deliveryQuantileMS(latencies, 0.95)
		report.E2EP99MS = deliveryQuantileMS(latencies, 0.99)
		report.E2EMaxMS = deliveryQuantileMS(latencies, 1)
	}
	return report
}

func deliveryQuantileMS(values []time.Duration, quantile float64) float64 {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(float64(len(sorted))*quantile)) - 1
	index = max(0, min(index, len(sorted)-1))
	return durationMS(sorted[index])
}

func observeUpdatesClass(tracker *deliveryTracker, accountUserID int64, updates tg.UpdatesClass, source deliverySource) {
	switch value := updates.(type) {
	case *tg.Updates:
		observeUpdateClasses(tracker, accountUserID, value.Updates, source)
	case *tg.UpdatesCombined:
		observeUpdateClasses(tracker, accountUserID, value.Updates, source)
	case *tg.UpdateShort:
		observeUpdateClass(tracker, accountUserID, value.Update, source)
	case *tg.UpdateShortMessage:
		tracker.observe(value.Message, accountUserID, source)
	}
}

func observeUpdateClasses(tracker *deliveryTracker, accountUserID int64, updates []tg.UpdateClass, source deliverySource) {
	for _, update := range updates {
		observeUpdateClass(tracker, accountUserID, update, source)
	}
}

func observeUpdateClass(tracker *deliveryTracker, accountUserID int64, update tg.UpdateClass, source deliverySource) {
	switch value := update.(type) {
	case *tg.UpdateNewMessage:
		observeMessageClass(tracker, accountUserID, value.Message, source)
	case *tg.UpdateNewChannelMessage:
		observeMessageClass(tracker, accountUserID, value.Message, source)
	}
}

func observeMessageClasses(tracker *deliveryTracker, accountUserID int64, messages []tg.MessageClass, source deliverySource) {
	for _, message := range messages {
		observeMessageClass(tracker, accountUserID, message, source)
	}
}

func observeMessageClass(tracker *deliveryTracker, accountUserID int64, message tg.MessageClass, source deliverySource) {
	if value, ok := message.(*tg.Message); ok {
		tracker.observe(value.Message, accountUserID, source)
	}
}
