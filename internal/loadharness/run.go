package loadharness

import (
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/iamxvbaba/td/mtproto"
	"github.com/iamxvbaba/td/pool"
	tdrpc "github.com/iamxvbaba/td/rpc"
	"github.com/iamxvbaba/td/telegram"
	"github.com/iamxvbaba/td/tg"
)

const (
	workerConnecting int32 = iota
	workerReady
	workerDisconnected
	workerOffline
	workerStopped
)

type RunConfig struct {
	ManifestPath        string
	SessionKeyPath      string
	RSAKeyOverride      string
	ReportPath          string
	EventsPath          string
	FileFixturePath     string
	ServerMetricsURL    string
	StartOrder          string
	StartOrderSeed      int64
	SessionLimit        int
	Duration            time.Duration
	RecoveryDuration    time.Duration
	RampDuration        time.Duration
	RPCInterval         time.Duration
	MessageInterval     time.Duration
	MessageRate         float64
	MessageQueueDepth   int
	DeliverySettle      time.Duration
	FileInterval        time.Duration
	FileSizeBytes       int
	FileChunkBytes      int
	SetupTimeout        time.Duration
	OperationTimeout    time.Duration
	SampleInterval      time.Duration
	OfflineFraction     float64
	OfflineAt           time.Duration
	OfflineFor          time.Duration
	MinimumReadyRatio   float64
	ExpectServerRestart bool
}

const RunReportVersion = 7

func (c RunConfig) validate() error {
	if c.ManifestPath == "" || c.SessionKeyPath == "" || c.ReportPath == "" {
		return errors.New("manifest, session-key and report paths are required")
	}
	if c.Duration <= 0 || c.RecoveryDuration < 0 || c.RampDuration < 0 || c.RPCInterval <= 0 || c.OperationTimeout <= 0 || c.SampleInterval <= 0 {
		return errors.New("run durations and intervals are invalid")
	}
	if c.MessageRate < 0 || c.MessageRate > 100000 || c.MessageQueueDepth < 0 || c.MessageQueueDepth > 1024 || c.DeliverySettle < 0 {
		return errors.New("message rate, queue depth or delivery settle is invalid")
	}
	if c.MessageRate > 0 && c.MessageInterval > 0 {
		return errors.New("message-rate and message-interval workloads are mutually exclusive")
	}
	if c.MessageRate > 0 && (c.MessageQueueDepth == 0 || c.RampDuration >= c.Duration) {
		return errors.New("fixed-rate workload requires a queue depth and load duration beyond the connection ramp")
	}
	if c.FileSizeBytes < 0 || c.FileChunkBytes < 0 || c.FileChunkBytes > 1<<20 || c.FileSizeBytes > 64<<20 {
		return errors.New("file size must be <=64MiB and chunk size must be <=1MiB")
	}
	if c.FileSizeBytes > 0 && (c.FileInterval <= 0 || c.FileChunkBytes <= 0) {
		return errors.New("enabled file workload requires positive interval and chunk size")
	}
	if c.FileSizeBytes > 0 && c.SetupTimeout <= 0 {
		return errors.New("enabled file workload requires a positive setup timeout")
	}
	if c.OfflineFraction < 0 || c.OfflineFraction > 1 || c.MinimumReadyRatio <= 0 || c.MinimumReadyRatio > 1 {
		return errors.New("offline fraction and minimum ready ratio must be within [0,1]")
	}
	if c.OfflineFraction > 0 && (c.OfflineAt <= 0 || c.OfflineFor <= 0 || c.OfflineAt+c.OfflineFor >= c.Duration) {
		return errors.New("offline window must be positive and fit inside load duration")
	}
	if c.StartOrder != "" && c.StartOrder != StartupOrderShuffled && c.StartOrder != StartupOrderAccountIndex {
		return fmt.Errorf("unknown run start order %q", c.StartOrder)
	}
	return nil
}

type updateState struct {
	mu    sync.Mutex
	value tg.UpdatesState
	valid bool
}

func (s *updateState) load() (tg.UpdatesState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.valid
}

func (s *updateState) store(value tg.UpdatesState) {
	s.mu.Lock()
	s.value = value
	s.valid = true
	s.mu.Unlock()
}

type harnessCounters struct {
	connectionAttempts atomic.Uint64
	reconnects         atomic.Uint64
	disconnects        atomic.Uint64
	updates            atomic.Uint64
	fatalErrors        atomic.Uint64
	downloadBytes      atomic.Uint64
	messageScheduled   atomic.Uint64
	messageEnqueued    atomic.Uint64
	messageCompleted   atomic.Uint64
	messageQueueFull   atomic.Uint64
	messageNotReady    atomic.Uint64
}

var debugConnectionErrors atomic.Uint64
var debugOperationErrors atomic.Uint64

func debugConnectionError(sessionIndex int, err error) {
	if os.Getenv("TELESRV_LOAD_DEBUG_ERRORS") != "1" || err == nil || debugConnectionErrors.Add(1) > 20 {
		return
	}
	// Explicit opt-in diagnostics go only to stderr and are bounded. They are
	// never copied into the attachable report or event stream.
	fmt.Fprintf(os.Stderr, "load debug: session=%d error_type=%T error=%v\n", sessionIndex, err, err)
}

func debugOperationError(operation string, err error) {
	// Connection lifecycle errors have their own bounded diagnostic path. Do not
	// let an expected reconnect storm consume the operation-error budget that is
	// needed to diagnose actual RPC failures.
	if operation == "connection.dead" || os.Getenv("TELESRV_LOAD_DEBUG_ERRORS") != "1" || err == nil || debugOperationErrors.Add(1) > 20 {
		return
	}
	// Operation names are a code-owned finite vocabulary. Raw errors are
	// opt-in, stderr-only and bounded; reports/events retain only classifications.
	fmt.Fprintf(os.Stderr, "load debug: operation=%s error_type=%T error=%v\n", operation, err, err)
}

type downloadFixture struct {
	location *tg.InputDocumentFileLocation
	size     int
	chunk    int
}

type loadWorker struct {
	record           SessionRecord
	target           SessionRecord
	endpoint         Endpoint
	publicKey        *rsa.PublicKey
	storage          *EncryptedFileStorage
	metrics          *metricSet
	counters         *harnessCounters
	events           *eventWriter
	rpcInterval      time.Duration
	msgInterval      time.Duration
	fileInterval     time.Duration
	operationTimeout time.Duration
	fileFixture      *downloadFixture
	delivery         *deliveryTracker

	desired       atomic.Bool
	state         atomic.Int32
	everReady     atomic.Bool
	signal        chan struct{}
	lastUpdate    updateState
	deliveryState updateState
	messageSeq    atomic.Uint64
	sendQueue     chan struct{}
	reconcile     chan chan struct{}
}

func newLoadWorker(record, target SessionRecord, endpoint Endpoint, publicKey *rsa.PublicKey, storage *EncryptedFileStorage, metrics *metricSet, counters *harnessCounters, events *eventWriter, rpcInterval, messageInterval, fileInterval, operationTimeout time.Duration, fixture *downloadFixture, delivery *deliveryTracker, messageQueueDepth int) *loadWorker {
	w := &loadWorker{
		record: record, target: target, endpoint: endpoint, publicKey: publicKey, storage: storage,
		metrics: metrics, counters: counters, events: events, rpcInterval: rpcInterval,
		msgInterval: messageInterval, fileInterval: fileInterval, operationTimeout: operationTimeout, fileFixture: fixture,
		delivery: delivery, signal: make(chan struct{}, 1), sendQueue: make(chan struct{}, messageQueueDepth),
		reconcile: make(chan chan struct{}),
	}
	w.state.Store(workerStopped)
	return w
}

func (w *loadWorker) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, w.operationTimeout)
}

func (w *loadWorker) setOnline(online bool) {
	w.desired.Store(online)
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

func (w *loadWorker) supervise(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		if err := ctx.Err(); err != nil {
			w.state.Store(workerStopped)
			return
		}
		if !w.desired.Load() {
			w.state.Store(workerOffline)
			select {
			case <-ctx.Done():
				w.state.Store(workerStopped)
				return
			case <-w.signal:
				continue
			}
		}

		clientCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- w.runClient(clientCtx) }()
		for w.desired.Load() {
			select {
			case <-ctx.Done():
				cancel()
				<-done
				w.state.Store(workerStopped)
				return
			case <-w.signal:
				if !w.desired.Load() {
					cancel()
					<-done
					w.state.Store(workerOffline)
				}
			case err := <-done:
				cancel()
				if ctx.Err() != nil {
					w.state.Store(workerStopped)
					return
				}
				if !w.desired.Load() {
					w.state.Store(workerOffline)
					goto nextClient
				}
				w.counters.fatalErrors.Add(1)
				w.events.write(map[string]any{
					"type": "worker_error", "at": time.Now().UTC(), "session_index": w.record.Index,
					"class": classifyError(err),
				})
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				goto nextClient
			}
		}
		cancel()
		select {
		case <-done:
		default:
		}
	nextClient:
	}
}

func (w *loadWorker) runClient(ctx context.Context) error {
	reconnectSignal := make(chan struct{}, 1)
	var readySeen atomic.Bool
	client, err := newClient(w.endpoint, w.publicKey, w.storage, clientHooks{
		Update: telegram.UpdateHandlerFunc(func(_ context.Context, updates tg.UpdatesClass) error {
			w.counters.updates.Add(1)
			observeUpdatesClass(w.delivery, w.record.UserID, updates, deliveryLive)
			return nil
		}),
		ConnectionState: func(state telegram.ConnectionState) {
			switch state {
			case telegram.ConnectionStateConnecting:
				w.state.Store(workerConnecting)
				w.counters.connectionAttempts.Add(1)
				if w.everReady.Load() {
					w.counters.reconnects.Add(1)
				}
			case telegram.ConnectionStateReady:
				needsCatchUp := markClientReady(&w.everReady, &readySeen)
				w.state.Store(workerReady)
				if needsCatchUp {
					select {
					case reconnectSignal <- struct{}{}:
					default:
					}
				}
			case telegram.ConnectionStateDisconnected:
				w.state.Store(workerDisconnected)
				w.counters.disconnects.Add(1)
			}
		},
		Dead: func(err error) {
			debugConnectionError(w.record.Index, err)
			w.metrics.observe("connection.dead", time.Now(), err)
			w.events.write(map[string]any{
				"type": "connection_dead", "at": time.Now().UTC(), "session_index": w.record.Index,
				"class": classifyError(err), "reason": classifyErrorReason(err),
			})
		},
	})
	if err != nil {
		return err
	}
	return client.Run(ctx, func(ctx context.Context) error {
		statusStart := time.Now()
		operationCtx, cancelOperation := w.operationContext(ctx)
		status, err := client.Auth().Status(operationCtx)
		cancelOperation()
		w.metrics.observe("auth.status", statusStart, err)
		if err != nil {
			return err
		}
		if !status.Authorized || status.User == nil || status.User.ID != w.record.UserID {
			return errors.New("session is not authorized for its manifest user")
		}
		raw := tg.NewClient(client)
		if _, valid := w.lastUpdate.load(); valid {
			w.catchUp(ctx, raw)
		} else {
			w.refreshUpdateState(ctx, raw)
		}
		if _, valid := w.deliveryState.load(); !valid {
			w.refreshDeliveryState(ctx, raw)
		}

		rpcTicker := time.NewTicker(w.rpcInterval)
		defer rpcTicker.Stop()
		var messageTicker *time.Ticker
		var messageC <-chan time.Time
		if w.msgInterval > 0 && w.record.DeviceIndex == 0 && w.target.UserID > 0 {
			messageTicker = time.NewTicker(w.msgInterval)
			messageC = messageTicker.C
			defer messageTicker.Stop()
		}
		var fileTicker *time.Ticker
		var fileC <-chan time.Time
		if w.fileFixture != nil && w.fileInterval > 0 {
			fileTicker = time.NewTicker(w.fileInterval)
			fileC = fileTicker.C
			defer fileTicker.Stop()
		}
		cycle := 0
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-reconnectSignal:
				w.catchUp(ctx, raw)
			case <-rpcTicker.C:
				w.runRPC(ctx, client, raw, cycle)
				cycle++
			case <-messageC:
				w.sendMessage(ctx, raw)
			case <-w.sendQueue:
				w.sendMessage(ctx, raw)
			case done := <-w.reconcile:
				w.catchUpDelivery(ctx, raw)
				close(done)
			case <-fileC:
				w.downloadFileChunk(ctx, raw)
			}
		}
	})
}

// markClientReady distinguishes a transport reconnect inside one live gotd
// Client from the first Ready transition of a newly constructed Client. The
// client.Run callback already performs one cursor catch-up when it starts, so
// enqueueing a second catch-up for that first transition would duplicate every
// explicit offline->online getDifference request. Later Ready transitions do
// need the signal because the callback remains running across transport-level
// reconnects.
func markClientReady(everReady, readySeen *atomic.Bool) bool {
	firstForClient := !readySeen.Swap(true)
	wasReady := everReady.Swap(true)
	return wasReady && !firstForClient
}

func (w *loadWorker) runRPC(ctx context.Context, client *telegram.Client, raw *tg.Client, cycle int) {
	start := time.Now()
	operationCtx, cancel := w.operationContext(ctx)
	defer cancel()
	var err error
	switch cycle % 4 {
	case 0:
		err = client.Ping(operationCtx)
		w.metrics.observe("ping", start, err)
	case 1:
		var state *tg.UpdatesState
		state, err = raw.UpdatesGetState(operationCtx)
		if err == nil {
			w.lastUpdate.store(*state)
		}
		w.metrics.observe("updates.getState", start, err)
	case 2:
		_, err = raw.MessagesGetDialogs(operationCtx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 20})
		w.metrics.observe("messages.getDialogs", start, err)
	case 3:
		_, err = raw.HelpGetConfig(operationCtx)
		w.metrics.observe("help.getConfig", start, err)
	}
}

func (w *loadWorker) refreshUpdateState(ctx context.Context, raw *tg.Client) {
	start := time.Now()
	operationCtx, cancel := w.operationContext(ctx)
	state, err := raw.UpdatesGetState(operationCtx)
	cancel()
	w.metrics.observe("updates.getState", start, err)
	if err == nil {
		w.lastUpdate.store(*state)
		if _, valid := w.deliveryState.load(); !valid {
			w.deliveryState.store(*state)
		}
	}
}

func (w *loadWorker) refreshDeliveryState(ctx context.Context, raw *tg.Client) {
	start := time.Now()
	operationCtx, cancel := w.operationContext(ctx)
	state, err := raw.UpdatesGetState(operationCtx)
	cancel()
	w.metrics.observe("updates.getState.delivery", start, err)
	if err == nil {
		w.deliveryState.store(*state)
	}
}

func (w *loadWorker) catchUp(ctx context.Context, raw *tg.Client) {
	state, valid := w.lastUpdate.load()
	if !valid {
		w.refreshUpdateState(ctx, raw)
		return
	}
	for page := 0; page < 32; page++ {
		start := time.Now()
		operationCtx, cancel := w.operationContext(ctx)
		difference, err := raw.UpdatesGetDifference(operationCtx, &tg.UpdatesGetDifferenceRequest{Pts: state.Pts, Date: state.Date, Qts: state.Qts})
		cancel()
		w.metrics.observe("updates.getDifference", start, err)
		if err != nil {
			return
		}
		switch value := difference.(type) {
		case *tg.UpdatesDifferenceEmpty:
			state.Date, state.Seq = value.Date, value.Seq
			w.lastUpdate.store(state)
			return
		case *tg.UpdatesDifference:
			state = value.State
			w.counters.updates.Add(uint64(len(value.NewMessages) + len(value.NewEncryptedMessages) + len(value.OtherUpdates)))
			w.lastUpdate.store(state)
			return
		case *tg.UpdatesDifferenceSlice:
			state = value.IntermediateState
			w.counters.updates.Add(uint64(len(value.NewMessages) + len(value.NewEncryptedMessages) + len(value.OtherUpdates)))
			w.lastUpdate.store(state)
		case *tg.UpdatesDifferenceTooLong:
			state.Pts = value.Pts
			w.lastUpdate.store(state)
			return
		default:
			return
		}
	}
}

func (w *loadWorker) catchUpDelivery(ctx context.Context, raw *tg.Client) {
	state, valid := w.deliveryState.load()
	if !valid {
		w.refreshDeliveryState(ctx, raw)
		return
	}
	for page := 0; page < 256; page++ {
		start := time.Now()
		operationCtx, cancel := w.operationContext(ctx)
		difference, err := raw.UpdatesGetDifference(operationCtx, &tg.UpdatesGetDifferenceRequest{Pts: state.Pts, Date: state.Date, Qts: state.Qts})
		cancel()
		w.metrics.observe("updates.getDifference.delivery", start, err)
		if err != nil {
			return
		}
		switch value := difference.(type) {
		case *tg.UpdatesDifferenceEmpty:
			state.Date, state.Seq = value.Date, value.Seq
			w.deliveryState.store(state)
			return
		case *tg.UpdatesDifference:
			observeMessageClasses(w.delivery, w.record.UserID, value.NewMessages, deliveryDifference)
			observeUpdateClasses(w.delivery, w.record.UserID, value.OtherUpdates, deliveryDifference)
			state = value.State
			w.deliveryState.store(state)
			return
		case *tg.UpdatesDifferenceSlice:
			observeMessageClasses(w.delivery, w.record.UserID, value.NewMessages, deliveryDifference)
			observeUpdateClasses(w.delivery, w.record.UserID, value.OtherUpdates, deliveryDifference)
			state = value.IntermediateState
			w.deliveryState.store(state)
		case *tg.UpdatesDifferenceTooLong:
			state.Pts = value.Pts
			w.deliveryState.store(state)
			return
		default:
			return
		}
	}
}

func (w *loadWorker) sendMessage(ctx context.Context, raw *tg.Client) {
	defer w.counters.messageCompleted.Add(1)
	sequence := w.messageSeq.Add(1)
	var randomBytes [8]byte
	if _, err := cryptorand.Read(randomBytes[:]); err != nil {
		return
	}
	randomID := int64(binary.LittleEndian.Uint64(randomBytes[:]))
	if randomID == 0 {
		randomID = int64(sequence)
	}
	start := time.Now()
	marker := w.delivery.marker(w.record.Index, sequence)
	w.delivery.begin(marker, w.record.UserID, w.target.UserID, start)
	operationCtx, cancel := w.operationContext(ctx)
	_, err := raw.MessagesSendMessage(operationCtx, &tg.MessagesSendMessageRequest{
		Peer:    &tg.InputPeerUser{UserID: w.target.UserID, AccessHash: w.target.AccessHash},
		Message: marker, RandomID: randomID,
	})
	cancel()
	w.metrics.observe("messages.sendMessage", start, err)
	w.delivery.finish(marker, err == nil)
}

func (w *loadWorker) downloadFileChunk(ctx context.Context, raw *tg.Client) {
	fixture := w.fileFixture
	if fixture == nil || fixture.size <= 0 || fixture.chunk <= 0 {
		return
	}
	sequence := w.messageSeq.Add(1)
	offset := int64((int(sequence-1) * fixture.chunk) % fixture.size)
	limit := min(fixture.chunk, fixture.size-int(offset))
	start := time.Now()
	operationCtx, cancel := w.operationContext(ctx)
	result, err := raw.UploadGetFile(operationCtx, &tg.UploadGetFileRequest{
		Location: fixture.location, Offset: offset, Limit: limit,
	})
	cancel()
	if err == nil {
		file, ok := result.(*tg.UploadFile)
		switch {
		case !ok:
			err = fmt.Errorf("upload.getFile returned %T", result)
		case len(file.Bytes) != limit:
			err = fmt.Errorf("upload.getFile bytes=%d want=%d", len(file.Bytes), limit)
		case !validFixtureBytes(file.Bytes, offset):
			err = errors.New("upload.getFile payload mismatch")
		default:
			w.counters.downloadBytes.Add(uint64(len(file.Bytes)))
		}
	}
	w.metrics.observe("upload.getFile", start, err)
}

func prepareDownloadFixture(ctx context.Context, cfg RunConfig, manifest *Manifest, record SessionRecord, key [32]byte, publicKey *rsa.PublicKey, metrics *metricSet) (*downloadFixture, error) {
	data := make([]byte, cfg.FileSizeBytes)
	for i := range data {
		data[i] = fixtureByte(int64(i))
	}
	fileID, err := randomNonZeroInt64()
	if err != nil {
		return nil, err
	}
	storage := &EncryptedFileStorage{Path: resolveSessionPath(cfg.ManifestPath, record), Key: key}
	client, err := newClient(manifest.Endpoint, publicKey, storage, clientHooks{})
	if err != nil {
		return nil, err
	}
	var location *tg.InputDocumentFileLocation
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized || status.User == nil || status.User.ID != record.UserID {
			return errors.New("fixture session is not authorized")
		}
		raw := tg.NewClient(client)
		const partSize = 512 << 10
		parts := (len(data) + partSize - 1) / partSize
		big := len(data) > 10<<20
		for part := 0; part < parts; part++ {
			startOffset := part * partSize
			endOffset := min(len(data), startOffset+partSize)
			start := time.Now()
			var saved bool
			if big {
				saved, err = raw.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{
					FileID: fileID, FilePart: part, FileTotalParts: parts, Bytes: data[startOffset:endOffset],
				})
			} else {
				saved, err = raw.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{
					FileID: fileID, FilePart: part, Bytes: data[startOffset:endOffset],
				})
			}
			metrics.observe("upload.saveFilePart", start, err)
			if err != nil {
				return err
			}
			if !saved {
				return fmt.Errorf("upload part %d was not saved", part)
			}
		}
		var file tg.InputFileClass
		if big {
			file = &tg.InputFileBig{ID: fileID, Parts: parts, Name: "telesrv-load.bin"}
		} else {
			digest := md5.Sum(data)
			file = &tg.InputFile{ID: fileID, Parts: parts, Name: "telesrv-load.bin", MD5Checksum: hex.EncodeToString(digest[:])}
		}
		start := time.Now()
		media, err := raw.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{
			Peer: &tg.InputPeerEmpty{},
			Media: &tg.InputMediaUploadedDocument{
				File: file, MimeType: "application/octet-stream",
				Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "telesrv-load.bin"}},
			},
		})
		metrics.observe("messages.uploadMedia", start, err)
		if err != nil {
			return err
		}
		documentMedia, ok := media.(*tg.MessageMediaDocument)
		if !ok {
			return fmt.Errorf("messages.uploadMedia returned %T", media)
		}
		documentClass, ok := documentMedia.GetDocument()
		if !ok {
			return errors.New("messages.uploadMedia omitted document")
		}
		document, ok := documentClass.(*tg.Document)
		if !ok {
			return fmt.Errorf("uploaded document is %T", documentClass)
		}
		location = &tg.InputDocumentFileLocation{
			ID: document.ID, AccessHash: document.AccessHash,
			FileReference: append([]byte(nil), document.FileReference...),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if location == nil {
		return nil, errors.New("file fixture completed without a location")
	}
	return &downloadFixture{location: location, size: len(data), chunk: cfg.FileChunkBytes}, nil
}

func loadOrCreateDownloadFixture(ctx context.Context, cfg RunConfig, manifest *Manifest, record SessionRecord, key [32]byte, publicKey *rsa.PublicKey, metrics *metricSet) (*downloadFixture, error) {
	path := resolveFileFixturePath(cfg.ManifestPath, cfg.FileFixturePath)
	fixture, err := loadPersistedFileFixture(path, manifest.Endpoint, cfg.FileSizeBytes, cfg.FileChunkBytes)
	if err == nil {
		return fixture, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load file fixture %q: %w", path, err)
	}
	setupCtx, cancel := context.WithTimeout(ctx, cfg.SetupTimeout)
	defer cancel()
	fixture, err = prepareDownloadFixture(setupCtx, cfg, manifest, record, key, publicKey, metrics)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(setupCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("create file fixture timed out after %s", cfg.SetupTimeout)
		}
		return nil, err
	}
	if err := writePersistedFileFixture(path, manifest.Endpoint, fixture); err != nil {
		return nil, fmt.Errorf("persist file fixture: %w", err)
	}
	return fixture, nil
}

func randomNonZeroInt64() (int64, error) {
	var data [8]byte
	if _, err := cryptorand.Read(data[:]); err != nil {
		return 0, err
	}
	value := int64(binary.LittleEndian.Uint64(data[:]) & math.MaxInt64)
	if value == 0 {
		value = 1
	}
	return value, nil
}

func fixtureByte(offset int64) byte {
	return byte((uint64(offset)*31 + 17) % 251)
}

func validFixtureBytes(data []byte, offset int64) bool {
	for i, value := range data {
		if value != fixtureByte(offset+int64(i)) {
			return false
		}
	}
	return true
}

// Run executes a bounded real-client load and keeps scraping after all workers
// stop so logical-session/outbox reclamation can be proven rather than assumed.
func Run(ctx context.Context, cfg RunConfig) (*RunReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		return nil, err
	}
	key, err := LoadSessionKey(cfg.SessionKeyPath)
	if err != nil {
		return nil, err
	}
	publicKey, err := loadManifestPublicKey(cfg.ManifestPath, manifest.Endpoint, cfg.RSAKeyOverride)
	if err != nil {
		return nil, err
	}
	records := manifest.Sessions
	if cfg.SessionLimit > 0 && cfg.SessionLimit < len(records) {
		records = records[:cfg.SessionLimit]
	}
	if len(records) == 0 {
		return nil, errors.New("manifest has no selected sessions")
	}
	if err := validateProcessCapacity(len(records)); err != nil {
		return nil, err
	}
	events, err := newEventWriter(cfg.EventsPath)
	if err != nil {
		return nil, err
	}
	defer events.close()
	metrics := newMetricSet("auth.status", "connection.dead", "ping", "updates.getState", "updates.getDifference", "updates.getState.delivery", "updates.getDifference.delivery", "messages.getDialogs", "help.getConfig", "messages.sendMessage", "upload.saveFilePart", "messages.uploadMedia", "upload.getFile")
	counters := &harnessCounters{}
	runID, err := newLoadRunID()
	if err != nil {
		return nil, fmt.Errorf("create load run id: %w", err)
	}
	delivery := newDeliveryTracker(runID)
	serverMetrics := newServerMetricsClient(cfg.ServerMetricsURL)
	var baselineServerMetrics map[string]float64
	if serverMetrics != nil {
		if sample, scrapeErr := serverMetrics.scrape(ctx); scrapeErr == nil {
			baselineServerMetrics = sample
			events.write(map[string]any{"type": "server_baseline", "at": time.Now().UTC(), "server_metrics": sample})
		} else {
			events.write(map[string]any{"type": "server_baseline_error", "at": time.Now().UTC(), "class": classifyError(scrapeErr)})
		}
	}
	var fixture *downloadFixture
	if cfg.FileSizeBytes > 0 {
		fixture, err = loadOrCreateDownloadFixture(ctx, cfg, manifest, records[0], key, publicKey, metrics)
		if err != nil {
			return nil, fmt.Errorf("prepare file fixture: %w", err)
		}
	}
	targets := primaryTargets(records)
	workers := make([]*loadWorker, 0, len(records))
	for _, record := range records {
		target := targets[(record.AccountIndex+1)%len(targets)]
		workers = append(workers, newLoadWorker(
			record, target, manifest.Endpoint, publicKey,
			&EncryptedFileStorage{Path: resolveSessionPath(cfg.ManifestPath, record), Key: key},
			metrics, counters, events, cfg.RPCInterval, cfg.MessageInterval, cfg.FileInterval, cfg.OperationTimeout, fixture,
			delivery, cfg.MessageQueueDepth,
		))
	}
	messageWorkers := primaryWorkers(workers)

	startedAt := time.Now().UTC()
	loadCtx, stopLoad := context.WithCancel(ctx)
	var workerWG sync.WaitGroup
	for _, worker := range workers {
		workerWG.Add(1)
		go worker.supervise(loadCtx, &workerWG)
	}
	startOrder := cfg.StartOrder
	if startOrder == "" {
		startOrder = StartupOrderAccountIndex
	}
	startOrderSeed := cfg.StartOrderSeed
	if startOrderSeed == 0 {
		startOrderSeed = 20260827
	}
	launchOrder := startupAccountOrder(len(workers), startOrder, startOrderSeed)
	for position, workerIndex := range launchOrder {
		worker := workers[workerIndex]
		delay := time.Duration(0)
		if len(workers) > 1 {
			delay = time.Duration(position) * cfg.RampDuration / time.Duration(len(workers)-1)
		}
		go func(w *loadWorker, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-loadCtx.Done():
			case <-timer.C:
				w.setOnline(true)
			}
		}(worker, delay)
	}

	if cfg.OfflineFraction > 0 {
		go runOfflineWindow(loadCtx, workers, cfg.OfflineFraction, cfg.OfflineAt, cfg.OfflineFor, events)
	}
	messageCtx, stopMessages := context.WithCancel(loadCtx)
	defer stopMessages()
	var messageWG sync.WaitGroup
	if cfg.MessageRate > 0 {
		messageWG.Add(1)
		go runFixedMessageSchedule(messageCtx, &messageWG, cfg.RampDuration, cfg.MessageRate, messageWorkers, counters, events)
	}
	loadTimer := time.NewTimer(cfg.Duration)
	sampleTicker := time.NewTicker(cfg.SampleInterval)
	peakReady := 0
	steadySamples := 0
	steadyReadySum := 0
	steadyReadyMinimum := len(workers)
	var finalServerMetrics map[string]float64
	for {
		select {
		case <-ctx.Done():
			stopLoad()
			workerWG.Wait()
			return nil, ctx.Err()
		case <-sampleTicker.C:
			ready := countWorkerState(workers, workerReady)
			peakReady = max(peakReady, ready)
			elapsed := time.Since(startedAt)
			outsideOffline := cfg.OfflineFraction == 0 || elapsed < cfg.OfflineAt || elapsed > cfg.OfflineAt+cfg.OfflineFor+30*time.Second
			if elapsed >= cfg.RampDuration+30*time.Second && outsideOffline {
				steadySamples++
				steadyReadySum += ready
				steadyReadyMinimum = min(steadyReadyMinimum, ready)
			}
			finalServerMetrics = writeSample(ctx, events, "load", workers, metrics, counters, serverMetrics)
		case <-loadTimer.C:
			goto loadFinished
		}
	}

loadFinished:
	sampleTicker.Stop()
	stopMessages()
	messageWG.Wait()
	loadEndedAt := time.Now().UTC()
	if cfg.MessageRate > 0 {
		drainCtx, cancelDrain := context.WithTimeout(ctx, cfg.OperationTimeout+time.Duration(cfg.MessageQueueDepth)*cfg.OperationTimeout)
		waitMessageDrain(drainCtx, counters)
		cancelDrain()
	}
	if cfg.DeliverySettle > 0 && delivery.report().Expected > delivery.report().Delivered {
		settleTimer := time.NewTimer(cfg.DeliverySettle)
		settleTicker := time.NewTicker(min(cfg.SampleInterval, time.Second))
	settling:
		for {
			select {
			case <-ctx.Done():
				settleTimer.Stop()
				settleTicker.Stop()
				stopLoad()
				workerWG.Wait()
				return nil, ctx.Err()
			case <-settleTicker.C:
				if current := delivery.report(); current.Missing == 0 {
					settleTimer.Stop()
					settleTicker.Stop()
					break settling
				}
			case <-settleTimer.C:
				settleTicker.Stop()
				break settling
			}
		}
	}
	if delivery.report().Missing > 0 {
		reconcileCtx, cancelReconcile := context.WithTimeout(ctx, cfg.OperationTimeout*2)
		reconcileDeliveries(reconcileCtx, workers)
		cancelReconcile()
	}
	// Take the authoritative business-work cutoff before canceling clients.
	// Coordinated teardown can cancel an RPC already in flight; both server
	// outcome counters and client operation counters must therefore stop before
	// that cancellation begins. FinalServerMetrics remains the post-recovery
	// resource-reclamation snapshot.
	var workloadEndServerMetrics map[string]float64
	if serverMetrics != nil {
		if sample, scrapeErr := serverMetrics.scrape(ctx); scrapeErr == nil {
			workloadEndServerMetrics = sample
			finalServerMetrics = sample
			events.write(map[string]any{"type": "server_workload_end", "at": time.Now().UTC(), "server_metrics": sample})
		} else {
			events.write(map[string]any{"type": "server_workload_end_error", "at": time.Now().UTC(), "class": classifyError(scrapeErr)})
		}
	}
	workloadEndOperations := metrics.freeze()
	finalReady := countWorkerState(workers, workerReady)
	stopLoad()
	workerWG.Wait()
	if finalReady > peakReady {
		peakReady = finalReady
	}

	if cfg.RecoveryDuration > 0 {
		recoveryDeadline := time.NewTimer(cfg.RecoveryDuration)
		recoveryTicker := time.NewTicker(cfg.SampleInterval)
		for {
			select {
			case <-ctx.Done():
				recoveryTicker.Stop()
				recoveryDeadline.Stop()
				return nil, ctx.Err()
			case <-recoveryTicker.C:
				finalServerMetrics = writeSample(ctx, events, "recovery", workers, metrics, counters, serverMetrics)
			case <-recoveryDeadline.C:
				recoveryTicker.Stop()
				goto recoveryFinished
			}
		}
	}

recoveryFinished:
	if serverMetrics != nil {
		if sample, scrapeErr := serverMetrics.scrape(ctx); scrapeErr == nil {
			finalServerMetrics = sample
		} else {
			finalServerMetrics = nil
		}
	}
	steadyRatio := float64(0)
	if steadySamples > 0 {
		steadyRatio = float64(steadyReadySum) / float64(steadySamples*len(workers))
	}
	report := &RunReport{
		Version: RunReportVersion, StartedAt: startedAt, LoadEndedAt: loadEndedAt, FinishedAt: time.Now().UTC(),
		StartOrder: startOrder, StartOrderSeed: startOrderSeed,
		RequestedDuration: cfg.Duration.String(), RecoveryDuration: cfg.RecoveryDuration.String(),
		ExpectedSessions: len(workers), PeakReadySessions: peakReady, FinalReadySessions: finalReady,
		ConnectionAttempts: counters.connectionAttempts.Load(), Reconnects: counters.reconnects.Load(),
		Disconnects: counters.disconnects.Load(), UpdatesReceived: counters.updates.Load(), DownloadedBytes: counters.downloadBytes.Load(),
		WorkerFatalErrors: counters.fatalErrors.Load(), Operations: workloadEndOperations,
		BaselineServerMetrics: baselineServerMetrics, WorkloadEndServerMetrics: workloadEndServerMetrics, FinalServerMetrics: finalServerMetrics,
		ServerMetricsScrapes: serverMetrics.successes(), ServerMetricsErrors: serverMetrics.failures(),
		SteadySamples: steadySamples, SteadyReadyRatio: steadyRatio, MinSteadyReadySessions: steadyReadyMinimum,
		MessageRatePerSecond: cfg.MessageRate, MessageScheduled: counters.messageScheduled.Load(),
		MessageEnqueued: counters.messageEnqueued.Load(), MessageCompleted: counters.messageCompleted.Load(), MessageQueueFull: counters.messageQueueFull.Load(),
		MessageNotReady: counters.messageNotReady.Load(), Delivery: delivery.report(),
	}
	report.ResponseBytes = startupResponseBytes(baselineServerMetrics, workloadEndServerMetrics)
	report.RPCDeliveryOutcomes = startupRPCDeliveryOutcomes(baselineServerMetrics, workloadEndServerMetrics)
	report.DatabaseWork = startupDatabaseWork(baselineServerMetrics, workloadEndServerMetrics)
	report.EventsWritten, report.EventsDropped = events.counts()
	evaluateReport(report, cfg)
	if err := WriteReport(cfg.ReportPath, report); err != nil {
		return nil, err
	}
	return report, nil
}

func primaryTargets(records []SessionRecord) []SessionRecord {
	byAccount := make(map[int]SessionRecord)
	for _, record := range records {
		if existing, ok := byAccount[record.AccountIndex]; !ok || record.DeviceIndex < existing.DeviceIndex {
			byAccount[record.AccountIndex] = record
		}
	}
	maxAccount := -1
	for account := range byAccount {
		maxAccount = max(maxAccount, account)
	}
	targets := make([]SessionRecord, maxAccount+1)
	for account, record := range byAccount {
		targets[account] = record
	}
	return targets
}

func newLoadRunID() (string, error) {
	var value [8]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func primaryWorkers(workers []*loadWorker) []*loadWorker {
	primary := make([]*loadWorker, 0, len(workers))
	for _, worker := range workers {
		if worker.record.DeviceIndex == 0 && worker.target.UserID > 0 {
			primary = append(primary, worker)
		}
	}
	return primary
}

func runFixedMessageSchedule(ctx context.Context, wg *sync.WaitGroup, startDelay time.Duration, rate float64, workers []*loadWorker, counters *harnessCounters, events *eventWriter) {
	defer wg.Done()
	if rate <= 0 || len(workers) == 0 {
		return
	}
	startTimer := time.NewTimer(startDelay)
	defer startTimer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-startTimer.C:
	}
	readyTicker := time.NewTicker(10 * time.Millisecond)
	for countWorkerState(workers, workerReady) != len(workers) {
		select {
		case <-ctx.Done():
			readyTicker.Stop()
			return
		case <-readyTicker.C:
		}
	}
	readyTicker.Stop()
	interval := time.Duration(float64(time.Second) / rate)
	if interval < time.Microsecond {
		interval = time.Microsecond
	}
	events.write(map[string]any{"type": "fixed_message_rate_start", "at": time.Now().UTC(), "rate_per_second": rate, "senders": len(workers)})
	next := time.Now()
	workerIndex := 0
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			events.write(map[string]any{
				"type": "fixed_message_rate_stop", "at": time.Now().UTC(),
				"scheduled": counters.messageScheduled.Load(), "enqueued": counters.messageEnqueued.Load(),
				"queue_full": counters.messageQueueFull.Load(), "not_ready": counters.messageNotReady.Load(),
			})
			return
		case <-timer.C:
			worker := workers[workerIndex]
			workerIndex = (workerIndex + 1) % len(workers)
			counters.messageScheduled.Add(1)
			if worker.state.Load() != workerReady {
				counters.messageNotReady.Add(1)
			} else {
				select {
				case worker.sendQueue <- struct{}{}:
					counters.messageEnqueued.Add(1)
				default:
					counters.messageQueueFull.Add(1)
				}
			}
			next = next.Add(interval)
			timer.Reset(max(time.Until(next), time.Duration(0)))
		}
	}
}

func reconcileDeliveries(ctx context.Context, workers []*loadWorker) {
	waits := make([]chan struct{}, 0, len(workers))
	for _, worker := range workers {
		if worker.state.Load() != workerReady {
			continue
		}
		done := make(chan struct{})
		select {
		case <-ctx.Done():
			return
		case worker.reconcile <- done:
			waits = append(waits, done)
		}
	}
	for _, done := range waits {
		select {
		case <-ctx.Done():
			return
		case <-done:
		}
	}
}

func waitMessageDrain(ctx context.Context, counters *harnessCounters) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for counters.messageCompleted.Load() < counters.messageEnqueued.Load() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func minimumOpenFiles(sessions int) int {
	if sessions < 0 {
		sessions = 0
	}
	// gotd may transiently hold primary, PFS and replacement sockets together;
	// retain fixed room for the process, resolver, event/report files and scrapes.
	return 256 + sessions*6
}

func runOfflineWindow(ctx context.Context, workers []*loadWorker, fraction float64, at, duration time.Duration, events *eventWriter) {
	timer := time.NewTimer(at)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	count := int(math.Ceil(float64(len(workers)) * fraction))
	selected := make([]*loadWorker, 0, count)
	for i, worker := range workers {
		if i%len(workers) < count {
			worker.setOnline(false)
			selected = append(selected, worker)
		}
	}
	events.write(map[string]any{"type": "offline_start", "at": time.Now().UTC(), "sessions": len(selected)})
	timer.Reset(duration)
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	for _, worker := range selected {
		worker.setOnline(true)
	}
	events.write(map[string]any{"type": "offline_end", "at": time.Now().UTC(), "sessions": len(selected)})
}

func countWorkerState(workers []*loadWorker, state int32) int {
	count := 0
	for _, worker := range workers {
		if worker.state.Load() == state {
			count++
		}
	}
	return count
}

func writeSample(ctx context.Context, events *eventWriter, phase string, workers []*loadWorker, metrics *metricSet, counters *harnessCounters, server *serverMetricsClient) map[string]float64 {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	serverValues, scrapeErr := server.scrape(ctx)
	value := map[string]any{
		"type": "sample", "phase": phase, "at": time.Now().UTC(),
		"workers": map[string]int{
			"connecting": countWorkerState(workers, workerConnecting), "ready": countWorkerState(workers, workerReady),
			"disconnected": countWorkerState(workers, workerDisconnected), "offline": countWorkerState(workers, workerOffline),
			"stopped": countWorkerState(workers, workerStopped),
		},
		"client_runtime": map[string]uint64{"goroutines": uint64(runtime.NumGoroutine()), "heap_alloc_bytes": mem.HeapAlloc, "sys_bytes": mem.Sys},
		"connections": map[string]uint64{
			"attempts": counters.connectionAttempts.Load(), "reconnects": counters.reconnects.Load(),
			"disconnects": counters.disconnects.Load(), "fatal_errors": counters.fatalErrors.Load(),
			"downloaded_bytes": counters.downloadBytes.Load(),
		},
		"operations": metrics.report(), "server_metrics": serverValues,
	}
	if scrapeErr != nil {
		value["server_metrics_error"] = classifyError(scrapeErr)
	}
	events.write(value)
	return serverValues
}

func evaluateReport(report *RunReport, cfg RunConfig) {
	requiredReady := int(math.Ceil(float64(report.ExpectedSessions) * cfg.MinimumReadyRatio))
	if report.PeakReadySessions < requiredReady {
		report.Failures = append(report.Failures, fmt.Sprintf("peak ready sessions %d below required %d", report.PeakReadySessions, requiredReady))
	}
	if report.SteadySamples == 0 {
		report.Failures = append(report.Failures, "no post-ramp steady-state samples were collected")
	} else if report.SteadyReadyRatio < cfg.MinimumReadyRatio {
		report.Failures = append(report.Failures, fmt.Sprintf("steady ready ratio %.4f below required %.4f", report.SteadyReadyRatio, cfg.MinimumReadyRatio))
	}
	if report.WorkerFatalErrors > 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("worker fatal errors: %d", report.WorkerFatalErrors))
	}
	if cfg.MessageRate > 0 {
		if report.MessageScheduled == 0 {
			report.Failures = append(report.Failures, "fixed-rate message scheduler produced no arrivals")
		}
		if report.MessageNotReady > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("fixed-rate arrivals rejected because sender was not ready: %d", report.MessageNotReady))
		}
		if report.MessageQueueFull > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("fixed-rate arrivals rejected by bounded sender queues: %d", report.MessageQueueFull))
		}
		if report.MessageEnqueued != report.MessageScheduled {
			report.Failures = append(report.Failures, fmt.Sprintf("fixed-rate scheduler enqueued %d of %d arrivals", report.MessageEnqueued, report.MessageScheduled))
		}
		if report.MessageCompleted != report.MessageEnqueued {
			report.Failures = append(report.Failures, fmt.Sprintf("message workers completed %d of %d enqueued sends", report.MessageCompleted, report.MessageEnqueued))
		}
		sendOperation := report.Operations["messages.sendMessage"]
		if sendOperation.Count != report.MessageCompleted {
			report.Failures = append(report.Failures, fmt.Sprintf("messages.sendMessage recorded %d of %d completed jobs", sendOperation.Count, report.MessageCompleted))
		}
		successfulSends := sendOperation.Count - min(sendOperation.Count, sendOperation.Errors+sendOperation.Canceled)
		if report.Delivery.Expected != successfulSends {
			report.Failures = append(report.Failures, fmt.Sprintf("delivery tracker committed %d of %d successful send RPCs", report.Delivery.Expected, successfulSends))
		}
		if report.Delivery.Missing > 0 || report.Delivery.Delivered != report.Delivery.Expected {
			report.Failures = append(report.Failures, fmt.Sprintf("recipient delivery incomplete: delivered %d of %d, missing %d", report.Delivery.Delivered, report.Delivery.Expected, report.Delivery.Missing))
		}
		if cfg.OfflineFraction == 0 && report.Delivery.DifferenceRecovered > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("online recipients recovered %d messages only through updates.getDifference", report.Delivery.DifferenceRecovered))
		}
		if report.Delivery.DuplicateObservations > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("recipient observed %d duplicate message updates", report.Delivery.DuplicateObservations))
		}
		if report.Delivery.WrongAccountObserved > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("load markers appeared on %d wrong recipient accounts", report.Delivery.WrongAccountObserved))
		}
		if report.Delivery.UnmatchedMarkers > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("observed %d load markers without a successful send RPC", report.Delivery.UnmatchedMarkers))
		}
	}
	for name, operation := range report.Operations {
		if operation.FloodWaits > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s returned FLOOD_WAIT %d times", name, operation.FloodWaits))
		}
		unexpectedErrors := operation.Errors
		if cfg.ExpectServerRestart {
			unexpectedErrors -= min(unexpectedErrors, operation.ConnectionErrors)
		}
		if unexpectedErrors > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s returned %d unexpected non-cancel errors", name, unexpectedErrors))
		}
	}
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
			if count := outcomes[outcome]; outcome != "ok" && count > 0 {
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
		if errors := report.DatabaseWork[method].Errors; errors > 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s database errors: %d", method, errors))
		}
	}
	if cfg.ExpectServerRestart && report.Reconnects < uint64(requiredReady) {
		report.Failures = append(report.Failures, fmt.Sprintf("server restart expected at least %d reconnect attempts, observed %d", requiredReady, report.Reconnects))
	}
	if report.FinalServerMetrics != nil && report.BaselineServerMetrics != nil && cfg.RecoveryDuration > 0 {
		checks := []string{
			"telesrv_mtproto_raw_connections", "telesrv_mtproto_logical_sessions",
			"telesrv_mtproto_logical_outbox_bytes", "telesrv_mtproto_pending_push_bytes",
			"telesrv_mtproto_outbound_tracked_bytes", "telesrv_mtproto_rpc_execution_owners",
			"telesrv_mtproto_rpc_execution_reserved_entries", "telesrv_mtproto_rpc_execution_receipts",
			"telesrv_mtproto_rpc_execution_receipt_budget_bytes", "telesrv_mtproto_rpc_execution_subscribers",
		}
		for _, name := range checks {
			baseline := metricValue(report.BaselineServerMetrics, name)
			final := metricValue(report.FinalServerMetrics, name)
			if final > baseline {
				report.Failures = append(report.Failures, fmt.Sprintf("server retained %.0f above baseline %.0f in %s after recovery", final-baseline, baseline, name))
			}
		}
	}
	if strings.TrimSpace(cfg.ServerMetricsURL) != "" && report.ServerMetricsScrapes == 0 {
		report.Failures = append(report.Failures, "server metrics endpoint produced no successful scrapes")
	}
	if strings.TrimSpace(cfg.ServerMetricsURL) != "" && report.FinalServerMetrics == nil {
		report.Failures = append(report.Failures, "final post-recovery server metrics scrape failed")
	}
	if strings.TrimSpace(cfg.ServerMetricsURL) != "" && report.WorkloadEndServerMetrics == nil {
		report.Failures = append(report.Failures, "pre-teardown workload-end server metrics scrape failed")
	}
	if strings.TrimSpace(cfg.ServerMetricsURL) != "" && report.BaselineServerMetrics == nil {
		report.Failures = append(report.Failures, "pre-load server metrics baseline scrape failed")
	}
	report.Pass = len(report.Failures) == 0
}

func metricValue(values map[string]float64, name string) float64 {
	// The scraper always stores an aggregate family value in the bare key and
	// may additionally retain bounded state/method label breakdowns. Prefer that
	// aggregate; summing both would double-count every labeled family in resource
	// recovery checks (for example retained/offline logical sessions).
	if value, ok := values[name]; ok {
		return value
	}
	var total float64
	for key, value := range values {
		if strings.HasPrefix(key, name+"{") {
			total += value
		}
	}
	return total
}

func classifyError(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, mtproto.ErrPFSReconnectRequired) || errors.Is(err, mtproto.ErrPFSDropKeysRequired) || errors.Is(err, mtproto.ErrTransportNotReady) {
		return "connection"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, pool.ErrConnDead) || errors.Is(err, tdrpc.ErrEngineClosed) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EPIPE) {
		return "connection"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "timeout"
		}
		return "connection"
	}
	message := strings.ToUpper(err.Error())
	switch {
	case strings.Contains(message, "FLOOD_WAIT"):
		return "flood_wait"
	case strings.Contains(message, "ENCRYPTED_MESSAGE_INVALID"):
		return "encrypted_message_invalid"
	case strings.Contains(message, "AUTH_KEY"):
		return "auth_key"
	case strings.Contains(message, "CONNECTION"), strings.Contains(message, "CONNECT"), strings.Contains(message, "EOF"),
		strings.Contains(message, "ENGINE WAS CLOSED"), strings.Contains(message, "BROKEN PIPE"),
		strings.Contains(message, "CLOSED NETWORK"), strings.Contains(message, "NO ROUTE TO HOST"),
		strings.Contains(message, "NETWORK IS UNREACHABLE"):
		return "connection"
	default:
		return "error"
	}
}

// classifyErrorReason intentionally returns a finite vocabulary. It preserves
// enough transport/PFS evidence to diagnose a failed load without persisting
// raw error strings, addresses, auth-key IDs or request payloads.
func classifyErrorReason(err error) string {
	if err == nil {
		return "ok"
	}
	message := strings.ToUpper(err.Error())
	switch {
	case errors.Is(err, mtproto.ErrPFSDropKeysRequired):
		return "pfs_drop_keys"
	case errors.Is(err, mtproto.ErrPFSReconnectRequired):
		return "pfs_reconnect"
	case errors.Is(err, mtproto.ErrTransportNotReady):
		return "transport_not_ready"
	case strings.Contains(message, "TOO MANY OPEN FILES"):
		return "file_descriptor_limit"
	case strings.Contains(message, "PFS RECONNECT"):
		return "pfs_reconnect"
	case strings.Contains(message, "AUTH KEY NOT FOUND"), strings.Contains(message, "AUTH_KEY_NOT_FOUND"), strings.Contains(message, "PROTOCOL ERROR 404"):
		return "auth_key_not_found"
	case strings.Contains(message, "ENCRYPTED_MESSAGE_INVALID"):
		return "encrypted_message_invalid"
	case strings.Contains(message, "FINGERPRINT"):
		return "rsa_fingerprint"
	case strings.Contains(message, "CONNECTION REFUSED"):
		return "connection_refused"
	case strings.Contains(message, "CONNECTION RESET"):
		return "connection_reset"
	case strings.Contains(message, "NETWORK IS UNREACHABLE"), strings.Contains(message, "NO ROUTE TO HOST"):
		return "network_unreachable"
	case strings.Contains(message, "NO SUCH HOST"):
		return "dns"
	case strings.Contains(message, "BROKEN PIPE"):
		return "broken_pipe"
	case strings.Contains(message, "ENDED BEFORE BUSINESS READINESS"):
		return "business_readiness_incomplete"
	case strings.Contains(message, "EOF"):
		return "eof"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return classifyError(err)
	}
}
