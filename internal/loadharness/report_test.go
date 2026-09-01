package loadharness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iamxvbaba/td/pool"
	tdrpc "github.com/iamxvbaba/td/rpc"
)

func TestMarkClientReadyAvoidsDuplicateInitialCatchUp(t *testing.T) {
	var everReady atomic.Bool
	var firstClient atomic.Bool
	if markClientReady(&everReady, &firstClient) {
		t.Fatal("first Ready transition requested a catch-up")
	}
	if !markClientReady(&everReady, &firstClient) {
		t.Fatal("transport reconnect did not request a catch-up")
	}

	var replacementClient atomic.Bool
	if markClientReady(&everReady, &replacementClient) {
		t.Fatal("replacement Client duplicated its callback-owned initial catch-up")
	}
	if !markClientReady(&everReady, &replacementClient) {
		t.Fatal("replacement Client transport reconnect did not request a catch-up")
	}
}

func TestEventWriterCapsConnectionDetailsWithoutDroppingSamples(t *testing.T) {
	w, err := newEventWriter(filepath.Join(t.TempDir(), "events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxConnectionDeadEventLines+1; i++ {
		w.write(map[string]any{"type": "connection_dead", "class": "connection"})
	}
	w.write(map[string]any{"type": "sample", "ready": 0})
	written, dropped := w.counts()
	if written != maxConnectionDeadEventLines+1 || dropped != 1 {
		t.Fatalf("event counts = %d/%d", written, dropped)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
}

func TestOperationMetricsUsesBoundedHistogramAndFixedErrorClasses(t *testing.T) {
	metrics := &operationMetrics{}
	metrics.observe(time.Now().Add(-20*time.Millisecond), nil)
	metrics.observe(time.Now().Add(-200*time.Millisecond), errors.New("FLOOD_WAIT_1 phone=secret"))
	report := metrics.report()
	if report.Count != 2 || report.Errors != 1 || report.FloodWaits != 1 {
		t.Fatalf("report = %#v", report)
	}
	if report.P50UpperMS <= 0 || report.P99UpperMS < report.P50UpperMS || report.MaxMS <= 0 {
		t.Fatalf("latency report = %#v", report)
	}
}

func TestClassifyErrorReasonUsesFiniteRedactedVocabulary(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.New("dial tcp 10.0.0.1:2398: socket: too many open files"), "file_descriptor_limit"},
		{errors.New("read: temporary auth key not found: pfs reconnect required"), "pfs_reconnect"},
		{errors.New("read tcp: EOF auth_key_id=secret"), "eof"},
		{errors.New("startup session ended before business readiness"), "business_readiness_incomplete"},
	}
	for _, test := range tests {
		if got := classifyErrorReason(test.err); got != test.want {
			t.Fatalf("classifyErrorReason(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestClassifyErrorRecognizesTypedReconnectFailures(t *testing.T) {
	tests := []error{
		fmt.Errorf("invoke: %w", tdrpc.ErrEngineClosed),
		fmt.Errorf("acquire: %w", pool.ErrConnDead),
		fmt.Errorf("read: %w", net.ErrClosed),
		errors.New("write: broken pipe"),
	}
	for _, err := range tests {
		if got := classifyError(err); got != "connection" {
			t.Fatalf("classifyError(%v) = %q, want connection", err, got)
		}
	}
}

func TestOperationMetricsSeparatesHarnessCancellation(t *testing.T) {
	metrics := &operationMetrics{}
	metrics.observe(time.Now(), context.Canceled)
	report := metrics.report()
	if report.Count != 1 || report.Canceled != 1 || report.Errors != 0 {
		t.Fatalf("report = %#v", report)
	}
}

func TestMetricSetFreezeExcludesCoordinatedTeardown(t *testing.T) {
	metrics := newMetricSet("ping")
	metrics.observe("ping", time.Now(), nil)
	cut := metrics.freeze()
	metrics.observe("ping", time.Now(), fmt.Errorf("engine forcibly closed: %w", context.Canceled))

	if got := cut["ping"]; got.Count != 1 || got.Errors != 0 || got.Canceled != 0 {
		t.Fatalf("workload cut = %#v", got)
	}
	if got := metrics.report()["ping"]; got != cut["ping"] {
		t.Fatalf("post-freeze metrics changed: got %#v want %#v", got, cut["ping"])
	}
}

func TestEvaluateReportRejectsFixedRateDeliveryLoss(t *testing.T) {
	report := &RunReport{
		ExpectedSessions: 1, PeakReadySessions: 1,
		SteadySamples: 1, SteadyReadyRatio: 1, MinSteadyReadySessions: 1,
		MessageScheduled: 2, MessageEnqueued: 2, MessageCompleted: 2,
		Delivery:   DeliveryReport{Expected: 2, Delivered: 1, Missing: 1},
		Operations: map[string]OperationReport{},
	}
	evaluateReport(report, RunConfig{MinimumReadyRatio: 1, MessageRate: 1})
	if report.Pass {
		t.Fatal("fixed-rate report with a missing recipient delivery passed")
	}
}

func TestEvaluateReportAllowsOnlyConnectionErrorsForExpectedRestart(t *testing.T) {
	report := &RunReport{
		ExpectedSessions: 2, PeakReadySessions: 2, Reconnects: 2,
		SteadySamples: 1, SteadyReadyRatio: 1, MinSteadyReadySessions: 2,
		Operations: map[string]OperationReport{
			"connection.dead": {Count: 2, Errors: 2, ConnectionErrors: 2},
		},
	}
	evaluateReport(report, RunConfig{MinimumReadyRatio: 1, ExpectServerRestart: true})
	if !report.Pass {
		t.Fatalf("report = %#v", report)
	}
	report.Operations["ping"] = OperationReport{Count: 1, Errors: 1}
	report.Failures = nil
	evaluateReport(report, RunConfig{MinimumReadyRatio: 1, ExpectServerRestart: true})
	if report.Pass {
		t.Fatalf("unexpected application error passed: %#v", report)
	}
}

func TestEvaluateReportRejectsServerDeliveryAndDatabaseErrors(t *testing.T) {
	report := &RunReport{
		ExpectedSessions: 1, PeakReadySessions: 1,
		SteadySamples: 1, SteadyReadyRatio: 1, MinSteadyReadySessions: 1,
		Operations: map[string]OperationReport{},
		RPCDeliveryOutcomes: map[string]map[string]uint64{
			"updates.getState": {"ok": 1, "edge_overload": 2},
		},
		DatabaseWork: map[string]StartupDatabaseWork{
			"messages.getDialogs": {Errors: 3},
		},
	}
	evaluateReport(report, RunConfig{MinimumReadyRatio: 1})
	if report.Pass || len(report.Failures) != 2 {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvaluateReportRequiresReclamationAndNoFloodWait(t *testing.T) {
	report := &RunReport{
		ExpectedSessions: 10, PeakReadySessions: 10, ServerMetricsScrapes: 1,
		SteadySamples: 1, SteadyReadyRatio: 1, MinSteadyReadySessions: 10,
		Operations: map[string]OperationReport{"ping": {Count: 10}},
		BaselineServerMetrics: map[string]float64{
			"telesrv_mtproto_raw_connections":      2,
			"telesrv_mtproto_logical_outbox_bytes": 3,
		},
		FinalServerMetrics: map[string]float64{
			"telesrv_mtproto_raw_connections":      2,
			"telesrv_mtproto_logical_outbox_bytes": 4,
		},
		WorkloadEndServerMetrics: map[string]float64{},
	}
	evaluateReport(report, RunConfig{MinimumReadyRatio: 1, RecoveryDuration: time.Minute, ServerMetricsURL: "http://metrics"})
	if report.Pass || len(report.Failures) != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvaluateReportAcceptsReturnToNonZeroSharedServerBaseline(t *testing.T) {
	report := &RunReport{
		ExpectedSessions: 10, PeakReadySessions: 10, ServerMetricsScrapes: 2,
		SteadySamples: 1, SteadyReadyRatio: 1, MinSteadyReadySessions: 10,
		Operations: map[string]OperationReport{"ping": {Count: 10}},
		BaselineServerMetrics: map[string]float64{
			"telesrv_mtproto_raw_connections":      2,
			"telesrv_mtproto_logical_sessions":     2,
			"telesrv_mtproto_logical_outbox_bytes": 1024,
		},
		FinalServerMetrics: map[string]float64{
			"telesrv_mtproto_raw_connections":      2,
			"telesrv_mtproto_logical_sessions":     2,
			"telesrv_mtproto_logical_outbox_bytes": 1024,
		},
		WorkloadEndServerMetrics: map[string]float64{},
	}
	evaluateReport(report, RunConfig{MinimumReadyRatio: 1, RecoveryDuration: time.Minute, ServerMetricsURL: "http://metrics"})
	if !report.Pass || len(report.Failures) != 0 {
		t.Fatalf("report = %#v", report)
	}
}
