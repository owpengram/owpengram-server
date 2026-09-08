package loadharness

import (
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iamxvbaba/td/telegram"
	"github.com/iamxvbaba/td/tg"
)

// TestGroupMediaDownloadLoad exercises the complete real-wire group media path:
// authenticated members come online, multiple senders concurrently upload and
// send photo/video/audio/voice/document messages, every member treats the live
// channel delivery signal (or a too-long nudge) only as a reason to call
// getChannelDifference, and then downloads each file from the location in its
// own difference result.
//
// The test is deliberately opt-in because it requires an owner-only account
// bundle and a pre-seeded exact-membership supergroup. It never imports server
// handlers or writes database fixtures.
func TestGroupMediaDownloadLoad(t *testing.T) {
	if os.Getenv("TELESRV_GROUP_MEDIA_LOAD") != "1" {
		t.Skip("set TELESRV_GROUP_MEDIA_LOAD=1 to run the real-wire group media load")
	}
	cfg := loadGroupMediaConfig(t)
	report, err := runGroupMediaLoad(context.Background(), cfg, t.Logf)
	if writeErr := writeGroupMediaReport(cfg.ReportPath, report); writeErr != nil {
		t.Fatalf("write report: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("group media load: %v (report %s)", err, cfg.ReportPath)
	}
	if !report.Pass {
		t.Fatalf("group media acceptance failed: %v (report %s)", report.Failures, cfg.ReportPath)
	}
}

type groupMediaConfig struct {
	ManifestPath     string
	SessionKeyPath   string
	DatasetPath      string
	SeedStatePath    string
	RSAKeyOverride   string
	ReportPath       string
	ServerMetricsURL string
	Media            []groupMediaSource
	Accounts         int
	GroupPosition    int
	Copies           int
	ChunkBytes       int
	RampDuration     time.Duration
	ReadyTimeout     time.Duration
	OperationTimeout time.Duration
	FanoutTimeout    time.Duration
	DownloadTimeout  time.Duration
	RecoveryDuration time.Duration
}

type groupMediaSource struct {
	Kind     string
	Path     string
	Name     string
	MimeType string
	Data     []byte
}

type groupMediaTarget struct {
	Kind     string
	Marker   string
	Size     int64
	Location tg.InputFileLocationClass
	SHA256   [sha256.Size]byte
}

type groupMediaTypeReport struct {
	Kind              string `json:"kind"`
	SourceBytes       int64  `json:"source_bytes"`
	MessagesExpected  int    `json:"messages_expected"`
	MessagesSent      int    `json:"messages_sent"`
	DownloadFileBytes int64  `json:"download_file_bytes"`
	DownloadsExpected int    `json:"downloads_expected"`
	DownloadsComplete int64  `json:"downloads_complete"`
	DownloadedBytes   int64  `json:"downloaded_bytes"`
	DownloadErrors    int64  `json:"download_errors"`
}

type groupMediaFanoutReport struct {
	Messages           int            `json:"messages"`
	Expected           int64          `json:"expected"`
	Observed           int64          `json:"observed"`
	Missing            int64          `json:"missing"`
	Duplicate          int64          `json:"duplicate"`
	MissingByMessage   map[string]int `json:"missing_by_message,omitempty"`
	MissingMemberSlots []int          `json:"missing_member_slots,omitempty"`
	P50MS              float64        `json:"p50_ms"`
	P95MS              float64        `json:"p95_ms"`
	P99MS              float64        `json:"p99_ms"`
	MaxMS              float64        `json:"max_ms"`
}

type groupMediaLoadReport struct {
	Version               int                        `json:"version"`
	StartedAt             time.Time                  `json:"started_at"`
	LoadEndedAt           time.Time                  `json:"load_ended_at"`
	FinishedAt            time.Time                  `json:"finished_at"`
	Accounts              int                        `json:"accounts"`
	ExactGroupMembers     int                        `json:"exact_group_members"`
	PeakReady             int64                      `json:"peak_ready"`
	FinalReady            int64                      `json:"final_ready"`
	ConnectionAttempts    uint64                     `json:"connection_attempts"`
	Reconnects            uint64                     `json:"reconnects"`
	Disconnects           uint64                     `json:"disconnects"`
	FatalClients          uint64                     `json:"fatal_clients"`
	ChannelNudges         uint64                     `json:"channel_nudges"`
	ChannelLiveUpdates    uint64                     `json:"channel_live_updates"`
	UploadDurationMS      float64                    `json:"upload_duration_ms"`
	FanoutSettleMS        float64                    `json:"fanout_settle_ms"`
	DownloadDurationMS    float64                    `json:"download_duration_ms"`
	DownloadThroughputMiB float64                    `json:"download_throughput_mib_s"`
	DownloadedBytes       int64                      `json:"downloaded_bytes"`
	Media                 []groupMediaTypeReport     `json:"media"`
	Fanout                groupMediaFanoutReport     `json:"fanout"`
	Operations            map[string]OperationReport `json:"operations"`
	BaselineServerMetrics map[string]float64         `json:"baseline_server_metrics,omitempty"`
	PeakServerMetrics     map[string]float64         `json:"peak_server_metrics,omitempty"`
	LoadEndServerMetrics  map[string]float64         `json:"load_end_server_metrics,omitempty"`
	FinalServerMetrics    map[string]float64         `json:"final_server_metrics,omitempty"`
	ServerMetricsScrapes  uint64                     `json:"server_metrics_scrapes"`
	ServerMetricsErrors   uint64                     `json:"server_metrics_errors"`
	Pass                  bool                       `json:"pass"`
	Failures              []string                   `json:"failures,omitempty"`
}

type groupMediaCounters struct {
	connectionAttempts atomic.Uint64
	reconnects         atomic.Uint64
	disconnects        atomic.Uint64
	fatalClients       atomic.Uint64
	channelNudges      atomic.Uint64
	channelLiveUpdates atomic.Uint64
	differenceFailures atomic.Uint64
	ready              atomic.Int64
	peakReady          atomic.Int64
}

type groupMediaClient struct {
	record              SessionRecord
	raw                 atomic.Pointer[tg.Client]
	cancel              context.CancelFunc
	done                chan error
	ready               atomic.Bool
	channelPts          atomic.Int64
	differenceTargetPts atomic.Int64
	differenceRequested atomic.Bool
	differenceForce     atomic.Bool
	differenceWake      chan struct{}
}

type groupMediaFanoutTracker struct {
	mu           sync.Mutex
	prefix       string
	members      map[int64]int
	expected     map[string]time.Time
	committed    map[string]bool
	observations map[string]map[int64]groupMediaObservation
}

type groupMediaObservation struct {
	first  time.Time
	repeat int64
	target groupMediaTarget
}

type groupMediaSampler struct {
	mu     sync.Mutex
	client *serverMetricsClient
	peak   map[string]float64
}

func loadGroupMediaConfig(t *testing.T) groupMediaConfig {
	t.Helper()
	required := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	media := []groupMediaSource{
		{Kind: "photo", Path: required("TELESRV_GROUP_MEDIA_PHOTO"), Name: "load-photo.jpg", MimeType: "image/jpeg"},
		{Kind: "video", Path: required("TELESRV_GROUP_MEDIA_VIDEO"), Name: "load-video.mp4", MimeType: "video/mp4"},
		{Kind: "audio", Path: required("TELESRV_GROUP_MEDIA_AUDIO"), Name: "load-audio.mp3", MimeType: "audio/mpeg"},
		{Kind: "voice", Path: required("TELESRV_GROUP_MEDIA_VOICE"), Name: "load-voice.ogg", MimeType: "audio/ogg"},
		{Kind: "document", Path: required("TELESRV_GROUP_MEDIA_DOCUMENT"), Name: "load-document.bin", MimeType: "application/octet-stream"},
	}
	for i := range media {
		data, err := os.ReadFile(media[i].Path)
		if err != nil {
			t.Fatalf("read %s fixture: %v", media[i].Kind, err)
		}
		if len(data) == 0 || len(data) > 64<<20 {
			t.Fatalf("%s fixture size %d is outside (0,64MiB]", media[i].Kind, len(data))
		}
		media[i].Data = data
	}
	return groupMediaConfig{
		ManifestPath: required("TELESRV_GROUP_MEDIA_MANIFEST"), SessionKeyPath: required("TELESRV_GROUP_MEDIA_SESSION_KEY"),
		DatasetPath: required("TELESRV_GROUP_MEDIA_DATASET"), SeedStatePath: required("TELESRV_GROUP_MEDIA_SEED_STATE"),
		RSAKeyOverride: strings.TrimSpace(os.Getenv("TELESRV_GROUP_MEDIA_RSA_KEY")),
		ReportPath:     required("TELESRV_GROUP_MEDIA_REPORT"), ServerMetricsURL: groupMediaEnv("TELESRV_GROUP_MEDIA_SERVER_METRICS", "http://127.0.0.1:6060/metrics"),
		Media: media, Accounts: groupMediaEnvInt(t, "TELESRV_GROUP_MEDIA_ACCOUNTS", 2000),
		GroupPosition: groupMediaEnvInt(t, "TELESRV_GROUP_MEDIA_GROUP_POSITION", 0), Copies: groupMediaEnvInt(t, "TELESRV_GROUP_MEDIA_COPIES", 2),
		ChunkBytes:       groupMediaEnvInt(t, "TELESRV_GROUP_MEDIA_CHUNK_BYTES", 128<<10),
		RampDuration:     groupMediaEnvDuration(t, "TELESRV_GROUP_MEDIA_RAMP", 60*time.Second),
		ReadyTimeout:     groupMediaEnvDuration(t, "TELESRV_GROUP_MEDIA_READY_TIMEOUT", 5*time.Minute),
		OperationTimeout: groupMediaEnvDuration(t, "TELESRV_GROUP_MEDIA_OPERATION_TIMEOUT", 30*time.Second),
		FanoutTimeout:    groupMediaEnvDuration(t, "TELESRV_GROUP_MEDIA_FANOUT_TIMEOUT", 90*time.Second),
		DownloadTimeout:  groupMediaEnvDuration(t, "TELESRV_GROUP_MEDIA_DOWNLOAD_TIMEOUT", 15*time.Minute),
		RecoveryDuration: groupMediaEnvDuration(t, "TELESRV_GROUP_MEDIA_RECOVERY", 7*time.Minute),
	}
}

func groupMediaEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func groupMediaEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return parsed
}

func groupMediaEnvDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return parsed
}

func runGroupMediaLoad(ctx context.Context, cfg groupMediaConfig, logf func(string, ...any)) (*groupMediaLoadReport, error) {
	report := &groupMediaLoadReport{Version: 2, StartedAt: time.Now().UTC(), Accounts: cfg.Accounts}
	if cfg.Accounts < 2 || cfg.Copies < 1 || cfg.Copies > 20 || cfg.ChunkBytes <= 0 || cfg.ChunkBytes > 1<<20 ||
		cfg.RampDuration < 0 || cfg.ReadyTimeout <= 0 || cfg.OperationTimeout <= 0 || cfg.FanoutTimeout <= 0 || cfg.DownloadTimeout <= 0 || cfg.RecoveryDuration < 0 {
		return report, errors.New("invalid group media load configuration")
	}
	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		return report, err
	}
	dataset, err := LoadDataset(cfg.DatasetPath)
	if err != nil {
		return report, err
	}
	seedState, err := LoadDatasetSeedState(cfg.SeedStatePath, dataset)
	if err != nil {
		return report, err
	}
	if cfg.GroupPosition < 0 || cfg.GroupPosition >= len(dataset.Groups) {
		return report, errors.New("group position is outside the dataset")
	}
	group := dataset.Groups[cfg.GroupPosition]
	groupState := seedState.Groups[cfg.GroupPosition]
	if len(group.MemberAccounts) != cfg.Accounts || groupState.ChannelID <= 0 || groupState.AccessHash == 0 || groupState.InviteCursor != len(group.MemberAccounts)-1 {
		return report, fmt.Errorf("group is not an exact, fully seeded %d-member supergroup", cfg.Accounts)
	}
	report.ExactGroupMembers = len(group.MemberAccounts)
	primaries := primaryTargets(manifest.Sessions)
	records := make([]SessionRecord, 0, len(group.MemberAccounts))
	for _, account := range group.MemberAccounts {
		if account < 0 || account >= len(primaries) || primaries[account].AccountIndex != account {
			return report, fmt.Errorf("manifest has no primary session for member account %d", account)
		}
		records = append(records, primaries[account])
	}
	key, err := LoadSessionKey(cfg.SessionKeyPath)
	if err != nil {
		return report, err
	}
	publicKey, err := loadManifestPublicKey(cfg.ManifestPath, manifest.Endpoint, cfg.RSAKeyOverride)
	if err != nil {
		return report, err
	}
	metrics := newMetricSet("auth.status", "updates.getState", "channels.getFullChannel", "updates.getChannelDifference", "upload.saveFilePart", "messages.sendMedia", "upload.getFile.canonical", "upload.getFile")
	serverMetrics := newServerMetricsClient(cfg.ServerMetricsURL)
	baseline, err := serverMetrics.scrape(ctx)
	if err != nil {
		return report, fmt.Errorf("baseline server metrics: %w", err)
	}
	report.BaselineServerMetrics = baseline
	sampler := &groupMediaSampler{client: serverMetrics, peak: cloneMetricMap(baseline)}
	sampleCtx, stopSampler := context.WithCancel(ctx)
	defer stopSampler()
	go sampler.run(sampleCtx, 2*time.Second)

	memberIDs := make(map[int64]int, len(records))
	for slot, record := range records {
		memberIDs[record.UserID] = slot
	}
	runID := fmt.Sprintf("%d", report.StartedAt.UnixNano())
	fanout := &groupMediaFanoutTracker{
		prefix: "telesrv-group-media/" + runID + "/", members: memberIDs,
		expected: make(map[string]time.Time), committed: make(map[string]bool), observations: make(map[string]map[int64]groupMediaObservation),
	}
	counters := &groupMediaCounters{}
	clients, err := startGroupMediaClients(ctx, cfg, manifest.Endpoint, cfg.ManifestPath, records, key, publicKey, groupState, metrics, fanout, counters, logf)
	if err != nil {
		stopGroupMediaClients(clients)
		return report, err
	}
	report.PeakReady = counters.peakReady.Load()
	report.FinalReady = counters.ready.Load()
	logf("group media clients ready=%d/%d", report.FinalReady, cfg.Accounts)

	uploadStarted := time.Now()
	targets, typeReports, uploadErrs := uploadGroupMedia(ctx, cfg, clients, groupState, fanout, metrics, runID)
	report.UploadDurationMS = durationMS(time.Since(uploadStarted))
	report.Media = typeReports
	for _, uploadErr := range uploadErrs {
		report.Failures = append(report.Failures, uploadErr.Error())
	}
	if len(targets) > 0 {
		canonicalRaw := clients[0].raw.Load()
		for i := range targets {
			digest, _, downloadErr := downloadGroupMediaFile(ctx, canonicalRaw, targets[i], cfg.ChunkBytes, cfg.OperationTimeout, metrics, "upload.getFile.canonical")
			if downloadErr != nil {
				report.Failures = append(report.Failures, fmt.Sprintf("canonical %s download: %v", targets[i].Kind, downloadErr))
				continue
			}
			targets[i].SHA256 = digest
		}
	}

	settleStarted := time.Now()
	fanout.wait(cfg.FanoutTimeout)
	report.FanoutSettleMS = durationMS(time.Since(settleStarted))
	report.Fanout = fanout.report()
	if report.Fanout.Missing != 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("group fanout missing %d/%d observations", report.Fanout.Missing, report.Fanout.Expected))
	}
	if report.Fanout.Duplicate != 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("group difference repeated %d marker observations", report.Fanout.Duplicate))
	}

	downloadStarted := time.Now()
	downloadCtx, cancelDownload := context.WithTimeout(ctx, cfg.DownloadTimeout)
	downloaded, downloadErrs, targetErr := runGroupMediaDownloads(downloadCtx, clients, targets, cfg, report.Media, fanout, metrics)
	cancelDownload()
	if targetErr != nil {
		report.Failures = append(report.Failures, targetErr.Error())
	}
	report.DownloadDurationMS = durationMS(time.Since(downloadStarted))
	report.DownloadedBytes = downloaded
	if elapsed := time.Since(downloadStarted).Seconds(); elapsed > 0 {
		report.DownloadThroughputMiB = float64(downloaded) / (1024 * 1024) / elapsed
	}
	if downloadErrs > 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("group downloads failed %d files", downloadErrs))
	}
	for _, media := range report.Media {
		if media.MessagesSent != media.MessagesExpected {
			report.Failures = append(report.Failures, fmt.Sprintf("%s messages sent=%d want=%d", media.Kind, media.MessagesSent, media.MessagesExpected))
		}
		if media.DownloadsComplete != int64(media.DownloadsExpected) {
			report.Failures = append(report.Failures, fmt.Sprintf("%s downloads complete=%d want=%d", media.Kind, media.DownloadsComplete, media.DownloadsExpected))
		}
	}
	report.LoadEndedAt = time.Now().UTC()
	loadEnd, scrapeErr := serverMetrics.scrape(ctx)
	if scrapeErr != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("load-end metrics: %v", scrapeErr))
	}
	report.LoadEndServerMetrics = loadEnd
	report.Operations = metrics.freeze()
	report.ConnectionAttempts = counters.connectionAttempts.Load()
	report.Reconnects = counters.reconnects.Load()
	report.Disconnects = counters.disconnects.Load()
	report.FatalClients = counters.fatalClients.Load()
	report.ChannelNudges = counters.channelNudges.Load()
	report.ChannelLiveUpdates = counters.channelLiveUpdates.Load()
	if report.PeakReady != int64(cfg.Accounts) || report.FinalReady != int64(cfg.Accounts) {
		report.Failures = append(report.Failures, fmt.Sprintf("ready sessions peak/final=%d/%d want=%d", report.PeakReady, report.FinalReady, cfg.Accounts))
	}
	if report.FatalClients != 0 || report.Disconnects != 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("client fatal/disconnect=%d/%d", report.FatalClients, report.Disconnects))
	}
	if failures := counters.differenceFailures.Load(); failures != 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("channel difference failures=%d", failures))
	}
	for name, operation := range report.Operations {
		if operation.Errors != 0 || operation.FloodWaits != 0 || operation.Timeouts != 0 || operation.ConnectionErrors != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s errors/flood/timeouts/connection=%d/%d/%d/%d", name, operation.Errors, operation.FloodWaits, operation.Timeouts, operation.ConnectionErrors))
		}
	}
	if metricDelta(baseline, loadEnd, "telesrv_rpc_db_errors_total") > 0 {
		report.Failures = append(report.Failures, "server reported database errors during load")
	}

	stopGroupMediaClients(clients)
	if cfg.RecoveryDuration > 0 {
		deadline := time.NewTimer(cfg.RecoveryDuration)
		ticker := time.NewTicker(10 * time.Second)
		defer deadline.Stop()
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-ticker.C:
				_, _ = serverMetrics.scrape(ctx)
			case <-deadline.C:
				goto recovered
			}
		}
	}
recovered:
	stopSampler()
	report.FinalServerMetrics, _ = serverMetrics.scrape(ctx)
	report.PeakServerMetrics = sampler.snapshot()
	report.ServerMetricsScrapes = serverMetrics.success.Load()
	report.ServerMetricsErrors = serverMetrics.errors.Load()
	if report.ServerMetricsErrors != 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("server metrics errors=%d", report.ServerMetricsErrors))
	}
	if cfg.RecoveryDuration >= 6*time.Minute && metricValue(report.FinalServerMetrics, "telesrv_mtproto_raw_connections") != 0 {
		report.Failures = append(report.Failures, "raw connections did not return to zero after recovery")
	}
	report.FinishedAt = time.Now().UTC()
	report.Pass = len(report.Failures) == 0
	return report, nil
}

func startGroupMediaClients(
	ctx context.Context,
	cfg groupMediaConfig,
	endpoint Endpoint,
	manifestPath string,
	records []SessionRecord,
	key [32]byte,
	publicKey *rsa.PublicKey,
	group DatasetSeedGroupState,
	metrics *metricSet,
	fanout *groupMediaFanoutTracker,
	counters *groupMediaCounters,
	logf func(string, ...any),
) ([]*groupMediaClient, error) {
	clients := make([]*groupMediaClient, len(records))
	readyDeadline := time.Now().Add(cfg.RampDuration + cfg.ReadyTimeout)
	for i, record := range records {
		if i > 0 && cfg.RampDuration > 0 {
			want := cfg.RampDuration * time.Duration(i) / time.Duration(len(records))
			previous := cfg.RampDuration * time.Duration(i-1) / time.Duration(len(records))
			delay := want - previous
			select {
			case <-ctx.Done():
				return clients, ctx.Err()
			case <-time.After(delay):
			}
		}
		clientCtx, cancel := context.WithCancel(ctx)
		holder := &groupMediaClient{
			record: record, cancel: cancel, done: make(chan error, 1), differenceWake: make(chan struct{}, 1),
		}
		clients[i] = holder
		var everReady atomic.Bool
		client, err := newClient(endpoint, publicKey, &EncryptedFileStorage{Path: resolveSessionPath(manifestPath, record), Key: key}, clientHooks{
			Update: telegram.UpdateHandlerFunc(func(_ context.Context, updates tg.UpdatesClass) error {
				nudgePTS, nudged := groupMediaChannelNudge(updates, group.ChannelID)
				livePTS, live := groupMediaChannelLiveUpdate(updates, group.ChannelID)
				if nudged {
					counters.channelNudges.Add(1)
				}
				if live > 0 {
					counters.channelLiveUpdates.Add(uint64(live))
				}
				if nudged || live > 0 {
					holder.requestChannelDifference(max(nudgePTS, livePTS))
				}
				return nil
			}),
			ConnectionState: func(state telegram.ConnectionState) {
				switch state {
				case telegram.ConnectionStateConnecting:
					counters.connectionAttempts.Add(1)
					if everReady.Load() {
						counters.reconnects.Add(1)
					}
				case telegram.ConnectionStateReady:
					everReady.Store(true)
				case telegram.ConnectionStateDisconnected:
					counters.disconnects.Add(1)
				}
			},
		})
		if err != nil {
			cancel()
			return clients, err
		}
		go func() {
			runErr := client.Run(clientCtx, func(runCtx context.Context) error {
				started := time.Now()
				opCtx, stop := context.WithTimeout(runCtx, cfg.OperationTimeout)
				status, statusErr := client.Auth().Status(opCtx)
				stop()
				metrics.observe("auth.status", started, statusErr)
				if statusErr != nil {
					return statusErr
				}
				if !status.Authorized || status.User == nil || status.User.ID != record.UserID {
					return errors.New("session authorization does not match manifest")
				}
				raw := tg.NewClient(client)
				started = time.Now()
				opCtx, stop = context.WithTimeout(runCtx, cfg.OperationTimeout)
				_, stateErr := raw.UpdatesGetState(opCtx)
				stop()
				metrics.observe("updates.getState", started, stateErr)
				if stateErr != nil {
					return stateErr
				}
				started = time.Now()
				opCtx, stop = context.WithTimeout(runCtx, cfg.OperationTimeout)
				full, fullErr := raw.ChannelsGetFullChannel(opCtx, &tg.InputChannel{ChannelID: group.ChannelID, AccessHash: group.AccessHash})
				stop()
				metrics.observe("channels.getFullChannel", started, fullErr)
				if fullErr != nil {
					return fullErr
				}
				channel, ok := full.FullChat.(*tg.ChannelFull)
				if !ok {
					return fmt.Errorf("channels.getFullChannel returned %T", full.FullChat)
				}
				if channel.Pts <= 0 {
					return fmt.Errorf("channels.getFullChannel returned invalid pts=%d", channel.Pts)
				}
				holder.channelPts.Store(int64(channel.Pts))
				holder.raw.Store(raw)
				holder.ready.Store(true)
				ready := counters.ready.Add(1)
				for {
					peak := counters.peakReady.Load()
					if ready <= peak || counters.peakReady.CompareAndSwap(peak, ready) {
						break
					}
				}
				for {
					select {
					case <-runCtx.Done():
						holder.ready.Store(false)
						counters.ready.Add(-1)
						return runCtx.Err()
					case <-holder.differenceWake:
						for holder.differenceRequested.Swap(false) {
							if err := holder.catchUpChannelDifference(runCtx, raw, group, cfg.OperationTimeout, fanout, metrics); err != nil {
								counters.differenceFailures.Add(1)
								break
							}
						}
					}
				}
			})
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				counters.fatalClients.Add(1)
			}
			holder.done <- runErr
		}()
		if (i+1)%250 == 0 {
			logf("group media ramp launched=%d/%d ready=%d", i+1, len(records), counters.ready.Load())
		}
	}
	for counters.ready.Load() != int64(len(records)) {
		if time.Now().After(readyDeadline) {
			return clients, fmt.Errorf("ready timeout: %d/%d", counters.ready.Load(), len(records))
		}
		select {
		case <-ctx.Done():
			return clients, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return clients, nil
}

func (c *groupMediaClient) requestChannelDifference(pts int) {
	if c == nil {
		return
	}
	for pts > 0 {
		current := c.differenceTargetPts.Load()
		if int64(pts) <= current || c.differenceTargetPts.CompareAndSwap(current, int64(pts)) {
			break
		}
	}
	if pts <= 0 {
		c.differenceForce.Store(true)
	}
	c.differenceRequested.Store(true)
	select {
	case c.differenceWake <- struct{}{}:
	default:
	}
}

func groupMediaChannelNudge(updates tg.UpdatesClass, channelID int64) (int, bool) {
	maxPTS := 0
	found := false
	for _, update := range groupMediaUpdateClasses(updates) {
		nudge, ok := update.(*tg.UpdateChannelTooLong)
		if !ok || nudge.ChannelID != channelID {
			continue
		}
		found = true
		if pts, ok := nudge.GetPts(); ok && pts > maxPTS {
			maxPTS = pts
		}
	}
	return maxPTS, found
}

func groupMediaChannelLiveUpdate(updates tg.UpdatesClass, channelID int64) (int, int) {
	maxPTS := 0
	count := 0
	for _, update := range groupMediaUpdateClasses(updates) {
		live, ok := update.(*tg.UpdateNewChannelMessage)
		if !ok {
			continue
		}
		message, ok := live.Message.(*tg.Message)
		if !ok {
			continue
		}
		peer, ok := message.PeerID.(*tg.PeerChannel)
		if !ok || peer.ChannelID != channelID {
			continue
		}
		count++
		if live.Pts > maxPTS {
			maxPTS = live.Pts
		}
	}
	return maxPTS, count
}

func groupMediaUpdateClasses(updates tg.UpdatesClass) []tg.UpdateClass {
	switch value := updates.(type) {
	case *tg.Updates:
		return value.Updates
	case *tg.UpdatesCombined:
		return value.Updates
	case *tg.UpdateShort:
		return []tg.UpdateClass{value.Update}
	default:
		return nil
	}
}

func (c *groupMediaClient) catchUpChannelDifference(
	ctx context.Context,
	raw *tg.Client,
	group DatasetSeedGroupState,
	timeout time.Duration,
	fanout *groupMediaFanoutTracker,
	metrics *metricSet,
) error {
	if c == nil || raw == nil {
		return errors.New("channel difference client is not ready")
	}
	pts := int(c.channelPts.Load())
	if pts <= 0 {
		return errors.New("channel difference has no baseline pts")
	}
	targetPTS := int(c.differenceTargetPts.Load())
	force := c.differenceForce.Swap(false)
	if !force && targetPTS > 0 && pts >= targetPTS {
		return nil
	}
	for page := 0; page < 256; page++ {
		started := time.Now()
		opCtx, cancel := context.WithTimeout(ctx, timeout)
		difference, err := raw.UpdatesGetChannelDifference(opCtx, &tg.UpdatesGetChannelDifferenceRequest{
			Channel: &tg.InputChannel{ChannelID: group.ChannelID, AccessHash: group.AccessHash},
			Filter:  &tg.ChannelMessagesFilterEmpty{}, Pts: pts, Limit: 100,
		})
		cancel()
		metrics.observe("updates.getChannelDifference", started, err)
		if err != nil {
			return err
		}

		var final bool
		var messages []tg.MessageClass
		var updates []tg.UpdateClass
		pts, final, messages, updates, err = groupMediaDifferencePage(pts, difference)
		if err != nil {
			return err
		}
		fanout.observeDifference(c.record.UserID, messages, updates)
		c.channelPts.Store(int64(pts))
		if !final {
			continue
		}
		latestTarget := int(c.differenceTargetPts.Load())
		if latestTarget > pts {
			return fmt.Errorf("channel difference stopped at pts=%d before nudge pts=%d", pts, latestTarget)
		}
		return nil
	}
	return errors.New("channel difference exceeded 256 pages")
}

func groupMediaDifferencePage(previous int, difference tg.UpdatesChannelDifferenceClass) (int, bool, []tg.MessageClass, []tg.UpdateClass, error) {
	pts := previous
	final := true
	var messages []tg.MessageClass
	var updates []tg.UpdateClass
	switch value := difference.(type) {
	case *tg.UpdatesChannelDifferenceEmpty:
		if !value.Final {
			return 0, false, nil, nil, errors.New("channelDifferenceEmpty is not final")
		}
		pts = value.Pts
		final = value.Final
	case *tg.UpdatesChannelDifference:
		pts = value.Pts
		final = value.Final
		messages = value.NewMessages
		updates = value.OtherUpdates
	case *tg.UpdatesChannelDifferenceTooLong:
		if !value.Final {
			return 0, false, nil, nil, errors.New("channelDifferenceTooLong is not final")
		}
		dialog, ok := value.Dialog.(*tg.Dialog)
		if !ok {
			return 0, false, nil, nil, fmt.Errorf("channelDifferenceTooLong dialog is %T", value.Dialog)
		}
		current, ok := dialog.GetPts()
		if !ok {
			return 0, false, nil, nil, errors.New("channelDifferenceTooLong omitted pts")
		}
		pts = current
		final = value.Final
		messages = value.Messages
	default:
		return 0, false, nil, nil, fmt.Errorf("updates.getChannelDifference returned %T", difference)
	}
	if pts < previous || (!final && pts == previous) {
		return 0, false, nil, nil, fmt.Errorf("channel difference pts did not advance: previous=%d current=%d final=%t", previous, pts, final)
	}
	return pts, final, messages, updates, nil
}

func stopGroupMediaClients(clients []*groupMediaClient) {
	for _, client := range clients {
		if client != nil && client.cancel != nil {
			client.cancel()
		}
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for _, client := range clients {
		if client == nil || client.done == nil {
			continue
		}
		select {
		case <-client.done:
		case <-deadline.C:
			return
		}
	}
}

func uploadGroupMedia(
	ctx context.Context,
	cfg groupMediaConfig,
	clients []*groupMediaClient,
	group DatasetSeedGroupState,
	fanout *groupMediaFanoutTracker,
	metrics *metricSet,
	runID string,
) ([]groupMediaTarget, []groupMediaTypeReport, []error) {
	type task struct {
		source groupMediaSource
		copy   int
		client *groupMediaClient
	}
	tasks := make([]task, 0, len(cfg.Media)*cfg.Copies)
	typeIndex := make(map[string]int, len(cfg.Media))
	reports := make([]groupMediaTypeReport, len(cfg.Media))
	for i, source := range cfg.Media {
		typeIndex[source.Kind] = i
		reports[i] = groupMediaTypeReport{Kind: source.Kind, SourceBytes: int64(len(source.Data)), MessagesExpected: cfg.Copies, DownloadsExpected: cfg.Copies * cfg.Accounts}
		for copyIndex := 0; copyIndex < cfg.Copies; copyIndex++ {
			tasks = append(tasks, task{source: source, copy: copyIndex, client: clients[len(tasks)%len(clients)]})
		}
	}
	results := make(chan struct {
		target groupMediaTarget
		err    error
	}, len(tasks))
	var wg sync.WaitGroup
	for _, upload := range tasks {
		upload := upload
		wg.Add(1)
		go func() {
			defer wg.Done()
			marker := fmt.Sprintf("telesrv-group-media/%s/%s/%d", runID, upload.source.Kind, upload.copy+1)
			target, err := uploadAndSendGroupMedia(ctx, upload.client, upload.source, marker, group, cfg.OperationTimeout, fanout, metrics)
			results <- struct {
				target groupMediaTarget
				err    error
			}{target: target, err: err}
		}()
	}
	wg.Wait()
	close(results)
	targets := make([]groupMediaTarget, 0, len(tasks))
	var errs []error
	for result := range results {
		if result.err != nil {
			errs = append(errs, result.err)
			continue
		}
		targets = append(targets, result.target)
		report := &reports[typeIndex[result.target.Kind]]
		report.MessagesSent++
		report.DownloadFileBytes += result.target.Size
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Marker < targets[j].Marker })
	return targets, reports, errs
}

func uploadAndSendGroupMedia(
	ctx context.Context,
	client *groupMediaClient,
	source groupMediaSource,
	marker string,
	group DatasetSeedGroupState,
	timeout time.Duration,
	fanout *groupMediaFanoutTracker,
	metrics *metricSet,
) (groupMediaTarget, error) {
	if client == nil {
		return groupMediaTarget{}, errors.New("sender is not ready")
	}
	raw := client.raw.Load()
	if raw == nil {
		return groupMediaTarget{}, errors.New("sender is not ready")
	}
	fileID, err := randomNonZeroInt64()
	if err != nil {
		return groupMediaTarget{}, err
	}
	const partSize = 512 << 10
	parts := (len(source.Data) + partSize - 1) / partSize
	big := len(source.Data) > 10<<20
	for part := 0; part < parts; part++ {
		startOffset := part * partSize
		endOffset := min(len(source.Data), startOffset+partSize)
		started := time.Now()
		opCtx, cancel := context.WithTimeout(ctx, timeout)
		var saved bool
		if big {
			saved, err = raw.UploadSaveBigFilePart(opCtx, &tg.UploadSaveBigFilePartRequest{FileID: fileID, FilePart: part, FileTotalParts: parts, Bytes: source.Data[startOffset:endOffset]})
		} else {
			saved, err = raw.UploadSaveFilePart(opCtx, &tg.UploadSaveFilePartRequest{FileID: fileID, FilePart: part, Bytes: source.Data[startOffset:endOffset]})
		}
		cancel()
		metrics.observe("upload.saveFilePart", started, err)
		if err != nil || !saved {
			if err == nil {
				err = fmt.Errorf("upload part %d returned false", part)
			}
			return groupMediaTarget{}, fmt.Errorf("%s: %w", source.Kind, err)
		}
	}
	var file tg.InputFileClass
	if big {
		file = &tg.InputFileBig{ID: fileID, Parts: parts, Name: source.Name}
	} else {
		digest := md5.Sum(source.Data)
		file = &tg.InputFile{ID: fileID, Parts: parts, Name: source.Name, MD5Checksum: hex.EncodeToString(digest[:])}
	}
	media := groupMediaInput(source, file)
	fanout.begin(marker)
	started := time.Now()
	opCtx, cancel := context.WithTimeout(ctx, timeout)
	updates, err := raw.MessagesSendMedia(opCtx, &tg.MessagesSendMediaRequest{
		Peer:  &tg.InputPeerChannel{ChannelID: group.ChannelID, AccessHash: group.AccessHash},
		Media: media, Message: marker, RandomID: randomGroupMediaID(),
	})
	cancel()
	metrics.observe("messages.sendMedia", started, err)
	if err != nil {
		fanout.finish(marker, false)
		return groupMediaTarget{}, fmt.Errorf("%s sendMedia: %w", source.Kind, err)
	}
	fanout.finish(marker, true)
	target, err := groupMediaTargetFromUpdates(source.Kind, marker, updates)
	if err != nil {
		return groupMediaTarget{}, err
	}
	nudgePTS, _ := groupMediaChannelNudge(updates, group.ChannelID)
	livePTS, _ := groupMediaChannelLiveUpdate(updates, group.ChannelID)
	client.requestChannelDifference(max(nudgePTS, livePTS))
	return target, nil
}

func groupMediaInput(source groupMediaSource, file tg.InputFileClass) tg.InputMediaClass {
	filename := &tg.DocumentAttributeFilename{FileName: source.Name}
	switch source.Kind {
	case "photo":
		return &tg.InputMediaUploadedPhoto{File: file}
	case "video":
		video := &tg.DocumentAttributeVideo{Duration: 5, W: 1280, H: 720}
		video.SetSupportsStreaming(true)
		return &tg.InputMediaUploadedDocument{File: file, MimeType: source.MimeType, Attributes: []tg.DocumentAttributeClass{filename, video}}
	case "audio":
		audio := &tg.DocumentAttributeAudio{Duration: 20}
		audio.SetTitle("telesrv load audio")
		audio.SetPerformer("telesrv")
		return &tg.InputMediaUploadedDocument{File: file, MimeType: source.MimeType, Attributes: []tg.DocumentAttributeClass{filename, audio}}
	case "voice":
		voice := &tg.DocumentAttributeAudio{Duration: 20}
		voice.SetVoice(true)
		return &tg.InputMediaUploadedDocument{File: file, MimeType: source.MimeType, Attributes: []tg.DocumentAttributeClass{filename, voice}}
	default:
		return &tg.InputMediaUploadedDocument{File: file, MimeType: source.MimeType, ForceFile: true, Attributes: []tg.DocumentAttributeClass{filename}}
	}
}

func groupMediaTargetFromUpdates(kind, marker string, updates tg.UpdatesClass) (groupMediaTarget, error) {
	var classes []tg.UpdateClass
	switch value := updates.(type) {
	case *tg.Updates:
		classes = value.Updates
	case *tg.UpdatesCombined:
		classes = value.Updates
	case *tg.UpdateShort:
		classes = []tg.UpdateClass{value.Update}
	default:
		return groupMediaTarget{}, fmt.Errorf("%s sendMedia returned %T", kind, updates)
	}
	for _, update := range classes {
		var message tg.MessageClass
		switch value := update.(type) {
		case *tg.UpdateNewChannelMessage:
			message = value.Message
		case *tg.UpdateNewMessage:
			message = value.Message
		default:
			continue
		}
		full, ok := message.(*tg.Message)
		if !ok || full.Message != marker {
			continue
		}
		return groupMediaTargetFromMessage(kind, marker, full)
	}
	return groupMediaTarget{}, fmt.Errorf("%s sendMedia response omitted marker", kind)
}

func groupMediaTargetFromMessage(kind, marker string, message *tg.Message) (groupMediaTarget, error) {
	if message == nil || message.Message != marker {
		return groupMediaTarget{}, errors.New("group media marker does not match message")
	}
	media, ok := message.GetMedia()
	if !ok {
		return groupMediaTarget{}, fmt.Errorf("%s message omitted media", kind)
	}
	switch value := media.(type) {
	case *tg.MessageMediaPhoto:
		photoClass, ok := value.GetPhoto()
		photo, okPhoto := photoClass.(*tg.Photo)
		if !ok || !okPhoto {
			return groupMediaTarget{}, fmt.Errorf("photo message returned %T", photoClass)
		}
		var best *tg.PhotoSize
		for _, sizeClass := range photo.Sizes {
			if size, ok := sizeClass.(*tg.PhotoSize); ok && (best == nil || size.Size > best.Size) {
				best = size
			}
		}
		if best == nil || best.Size <= 0 {
			return groupMediaTarget{}, errors.New("photo message has no downloadable size")
		}
		return groupMediaTarget{Kind: kind, Marker: marker, Size: int64(best.Size), Location: photo.AsInputPhotoFileLocation(best.Type)}, nil
	case *tg.MessageMediaDocument:
		documentClass, ok := value.GetDocument()
		document, okDocument := documentClass.(*tg.Document)
		if !ok || !okDocument || document.Size <= 0 {
			return groupMediaTarget{}, fmt.Errorf("%s message returned %T", kind, documentClass)
		}
		return groupMediaTarget{Kind: kind, Marker: marker, Size: document.Size, Location: document.AsInputDocumentFileLocation("")}, nil
	default:
		return groupMediaTarget{}, fmt.Errorf("%s message media is %T", kind, media)
	}
}

func randomGroupMediaID() int64 {
	var data [8]byte
	if _, err := cryptorand.Read(data[:]); err != nil {
		return time.Now().UnixNano()
	}
	value := int64(0)
	for _, b := range data {
		value = value<<8 | int64(b)
	}
	if value == 0 {
		return 1
	}
	return value
}

func runGroupMediaDownloads(
	ctx context.Context,
	clients []*groupMediaClient,
	canonicalTargets []groupMediaTarget,
	cfg groupMediaConfig,
	reports []groupMediaTypeReport,
	fanout *groupMediaFanoutTracker,
	metrics *metricSet,
) (int64, int64, error) {
	byKind := make(map[string]int, len(reports))
	for i := range reports {
		byKind[reports[i].Kind] = i
	}
	type counters struct {
		complete atomic.Int64
		bytes    atomic.Int64
		errors   atomic.Int64
	}
	typeCounters := make([]counters, len(reports))
	clientTargets := make([][]groupMediaTarget, len(clients))
	for i, client := range clients {
		if client == nil {
			return 0, int64(len(clients) * len(canonicalTargets)), errors.New("nil group media download client")
		}
		targets, err := fanout.targetsForUser(client.record.UserID, canonicalTargets)
		if err != nil {
			return 0, int64(len(clients) * len(canonicalTargets)), fmt.Errorf("member %d difference targets: %w", client.record.UserID, err)
		}
		clientTargets[i] = targets
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for clientIndex, client := range clients {
		clientIndex, client := clientIndex, client
		targets := clientTargets[clientIndex]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for offset := range targets {
				target := targets[(offset+clientIndex)%len(targets)]
				counter := &typeCounters[byKind[target.Kind]]
				digest, bytesRead, err := downloadGroupMediaFile(ctx, client.raw.Load(), target, cfg.ChunkBytes, cfg.OperationTimeout, metrics, "upload.getFile")
				if err != nil || digest != target.SHA256 {
					counter.errors.Add(1)
					continue
				}
				counter.complete.Add(1)
				counter.bytes.Add(bytesRead)
			}
		}()
	}
	close(start)
	wg.Wait()
	var totalBytes, totalErrors int64
	for i := range reports {
		reports[i].DownloadsComplete = typeCounters[i].complete.Load()
		reports[i].DownloadedBytes = typeCounters[i].bytes.Load()
		reports[i].DownloadErrors = typeCounters[i].errors.Load()
		totalBytes += reports[i].DownloadedBytes
		totalErrors += reports[i].DownloadErrors
	}
	return totalBytes, totalErrors, nil
}

func downloadGroupMediaFile(
	ctx context.Context,
	raw *tg.Client,
	target groupMediaTarget,
	chunk int,
	timeout time.Duration,
	metrics *metricSet,
	operation string,
) ([sha256.Size]byte, int64, error) {
	if raw == nil {
		return [sha256.Size]byte{}, 0, errors.New("download client is not ready")
	}
	var offset int64
	digest := sha256.New()
	for offset < target.Size {
		limit := min(chunk, int(target.Size-offset))
		started := time.Now()
		opCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := raw.UploadGetFile(opCtx, &tg.UploadGetFileRequest{Location: target.Location, Offset: offset, Limit: limit})
		cancel()
		if err == nil {
			file, ok := result.(*tg.UploadFile)
			if !ok {
				err = fmt.Errorf("upload.getFile returned %T", result)
			} else if len(file.Bytes) != limit {
				err = fmt.Errorf("upload.getFile bytes=%d want=%d", len(file.Bytes), limit)
			} else {
				_, _ = digest.Write(file.Bytes)
				offset += int64(len(file.Bytes))
			}
		}
		metrics.observe(operation, started, err)
		if err != nil {
			return [sha256.Size]byte{}, offset, err
		}
	}
	return sumGroupMediaHash(digest), offset, nil
}

func sumGroupMediaHash(digest hash.Hash) [sha256.Size]byte {
	var out [sha256.Size]byte
	copy(out[:], digest.Sum(nil))
	return out
}

func (t *groupMediaFanoutTracker) begin(marker string) {
	t.mu.Lock()
	t.expected[marker] = time.Now()
	t.mu.Unlock()
}

func (t *groupMediaFanoutTracker) finish(marker string, success bool) {
	t.mu.Lock()
	if success {
		t.committed[marker] = true
	} else {
		delete(t.expected, marker)
		delete(t.observations, marker)
	}
	t.mu.Unlock()
}

func (t *groupMediaFanoutTracker) observeDifference(userID int64, messages []tg.MessageClass, updates []tg.UpdateClass) {
	for _, message := range messages {
		if full, ok := message.(*tg.Message); ok {
			t.observeMessage(full, userID)
		}
	}
	for _, update := range updates {
		var message tg.MessageClass
		switch value := update.(type) {
		case *tg.UpdateNewChannelMessage:
			message = value.Message
		default:
			continue
		}
		if full, ok := message.(*tg.Message); ok {
			t.observeMessage(full, userID)
		}
	}
}

func (t *groupMediaFanoutTracker) observeMessage(message *tg.Message, userID int64) {
	if message == nil {
		return
	}
	marker := message.Message
	if !strings.HasPrefix(marker, t.prefix) {
		return
	}
	if _, ok := t.members[userID]; !ok {
		return
	}
	remainder := strings.TrimPrefix(marker, t.prefix)
	kind, _, ok := strings.Cut(remainder, "/")
	if !ok || kind == "" {
		return
	}
	target, err := groupMediaTargetFromMessage(kind, marker, message)
	if err != nil {
		return
	}
	t.mu.Lock()
	if _, ok := t.expected[marker]; !ok {
		t.mu.Unlock()
		return
	}
	byUser := t.observations[marker]
	if byUser == nil {
		byUser = make(map[int64]groupMediaObservation)
		t.observations[marker] = byUser
	}
	observation := byUser[userID]
	if observation.first.IsZero() {
		observation.first = time.Now()
		observation.target = target
	} else {
		observation.repeat++
	}
	byUser[userID] = observation
	t.mu.Unlock()
}

func (t *groupMediaFanoutTracker) targetsForUser(userID int64, canonical []groupMediaTarget) ([]groupMediaTarget, error) {
	if t == nil {
		return nil, errors.New("nil group media fanout tracker")
	}
	canonicalByMarker := make(map[string]groupMediaTarget, len(canonical))
	for _, target := range canonical {
		canonicalByMarker[target.Marker] = target
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]groupMediaTarget, 0, len(canonical))
	for marker, expected := range canonicalByMarker {
		observation, ok := t.observations[marker][userID]
		if !ok || observation.first.IsZero() || observation.target.Location == nil {
			return nil, fmt.Errorf("marker %q was not recovered through channel difference", marker)
		}
		if observation.target.Kind != expected.Kind || observation.target.Size != expected.Size {
			return nil, fmt.Errorf("marker %q target identity differs from sender result", marker)
		}
		target := observation.target
		target.SHA256 = expected.SHA256
		result = append(result, target)
	}
	if len(result) != len(canonical) {
		return nil, fmt.Errorf("recovered %d targets, want %d", len(result), len(canonical))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Marker < result[j].Marker })
	return result, nil
}

func (t *groupMediaFanoutTracker) wait(timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		report := t.report()
		if report.Expected > 0 && report.Observed == report.Expected {
			return
		}
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (t *groupMediaFanoutTracker) report() groupMediaFanoutReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	report := groupMediaFanoutReport{MissingByMessage: make(map[string]int)}
	missingSlots := make(map[int]struct{})
	latencies := make([]time.Duration, 0, len(t.expected)*len(t.members))
	for marker, started := range t.expected {
		if !t.committed[marker] {
			continue
		}
		report.Messages++
		report.Expected += int64(len(t.members))
		observed := t.observations[marker]
		for _, observation := range observed {
			report.Observed++
			report.Duplicate += observation.repeat
			if !observation.first.Before(started) {
				latencies = append(latencies, observation.first.Sub(started))
			}
		}
		missing := 0
		for userID, slot := range t.members {
			if _, ok := observed[userID]; ok {
				continue
			}
			missing++
			if len(missingSlots) < 64 {
				missingSlots[slot] = struct{}{}
			}
		}
		if missing > 0 {
			report.MissingByMessage[marker] = missing
		}
	}
	report.Missing = report.Expected - report.Observed
	for slot := range missingSlots {
		report.MissingMemberSlots = append(report.MissingMemberSlots, slot)
	}
	sort.Ints(report.MissingMemberSlots)
	if len(latencies) > 0 {
		report.P50MS = groupMediaQuantile(latencies, .50)
		report.P95MS = groupMediaQuantile(latencies, .95)
		report.P99MS = groupMediaQuantile(latencies, .99)
		report.MaxMS = groupMediaQuantile(latencies, 1)
	}
	return report
}

func groupMediaQuantile(values []time.Duration, q float64) float64 {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted)-1) * q)
	return durationMS(sorted[index])
}

func (s *groupMediaSampler) run(ctx context.Context, interval time.Duration) {
	if s == nil || s.client == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics, err := s.client.scrape(ctx)
			if err != nil {
				continue
			}
			s.mu.Lock()
			for name, value := range metrics {
				if value > s.peak[name] {
					s.peak[name] = value
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *groupMediaSampler) snapshot() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMetricMap(s.peak)
}

func cloneMetricMap(source map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(source))
	for name, value := range source {
		out[name] = value
	}
	return out
}

func metricDelta(before, after map[string]float64, name string) float64 {
	return metricValue(after, name) - metricValue(before, name)
}

func writeGroupMediaReport(path string, report *groupMediaLoadReport) error {
	if report == nil {
		return errors.New("nil group media report")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}
