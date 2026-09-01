package loadharness

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iamxvbaba/td/telegram"
	"github.com/iamxvbaba/td/tg"
)

const StartupReportVersion = 3

const (
	StartupOrderShuffled     = "shuffled"
	StartupOrderAccountIndex = "account-index"
)

type StartupRunConfig struct {
	ManifestPath      string
	SessionKeyPath    string
	RSAKeyOverride    string
	DatasetPath       string
	SeedStatePath     string
	ClientStatePath   string
	MutationStatePath string
	ReportPath        string
	EventsPath        string
	ServerMetricsURL  string
	Profile           string
	StartOrder        string
	StartOrderSeed    int64
	AccountLimit      int
	RampDuration      time.Duration
	OperationTimeout  time.Duration
	SampleInterval    time.Duration
}

type StartupDifferenceCounts struct {
	Empty   int `json:"empty"`
	Full    int `json:"full"`
	Slice   int `json:"slice"`
	TooLong int `json:"too_long"`
	Calls   int `json:"calls"`
	Events  int `json:"events"`
}

type StartupDialogsCounts struct {
	PinnedCalls   int `json:"pinned_calls"`
	PinnedOverlap int `json:"pinned_overlap"`
	Calls         int `json:"calls"`
	Full          int `json:"full"`
	Slice         int `json:"slice"`
	Dialogs       int `json:"dialogs"`
}

type StartupResponseBytes struct {
	Inner     uint64 `json:"inner"`
	Wire      uint64 `json:"wire"`
	Delivered uint64 `json:"delivered"`
}

type StartupDatabaseWork struct {
	Queries         uint64  `json:"queries"`
	Errors          uint64  `json:"errors"`
	RPCs            uint64  `json:"rpcs"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type StartupRunReport struct {
	Version               int                             `json:"version"`
	StartedAt             time.Time                       `json:"started_at"`
	FinishedAt            time.Time                       `json:"finished_at"`
	Profile               string                          `json:"profile"`
	StartOrder            string                          `json:"start_order"`
	StartOrderSeed        int64                           `json:"start_order_seed"`
	DatasetSHA256         string                          `json:"dataset_sha256"`
	ExpectedAccounts      int                             `json:"expected_accounts"`
	BusinessReady         int                             `json:"business_ready"`
	AccountDifference     StartupDifferenceCounts         `json:"account_difference"`
	ChannelDifference     StartupDifferenceCounts         `json:"channel_difference"`
	DialogsWorkload       StartupDialogsCounts            `json:"dialogs_workload"`
	DialogsObserved       int                             `json:"dialogs_observed"`
	ChannelDialogs        int                             `json:"channel_dialogs_observed"`
	Operations            map[string]OperationReport      `json:"operations"`
	ResponseBytes         map[string]StartupResponseBytes `json:"response_bytes,omitempty"`
	RPCDeliveryOutcomes   map[string]map[string]uint64    `json:"rpc_delivery_outcomes,omitempty"`
	DatabaseWork          map[string]StartupDatabaseWork  `json:"database_work,omitempty"`
	BaselineServerMetrics map[string]float64              `json:"baseline_server_metrics,omitempty"`
	FinalServerMetrics    map[string]float64              `json:"final_server_metrics,omitempty"`
	PeakServerMetrics     map[string]float64              `json:"peak_server_metrics,omitempty"`
	ServerMetricsScrapes  uint64                          `json:"server_metrics_scrapes"`
	ServerMetricsErrors   uint64                          `json:"server_metrics_errors"`
	EventsWritten         uint64                          `json:"events_written"`
	EventsDropped         uint64                          `json:"events_dropped"`
	Pass                  bool                            `json:"pass"`
	Failures              []string                        `json:"failures,omitempty"`
}

type startupAccountResult struct {
	account           int
	stage             string
	err               error
	accountDifference StartupDifferenceCounts
	channelDifference StartupDifferenceCounts
	dialogsWorkload   StartupDialogsCounts
	dialogs           int
	channelDialogs    int
}

func (c StartupRunConfig) validate() error {
	if c.ManifestPath == "" || c.SessionKeyPath == "" || c.DatasetPath == "" || c.SeedStatePath == "" || c.ClientStatePath == "" || c.MutationStatePath == "" || c.ReportPath == "" {
		return errors.New("startup-run requires all input artifact paths and a report path")
	}
	if c.AccountLimit < 0 || c.RampDuration < 0 || c.OperationTimeout <= 0 || c.SampleInterval <= 0 {
		return errors.New("invalid startup-run account limit, ramp or operation timeout")
	}
	if _, err := resolveStartupProfile(c.Profile); err != nil {
		return err
	}
	if c.StartOrder != StartupOrderShuffled && c.StartOrder != StartupOrderAccountIndex {
		return fmt.Errorf("unknown startup order %q", c.StartOrder)
	}
	return nil
}

// StartupRun restores every permanent session, catches up from the immutable
// pre-mutation cursors, performs real paginated dialogs RPCs, then converges all
// dirty channel cursors. Socket readiness alone never counts as business ready.
func StartupRun(ctx context.Context, cfg StartupRunConfig) (*StartupRunReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	profile, _ := resolveStartupProfile(cfg.Profile)
	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		return nil, err
	}
	dataset, err := LoadDataset(cfg.DatasetPath)
	if err != nil {
		return nil, err
	}
	targets, err := seedPrimaryTargets(manifest, dataset.Config.Accounts)
	if err != nil {
		return nil, err
	}
	seedState, err := LoadDatasetSeedState(cfg.SeedStatePath, dataset)
	if err != nil {
		return nil, err
	}
	clientState, err := LoadClientState(cfg.ClientStatePath)
	if err != nil {
		return nil, err
	}
	if err := clientState.Validate(dataset, seedState, targets); err != nil {
		return nil, err
	}
	baselineSHA, err := fileSHA256(cfg.ClientStatePath)
	if err != nil {
		return nil, err
	}
	seedIdentity, err := seedIdentitySHA256(seedState)
	if err != nil {
		return nil, err
	}
	mutationPlan := planOfflineMutation(dataset)
	mutationState, err := loadOrCreateOfflineMutationState(cfg.MutationStatePath, dataset, seedIdentity, baselineSHA, mutationPlan)
	if err != nil {
		return nil, err
	}
	mutationJournal := &mutationJournal{dataset: dataset, plan: mutationPlan, state: mutationState}
	if err := mutationJournal.assertComplete(); err != nil {
		return nil, fmt.Errorf("startup-run requires complete offline mutations: %w", err)
	}
	key, err := LoadSessionKey(cfg.SessionKeyPath)
	if err != nil {
		return nil, err
	}
	publicKey, err := loadManifestPublicKey(cfg.ManifestPath, manifest.Endpoint, cfg.RSAKeyOverride)
	if err != nil {
		return nil, err
	}
	accounts := dataset.Config.Accounts
	if cfg.AccountLimit > 0 {
		accounts = min(accounts, cfg.AccountLimit)
	}
	startOrderSeed := cfg.StartOrderSeed
	if startOrderSeed == 0 {
		startOrderSeed = dataset.Config.Seed
	}
	accountOrder := startupAccountOrder(accounts, cfg.StartOrder, startOrderSeed)
	metrics := newMetricSet(
		"lifecycle.transport_ready", "lifecycle.dialogs_ready", "lifecycle.difference_converged", "lifecycle.business_ready",
		"auth.status", "updates.getState", "updates.getDifference", "updates.getDifference.empty", "updates.getDifference.full", "updates.getDifference.slice", "updates.getDifference.too_long",
		"messages.getPinnedDialogs", "messages.getDialogs", "messages.getDialogs.first", "messages.getDialogs.next", "messages.getDialogs.full", "messages.getDialogs.slice",
		"updates.getChannelDifference", "updates.getChannelDifference.small", "updates.getChannelDifference.boundary", "updates.getChannelDifference.too_long", "updates.getChannelDifference.empty",
	)
	events, err := newEventWriter(cfg.EventsPath)
	if err != nil {
		return nil, err
	}
	defer events.close()
	serverMetrics := newServerMetricsClient(cfg.ServerMetricsURL)
	var baselineServerMetrics map[string]float64
	peakServerMetrics := make(map[string]float64)
	if serverMetrics != nil {
		if sample, scrapeErr := serverMetrics.scrape(ctx); scrapeErr == nil {
			baselineServerMetrics = sample
			updateMetricPeaks(peakServerMetrics, sample)
			events.write(map[string]any{"type": "startup_server_baseline", "at": time.Now().UTC(), "server_metrics": sample})
		} else {
			events.write(map[string]any{"type": "startup_server_baseline_error", "at": time.Now().UTC(), "class": classifyError(scrapeErr)})
		}
	}
	startedAt := time.Now().UTC()
	results := make(chan startupAccountResult, accounts)
	var wg sync.WaitGroup
	for position, account := range accountOrder {
		account := account
		delay := startupRampDelay(cfg.RampDuration, position, accounts)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					results <- startupAccountResult{account: account, stage: "ramp", err: ctx.Err()}
					return
				case <-timer.C:
				}
			}
			results <- runStartupAccount(ctx, cfg, profile, manifest, dataset, seedState, clientState, mutationPlan, mutationState, targets, key, publicKey, metrics, account)
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	report := &StartupRunReport{
		Version: StartupReportVersion, StartedAt: startedAt, Profile: profile.Name,
		StartOrder: cfg.StartOrder, StartOrderSeed: startOrderSeed,
		DatasetSHA256: dataset.PlanSHA256, ExpectedAccounts: accounts,
		BaselineServerMetrics: baselineServerMetrics,
	}
	consumeResult := func(result startupAccountResult) {
		if result.err == nil {
			report.BusinessReady++
		} else if len(report.Failures) < 100 {
			report.Failures = append(report.Failures, fmt.Sprintf("account=%d stage=%s class=%s reason=%s",
				result.account, result.stage, classifyError(result.err), classifyErrorReason(result.err)))
		}
		addDifferenceCounts(&report.AccountDifference, result.accountDifference)
		addDifferenceCounts(&report.ChannelDifference, result.channelDifference)
		addDialogsCounts(&report.DialogsWorkload, result.dialogsWorkload)
		report.DialogsObserved += result.dialogs
		report.ChannelDialogs += result.channelDialogs
	}
	var sampleTicker *time.Ticker
	var sampleC <-chan time.Time
	if serverMetrics != nil {
		sampleTicker = time.NewTicker(cfg.SampleInterval)
		sampleC = sampleTicker.C
		defer sampleTicker.Stop()
	}
	for results != nil {
		select {
		case result, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			consumeResult(result)
		case sampledAt := <-sampleC:
			sample, scrapeErr := serverMetrics.scrape(ctx)
			if scrapeErr != nil {
				events.write(map[string]any{"type": "startup_sample_error", "at": sampledAt.UTC(), "class": classifyError(scrapeErr)})
				continue
			}
			updateMetricPeaks(peakServerMetrics, sample)
			events.write(map[string]any{
				"type": "startup_sample", "at": sampledAt.UTC(), "business_ready": report.BusinessReady,
				"expected_accounts": accounts, "operations": metrics.report(), "server_metrics": sample,
			})
		}
	}
	report.FinishedAt = time.Now().UTC()
	report.Operations = metrics.report()
	if serverMetrics != nil {
		baselineSubmitted := metricValue(report.BaselineServerMetrics, "telesrv_presence_last_seen_submitted_total")
		sample, settleErr := serverMetrics.waitForPresenceLastSeenSettlement(
			ctx, baselineSubmitted, uint64(2*report.BusinessReady), 15*time.Second,
		)
		if sample != nil {
			report.FinalServerMetrics = sample
			updateMetricPeaks(peakServerMetrics, sample)
		}
		if settleErr == nil {
			events.write(map[string]any{"type": "startup_server_final", "at": time.Now().UTC(), "server_metrics": sample})
		} else {
			report.Failures = append(report.Failures, "presence last-seen batch did not settle before final metrics")
			events.write(map[string]any{"type": "startup_server_final_error", "at": time.Now().UTC(), "class": classifyError(settleErr)})
		}
		report.ServerMetricsScrapes = serverMetrics.successes()
		report.ServerMetricsErrors = serverMetrics.failures()
		if report.BaselineServerMetrics == nil {
			report.Failures = append(report.Failures, "pre-startup server metrics baseline scrape failed")
		}
		if report.FinalServerMetrics == nil {
			report.Failures = append(report.Failures, "final startup server metrics scrape failed")
		}
	}
	report.PeakServerMetrics = peakServerMetrics
	report.ResponseBytes = startupResponseBytes(report.BaselineServerMetrics, report.FinalServerMetrics)
	report.RPCDeliveryOutcomes = startupRPCDeliveryOutcomes(report.BaselineServerMetrics, report.FinalServerMetrics)
	report.DatabaseWork = startupDatabaseWork(report.BaselineServerMetrics, report.FinalServerMetrics)
	report.EventsWritten, report.EventsDropped = events.counts()
	methods := make([]string, 0, len(report.RPCDeliveryOutcomes))
	for method := range report.RPCDeliveryOutcomes {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		outcomes := report.RPCDeliveryOutcomes[method]
		outcomeNames := make([]string, 0, len(outcomes))
		for outcome := range outcomes {
			outcomeNames = append(outcomeNames, outcome)
		}
		sort.Strings(outcomeNames)
		for _, outcome := range outcomeNames {
			count := outcomes[outcome]
			if outcome != "ok" && count > 0 {
				report.Failures = append(report.Failures, fmt.Sprintf("%s rpc_result delivery outcome %s: %d", method, outcome, count))
			}
		}
	}
	methods = methods[:0]
	for method := range report.DatabaseWork {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		work := report.DatabaseWork[method]
		if work.Errors > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s database errors: %d", method, work.Errors))
		}
	}
	if report.BaselineServerMetrics != nil && report.FinalServerMetrics != nil {
		counterDelta := func(name string) float64 {
			delta := metricValue(report.FinalServerMetrics, name) - metricValue(report.BaselineServerMetrics, name)
			return max(delta, 0)
		}
		expectedSubmitted := float64(2 * report.BusinessReady)
		if submitted := counterDelta("telesrv_presence_last_seen_submitted_total"); submitted < expectedSubmitted {
			report.Failures = append(report.Failures, fmt.Sprintf("presence last-seen submitted %.0f, want at least %.0f", submitted, expectedSubmitted))
		}
		if pending := metricValue(report.FinalServerMetrics, "telesrv_presence_last_seen_pending"); pending != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("presence last-seen pending at final scrape: %.0f", pending))
		}
		bootstrapSelectors := counterDelta(`telesrv_bootstrap_ready_selectors_total{outcome="matched"}`) +
			counterDelta(`telesrv_bootstrap_ready_selectors_total{outcome="miss"}`) +
			counterDelta(`telesrv_bootstrap_ready_selectors_total{outcome="error"}`)
		if bootstrapSelectors < float64(report.BusinessReady) {
			report.Failures = append(report.Failures, fmt.Sprintf("bootstrap readiness selectors %.0f, want at least %d", bootstrapSelectors, report.BusinessReady))
		}
		if pending := metricValue(report.FinalServerMetrics, "telesrv_bootstrap_ready_pending"); pending != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("bootstrap readiness pending at final scrape: %.0f", pending))
		}
		if failures := counterDelta(`telesrv_bootstrap_ready_selectors_total{outcome="error"}`); failures > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("bootstrap readiness selector errors: %.0f", failures))
		}
		if served := counterDelta(`telesrv_active_channel_ids_cache_total{outcome="served"}`); served < float64(report.BusinessReady) {
			report.Failures = append(report.Failures, fmt.Sprintf("active channel IDs pages served %.0f, want at least %d", served, report.BusinessReady))
		}
		if pending := metricValue(report.FinalServerMetrics, "telesrv_active_channel_ids_pending"); pending != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("active channel IDs cold-loader pending at final scrape: %.0f", pending))
		}
		for _, check := range []struct {
			name  string
			label string
		}{
			{name: `telesrv_active_channel_ids_batches_total{outcome="error"}`, label: "batch errors"},
			{name: `telesrv_active_channel_ids_selectors_total{outcome="error"}`, label: "selector errors"},
			{name: `telesrv_active_channel_ids_cache_total{outcome="read_error"}`, label: "Redis read errors"},
			{name: `telesrv_active_channel_ids_cache_total{outcome="write_error"}`, label: "Redis write errors"},
		} {
			if delta := counterDelta(check.name); delta > 0 {
				report.Failures = append(report.Failures, fmt.Sprintf("active channel IDs %s: %.0f", check.label, delta))
			}
		}
		for _, check := range []struct {
			name  string
			label string
		}{
			{name: `telesrv_presence_last_seen_batches_total{outcome="error"}`, label: "batch errors"},
			{name: "telesrv_presence_last_seen_overflow_total", label: "queue overflow"},
			{name: "telesrv_presence_last_seen_drain_dropped_total", label: "shutdown drain dropped"},
		} {
			if delta := counterDelta(check.name); delta > 0 {
				report.Failures = append(report.Failures, fmt.Sprintf("presence last-seen %s: %.0f", check.label, delta))
			}
		}
	}
	report.Pass = report.BusinessReady == report.ExpectedAccounts && len(report.Failures) == 0
	if err := WriteStartupReport(cfg.ReportPath, report); err != nil {
		return nil, err
	}
	return report, nil
}

func runStartupAccount(
	ctx context.Context,
	cfg StartupRunConfig,
	profile startupWorkloadProfile,
	manifest *Manifest,
	dataset *Dataset,
	seedState *DatasetSeedState,
	clientState *ClientState,
	mutationPlan []OfflineMutationChannelPlan,
	mutationState *OfflineMutationState,
	targets []SessionRecord,
	key [32]byte,
	publicKey *rsa.PublicKey,
	metrics *metricSet,
	account int,
) startupAccountResult {
	result := startupAccountResult{account: account}
	connectStart := time.Now()
	var ready atomic.Bool
	var businessReady atomic.Bool
	var readyOnce sync.Once
	storage := &EncryptedFileStorage{Path: resolveSessionPath(cfg.ManifestPath, targets[account]), Key: key}
	device := profile.device()
	client, err := newClient(manifest.Endpoint, publicKey, storage, clientHooks{
		Device: &device,
		ConnectionState: func(state telegram.ConnectionState) {
			if state == telegram.ConnectionStateReady {
				readyOnce.Do(func() {
					ready.Store(true)
					metrics.observe("lifecycle.transport_ready", connectStart, nil)
				})
			}
		},
	})
	if err != nil {
		result.stage, result.err = "client", err
		metrics.observe("lifecycle.transport_ready", connectStart, err)
		return result
	}
	err = client.Run(ctx, func(ctx context.Context) error {
		statusStart := time.Now()
		statusCtx, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
		status, err := client.Auth().Status(statusCtx)
		cancel()
		metrics.observe("auth.status", statusStart, err)
		if err != nil {
			result.stage = "auth"
			return err
		}
		if !status.Authorized || status.User == nil || status.User.ID != targets[account].UserID {
			result.stage = "auth"
			return errors.New("session is not authorized as the manifest user")
		}
		raw := tg.NewClient(client)
		accountState := clientState.Accounts[account].State
		if profile.GetStateBeforeDifference {
			stateStart := time.Now()
			stateCtx, cancel := context.WithTimeout(ctx, cfg.OperationTimeout)
			current, stateErr := raw.UpdatesGetState(stateCtx)
			cancel()
			metrics.observe("updates.getState", stateStart, stateErr)
			if stateErr != nil {
				result.stage = "state"
				return stateErr
			}
			if current.Pts < clientState.Accounts[account].State.Pts || current.Qts < clientState.Accounts[account].State.Qts {
				result.stage = "state"
				return errors.New("updates.getState moved behind the persisted startup cursor")
			}
			accountState = clientUpdateState(*current)
		}
		if profile.AccountDifference {
			var counts StartupDifferenceCounts
			accountState, counts, err = startupAccountDifference(ctx, cfg.OperationTimeout, raw, clientState.Accounts[account], dataset, metrics)
			result.accountDifference = counts
			if err != nil {
				result.stage = "account_difference"
				return err
			}
		}
		dialogs, dialogCounts, err := snapshotDialogsObserved(ctx, cfg.OperationTimeout, raw, profile.Dialogs, metrics.observe)
		result.dialogsWorkload = dialogCounts
		if err != nil {
			result.stage = "dialogs"
			return err
		}
		result.dialogs = len(dialogs)
		for _, dialog := range dialogs {
			if dialog.PeerType == "channel" {
				result.channelDialogs++
			}
		}
		if err := validateStartupDialogs(dataset, seedState, mutationPlan, mutationState, targets, account, dialogs); err != nil {
			result.stage = "dialogs"
			return err
		}
		metrics.observe("lifecycle.dialogs_ready", connectStart, nil)
		channelCounts, err := startupChannelDifferences(ctx, cfg.OperationTimeout, raw, dataset, seedState, clientState.Accounts[account], mutationPlan, mutationState, account, profile.ForceChannelDifference, metrics)
		result.channelDifference = channelCounts
		if err != nil {
			result.stage = "channel_difference"
			return err
		}
		_ = accountState // retained separately from immutable baseline for future steady polling.
		metrics.observe("lifecycle.difference_converged", connectStart, nil)
		metrics.observe("lifecycle.business_ready", connectStart, nil)
		businessReady.Store(true)
		return nil
	})
	if err == nil && !businessReady.Load() {
		result.stage = "connection"
		err = errors.New("startup session ended before business readiness")
	}
	if err != nil {
		result.err = err
		if result.stage == "" {
			result.stage = "connection"
		}
		debugOperationError("startup."+result.stage, err)
		if !ready.Load() {
			readyOnce.Do(func() { metrics.observe("lifecycle.transport_ready", connectStart, err) })
		}
	}
	return result
}

func startupAccountDifference(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	baseline ClientAccountState,
	dataset *Dataset,
	metrics *metricSet,
) (ClientUpdateState, StartupDifferenceCounts, error) {
	state := baseline.State
	counts := StartupDifferenceCounts{}
	markers := make(map[string]int)
	finished := false
	for page := 0; page < 256; page++ {
		start := time.Now()
		rpcCtx, cancel := context.WithTimeout(ctx, timeout)
		difference, err := raw.UpdatesGetDifference(rpcCtx, &tg.UpdatesGetDifferenceRequest{Pts: state.Pts, Date: state.Date, Qts: state.Qts})
		cancel()
		metrics.observe("updates.getDifference", start, err)
		counts.Calls++
		if err != nil {
			return state, counts, fmt.Errorf("updates.getDifference page %d: %w", page+1, err)
		}
		switch value := difference.(type) {
		case *tg.UpdatesDifferenceEmpty:
			metrics.observe("updates.getDifference.empty", start, nil)
			counts.Empty++
			state.Date, state.Seq = value.Date, value.Seq
			finished = true
		case *tg.UpdatesDifference:
			metrics.observe("updates.getDifference.full", start, nil)
			counts.Full++
			counts.Events += len(value.NewMessages) + len(value.NewEncryptedMessages) + len(value.OtherUpdates)
			collectAccountDifferenceMarkers(markers, value.NewMessages, value.OtherUpdates, dataset.RunID)
			state = clientUpdateState(value.State)
			finished = true
		case *tg.UpdatesDifferenceSlice:
			metrics.observe("updates.getDifference.slice", start, nil)
			counts.Slice++
			counts.Events += len(value.NewMessages) + len(value.NewEncryptedMessages) + len(value.OtherUpdates)
			collectAccountDifferenceMarkers(markers, value.NewMessages, value.OtherUpdates, dataset.RunID)
			state = clientUpdateState(value.IntermediateState)
		case *tg.UpdatesDifferenceTooLong:
			metrics.observe("updates.getDifference.too_long", start, nil)
			counts.TooLong++
			state.Pts = value.Pts
			return state, counts, errors.New("account difference returned updates.differenceTooLong")
		default:
			return state, counts, fmt.Errorf("updates.getDifference returned %T", difference)
		}
		if finished {
			break
		}
		if page == 255 {
			return state, counts, errors.New("updates.getDifference exceeded 256 pages")
		}
	}
	account := baseline.AccountIndex
	expected := []string{
		offlinePrivateMarker(dataset, account, (account+1)%dataset.Config.Accounts),
		offlinePrivateMarker(dataset, (account-1+dataset.Config.Accounts)%dataset.Config.Accounts, account),
	}
	if err := requireExactMarkers(markers, expected); err != nil {
		return state, counts, fmt.Errorf("account private markers: %w", err)
	}
	// A non-slice updates.difference is final for the cursor used by this
	// request. Do not require a second account-level call to be empty: binding
	// the startup temp key can legitimately append a new authorization update
	// after the first result. Channel differences have the stricter repeat-empty
	// assertion below because their PTS is isolated from account authorization.
	return state, counts, nil
}

func clientUpdateState(state tg.UpdatesState) ClientUpdateState {
	return ClientUpdateState{Pts: state.Pts, Qts: state.Qts, Date: state.Date, Seq: state.Seq, UnreadCount: state.UnreadCount}
}

func collectAccountDifferenceMarkers(destination map[string]int, messages []tg.MessageClass, updates []tg.UpdateClass, runID string) {
	prefix := "[" + runID + " offline private "
	for _, message := range messages {
		collectMessageMarker(destination, message, prefix)
	}
	for _, update := range updates {
		switch value := update.(type) {
		case *tg.UpdateNewMessage:
			collectMessageMarker(destination, value.Message, prefix)
		}
	}
}

func collectMessageMarker(destination map[string]int, message tg.MessageClass, prefix string) {
	full, ok := message.(*tg.Message)
	if ok && strings.HasPrefix(full.Message, prefix) {
		destination[full.Message]++
	}
}

func requireExactMarkers(observed map[string]int, expected []string) error {
	wanted := make(map[string]struct{}, len(expected))
	for _, marker := range expected {
		wanted[marker] = struct{}{}
		if observed[marker] != 1 {
			return fmt.Errorf("marker count=%d want=1", observed[marker])
		}
	}
	for marker, count := range observed {
		if _, ok := wanted[marker]; !ok || count != 1 {
			return errors.New("difference contained duplicate or wrong-account marker")
		}
	}
	return nil
}

func validateStartupDialogs(
	dataset *Dataset,
	seedState *DatasetSeedState,
	mutationPlan []OfflineMutationChannelPlan,
	mutationState *OfflineMutationState,
	targets []SessionRecord,
	account int,
	dialogs []ClientDialogState,
) error {
	expected := expectedDatasetPeers(dataset, seedState, targets, account)
	byPeer := make(map[clientPeerKey]ClientDialogState, len(dialogs))
	for _, dialog := range dialogs {
		peer := clientPeerKey{typ: dialog.PeerType, id: dialog.PeerID}
		byPeer[peer] = dialog
		delete(expected, peer)
	}
	if len(expected) != 0 {
		return fmt.Errorf("current dialogs omit %d expected dataset peers", len(expected))
	}
	if err := validateSeededRichDialogs(dataset, seedState, targets, account, dialogs, false); err != nil {
		return fmt.Errorf("rich dialog state: %w", err)
	}
	offlinePeer := clientPeerKey{typ: "user", id: targets[(account+1)%dataset.Config.Accounts].UserID}
	offlineDialog, ok := byPeer[offlinePeer]
	if !ok || mutationState.PrivateMessageIDs[account] <= 0 || offlineDialog.TopMessage != mutationState.PrivateMessageIDs[account] {
		return errors.New("current dialogs omitted the account's exact offline private top message")
	}
	for planPosition, channelPlan := range mutationPlan {
		group := dataset.Groups[channelPlan.GroupPosition]
		if !datasetGroupHasAccount(group, account) {
			continue
		}
		channelID := seedState.Groups[channelPlan.GroupPosition].ChannelID
		dialog, ok := byPeer[clientPeerKey{typ: "channel", id: channelID}]
		if !ok || !dialog.HasPts || dialog.Pts < mutationState.Channels[planPosition].LatestPts {
			return fmt.Errorf("dirty channel group %d dialog pts is stale", group.Index)
		}
	}
	return nil
}

func datasetGroupHasAccount(group DatasetGroup, account int) bool {
	index := sort.SearchInts(group.MemberAccounts, account)
	return index < len(group.MemberAccounts) && group.MemberAccounts[index] == account
}

func startupChannelDifferences(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	dataset *Dataset,
	seedState *DatasetSeedState,
	baseline ClientAccountState,
	mutationPlan []OfflineMutationChannelPlan,
	mutationState *OfflineMutationState,
	account int,
	force bool,
	metrics *metricSet,
) (StartupDifferenceCounts, error) {
	counts := StartupDifferenceCounts{}
	baselineChannels := make(map[int64]ClientDialogState)
	for _, dialog := range baseline.Dialogs {
		if dialog.PeerType == "channel" {
			baselineChannels[dialog.PeerID] = dialog
		}
	}
	for planPosition, channelPlan := range mutationPlan {
		group := dataset.Groups[channelPlan.GroupPosition]
		if !datasetGroupHasAccount(group, account) {
			continue
		}
		channelIdentity := seedState.Groups[channelPlan.GroupPosition]
		old, ok := baselineChannels[channelIdentity.ChannelID]
		if !ok || !old.HasPts {
			return counts, fmt.Errorf("group %d has no old channel cursor", group.Index)
		}
		channelCounts, err := catchUpStartupChannel(ctx, timeout, raw, dataset, group, channelIdentity, old.Pts, channelPlan, mutationState.Channels[planPosition], planPosition == 0, force, metrics)
		addDifferenceCounts(&counts, channelCounts)
		if err != nil {
			return counts, fmt.Errorf("group %d: %w", group.Index, err)
		}
	}
	return counts, nil
}

func catchUpStartupChannel(
	ctx context.Context,
	timeout time.Duration,
	raw *tg.Client,
	dataset *Dataset,
	group DatasetGroup,
	identity DatasetSeedGroupState,
	oldPts int,
	plan OfflineMutationChannelPlan,
	state OfflineMutationChannelState,
	expectTooLong bool,
	force bool,
	metrics *metricSet,
) (StartupDifferenceCounts, error) {
	counts := StartupDifferenceCounts{}
	pts := oldPts
	markers := make(map[string]int)
	tooLongSnapshot := false
	for page := 0; page < 256; page++ {
		start := time.Now()
		rpcCtx, cancel := context.WithTimeout(ctx, timeout)
		difference, err := raw.UpdatesGetChannelDifference(rpcCtx, &tg.UpdatesGetChannelDifferenceRequest{
			Force:   force,
			Channel: &tg.InputChannel{ChannelID: identity.ChannelID, AccessHash: identity.AccessHash},
			Filter:  &tg.ChannelMessagesFilterEmpty{}, Pts: pts, Limit: 100,
		})
		cancel()
		metrics.observe("updates.getChannelDifference", start, err)
		counts.Calls++
		if err != nil {
			return counts, err
		}
		switch value := difference.(type) {
		case *tg.UpdatesChannelDifferenceEmpty:
			counts.Empty++
			if page == 0 {
				return counts, errors.New("dirty channel returned empty difference")
			}
			if value.Pts < pts {
				return counts, errors.New("channel empty difference moved pts backwards")
			}
			pts = value.Pts
		case *tg.UpdatesChannelDifference:
			if len(value.NewMessages)+len(value.OtherUpdates) >= 100 {
				metrics.observe("updates.getChannelDifference.boundary", start, nil)
			} else {
				metrics.observe("updates.getChannelDifference.small", start, nil)
			}
			counts.Full++
			counts.Events += len(value.NewMessages) + len(value.OtherUpdates)
			collectChannelMarkers(markers, value.NewMessages, value.OtherUpdates, dataset.RunID)
			if value.Pts <= pts {
				return counts, errors.New("channel full difference did not advance pts")
			}
			pts = value.Pts
			if !value.Final {
				continue
			}
		case *tg.UpdatesChannelDifferenceTooLong:
			metrics.observe("updates.getChannelDifference.too_long", start, nil)
			counts.TooLong++
			counts.Events += len(value.Messages)
			collectChannelMarkers(markers, value.Messages, nil, dataset.RunID)
			dialog, ok := value.Dialog.(*tg.Dialog)
			if !ok {
				return counts, fmt.Errorf("channelDifferenceTooLong dialog is %T", value.Dialog)
			}
			current, ok := dialog.GetPts()
			if !ok || current <= pts || !value.Final {
				return counts, errors.New("channelDifferenceTooLong has invalid final pts")
			}
			pts = current
			tooLongSnapshot = true
		default:
			return counts, fmt.Errorf("updates.getChannelDifference returned %T", difference)
		}
		break
	}
	if pts < state.LatestPts {
		return counts, fmt.Errorf("channel converged pts %d below observed %d", pts, state.LatestPts)
	}
	if expectTooLong != tooLongSnapshot {
		return counts, fmt.Errorf("tooLong=%v want=%v", tooLongSnapshot, expectTooLong)
	}
	if tooLongSnapshot {
		latest := offlineChannelMarker(dataset, group, plan.Messages-1)
		if markers[latest] != 1 {
			return counts, errors.New("tooLong snapshot omitted latest mutation marker")
		}
		deleted := offlineChannelMarker(dataset, group, plan.Messages-2)
		if markers[deleted] != 0 {
			return counts, errors.New("tooLong snapshot retained deleted mutation marker")
		}
	} else {
		expected := make([]string, 0, plan.Messages)
		for message := 0; message < plan.Messages; message++ {
			expected = append(expected, offlineChannelMarker(dataset, group, message))
		}
		if err := requireExactMarkers(markers, expected); err != nil {
			return counts, fmt.Errorf("channel markers: %w", err)
		}
	}
	start := time.Now()
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	empty, err := raw.UpdatesGetChannelDifference(rpcCtx, &tg.UpdatesGetChannelDifferenceRequest{
		Channel: &tg.InputChannel{ChannelID: identity.ChannelID, AccessHash: identity.AccessHash},
		Filter:  &tg.ChannelMessagesFilterEmpty{}, Pts: pts, Limit: 100,
	})
	cancel()
	metrics.observe("updates.getChannelDifference.empty", start, err)
	counts.Calls++
	if err != nil {
		return counts, err
	}
	emptyDifference, ok := empty.(*tg.UpdatesChannelDifferenceEmpty)
	if !ok || !emptyDifference.Final || emptyDifference.Pts != pts {
		return counts, fmt.Errorf("repeat channel difference returned %T at unexpected pts", empty)
	}
	counts.Empty++
	return counts, nil
}

func collectChannelMarkers(destination map[string]int, messages []tg.MessageClass, updates []tg.UpdateClass, runID string) {
	prefix := "[" + runID + " offline channel "
	for _, message := range messages {
		collectMessageMarker(destination, message, prefix)
	}
	for _, update := range updates {
		switch value := update.(type) {
		case *tg.UpdateNewChannelMessage:
			collectMessageMarker(destination, value.Message, prefix)
		case *tg.UpdateEditChannelMessage:
			collectMessageMarker(destination, value.Message, prefix)
		}
	}
}

func startupRampDelay(ramp time.Duration, account, accounts int) time.Duration {
	if ramp <= 0 || accounts <= 1 || account <= 0 {
		return 0
	}
	return time.Duration(int64(ramp) * int64(account) / int64(accounts-1))
}

func startupAccountOrder(accounts int, order string, seed int64) []int {
	result := make([]int, accounts)
	for account := range result {
		result[account] = account
	}
	if order == StartupOrderShuffled {
		rand.New(rand.NewSource(seed)).Shuffle(len(result), func(i, j int) {
			result[i], result[j] = result[j], result[i]
		})
	}
	return result
}

func addDifferenceCounts(destination *StartupDifferenceCounts, value StartupDifferenceCounts) {
	destination.Empty += value.Empty
	destination.Full += value.Full
	destination.Slice += value.Slice
	destination.TooLong += value.TooLong
	destination.Calls += value.Calls
	destination.Events += value.Events
}

func addDialogsCounts(destination *StartupDialogsCounts, value StartupDialogsCounts) {
	destination.PinnedCalls += value.PinnedCalls
	destination.PinnedOverlap += value.PinnedOverlap
	destination.Calls += value.Calls
	destination.Full += value.Full
	destination.Slice += value.Slice
	destination.Dialogs += value.Dialogs
}

func updateMetricPeaks(peaks, sample map[string]float64) {
	for name, value := range sample {
		if current, ok := peaks[name]; !ok || value > current {
			peaks[name] = value
		}
	}
}

func startupResponseBytes(baseline, final map[string]float64) map[string]StartupResponseBytes {
	if baseline == nil || final == nil {
		return nil
	}
	result := make(map[string]StartupResponseBytes)
	families := []struct {
		name string
		set  func(*StartupResponseBytes, uint64)
	}{
		{"telesrv_mtproto_rpc_result_inner_bytes_total", func(value *StartupResponseBytes, bytes uint64) { value.Inner = bytes }},
		{"telesrv_mtproto_rpc_result_wire_bytes_total", func(value *StartupResponseBytes, bytes uint64) { value.Wire = bytes }},
		{"telesrv_mtproto_rpc_result_delivered_bytes_total", func(value *StartupResponseBytes, bytes uint64) { value.Delivered = bytes }},
	}
	for _, family := range families {
		prefix := family.name + "{"
		for key, finalValue := range final {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			method, ok := prometheusLabelValue(key, "method")
			if !ok || method == "" {
				continue
			}
			if family.name == "telesrv_mtproto_rpc_result_delivered_bytes_total" {
				outcome, ok := prometheusLabelValue(key, "outcome")
				if !ok || outcome != "ok" {
					continue
				}
			}
			delta := finalValue - baseline[key]
			if delta < 0 {
				delta = 0
			}
			value := result[method]
			family.set(&value, uint64(delta))
			result[method] = value
		}
	}
	return result
}

func startupDatabaseWork(baseline, final map[string]float64) map[string]StartupDatabaseWork {
	if baseline == nil || final == nil {
		return nil
	}
	result := make(map[string]StartupDatabaseWork)
	for key, finalValue := range final {
		method, ok := prometheusLabelValue(key, "method")
		if !ok || method == "" {
			continue
		}
		delta := finalValue - baseline[key]
		if delta < 0 {
			delta = 0
		}
		value := result[method]
		switch {
		case strings.HasPrefix(key, "telesrv_rpc_db_queries_total{"):
			value.Queries = uint64(delta)
		case strings.HasPrefix(key, "telesrv_rpc_db_errors_total{"):
			value.Errors = uint64(delta)
		case strings.HasPrefix(key, "telesrv_rpc_db_time_seconds_count{"):
			value.RPCs = uint64(delta)
		case strings.HasPrefix(key, "telesrv_rpc_db_time_seconds_sum{"):
			value.DurationSeconds = delta
		default:
			continue
		}
		result[method] = value
	}
	return result
}

func startupRPCDeliveryOutcomes(baseline, final map[string]float64) map[string]map[string]uint64 {
	if baseline == nil || final == nil {
		return nil
	}
	const prefix = "telesrv_mtproto_rpc_result_delivered_total{"
	result := make(map[string]map[string]uint64)
	for key, finalValue := range final {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		method, methodOK := prometheusLabelValue(key, "method")
		outcome, outcomeOK := prometheusLabelValue(key, "outcome")
		if !methodOK || !outcomeOK || method == "" || outcome == "" {
			continue
		}
		delta := finalValue - baseline[key]
		if delta < 0 {
			delta = 0
		}
		if result[method] == nil {
			result[method] = make(map[string]uint64)
		}
		result[method][outcome] = uint64(delta)
	}
	return result
}

func WriteStartupReport(path string, report *StartupRunReport) error {
	if report == nil || report.Version != StartupReportVersion {
		return errors.New("invalid startup report")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}
