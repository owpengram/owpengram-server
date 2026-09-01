// Command telesrv-load provisions and drives real encrypted MTProto sessions.
// It is intentionally separate from the server process so a load generator can
// run on the M2 host without sharing server memory, database connections or
// internal handler shortcuts.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"telesrv/internal/loadharness"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "telesrv-load:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:])
	case "provision":
		return runProvision(ctx, args[1:])
	case "plan-dataset":
		return runPlanDataset(args[1:])
	case "seed":
		return runSeed(ctx, args[1:])
	case "snapshot":
		return runSnapshot(ctx, args[1:])
	case "mutate-offline":
		return runMutateOffline(ctx, args[1:])
	case "startup-run":
		return runStartup(ctx, args[1:])
	case "run":
		return runLoad(ctx, args[1:])
	case "summarize":
		return runSummarize(args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(os.Stdout, usageText)
		return nil
	default:
		return usageError()
	}
}

func runPlanDataset(args []string) error {
	flags := flag.NewFlagSet("plan-dataset", flag.ContinueOnError)
	out := flags.String("out", filepath.FromSlash("data/loadtest/dataset.json"), "owner-only immutable dataset plan")
	accounts := flags.Int("accounts", 1000, "logical primary accounts in the provisioned manifest")
	seed := flags.Int64("seed", 20260827, "deterministic topology and idempotency seed")
	privateFanout := flags.Int("private-fanout", -1, "outgoing private messages per account; -1 uses min(10, accounts-1)")
	hotGroups := flags.Int("hot-groups", 10, "hot supergroup count")
	hotMembers := flags.Int("hot-members", 0, "members per hot supergroup; 0 uses all accounts")
	hotHistory := flags.Int("hot-history", 100, "messages per hot supergroup")
	mediumGroups := flags.Int("medium-groups", 100, "medium supergroup count")
	mediumMembers := flags.Int("medium-members", 100, "members per medium supergroup")
	mediumHistory := flags.Int("medium-history", 30, "messages per medium supergroup")
	smallGroups := flags.Int("small-groups", 200, "small supergroup count")
	smallMembers := flags.Int("small-members", 20, "members per small supergroup")
	smallHistory := flags.Int("small-history", 10, "messages per small supergroup")
	heavyGroups := flags.Int("heavy-groups", 200, "heavy-user supergroup count")
	heavyAccounts := flags.Int("heavy-accounts", 100, "accounts included in every heavy supergroup")
	heavyHistory := flags.Int("heavy-history", 30, "messages per heavy supergroup")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("plan-dataset accepts no positional arguments")
	}
	if *hotMembers == 0 {
		*hotMembers = *accounts
	}
	if *privateFanout == -1 {
		*privateFanout = min(10, max(*accounts-1, 0))
	}
	cfg := loadharness.DatasetConfig{
		Accounts: *accounts, Seed: *seed, PrivateFanout: *privateFanout,
		HotGroups: *hotGroups, HotMembers: *hotMembers, HotHistory: *hotHistory,
		MediumGroups: *mediumGroups, MediumMembers: min(*mediumMembers, *accounts), MediumHistory: *mediumHistory,
		SmallGroups: *smallGroups, SmallMembers: min(*smallMembers, *accounts), SmallHistory: *smallHistory,
		HeavyGroups: *heavyGroups, HeavyAccounts: min(*heavyAccounts, *accounts), HeavyHistory: *heavyHistory,
	}
	if _, err := os.Stat(*out); err == nil {
		existing, loadErr := loadharness.LoadDataset(*out)
		if loadErr != nil {
			return loadErr
		}
		if existing.Config != cfg {
			return fmt.Errorf("refusing to replace existing dataset plan %s with different config", *out)
		}
		fmt.Fprintf(os.Stdout, "dataset plan already exists at %s hash=%s groups=%d private_messages=%d\n",
			*out, existing.PlanSHA256, len(existing.Groups), len(existing.PrivateEdges))
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	dataset, err := loadharness.PlanDataset(cfg)
	if err != nil {
		return err
	}
	if err := loadharness.WriteDataset(*out, dataset); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "dataset plan written to %s hash=%s groups=%d private_messages=%d\n",
		*out, dataset.PlanSHA256, len(dataset.Groups), len(dataset.PrivateEdges))
	return nil
}

func runSeed(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	manifest := flags.String("manifest", filepath.FromSlash("data/loadtest/manifest.json"), "provisioned manifest")
	sessionKey := flags.String("session-key", filepath.FromSlash("data/loadtest/session.key"), "session encryption key")
	rsaOverride := flags.String("rsa-key", "", "optional RSA public/private PEM override")
	dataset := flags.String("dataset", filepath.FromSlash("data/loadtest/dataset.json"), "immutable dataset plan")
	state := flags.String("state", filepath.FromSlash("data/loadtest/dataset-seed-state.json"), "resumable seed journal")
	concurrency := flags.Int("concurrency", 8, "parallel account workers (max 64)")
	operationTimeout := flags.Duration("operation-timeout", 30*time.Second, "maximum duration of one seed RPC")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("seed accepts no positional arguments")
	}
	result, err := loadharness.Seed(ctx, loadharness.SeedConfig{
		ManifestPath: *manifest, SessionKeyPath: *sessionKey, RSAKeyOverride: *rsaOverride,
		DatasetPath: *dataset, SeedStatePath: *state, Concurrency: *concurrency, OperationTimeout: *operationTimeout,
	}, func(event loadharness.SeedEvent) {
		status := "ok"
		if event.Err != nil {
			status = "error"
		}
		fmt.Fprintf(os.Stdout, "seed phase=%s %d/%d account=%d status=%s\n",
			event.Phase, event.Completed, event.Total, event.Account, status)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "seed complete private_messages=%d supergroups=%d invited_members=%d group_messages=%d rich_state_accounts=%d state=%s\n",
		result.PrivateMessages, result.Groups, result.InvitedMembers, result.GroupMessages, result.RichStateAccounts, *state)
	return nil
}

func runSnapshot(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	manifest := flags.String("manifest", filepath.FromSlash("data/loadtest/manifest.json"), "provisioned manifest")
	sessionKey := flags.String("session-key", filepath.FromSlash("data/loadtest/session.key"), "session encryption key")
	rsaOverride := flags.String("rsa-key", "", "optional RSA public/private PEM override")
	dataset := flags.String("dataset", filepath.FromSlash("data/loadtest/dataset.json"), "immutable dataset plan")
	seedState := flags.String("seed-state", filepath.FromSlash("data/loadtest/dataset-seed-state.json"), "completed seed journal")
	clientState := flags.String("client-state", filepath.FromSlash("data/loadtest/client-state.json"), "baseline account/dialog/PTS snapshot")
	concurrency := flags.Int("concurrency", 8, "parallel account workers (max 64)")
	operationTimeout := flags.Duration("operation-timeout", 30*time.Second, "maximum duration of one snapshot RPC")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("snapshot accepts no positional arguments")
	}
	result, err := loadharness.SnapshotClientState(ctx, loadharness.SnapshotConfig{
		ManifestPath: *manifest, SessionKeyPath: *sessionKey, RSAKeyOverride: *rsaOverride,
		DatasetPath: *dataset, SeedStatePath: *seedState, ClientStatePath: *clientState,
		Concurrency: *concurrency, OperationTimeout: *operationTimeout,
	}, func(event loadharness.SnapshotEvent) {
		status := "ok"
		if event.Resumed {
			status = "resumed"
		}
		if event.Err != nil {
			status = "error"
		}
		fmt.Fprintf(os.Stdout, "snapshot %d/%d account=%d status=%s\n", event.Completed, event.Total, event.Account, status)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "snapshot complete accounts=%d dialogs=%d channel_dialogs=%d client_state=%s\n",
		result.Accounts, result.Dialogs, result.Channels, *clientState)
	return nil
}

func runMutateOffline(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("mutate-offline", flag.ContinueOnError)
	manifest := flags.String("manifest", filepath.FromSlash("data/loadtest/manifest.json"), "provisioned manifest")
	sessionKey := flags.String("session-key", filepath.FromSlash("data/loadtest/session.key"), "session encryption key")
	rsaOverride := flags.String("rsa-key", "", "optional RSA public/private PEM override")
	dataset := flags.String("dataset", filepath.FromSlash("data/loadtest/dataset.json"), "immutable dataset plan")
	seedState := flags.String("seed-state", filepath.FromSlash("data/loadtest/dataset-seed-state.json"), "completed seed journal")
	clientState := flags.String("client-state", filepath.FromSlash("data/loadtest/client-state.json"), "immutable old account/channel PTS snapshot")
	mutationState := flags.String("mutation-state", filepath.FromSlash("data/loadtest/offline-mutation-state.json"), "resumable offline mutation journal")
	concurrency := flags.Int("concurrency", 8, "parallel writer accounts (max 64)")
	operationTimeout := flags.Duration("operation-timeout", 30*time.Second, "maximum duration of one mutation RPC")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("mutate-offline accepts no positional arguments")
	}
	result, err := loadharness.MutateOffline(ctx, loadharness.MutateOfflineConfig{
		ManifestPath: *manifest, SessionKeyPath: *sessionKey, RSAKeyOverride: *rsaOverride,
		DatasetPath: *dataset, SeedStatePath: *seedState, ClientStatePath: *clientState,
		MutationStatePath: *mutationState, Concurrency: *concurrency, OperationTimeout: *operationTimeout,
	}, func(event loadharness.MutationEvent) {
		status := "ok"
		if event.Err != nil {
			status = "error"
		}
		fmt.Fprintf(os.Stdout, "mutate phase=%s %d/%d account=%d status=%s\n",
			event.Phase, event.Completed, event.Total, event.Account, status)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "offline mutation complete private_messages=%d dirty_channels=%d channel_messages=%d edited=%d deleted=%d pinned=%d state=%s\n",
		result.PrivateMessages, result.DirtyChannels, result.ChannelMessages, result.Edited, result.Deleted, result.Pinned, *mutationState)
	return nil
}

func runStartup(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("startup-run", flag.ContinueOnError)
	manifest := flags.String("manifest", filepath.FromSlash("data/loadtest/manifest.json"), "provisioned manifest")
	sessionKey := flags.String("session-key", filepath.FromSlash("data/loadtest/session.key"), "session encryption key")
	rsaOverride := flags.String("rsa-key", "", "optional RSA public/private PEM override")
	dataset := flags.String("dataset", filepath.FromSlash("data/loadtest/dataset.json"), "immutable dataset plan")
	seedState := flags.String("seed-state", filepath.FromSlash("data/loadtest/dataset-seed-state.json"), "completed seed journal")
	clientState := flags.String("client-state", filepath.FromSlash("data/loadtest/client-state.json"), "immutable old account/channel PTS snapshot")
	mutationState := flags.String("mutation-state", filepath.FromSlash("data/loadtest/offline-mutation-state.json"), "completed offline mutation journal")
	report := flags.String("report", filepath.FromSlash("data/loadtest/startup-report.json"), "startup correctness and latency report")
	events := flags.String("events", filepath.FromSlash("data/loadtest/startup-events.ndjson"), "periodic owner-only startup and server metric evidence")
	serverMetrics := flags.String("server-metrics", "http://127.0.0.1:6060/metrics", "server metrics URL; empty disables")
	profile := flags.String("profile", loadharness.StartupProfileTDesktopReturningV1, "startup workload: tdesktop-cold-returning-v1 or tdlib-returning-v1")
	startOrder := flags.String("start-order", loadharness.StartupOrderShuffled, "account launch order: shuffled or account-index")
	startOrderSeed := flags.Int64("start-order-seed", 0, "deterministic shuffled launch seed; 0 uses the dataset seed")
	accounts := flags.Int("accounts", 0, "limit first N accounts; 0 uses the complete dataset")
	ramp := flags.Duration("ramp", 30*time.Second, "connection start ramp duration")
	operationTimeout := flags.Duration("operation-timeout", 30*time.Second, "maximum duration of one startup RPC")
	sampleInterval := flags.Duration("sample-interval", 2*time.Second, "server resource sampling interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("startup-run accepts no positional arguments")
	}
	result, err := loadharness.StartupRun(ctx, loadharness.StartupRunConfig{
		ManifestPath: *manifest, SessionKeyPath: *sessionKey, RSAKeyOverride: *rsaOverride,
		DatasetPath: *dataset, SeedStatePath: *seedState, ClientStatePath: *clientState,
		MutationStatePath: *mutationState, ReportPath: *report, EventsPath: *events, ServerMetricsURL: *serverMetrics,
		Profile: *profile, StartOrder: *startOrder, StartOrderSeed: *startOrderSeed,
		AccountLimit: *accounts, RampDuration: *ramp, OperationTimeout: *operationTimeout,
		SampleInterval: *sampleInterval,
	})
	if err != nil {
		return err
	}
	printStartupSummary(result)
	if !result.Pass {
		return fmt.Errorf("startup acceptance failed; see %s", *report)
	}
	return nil
}

func printStartupSummary(report *loadharness.StartupRunReport) {
	fmt.Fprintf(os.Stdout, "pass=%v business_ready=%d/%d dialogs=%d channel_dialogs=%d account_diff_calls=%d channel_diff_calls=%d channel_full=%d channel_too_long=%d channel_empty=%d\n",
		report.Pass, report.BusinessReady, report.ExpectedAccounts, report.DialogsObserved, report.ChannelDialogs,
		report.AccountDifference.Calls, report.ChannelDifference.Calls, report.ChannelDifference.Full,
		report.ChannelDifference.TooLong, report.ChannelDifference.Empty)
	for _, failure := range report.Failures {
		fmt.Fprintln(os.Stdout, "failure:", failure)
	}
}

func runKeygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	path := flags.String("out", filepath.FromSlash("data/loadtest/session.key"), "owner-only session encryption key file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("keygen accepts no positional arguments")
	}
	if err := loadharness.GenerateSessionKey(*path); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "session encryption key written to %s\n", *path)
	return nil
}

func runProvision(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("provision", flag.ContinueOnError)
	manifest := flags.String("manifest", filepath.FromSlash("data/loadtest/manifest.json"), "output manifest")
	sessionKey := flags.String("session-key", filepath.FromSlash("data/loadtest/session.key"), "session encryption key")
	server := flags.String("server", "127.0.0.1:2398", "MTProto server address")
	dc := flags.Int("dc", 2, "wire DC label")
	rsaKey := flags.String("rsa-key", filepath.FromSlash("data/server_rsa.pem"), "server RSA private/public PEM")
	apiID := flags.Int("api-id", 1, "test application ID")
	apiHash := flags.String("api-hash", "hash", "test application hash")
	accounts := flags.Int("accounts", 450, "unique accounts")
	extraDevices := flags.Int("extra-devices", 50, "accounts receiving a second independent session")
	concurrency := flags.Int("concurrency", 8, "parallel provisioning workers (max 64)")
	phonePrefix := flags.String("phone-prefix", loadharness.DefaultPhonePrefix, "possible reserved NANP prefix followed by a six-digit account index")
	firstName := flags.String("first-name-prefix", "Load", "generated first-name prefix")
	obfuscated := flags.Bool("obfuscated", true, "use TDesktop-like Obfuscated2 + abridged transport")
	pfs := flags.Bool("pfs", true, "bind temporary auth keys using PFS")
	tempKeyTTL := flags.Int("temp-key-ttl", 86400, "temporary auth-key lifetime in seconds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("provision accepts no positional arguments")
	}
	code := os.Getenv("TELESRV_LOAD_LOGIN_CODE")
	if code == "" {
		return errors.New("TELESRV_LOAD_LOGIN_CODE must contain the test environment login code")
	}
	cfg := loadharness.ProvisionConfig{
		ManifestPath: *manifest, SessionKeyPath: *sessionKey, RSAKeyPath: *rsaKey,
		Endpoint: loadharness.Endpoint{
			Address: *server, DC: *dc, APIID: *apiID, APIHash: *apiHash, RSAKeyPath: *rsaKey,
			Obfuscated: *obfuscated, PFS: *pfs, TempKeyTTL: *tempKeyTTL,
		},
		Accounts: *accounts, ExtraDevices: *extraDevices, Concurrency: *concurrency,
		PhonePrefix: *phonePrefix, Code: code, FirstNamePrefix: *firstName,
	}
	result, err := loadharness.Provision(ctx, cfg, func(event loadharness.ProvisionEvent) {
		status := "ok"
		if event.Resumed {
			status = "resumed"
		}
		if event.Err != nil {
			status = "error"
		}
		fmt.Fprintf(os.Stdout, "provision %d/%d session=%d account=%d device=%d status=%s\n",
			event.Completed, event.Total, event.Session.Index, event.Session.AccountIndex, event.Session.DeviceIndex, status)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "provisioned %d real MTProto sessions into %s\n", len(result.Sessions), *manifest)
	return nil
}

func runLoad(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	manifest := flags.String("manifest", filepath.FromSlash("data/loadtest/manifest.json"), "provisioned manifest")
	sessionKey := flags.String("session-key", filepath.FromSlash("data/loadtest/session.key"), "session encryption key")
	rsaOverride := flags.String("rsa-key", "", "optional RSA public/private PEM override")
	report := flags.String("report", filepath.FromSlash("data/loadtest/report.json"), "final JSON report")
	events := flags.String("events", filepath.FromSlash("data/loadtest/events.ndjson"), "periodic NDJSON evidence")
	fileFixture := flags.String("file-fixture", "", "reusable fixture JSON; empty stores beside manifest")
	serverMetrics := flags.String("server-metrics", "http://127.0.0.1:6060/metrics", "server metrics URL; empty disables")
	startOrder := flags.String("start-order", loadharness.StartupOrderShuffled, "session launch order: shuffled or account-index")
	startOrderSeed := flags.Int64("start-order-seed", 20260827, "deterministic shuffled launch seed")
	sessions := flags.Int("sessions", 0, "limit selected sessions; 0 uses all")
	duration := flags.Duration("duration", 30*time.Minute, "sustained load duration")
	recovery := flags.Duration("recovery", 7*time.Minute, "post-disconnect reclamation observation")
	ramp := flags.Duration("ramp", 2*time.Minute, "connection ramp duration")
	rpcInterval := flags.Duration("rpc-interval", 5*time.Second, "per-session background RPC interval")
	messageInterval := flags.Duration("message-interval", 30*time.Second, "per-primary-session message interval; negative disables")
	messageRate := flags.Float64("message-rate", 0, "aggregate fixed arrival rate in messages/second; use with message-interval=-1")
	messageQueue := flags.Int("message-queue", 8, "bounded pending sends per primary session for fixed-rate workload")
	deliverySettle := flags.Duration("delivery-settle", 10*time.Second, "maximum live-delivery settle time before final updates.getDifference reconciliation")
	fileInterval := flags.Duration("file-interval", time.Minute, "per-session upload.getFile interval")
	fileSize := flags.Int("file-size", 4<<20, "generated shared download fixture bytes; 0 disables")
	fileChunk := flags.Int("file-chunk", 1<<20, "upload.getFile bytes per request (max 1MiB)")
	setupTimeout := flags.Duration("setup-timeout", 90*time.Second, "maximum first-time file fixture setup duration")
	operationTimeout := flags.Duration("operation-timeout", 30*time.Second, "maximum duration of one workload RPC")
	sampleInterval := flags.Duration("sample-interval", 10*time.Second, "evidence and server scrape interval")
	offlineFraction := flags.Float64("offline-fraction", 0.20, "fraction disconnected during offline window; 0 disables")
	offlineAt := flags.Duration("offline-at", 10*time.Minute, "offline window start from run start")
	offlineFor := flags.Duration("offline-for", 2*time.Minute, "offline window duration")
	readyRatio := flags.Float64("min-ready-ratio", 0.98, "minimum peak ready ratio")
	expectRestart := flags.Bool("expect-server-restart", false, "allow classified connection loss but require all selected sessions to reconnect")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("run accepts no positional arguments")
	}
	result, err := loadharness.Run(ctx, loadharness.RunConfig{
		ManifestPath: *manifest, SessionKeyPath: *sessionKey, RSAKeyOverride: *rsaOverride,
		ReportPath: *report, EventsPath: *events, FileFixturePath: *fileFixture, ServerMetricsURL: *serverMetrics,
		StartOrder: *startOrder, StartOrderSeed: *startOrderSeed,
		SessionLimit: *sessions, Duration: *duration, RecoveryDuration: *recovery, RampDuration: *ramp,
		RPCInterval: *rpcInterval, MessageInterval: *messageInterval, MessageRate: *messageRate,
		MessageQueueDepth: *messageQueue, DeliverySettle: *deliverySettle, SampleInterval: *sampleInterval,
		FileInterval: *fileInterval, FileSizeBytes: *fileSize, FileChunkBytes: *fileChunk, SetupTimeout: *setupTimeout,
		OperationTimeout: *operationTimeout,
		OfflineFraction:  *offlineFraction, OfflineAt: *offlineAt, OfflineFor: *offlineFor,
		MinimumReadyRatio:   *readyRatio,
		ExpectServerRestart: *expectRestart,
	})
	if err != nil {
		return err
	}
	printSummary(result)
	if !result.Pass {
		return fmt.Errorf("load acceptance failed; see %s", *report)
	}
	return nil
}

func runSummarize(args []string) error {
	flags := flag.NewFlagSet("summarize", flag.ContinueOnError)
	path := flags.String("report", filepath.FromSlash("data/loadtest/report.json"), "JSON report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	var shape struct {
		BusinessReady *int `json:"business_ready"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return err
	}
	if shape.BusinessReady != nil {
		var report loadharness.StartupRunReport
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&report); err != nil {
			return err
		}
		printStartupSummary(&report)
		if !report.Pass {
			return errors.New("startup report did not pass")
		}
		return nil
	}
	var report loadharness.RunReport
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return err
	}
	printSummary(&report)
	if !report.Pass {
		return errors.New("report did not pass")
	}
	return nil
}

func printSummary(report *loadharness.RunReport) {
	fmt.Fprintf(os.Stdout, "pass=%v sessions=%d peak_ready=%d reconnects=%d disconnects=%d flood_waits=%d fatal_errors=%d scheduled=%d delivered=%d missing=%d\n",
		report.Pass, report.ExpectedSessions, report.PeakReadySessions, report.Reconnects, report.Disconnects,
		totalFloodWaits(report), report.WorkerFatalErrors, report.MessageScheduled, report.Delivery.Delivered, report.Delivery.Missing)
	for _, failure := range report.Failures {
		fmt.Fprintln(os.Stdout, "failure:", failure)
	}
}

func totalFloodWaits(report *loadharness.RunReport) uint64 {
	var total uint64
	for _, operation := range report.Operations {
		total += operation.FloodWaits
	}
	return total
}

func usageError() error {
	return errors.New("expected one of: keygen, provision, plan-dataset, seed, snapshot, mutate-offline, startup-run, run, summarize, help")
}

const usageText = `telesrv-load commands:
  keygen     generate an owner-only AES-256 session key
  provision  create accounts and encrypted sessions through real MTProto auth
  plan-dataset create an immutable real-data topology with stable RPC identities
  seed       materialize private dialogs, supergroups and messages via real RPCs
  snapshot   save paginated real dialogs and old account/channel PTS cursors
  mutate-offline create account/channel gaps while preserving the old cursors
  startup-run restore old cursors and measure dialogs/difference business readiness
  run        execute sustained real-client load, offline recovery and reclamation
  summarize  print the acceptance summary from a JSON report

Use "telesrv-load <command> -h" for command flags.`
