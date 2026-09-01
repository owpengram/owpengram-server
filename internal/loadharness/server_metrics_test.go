package loadharness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerMetricsScrapeSelectsBoundedCapacitySignals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `telesrv_mtproto_raw_connections 500`)
		fmt.Fprintln(w, `telesrv_mtproto_sessions{state="active"} 499`)
		fmt.Fprintln(w, `telesrv_mtproto_sessions{state="provisional"} 1`)
		for i := 0; i < 256; i++ {
			fmt.Fprintf(w, "telesrv_mtproto_rpc_result_wire_bytes_total{method=%q} 1\n", fmt.Sprintf("method-%d", i))
		}
		fmt.Fprintln(w, `telesrv_mtproto_rpc_result_delivered_total{method="users.getUsers",outcome="ok"} 7`)
		fmt.Fprintln(w, `telesrv_mtproto_rpc_result_delivered_total{method="users.getUsers",outcome="edge_overload"} 3`)
		fmt.Fprintln(w, `telesrv_mtproto_rpc_result_delivered_bytes_total{method="users.getUsers",outcome="ok"} 700`)
		fmt.Fprintln(w, `telesrv_mtproto_rpc_result_delivered_bytes_total{method="users.getUsers",outcome="edge_overload"} 300`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_batches_total{outcome="ok"} 17`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_batches_total{outcome="error"} 2`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_updates_total{outcome="ok"} 900`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_updates_total{outcome="error"} 23`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_submitted_total 923`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_pending 11`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_overflow_total 1`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_drain_dropped_total 4`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_batches_total{outcome="ok"} 10`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_batches_total{outcome="error"} 1`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_selectors_total{outcome="matched"} 2`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_selectors_total{outcome="miss"} 800`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_selectors_total{outcome="error"} 3`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_pending 1`)
		fmt.Fprintln(w, `unrelated_high_cardinality{user_id="secret"} 1`)
	}))
	defer server.Close()
	client := newServerMetricsClient(server.URL)
	values, err := client.scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if values["telesrv_mtproto_raw_connections"] != 500 || values["telesrv_mtproto_sessions"] != 500 || values["telesrv_mtproto_rpc_result_wire_bytes_total"] != 256 {
		t.Fatalf("values = %#v", values)
	}
	if values[`telesrv_mtproto_rpc_result_wire_bytes_total{method="method-17"}`] != 1 {
		t.Fatalf("method response bytes = %#v", values)
	}
	if values[`telesrv_mtproto_sessions{state="active"}`] != 499 || values[`telesrv_mtproto_sessions{state="provisional"}`] != 1 {
		t.Fatalf("session state values = %#v", values)
	}
	if values[`telesrv_mtproto_rpc_result_delivered_total{method="users.getUsers",outcome="ok"}`] != 7 ||
		values[`telesrv_mtproto_rpc_result_delivered_total{method="users.getUsers",outcome="edge_overload"}`] != 3 {
		t.Fatalf("delivery outcomes = %#v", values)
	}
	if values[`telesrv_presence_last_seen_batches_total{outcome="ok"}`] != 17 ||
		values[`telesrv_presence_last_seen_batches_total{outcome="error"}`] != 2 ||
		values[`telesrv_presence_last_seen_updates_total{outcome="ok"}`] != 900 ||
		values[`telesrv_presence_last_seen_updates_total{outcome="error"}`] != 23 ||
		values["telesrv_presence_last_seen_submitted_total"] != 923 ||
		values["telesrv_presence_last_seen_pending"] != 11 ||
		values["telesrv_presence_last_seen_overflow_total"] != 1 ||
		values["telesrv_presence_last_seen_drain_dropped_total"] != 4 {
		t.Fatalf("presence batch values = %#v", values)
	}
	if values[`telesrv_bootstrap_ready_batches_total{outcome="ok"}`] != 10 ||
		values[`telesrv_bootstrap_ready_batches_total{outcome="error"}`] != 1 ||
		values[`telesrv_bootstrap_ready_selectors_total{outcome="matched"}`] != 2 ||
		values[`telesrv_bootstrap_ready_selectors_total{outcome="miss"}`] != 800 ||
		values[`telesrv_bootstrap_ready_selectors_total{outcome="error"}`] != 3 ||
		values["telesrv_bootstrap_ready_pending"] != 1 {
		t.Fatalf("bootstrap readiness values = %#v", values)
	}
	if len(values) != 285 || client.successes() != 1 || client.failures() != 0 {
		t.Fatalf("bounded values/scrapes = %#v, %d/%d", values, client.successes(), client.failures())
	}
}

func TestPrometheusLabelValue(t *testing.T) {
	value, ok := prometheusLabelValue(`metric{encoding="gzip",method="messages.getDialogs",outcome="ok"}`, "method")
	if !ok || value != "messages.getDialogs" {
		t.Fatalf("method = %q, %v", value, ok)
	}
	if _, ok := prometheusLabelValue(`metric{encoding="gzip"}`, "method"); ok {
		t.Fatal("missing method label was accepted")
	}
}

func TestServerMetricsWaitsForPresenceLastSeenSettlement(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		if call < 3 {
			fmt.Fprintln(w, `telesrv_presence_last_seen_submitted_total 1`)
			fmt.Fprintln(w, `telesrv_presence_last_seen_pending 1`)
			fmt.Fprintln(w, `telesrv_bootstrap_ready_pending 1`)
			return
		}
		fmt.Fprintln(w, `telesrv_presence_last_seen_submitted_total 2`)
		fmt.Fprintln(w, `telesrv_presence_last_seen_pending 0`)
		fmt.Fprintln(w, `telesrv_bootstrap_ready_pending 0`)
	}))
	defer server.Close()
	client := newServerMetricsClient(server.URL)
	values, err := client.waitForPresenceLastSeenSettlement(context.Background(), 0, 2, time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if values["telesrv_presence_last_seen_submitted_total"] != 2 || values["telesrv_presence_last_seen_pending"] != 0 ||
		values["telesrv_bootstrap_ready_pending"] != 0 {
		t.Fatalf("settled values = %#v", values)
	}
	if calls.Load() != 3 {
		t.Fatalf("scrape calls = %d, want 3", calls.Load())
	}
}

func TestMetricValueDoesNotDoubleCountAggregateAndStateBreakdown(t *testing.T) {
	values := map[string]float64{
		"telesrv_mtproto_logical_sessions":                   254,
		`telesrv_mtproto_logical_sessions{state="retained"}`: 254,
		`telesrv_mtproto_logical_sessions{state="offline"}`:  0,
	}
	if got := metricValue(values, "telesrv_mtproto_logical_sessions"); got != 254 {
		t.Fatalf("logical sessions = %v, want aggregate 254", got)
	}
	delete(values, "telesrv_mtproto_logical_sessions")
	if got := metricValue(values, "telesrv_mtproto_logical_sessions"); got != 254 {
		t.Fatalf("legacy labeled-only logical sessions = %v, want 254", got)
	}
}
