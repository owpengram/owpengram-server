package moderation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"telesrv/internal/domain"
)

type recordingReporter struct {
	mu      sync.Mutex
	reports []string
	targets []int64
	done    chan struct{}
}

func (r *recordingReporter) ReportPeer(_ context.Context, reporter int64, source domain.ModerationReportSource, target domain.Peer, reason domain.ModerationReason, option, comment string, _ time.Time) (domain.ModerationReport, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reporter != domain.OfficialSystemUserID || source != domain.ModerationSourceAccountPeer ||
		reason != domain.ModerationReasonOther || option != autoFlagOption || target.Type != domain.PeerTypeUser {
		panic("unexpected report shape")
	}
	r.reports = append(r.reports, comment)
	r.targets = append(r.targets, target.ID)
	r.done <- struct{}{}
	return domain.ModerationReport{}, true, nil
}

func (r *recordingReporter) wait(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for report %d", i+1)
		}
	}
}

func TestAutoFlaggerFilesRateLimitedReports(t *testing.T) {
	rep := &recordingReporter{done: make(chan struct{}, 16)}
	a := NewAutoFlagger(rep, domain.OfficialSystemUserID, nil)
	clock := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return clock }
	ctx := context.Background()

	// A blocked hit reports at once.
	a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: 500, Source: "upload", Kind: "blocked_format", Blocked: true})
	rep.wait(t, 1)
	// More hits within 24h do not file again.
	for i := 0; i < 5; i++ {
		a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: 500, Source: "upload", Kind: "opaque", Blocked: true})
	}
	// Report-only hits need 3 within the window.
	a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: 600, Source: "message", Kind: "opaque_token"})
	a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: 600, Source: "message", Kind: "opaque_token"})
	a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: 600, Source: "message", Kind: "opaque_token"})
	rep.wait(t, 1)
	// System accounts are never reported.
	a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: domain.OfficialSystemUserID, Kind: "opaque", Blocked: true})

	// After the window, a new report.
	clock = clock.Add(25 * time.Hour)
	a.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{UserID: 500, Source: "upload", Kind: "opaque", Blocked: true})
	rep.wait(t, 1)

	select {
	case <-rep.done:
		t.Fatal("unexpected extra report")
	case <-time.After(100 * time.Millisecond):
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.reports) != 3 || rep.targets[0] != 500 || rep.targets[1] != 600 || rep.targets[2] != 500 {
		t.Fatalf("reports = %v targets = %v", rep.reports, rep.targets)
	}
	if !strings.Contains(rep.reports[1], "message:opaque_token x3") {
		t.Fatalf("comment = %q", rep.reports[1])
	}
}
