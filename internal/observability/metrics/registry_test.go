package metrics

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"telesrv/internal/mtprotoedge"
	"telesrv/internal/rpc"
)

var (
	_ mtprotoedge.Metrics                 = (*Registry)(nil)
	_ mtprotoedge.RPCDatabaseMetrics      = (*Registry)(nil)
	_ mtprotoedge.RPCResultMetrics        = (*Registry)(nil)
	_ mtprotoedge.LogicalOutboxMetrics    = (*Registry)(nil)
	_ mtprotoedge.ConnectionIntakeMetrics = (*Registry)(nil)
	_ rpc.Metrics                         = (*Registry)(nil)
)

func TestRegistryExportsRPCDatabaseWork(t *testing.T) {
	registry := New()
	registry.RPCDatabase("messages.getDialogs", 17, 25*time.Millisecond, 0)
	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		`telesrv_rpc_db_queries_total{method="messages.getDialogs"} 17`,
		`telesrv_rpc_db_time_seconds_sum{method="messages.getDialogs"} 0.025`,
		`telesrv_rpc_db_time_seconds_count{method="messages.getDialogs"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q from:\n%s", want, body)
		}
	}
}

func TestRegistryExportsPresenceLastSeenBatchWork(t *testing.T) {
	registry := New()
	registry.PresenceLastSeenBatch(37, 25*time.Millisecond, nil)
	registry.PresenceLastSeenBatch(12, 50*time.Millisecond, errors.New("temporary"))
	registry.PresenceLastSeenSubmitted()
	registry.PresenceLastSeenSubmitted()
	registry.PresenceLastSeenPending(9)
	registry.PresenceLastSeenPending(-4)
	registry.PresenceLastSeenOverflow()
	registry.PresenceLastSeenDrainDropped(3)
	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		`telesrv_presence_last_seen_batches_total{outcome="ok"} 1`,
		`telesrv_presence_last_seen_batches_total{outcome="error"} 1`,
		`telesrv_presence_last_seen_updates_total{outcome="ok"} 37`,
		`telesrv_presence_last_seen_updates_total{outcome="error"} 12`,
		`telesrv_presence_last_seen_submitted_total 2`,
		`telesrv_presence_last_seen_pending 5`,
		`telesrv_presence_last_seen_overflow_total 1`,
		`telesrv_presence_last_seen_drain_dropped_total 3`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q from:\n%s", want, body)
		}
	}
}

func TestRegistryExportsBootstrapReadyBatchWork(t *testing.T) {
	registry := New()
	registry.BootstrapReadyPending(7)
	registry.BootstrapReadyBatch(7, 2, 25*time.Millisecond, nil)
	registry.BootstrapReadyPending(-7)
	registry.BootstrapReadyBatch(3, 0, 50*time.Millisecond, errors.New("temporary"))
	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		`telesrv_bootstrap_ready_batches_total{outcome="ok"} 1`,
		`telesrv_bootstrap_ready_batches_total{outcome="error"} 1`,
		`telesrv_bootstrap_ready_selectors_total{outcome="matched"} 2`,
		`telesrv_bootstrap_ready_selectors_total{outcome="miss"} 5`,
		`telesrv_bootstrap_ready_selectors_total{outcome="error"} 3`,
		`telesrv_bootstrap_ready_pending 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q from:\n%s", want, body)
		}
	}
}

func TestRegistryExportsActiveChannelIDsReadModelWork(t *testing.T) {
	registry := New()
	registry.ActiveChannelIDsCache("hit")
	registry.ActiveChannelIDsCache("miss")
	registry.ActiveChannelIDsCache("served")
	registry.ActiveChannelIDsPending(7)
	registry.ActiveChannelIDsBatch(7, 19, 25*time.Millisecond, nil)
	registry.ActiveChannelIDsPending(-7)
	registry.ActiveChannelIDsBatch(3, 0, 50*time.Millisecond, errors.New("temporary"))
	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		`telesrv_active_channel_ids_cache_total{outcome="hit"} 1`,
		`telesrv_active_channel_ids_cache_total{outcome="miss"} 1`,
		`telesrv_active_channel_ids_cache_total{outcome="served"} 1`,
		`telesrv_active_channel_ids_batches_total{outcome="ok"} 1`,
		`telesrv_active_channel_ids_batches_total{outcome="error"} 1`,
		`telesrv_active_channel_ids_selectors_total{outcome="ok"} 7`,
		`telesrv_active_channel_ids_selectors_total{outcome="error"} 3`,
		`telesrv_active_channel_ids_rows_total 19`,
		`telesrv_active_channel_ids_pending 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q from:\n%s", want, body)
		}
	}
}

func TestRegistryExportsBoundedAggregateMetrics(t *testing.T) {
	registry := New()
	registry.maxSeries = 2
	registry.RPCHandled("help.getConfig", 5*time.Millisecond, nil)
	registry.RPCHandled("users.getUsers", time.Second, errors.New("secret auth_key_id=deadbeef session=123"))

	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(body, "telesrv_mtproto_rpc_handled_total") || !strings.Contains(body, "telesrv_mtproto_rpc_duration_seconds_bucket") {
		t.Fatalf("expected RPC counter and histogram, got:\n%s", body)
	}
	if strings.Contains(body, "deadbeef") || strings.Contains(body, "session=123") {
		t.Fatalf("raw error identity leaked into metrics:\n%s", body)
	}
	if got := registry.series.Load(); got != registry.maxSeries {
		t.Fatalf("resident dynamic series = %d, want cap %d", got, registry.maxSeries)
	}
	if got := registry.dropped.Load(); got == 0 {
		t.Fatal("series overflow was not reported")
	}
	if !strings.Contains(body, "telesrv_metrics_dropped_observations_total 2") {
		t.Fatalf("overflow counter missing from:\n%s", body)
	}
}

func TestRegistrySanitizesAndBoundsProviderSamples(t *testing.T) {
	registry := New()
	registry.AddGaugeProvider(func() []GaugeSample {
		return []GaugeSample{{
			Name:   "9 invalid metric",
			Labels: []Label{{Name: "bad:label", Value: "quoted\"\nvalue"}},
			Value:  3,
		}}
	})
	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `__invalid_metric{bad_label="quoted\"\nvalue"} 3`) {
		t.Fatalf("provider sample was not safely sanitized:\n%s", body)
	}
}

func TestErrorOutcomeHasFixedCardinality(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{errors.New("FLOOD_WAIT_1 for phone 123"), "flood_wait"},
		{errors.New("global capacity exceeded for auth key"), "edge_overload"},
		{errors.New("arbitrary user-controlled failure"), "error"},
	}
	for _, test := range tests {
		if got := errorOutcome(test.err); got != test.want {
			t.Errorf("errorOutcome(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
