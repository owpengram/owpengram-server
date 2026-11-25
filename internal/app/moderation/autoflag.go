package moderation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// AutoFlagger turns content-policy hits (uploads and message texts that
// tripped an anti-abuse check) into moderation reports so they show up in the
// admin moderation queue, filed by the official system account against the
// user. It never blocks the caller: reports are filed asynchronously.
//
// Rate limiting: per user at most one report per autoFlagWindow. A blocked hit
// files immediately; report-only hits file once a user reaches
// autoFlagSoftThreshold hits inside the window. The report comment lists the
// signal counts (metadata only, never content).
type AutoFlagger struct {
	reports  peerReporter
	reporter int64
	log      *zap.Logger
	now      func() time.Time

	mu    sync.Mutex
	users map[int64]*autoFlagState
}

type peerReporter interface {
	ReportPeer(ctx context.Context, reporterUserID int64, source domain.ModerationReportSource, target domain.Peer, reason domain.ModerationReason, option, comment string, createdAt time.Time) (domain.ModerationReport, bool, error)
}

type autoFlagState struct {
	windowStart time.Time
	counts      map[string]int
	blocked     int
	lastReport  time.Time
}

const (
	autoFlagWindow        = 24 * time.Hour
	autoFlagSoftThreshold = 3
	autoFlagMaxUsers      = 50000
	autoFlagOption        = "auto_content_policy"
)

// NewAutoFlagger files reports through reports as reporterUserID (normally
// domain.OfficialSystemUserID).
func NewAutoFlagger(reports peerReporter, reporterUserID int64, log *zap.Logger) *AutoFlagger {
	if log == nil {
		log = zap.NewNop()
	}
	return &AutoFlagger{reports: reports, reporter: reporterUserID, log: log, now: time.Now, users: map[int64]*autoFlagState{}}
}

// ReportContentPolicyHit implements domain.ContentPolicyReporter.
func (a *AutoFlagger) ReportContentPolicyHit(_ context.Context, hit domain.ContentPolicyHit) {
	if a == nil || a.reports == nil || hit.UserID <= 0 || domain.IsSystemUserID(hit.UserID) {
		return
	}
	now := a.now()
	comment, file := a.record(hit, now)
	if !file {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _, err := a.reports.ReportPeer(ctx, a.reporter, domain.ModerationSourceAccountPeer,
			domain.Peer{Type: domain.PeerTypeUser, ID: hit.UserID},
			domain.ModerationReasonOther, autoFlagOption, comment, now)
		if err != nil {
			a.log.Warn("auto content-policy report failed", zap.Int64("user_id", hit.UserID), zap.Error(err))
		}
	}()
}

// record updates the per-user window and decides whether to file a report.
func (a *AutoFlagger) record(hit domain.ContentPolicyHit, now time.Time) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.users[hit.UserID]
	if st == nil || now.Sub(st.windowStart) >= autoFlagWindow {
		if st == nil && len(a.users) >= autoFlagMaxUsers {
			a.pruneLocked(now)
		}
		last := time.Time{}
		if st != nil {
			last = st.lastReport
		}
		st = &autoFlagState{windowStart: now, counts: map[string]int{}, lastReport: last}
		a.users[hit.UserID] = st
	}
	key := hit.Source + ":" + hit.Kind
	if hit.Format != "" {
		key += "(" + hit.Format + ")"
	}
	st.counts[key]++
	if hit.Blocked {
		st.blocked++
	}
	total := 0
	for _, c := range st.counts {
		total += c
	}
	if !st.lastReport.IsZero() && now.Sub(st.lastReport) < autoFlagWindow {
		return "", false
	}
	if !hit.Blocked && total < autoFlagSoftThreshold {
		return "", false
	}
	st.lastReport = now
	keys := make([]string, 0, len(st.counts))
	for k := range st.counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s x%d", k, st.counts[k]))
	}
	comment := fmt.Sprintf("Automatic content-policy flag (server signal, no human has viewed content). "+
		"Last 24h: %d hits, %d blocked: %s.", total, st.blocked, strings.Join(parts, ", "))
	return comment, true
}

func (a *AutoFlagger) pruneLocked(now time.Time) {
	for id, st := range a.users {
		if now.Sub(st.windowStart) >= autoFlagWindow && now.Sub(st.lastReport) >= autoFlagWindow {
			delete(a.users, id)
		}
	}
}
