package loadharness

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var latencyBounds = [...]time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond,
	50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond,
	500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second,
	10 * time.Second, 30 * time.Second,
}

type operationMetrics struct {
	count       atomic.Uint64
	errors      atomic.Uint64
	canceled    atomic.Uint64
	floodWaits  atomic.Uint64
	timeouts    atomic.Uint64
	connections atomic.Uint64
	sumNS       atomic.Int64
	maxNS       atomic.Int64
	buckets     [len(latencyBounds)]atomic.Uint64
}

func (m *operationMetrics) observe(start time.Time, err error) {
	d := time.Since(start)
	if d < 0 {
		d = 0
	}
	m.count.Add(1)
	m.sumNS.Add(int64(d))
	for {
		previous := m.maxNS.Load()
		if int64(d) <= previous || m.maxNS.CompareAndSwap(previous, int64(d)) {
			break
		}
	}
	for i, bound := range latencyBounds {
		if d <= bound {
			m.buckets[i].Add(1)
		}
	}
	if err != nil {
		outcome := classifyError(err)
		if outcome == "canceled" {
			m.canceled.Add(1)
			return
		}
		m.errors.Add(1)
		switch outcome {
		case "flood_wait":
			m.floodWaits.Add(1)
		case "timeout":
			m.timeouts.Add(1)
		case "connection":
			m.connections.Add(1)
		}
	}
}

type OperationReport struct {
	Count            uint64  `json:"count"`
	Errors           uint64  `json:"errors"`
	Canceled         uint64  `json:"canceled"`
	FloodWaits       uint64  `json:"flood_waits"`
	Timeouts         uint64  `json:"timeouts"`
	ConnectionErrors uint64  `json:"connection_errors"`
	MeanMS           float64 `json:"mean_ms"`
	P50UpperMS       float64 `json:"p50_upper_ms"`
	P95UpperMS       float64 `json:"p95_upper_ms"`
	P99UpperMS       float64 `json:"p99_upper_ms"`
	MaxMS            float64 `json:"max_ms"`
}

func (m *operationMetrics) report() OperationReport {
	count := m.count.Load()
	report := OperationReport{
		Count: count, Errors: m.errors.Load(), Canceled: m.canceled.Load(), FloodWaits: m.floodWaits.Load(), Timeouts: m.timeouts.Load(), ConnectionErrors: m.connections.Load(),
		MaxMS: durationMS(time.Duration(m.maxNS.Load())),
	}
	if count > 0 {
		report.MeanMS = durationMS(time.Duration(m.sumNS.Load() / int64(count)))
		report.P50UpperMS = durationMS(m.quantile(count, 0.50))
		report.P95UpperMS = durationMS(m.quantile(count, 0.95))
		report.P99UpperMS = durationMS(m.quantile(count, 0.99))
	}
	return report
}

func (m *operationMetrics) quantile(count uint64, q float64) time.Duration {
	target := uint64(math.Ceil(float64(count) * q))
	for i, bound := range latencyBounds {
		if m.buckets[i].Load() >= target {
			return bound
		}
	}
	return latencyBounds[len(latencyBounds)-1]
}

func durationMS(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*1000) / 1000
}

type metricSet struct {
	mu     sync.RWMutex
	ops    map[string]*operationMetrics
	frozen bool
}

func newMetricSet(names ...string) *metricSet {
	m := &metricSet{ops: make(map[string]*operationMetrics, len(names))}
	for _, name := range names {
		m.ops[name] = &operationMetrics{}
	}
	return m
}

func (m *metricSet) observe(name string, start time.Time, err error) {
	m.mu.RLock()
	if m.frozen {
		m.mu.RUnlock()
		return
	}
	op := m.ops[name]
	if op != nil {
		debugOperationError(name, err)
		op.observe(start, err)
		m.mu.RUnlock()
		return
	}
	m.mu.RUnlock()
	{
		// Operation names are code-owned and finite, but retain a lock-protected
		// fallback for optional scenarios added by the harness.
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.frozen {
			return
		}
		op = m.ops[name]
		if op == nil && len(m.ops) < 32 {
			op = &operationMetrics{}
			m.ops[name] = op
		}
		if op != nil {
			debugOperationError(name, err)
			op.observe(start, err)
		}
	}
}

func (m *metricSet) report() map[string]OperationReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reportLocked()
}

// freeze returns the immutable pre-teardown operation cut. Holding the write
// lock waits for any observer already publishing its complete metric tuple and
// prevents later coordinated-cancel outcomes from entering the business report.
func (m *metricSet) freeze() map[string]OperationReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frozen = true
	return m.reportLocked()
}

func (m *metricSet) reportLocked() map[string]OperationReport {
	out := make(map[string]OperationReport, len(m.ops))
	for name, op := range m.ops {
		out[name] = op.report()
	}
	return out
}

type RunReport struct {
	Version                  int                             `json:"version"`
	StartOrder               string                          `json:"start_order,omitempty"`
	StartOrderSeed           int64                           `json:"start_order_seed,omitempty"`
	StartedAt                time.Time                       `json:"started_at"`
	LoadEndedAt              time.Time                       `json:"load_ended_at"`
	FinishedAt               time.Time                       `json:"finished_at"`
	RequestedDuration        string                          `json:"requested_duration"`
	RecoveryDuration         string                          `json:"recovery_duration"`
	ExpectedSessions         int                             `json:"expected_sessions"`
	PeakReadySessions        int                             `json:"peak_ready_sessions"`
	FinalReadySessions       int                             `json:"final_ready_sessions"`
	SteadySamples            int                             `json:"steady_samples"`
	SteadyReadyRatio         float64                         `json:"steady_ready_ratio"`
	MinSteadyReadySessions   int                             `json:"min_steady_ready_sessions"`
	ConnectionAttempts       uint64                          `json:"connection_attempts"`
	Reconnects               uint64                          `json:"reconnects"`
	Disconnects              uint64                          `json:"disconnects"`
	UpdatesReceived          uint64                          `json:"updates_received"`
	DownloadedBytes          uint64                          `json:"downloaded_bytes"`
	WorkerFatalErrors        uint64                          `json:"worker_fatal_errors"`
	MessageRatePerSecond     float64                         `json:"message_rate_per_second"`
	MessageScheduled         uint64                          `json:"message_scheduled"`
	MessageEnqueued          uint64                          `json:"message_enqueued"`
	MessageCompleted         uint64                          `json:"message_completed"`
	MessageQueueFull         uint64                          `json:"message_queue_full"`
	MessageNotReady          uint64                          `json:"message_not_ready"`
	Delivery                 DeliveryReport                  `json:"delivery"`
	Operations               map[string]OperationReport      `json:"operations"`
	ResponseBytes            map[string]StartupResponseBytes `json:"response_bytes,omitempty"`
	RPCDeliveryOutcomes      map[string]map[string]uint64    `json:"rpc_delivery_outcomes,omitempty"`
	DatabaseWork             map[string]StartupDatabaseWork  `json:"database_work,omitempty"`
	BaselineServerMetrics    map[string]float64              `json:"baseline_server_metrics,omitempty"`
	WorkloadEndServerMetrics map[string]float64              `json:"workload_end_server_metrics,omitempty"`
	FinalServerMetrics       map[string]float64              `json:"final_server_metrics,omitempty"`
	ServerMetricsScrapes     uint64                          `json:"server_metrics_scrapes"`
	ServerMetricsErrors      uint64                          `json:"server_metrics_errors"`
	EventsWritten            uint64                          `json:"events_written"`
	EventsDropped            uint64                          `json:"events_dropped"`
	Pass                     bool                            `json:"pass"`
	Failures                 []string                        `json:"failures,omitempty"`
}

func WriteReport(path string, report *RunReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

type eventWriter struct {
	mu                    sync.Mutex
	f                     *os.File
	written               uint64
	dropped               uint64
	connectionDeadWritten uint64
}

const (
	maxEventLines               = 100000
	maxConnectionDeadEventLines = 5000
)

func newEventWriter(path string) (*eventWriter, error) {
	if path == "" {
		return &eventWriter{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	return &eventWriter{f: f}, nil
}

func (w *eventWriter) write(value any) {
	if w == nil || w.f == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	w.mu.Lock()
	if event, ok := value.(map[string]any); ok && event["type"] == "connection_dead" {
		if w.connectionDeadWritten >= maxConnectionDeadEventLines {
			w.dropped++
			w.mu.Unlock()
			return
		}
		w.connectionDeadWritten++
	}
	if w.written >= maxEventLines {
		w.dropped++
		w.mu.Unlock()
		return
	}
	_, _ = w.f.Write(append(data, '\n'))
	w.written++
	w.mu.Unlock()
}

func (w *eventWriter) counts() (written, dropped uint64) {
	if w == nil {
		return 0, 0
	}
	w.mu.Lock()
	written, dropped = w.written, w.dropped
	w.mu.Unlock()
	return written, dropped
}

func (w *eventWriter) close() error {
	if w == nil || w.f == nil {
		return nil
	}
	w.mu.Lock()
	err := w.f.Close()
	w.f = nil
	w.mu.Unlock()
	return err
}

func sortedOperationNames(ops map[string]OperationReport) []string {
	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
