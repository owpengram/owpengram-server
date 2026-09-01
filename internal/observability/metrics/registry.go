// Package metrics provides a dependency-free Prometheus text exporter for the
// bounded runtime signals emitted by telesrv. It deliberately accepts only a
// small fixed label shape and caps dynamic series so observability cannot become
// an attacker-controlled memory cache.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// One additional fixed series reports observations rejected by this cap, so
	// the complete exporter remains bounded to 8192 resident series.
	defaultMaxSeries  = int64(8191)
	maxLabelBytes     = 96
	maxProviderSeries = 1024
)

var durationBuckets = [...]time.Duration{
	time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

// Label is a bounded Prometheus label attached to a provider sample.
type Label struct {
	Name  string
	Value string
}

// GaugeSample is an identity-free point-in-time value supplied at scrape time.
type GaugeSample struct {
	Name   string
	Labels []Label
	Value  float64
}

// GaugeProvider is evaluated only during a scrape. Providers must be bounded
// and must not perform unbounded database scans.
type GaugeProvider func() []GaugeSample

type seriesKey struct {
	name string
	k1   string
	v1   string
	k2   string
	v2   string
	k3   string
	v3   string
}

func newSeriesKey(name string, labels ...Label) seriesKey {
	key := seriesKey{name: sanitizeMetricName(name)}
	if len(labels) > 0 {
		key.k1, key.v1 = sanitizeLabelName(labels[0].Name), sanitizeLabelValue(labels[0].Value)
	}
	if len(labels) > 1 {
		key.k2, key.v2 = sanitizeLabelName(labels[1].Name), sanitizeLabelValue(labels[1].Value)
	}
	if len(labels) > 2 {
		key.k3, key.v3 = sanitizeLabelName(labels[2].Name), sanitizeLabelValue(labels[2].Value)
	}
	return key
}

func (k seriesKey) overflow() seriesKey {
	if k.k1 != "" {
		k.v1 = "overflow"
	}
	if k.k2 != "" {
		k.v2 = "overflow"
	}
	if k.k3 != "" {
		k.v3 = "overflow"
	}
	return k
}

type counterSeries struct {
	key   seriesKey
	value atomic.Uint64
}

type gaugeSeries struct {
	key   seriesKey
	value atomic.Int64
}

type histogramSeries struct {
	key     seriesKey
	buckets [len(durationBuckets)]atomic.Uint64
	count   atomic.Uint64
	sumNS   atomic.Int64
}

func (h *histogramSeries) observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	h.count.Add(1)
	h.sumNS.Add(int64(d))
	for i, bound := range durationBuckets {
		if d <= bound {
			h.buckets[i].Add(1)
		}
	}
}

// Registry implements the mtprotoedge and rpc metric hooks and serves the
// Prometheus text exposition format.
type Registry struct {
	maxSeries int64
	series    atomic.Int64
	seriesMu  sync.Mutex
	dropped   atomic.Uint64
	counters  sync.Map // seriesKey -> *counterSeries
	gauges    sync.Map // seriesKey -> *gaugeSeries
	hist      sync.Map // seriesKey -> *histogramSeries

	providersMu sync.RWMutex
	providers   []GaugeProvider
}

// New returns an empty bounded registry.
func New() *Registry {
	return &Registry{maxSeries: defaultMaxSeries}
}

// AddGaugeProvider registers a bounded point-in-time provider.
func (r *Registry) AddGaugeProvider(provider GaugeProvider) {
	if r == nil || provider == nil {
		return
	}
	r.providersMu.Lock()
	r.providers = append(r.providers, provider)
	r.providersMu.Unlock()
}

func (r *Registry) counter(key seriesKey) *counterSeries {
	if existing, ok := r.counters.Load(key); ok {
		return existing.(*counterSeries)
	}
	r.seriesMu.Lock()
	defer r.seriesMu.Unlock()
	if existing, ok := r.counters.Load(key); ok {
		return existing.(*counterSeries)
	}
	if r.series.Load() >= r.maxSeries {
		r.dropped.Add(1)
		return &counterSeries{}
	}
	created := &counterSeries{key: key}
	r.counters.Store(key, created)
	r.series.Add(1)
	return created
}

func (r *Registry) gauge(key seriesKey) *gaugeSeries {
	if existing, ok := r.gauges.Load(key); ok {
		return existing.(*gaugeSeries)
	}
	r.seriesMu.Lock()
	defer r.seriesMu.Unlock()
	if existing, ok := r.gauges.Load(key); ok {
		return existing.(*gaugeSeries)
	}
	if r.series.Load() >= r.maxSeries {
		r.dropped.Add(1)
		return &gaugeSeries{}
	}
	created := &gaugeSeries{key: key}
	r.gauges.Store(key, created)
	r.series.Add(1)
	return created
}

func (r *Registry) histogram(key seriesKey) *histogramSeries {
	if existing, ok := r.hist.Load(key); ok {
		return existing.(*histogramSeries)
	}
	r.seriesMu.Lock()
	defer r.seriesMu.Unlock()
	if existing, ok := r.hist.Load(key); ok {
		return existing.(*histogramSeries)
	}
	if r.series.Load() >= r.maxSeries {
		r.dropped.Add(1)
		return &histogramSeries{}
	}
	created := &histogramSeries{key: key}
	r.hist.Store(key, created)
	r.series.Add(1)
	return created
}

func (r *Registry) inc(name string, labels ...Label) {
	if r == nil {
		return
	}
	r.counter(newSeriesKey(name, labels...)).value.Add(1)
}

func (r *Registry) add(name string, value uint64, labels ...Label) {
	if r == nil || value == 0 {
		return
	}
	r.counter(newSeriesKey(name, labels...)).value.Add(value)
}

func (r *Registry) addGauge(name string, delta int64, labels ...Label) {
	if r == nil || delta == 0 {
		return
	}
	r.gauge(newSeriesKey(name, labels...)).value.Add(delta)
}

func (r *Registry) observe(name string, d time.Duration, labels ...Label) {
	if r == nil {
		return
	}
	r.histogram(newSeriesKey(name, labels...)).observe(d)
}

// ConnOpened implements mtprotoedge.Metrics.
func (r *Registry) ConnOpened() {
	r.inc("telesrv_mtproto_connections_opened_total")
	r.addGauge("telesrv_mtproto_connections_active", 1)
}

// ConnClosed implements mtprotoedge.Metrics.
func (r *Registry) ConnClosed() {
	r.inc("telesrv_mtproto_connections_closed_total")
	r.addGauge("telesrv_mtproto_connections_active", -1)
}

// HandshakeDone implements mtprotoedge.Metrics.
func (r *Registry) HandshakeDone(d time.Duration) {
	r.inc("telesrv_mtproto_handshakes_total")
	r.observe("telesrv_mtproto_handshake_duration_seconds", d)
}

// RPCHandled implements mtprotoedge.Metrics.
func (r *Registry) RPCHandled(method string, d time.Duration, err error) {
	labels := []Label{{Name: "method", Value: method}, {Name: "outcome", Value: errorOutcome(err)}}
	r.inc("telesrv_mtproto_rpc_handled_total", labels...)
	r.observe("telesrv_mtproto_rpc_duration_seconds", d, labels...)
}

// RPCDatabase implements mtprotoedge.RPCDatabaseMetrics.
func (r *Registry) RPCDatabase(method string, queries int64, d time.Duration, errors int64) {
	labels := []Label{{Name: "method", Value: method}}
	if queries > 0 {
		r.add("telesrv_rpc_db_queries_total", uint64(queries), labels...)
	}
	if errors > 0 {
		r.add("telesrv_rpc_db_errors_total", uint64(errors), labels...)
	}
	if d > 0 {
		r.observe("telesrv_rpc_db_time_seconds", d, labels...)
	}
}

// InboundRPCQueued implements mtprotoedge.Metrics.
func (r *Registry) InboundRPCQueued(method string, length, capacity int) {
	r.inc("telesrv_mtproto_inbound_rpc_queued_total", Label{Name: "method", Value: method})
	r.add("telesrv_mtproto_inbound_rpc_queue_depth_observed_total", uint64(max(length, 0)), Label{Name: "method", Value: method})
	if capacity > 0 && length >= capacity {
		r.inc("telesrv_mtproto_inbound_rpc_queue_full_total", Label{Name: "method", Value: method})
	}
}

// InboundRPCStarted implements mtprotoedge.Metrics.
func (r *Registry) InboundRPCStarted(method string, queueWait time.Duration) {
	r.observe("telesrv_mtproto_inbound_rpc_queue_wait_seconds", queueWait, Label{Name: "method", Value: method})
}

// InboundRPCDropped implements mtprotoedge.Metrics.
func (r *Registry) InboundRPCDropped(method, reason string) {
	r.inc("telesrv_mtproto_inbound_rpc_dropped_total", Label{Name: "method", Value: method}, Label{Name: "reason", Value: reason})
}

// OutboundSend implements mtprotoedge.Metrics.
func (r *Registry) OutboundSend(typeID uint32, queueWait time.Duration, bytes int, err error) {
	labels := []Label{{Name: "type_id", Value: fmt.Sprintf("%08x", typeID)}, {Name: "outcome", Value: errorOutcome(err)}}
	r.inc("telesrv_mtproto_outbound_send_total", labels...)
	r.add("telesrv_mtproto_outbound_send_bytes_total", uint64(max(bytes, 0)), labels...)
	r.observe("telesrv_mtproto_outbound_queue_wait_seconds", queueWait, labels...)
}

// OutboundResend implements mtprotoedge.Metrics.
func (r *Registry) OutboundResend(count int, err error) {
	labels := []Label{{Name: "outcome", Value: errorOutcome(err)}}
	r.inc("telesrv_mtproto_outbound_resend_requests_total", labels...)
	r.add("telesrv_mtproto_outbound_resent_frames_total", uint64(max(count, 0)), labels...)
}

// OutboundDropped implements mtprotoedge.Metrics.
func (r *Registry) OutboundDropped(reason string) {
	r.inc("telesrv_mtproto_outbound_dropped_total", Label{Name: "reason", Value: reason})
}

// OutboundQueueWait implements mtprotoedge.Metrics.
func (r *Registry) OutboundQueueWait(length, capacity int) {
	r.inc("telesrv_mtproto_outbound_queue_wait_total")
	if capacity > 0 && length >= capacity {
		r.inc("telesrv_mtproto_outbound_queue_full_total")
	}
}

// RPCResultPrepared implements mtprotoedge.RPCResultMetrics.
func (r *Registry) RPCResultPrepared(method, priority string, innerBytes, wireBytes int, compressed bool) {
	encoding := "plain"
	if compressed {
		encoding = "gzip"
	}
	labels := []Label{{Name: "method", Value: method}, {Name: "priority", Value: priority}, {Name: "encoding", Value: encoding}}
	r.inc("telesrv_mtproto_rpc_result_prepared_total", labels...)
	r.add("telesrv_mtproto_rpc_result_inner_bytes_total", uint64(max(innerBytes, 0)), labels...)
	r.add("telesrv_mtproto_rpc_result_wire_bytes_total", uint64(max(wireBytes, 0)), labels...)
}

// RPCResultDelivered implements mtprotoedge.RPCResultMetrics.
func (r *Registry) RPCResultDelivered(method string, egressLatency time.Duration, wireBytes int, err error) {
	labels := []Label{{Name: "method", Value: method}, {Name: "outcome", Value: errorOutcome(err)}}
	r.inc("telesrv_mtproto_rpc_result_delivered_total", labels...)
	r.add("telesrv_mtproto_rpc_result_delivered_bytes_total", uint64(max(wireBytes, 0)), labels...)
	r.observe("telesrv_mtproto_rpc_result_egress_seconds", egressLatency, labels...)
}

// LogicalOutboxAcknowledged implements mtprotoedge.LogicalOutboxMetrics.
func (r *Registry) LogicalOutboxAcknowledged(bytes int, retainedFor time.Duration, rpcResult bool) {
	kind := "service_or_update"
	if rpcResult {
		kind = "rpc_result"
	}
	labels := []Label{{Name: "kind", Value: kind}}
	r.inc("telesrv_mtproto_logical_outbox_acked_frames_total", labels...)
	r.add("telesrv_mtproto_logical_outbox_acked_bytes_total", uint64(max(bytes, 0)), labels...)
	r.observe("telesrv_mtproto_logical_outbox_retained_seconds", retainedFor, labels...)
}

// ConnectionIntake implements mtprotoedge.ConnectionIntakeMetrics.
func (r *Registry) ConnectionIntake(stage, outcome string, d time.Duration) {
	labels := []Label{{Name: "stage", Value: stage}, {Name: "outcome", Value: outcome}}
	r.inc("telesrv_mtproto_connection_intake_total", labels...)
	r.observe("telesrv_mtproto_connection_intake_seconds", d, labels...)
}

// MessageSend implements rpc.Metrics.
func (r *Registry) MessageSend(d time.Duration, duplicate bool, err error) {
	dup := "false"
	if duplicate {
		dup = "true"
	}
	labels := []Label{{Name: "outcome", Value: errorOutcome(err)}, {Name: "duplicate", Value: dup}}
	r.inc("telesrv_rpc_message_send_total", labels...)
	r.observe("telesrv_rpc_message_send_duration_seconds", d, labels...)
}

// MessageRateLimited implements rpc.Metrics.
func (r *Registry) MessageRateLimited(retryAfterSeconds int) {
	r.inc("telesrv_rpc_message_rate_limited_total")
	r.add("telesrv_rpc_message_rate_limit_wait_seconds_total", uint64(max(retryAfterSeconds, 0)))
}

// OutboxClaimed implements rpc.Metrics.
func (r *Registry) OutboxClaimed(count int) {
	r.add("telesrv_rpc_outbox_claimed_total", uint64(max(count, 0)))
}

// OutboxDelivered implements rpc.Metrics.
func (r *Registry) OutboxDelivered(d time.Duration) {
	r.inc("telesrv_rpc_outbox_delivered_total")
	r.observe("telesrv_rpc_outbox_delivery_seconds", d)
}

// OutboxFailed implements rpc.Metrics.
func (r *Registry) OutboxFailed(err error) {
	r.inc("telesrv_rpc_outbox_failed_total", Label{Name: "outcome", Value: errorOutcome(err)})
}

// BootstrapReadyBatch implements postgres.BootstrapReadyBatchMetrics without
// importing store identities into the observability layer.
func (r *Registry) BootstrapReadyBatch(inputs int, matched int, d time.Duration, err error) {
	outcome := errorOutcome(err)
	r.inc("telesrv_bootstrap_ready_batches_total", Label{Name: "outcome", Value: outcome})
	r.observe("telesrv_bootstrap_ready_batch_duration_seconds", d, Label{Name: "outcome", Value: outcome})
	inputs = max(inputs, 0)
	matched = max(min(matched, inputs), 0)
	if err != nil {
		r.add("telesrv_bootstrap_ready_selectors_total", uint64(inputs), Label{Name: "outcome", Value: "error"})
		return
	}
	r.add("telesrv_bootstrap_ready_selectors_total", uint64(matched), Label{Name: "outcome", Value: "matched"})
	r.add("telesrv_bootstrap_ready_selectors_total", uint64(inputs-matched), Label{Name: "outcome", Value: "miss"})
}

// BootstrapReadyPending implements postgres.BootstrapReadyBatchMetrics.
func (r *Registry) BootstrapReadyPending(delta int) {
	r.addGauge("telesrv_bootstrap_ready_pending", int64(delta))
}

// ActiveChannelIDsCache implements channels.ActiveChannelIDsReadModelMetrics.
func (r *Registry) ActiveChannelIDsCache(outcome string) {
	r.inc("telesrv_active_channel_ids_cache_total", Label{Name: "outcome", Value: outcome})
}

// ActiveChannelIDsBatch implements postgres.ActiveChannelIDsBatchMetrics.
func (r *Registry) ActiveChannelIDsBatch(selectors int, rows int, d time.Duration, err error) {
	outcome := errorOutcome(err)
	r.inc("telesrv_active_channel_ids_batches_total", Label{Name: "outcome", Value: outcome})
	r.add("telesrv_active_channel_ids_selectors_total", uint64(max(selectors, 0)), Label{Name: "outcome", Value: outcome})
	if err == nil {
		r.add("telesrv_active_channel_ids_rows_total", uint64(max(rows, 0)))
	}
	r.observe("telesrv_active_channel_ids_batch_duration_seconds", d, Label{Name: "outcome", Value: outcome})
}

// ActiveChannelIDsPending implements postgres.ActiveChannelIDsBatchMetrics.
func (r *Registry) ActiveChannelIDsPending(delta int) {
	r.addGauge("telesrv_active_channel_ids_pending", int64(delta))
}

// PresenceLastSeenBatch implements rpc.Metrics.
func (r *Registry) PresenceLastSeenBatch(count int, d time.Duration, err error) {
	labels := []Label{{Name: "outcome", Value: errorOutcome(err)}}
	r.inc("telesrv_presence_last_seen_batches_total", labels...)
	r.add("telesrv_presence_last_seen_updates_total", uint64(max(count, 0)), labels...)
	r.observe("telesrv_presence_last_seen_batch_duration_seconds", d, labels...)
}

// PresenceLastSeenSubmitted implements rpc.Metrics.
func (r *Registry) PresenceLastSeenSubmitted() {
	r.inc("telesrv_presence_last_seen_submitted_total")
}

// PresenceLastSeenPending implements rpc.Metrics.
func (r *Registry) PresenceLastSeenPending(delta int) {
	r.addGauge("telesrv_presence_last_seen_pending", int64(delta))
}

// PresenceLastSeenOverflow implements rpc.Metrics.
func (r *Registry) PresenceLastSeenOverflow() {
	r.inc("telesrv_presence_last_seen_overflow_total")
}

// PresenceLastSeenDrainDropped implements rpc.Metrics.
func (r *Registry) PresenceLastSeenDrainDropped(count int) {
	r.add("telesrv_presence_last_seen_drain_dropped_total", uint64(max(count, 0)))
}

// ServeHTTP writes Prometheus text format.
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var counters []*counterSeries
	r.counters.Range(func(_, value any) bool {
		counters = append(counters, value.(*counterSeries))
		return true
	})
	var gauges []*gaugeSeries
	r.gauges.Range(func(_, value any) bool {
		gauges = append(gauges, value.(*gaugeSeries))
		return true
	})
	var histograms []*histogramSeries
	r.hist.Range(func(_, value any) bool {
		histograms = append(histograms, value.(*histogramSeries))
		return true
	})

	providerSamples := r.providerSamples()
	sort.Slice(counters, func(i, j int) bool { return lessSeries(counters[i].key, counters[j].key) })
	sort.Slice(gauges, func(i, j int) bool { return lessSeries(gauges[i].key, gauges[j].key) })
	sort.Slice(histograms, func(i, j int) bool { return lessSeries(histograms[i].key, histograms[j].key) })
	sort.Slice(providerSamples, func(i, j int) bool {
		if providerSamples[i].Name != providerSamples[j].Name {
			return providerSamples[i].Name < providerSamples[j].Name
		}
		return labelsString(providerSamples[i].Labels) < labelsString(providerSamples[j].Labels)
	})

	var out strings.Builder
	fmt.Fprintln(&out, "# TYPE telesrv_metrics_dropped_observations_total counter")
	fmt.Fprintf(&out, "telesrv_metrics_dropped_observations_total %d\n", r.dropped.Load())
	writeCounterSeries(&out, counters)
	writeGaugeSeries(&out, gauges, providerSamples)
	writeHistogramSeries(&out, histograms)
	_, _ = w.Write([]byte(out.String()))
}

func (r *Registry) providerSamples() (samples []GaugeSample) {
	r.providersMu.RLock()
	providers := append([]GaugeProvider(nil), r.providers...)
	r.providersMu.RUnlock()
	for _, provider := range providers {
		func() {
			defer func() { _ = recover() }()
			for _, sample := range provider() {
				if len(samples) >= maxProviderSeries {
					r.dropped.Add(1)
					return
				}
				sample.Name = sanitizeMetricName(sample.Name)
				if len(sample.Labels) > 3 {
					sample.Labels = sample.Labels[:3]
				}
				for i := range sample.Labels {
					sample.Labels[i].Name = sanitizeLabelName(sample.Labels[i].Name)
					sample.Labels[i].Value = sanitizeLabelValue(sample.Labels[i].Value)
				}
				samples = append(samples, sample)
			}
		}()
	}
	return samples
}

func writeCounterSeries(out *strings.Builder, series []*counterSeries) {
	last := ""
	for _, item := range series {
		if item.key.name != last {
			fmt.Fprintf(out, "# TYPE %s counter\n", item.key.name)
			last = item.key.name
		}
		writeSample(out, item.key.name, keyLabels(item.key), float64(item.value.Load()))
	}
}

func writeGaugeSeries(out *strings.Builder, series []*gaugeSeries, provider []GaugeSample) {
	type sample struct {
		name   string
		labels []Label
		value  float64
	}
	all := make([]sample, 0, len(series)+len(provider))
	for _, item := range series {
		all = append(all, sample{name: item.key.name, labels: keyLabels(item.key), value: float64(item.value.Load())})
	}
	for _, item := range provider {
		all = append(all, sample{name: item.Name, labels: item.Labels, value: item.Value})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].name != all[j].name {
			return all[i].name < all[j].name
		}
		return labelsString(all[i].labels) < labelsString(all[j].labels)
	})
	last := ""
	for _, item := range all {
		if item.name != last {
			fmt.Fprintf(out, "# TYPE %s gauge\n", item.name)
			last = item.name
		}
		writeSample(out, item.name, item.labels, item.value)
	}
}

func writeHistogramSeries(out *strings.Builder, series []*histogramSeries) {
	last := ""
	for _, item := range series {
		if item.key.name != last {
			fmt.Fprintf(out, "# TYPE %s histogram\n", item.key.name)
			last = item.key.name
		}
		labels := keyLabels(item.key)
		for i, bound := range durationBuckets {
			bucketLabels := append(append([]Label(nil), labels...), Label{Name: "le", Value: strconv.FormatFloat(bound.Seconds(), 'g', -1, 64)})
			writeSample(out, item.key.name+"_bucket", bucketLabels, float64(item.buckets[i].Load()))
		}
		writeSample(out, item.key.name+"_bucket", append(append([]Label(nil), labels...), Label{Name: "le", Value: "+Inf"}), float64(item.count.Load()))
		writeSample(out, item.key.name+"_sum", labels, time.Duration(item.sumNS.Load()).Seconds())
		writeSample(out, item.key.name+"_count", labels, float64(item.count.Load()))
	}
}

func writeSample(out *strings.Builder, name string, labels []Label, value float64) {
	out.WriteString(name)
	if len(labels) > 0 {
		out.WriteByte('{')
		for i, label := range labels {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(label.Name)
			out.WriteString("=\"")
			out.WriteString(escapeLabel(label.Value))
			out.WriteByte('"')
		}
		out.WriteByte('}')
	}
	out.WriteByte(' ')
	out.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	out.WriteByte('\n')
}

func keyLabels(key seriesKey) []Label {
	labels := make([]Label, 0, 3)
	if key.k1 != "" {
		labels = append(labels, Label{Name: key.k1, Value: key.v1})
	}
	if key.k2 != "" {
		labels = append(labels, Label{Name: key.k2, Value: key.v2})
	}
	if key.k3 != "" {
		labels = append(labels, Label{Name: key.k3, Value: key.v3})
	}
	return labels
}

func lessSeries(a, b seriesKey) bool {
	if a.name != b.name {
		return a.name < b.name
	}
	return a.k1+a.v1+a.k2+a.v2+a.k3+a.v3 < b.k1+b.v1+b.k2+b.v2+b.k3+b.v3
}

func labelsString(labels []Label) string {
	var b strings.Builder
	for _, label := range labels {
		b.WriteString(label.Name)
		b.WriteByte('=')
		b.WriteString(label.Value)
		b.WriteByte(',')
	}
	return b.String()
}

func errorOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	message := strings.ToUpper(err.Error())
	switch {
	case strings.Contains(message, "FLOOD_WAIT"):
		return "flood_wait"
	case strings.Contains(message, "WORKER_BUSY") || strings.Contains(message, "BUDGET") || strings.Contains(message, "CAPACITY"):
		return "edge_overload"
	default:
		return "error"
	}
}

func sanitizeMetricName(value string) string {
	if value == "" {
		return "telesrv_invalid_metric"
	}
	var b strings.Builder
	for i, r := range value {
		valid := r == '_' || r == ':' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9'
		if valid {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func sanitizeLabelName(value string) string {
	return strings.ReplaceAll(sanitizeMetricName(value), ":", "_")
}

func sanitizeLabelValue(value string) string {
	if value == "" {
		return "unknown"
	}
	if len(value) > maxLabelBytes {
		return value[:maxLabelBytes]
	}
	return value
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\"", "\\\"")
}
