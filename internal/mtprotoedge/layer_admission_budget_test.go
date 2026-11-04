package mtprotoedge

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owpengram/owpengram-td/bin"
	"github.com/owpengram/owpengram-td/clock"
	"github.com/owpengram/owpengram-td/proto"
	"github.com/owpengram/owpengram-td/tg"
	"github.com/owpengram/owpengram-td/tgerr"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"

	"github.com/owpengram/owpengram-td/tlprofile"
	appfiles "telesrv/internal/app/files"
	"telesrv/internal/rpc"
)

func TestLayerRPCAdmissionMaterializationConstants(t *testing.T) {
	constructors := make([]reflect.Type, 0, len(tg.TypesConstructorMap()))
	for _, constructor := range tg.TypesConstructorMap() {
		if typ := reflect.TypeOf(constructor()); typ != nil {
			constructors = append(constructors, typ)
		}
	}
	seen := make(map[reflect.Type]struct{})
	var (
		maxSize uintptr
		maxType reflect.Type
		visit   func(reflect.Type)
	)
	visit = func(typ reflect.Type) {
		if typ == nil {
			return
		}
		if _, ok := seen[typ]; ok {
			return
		}
		seen[typ] = struct{}{}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			visit(typ.Elem())
		case reflect.Interface:
			for _, candidate := range constructors {
				// invokeWithLayer/query uses the deliberately broad bin.Object
				// interface. Exact admission restricts that slot to generated RPC
				// methods, so only request constructors are reachable there.
				broad := typ.PkgPath() != "github.com/owpengram/owpengram-td/tg"
				if candidate.Implements(typ) && (!broad || strings.HasSuffix(candidate.Elem().Name(), "Request")) {
					visit(candidate)
				}
			}
		case reflect.Struct:
			if typ.PkgPath() == "github.com/owpengram/owpengram-td/tg" && typ.Size() > maxSize {
				maxSize, maxType = typ.Size(), typ
			}
			for i := 0; i < typ.NumField(); i++ {
				visit(typ.Field(i).Type)
			}
		}
	}
	for _, typ := range constructors {
		if typ.Kind() == reflect.Pointer && strings.HasSuffix(typ.Elem().Name(), "Request") {
			visit(typ)
		}
	}
	if maxSize > layerRPCAdmissionStaticObjectBytes {
		t.Fatalf("request-reachable generated TL object %v is %d bytes, exceeds admission ceiling %d", maxType, maxSize, layerRPCAdmissionStaticObjectBytes)
	}
	if layerRPCAdmissionGraphSlack != layerRPCAdmissionStaticObjectBytes*inboundLayerDecodeLimits.MaxDepth {
		t.Fatalf("graph slack %d does not cover %d bytes across decode depth %d", layerRPCAdmissionGraphSlack, layerRPCAdmissionStaticObjectBytes, inboundLayerDecodeLimits.MaxDepth)
	}
	// Preserve enough room for the maximum upload payload plus any supported
	// transparent wrapper/client-info envelope on a default connection.
	if got := layerRPCAdmissionReservationSize(appfiles.MaxUploadPartBytes + (32 << 10)); got > maxInflightRPCBytes {
		t.Fatalf("largest legal upload request charge = %d, exceeds default connection budget %d", got, maxInflightRPCBytes)
	}
	maxInt := int(^uint(0) >> 1)
	if got := layerRPCAdmissionReservationSize(maxInt); got != maxInt {
		t.Fatalf("saturating charge = %d, want max int %d", got, maxInt)
	}
	if got := layerRPCAdmissionReservationSize(-1); got != layerRPCAdmissionGraphSlack {
		t.Fatalf("negative wire charge = %d, want fixed slack %d", got, layerRPCAdmissionGraphSlack)
	}
	if got := layerRPCFlatBytesWireCharge(100, 80); got != 20*layerRPCAdmissionWireFactor+80*layerRPCAdmissionFlatBytesFactor {
		t.Fatalf("flat bytes wire charge = %d", got)
	}
	if got := layerRPCFlatBytesWireCharge(10, 11); got != layerRPCAdmissionWireCharge(10) {
		t.Fatalf("invalid flat bytes hint charge = %d, want generic %d", got, layerRPCAdmissionWireCharge(10))
	}
	if got := layerRPCFlatBytesWireCharge(maxInt, maxInt); got != maxInt {
		t.Fatalf("saturating flat bytes wire charge = %d, want max int %d", got, maxInt)
	}
	if got := saturatingLayerRPCAdmissionCharge(maxInt, 1); got != maxInt {
		t.Fatalf("saturating addition = %d, want max int %d", got, maxInt)
	}
}

type countingLayerRPCAdmission struct {
	LayerRPCHandler
	decodeCalls atomic.Int32
}

func (h *countingLayerRPCAdmission) AdmitLayer(profile tlprofile.Profile, b *bin.Buffer, limits tlprofile.Limits) (tlprofile.Admission, error) {
	h.decodeCalls.Add(1)
	return h.LayerRPCHandler.AdmitLayer(profile, b, limits)
}

func (h *countingLayerRPCAdmission) AdmitLayerWithOptions(profile tlprofile.Profile, b *bin.Buffer, options tlprofile.AdmissionOptions) (tlprofile.Admission, error) {
	h.decodeCalls.Add(1)
	if admitter, ok := h.LayerRPCHandler.(LayerRPCOptionsAdmitter); ok {
		return admitter.AdmitLayerWithOptions(profile, b, options)
	}
	return h.LayerRPCHandler.AdmitLayer(profile, b, options.Limits)
}

func (h *countingLayerRPCAdmission) AdmitUnprofiled(b *bin.Buffer, limits tlprofile.Limits) (tlprofile.Admission, error) {
	h.decodeCalls.Add(1)
	return h.LayerRPCHandler.AdmitUnprofiled(b, limits)
}

func (h *countingLayerRPCAdmission) AdmitUnprofiledWithOptions(b *bin.Buffer, options tlprofile.AdmissionOptions) (tlprofile.Admission, error) {
	h.decodeCalls.Add(1)
	if admitter, ok := h.LayerRPCHandler.(LayerRPCOptionsAdmitter); ok {
		return admitter.AdmitUnprofiledWithOptions(b, options)
	}
	return h.LayerRPCHandler.AdmitUnprofiled(b, options.Limits)
}

func TestLayerRPCAdmissionCapacityRejectsBeforeDecoder(t *testing.T) {
	for _, test := range []struct {
		name       string
		fillGlobal bool
	}{
		{name: "connection_queue"},
		{name: "global_task", fillGlobal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
			counting := &countingLayerRPCAdmission{LayerRPCHandler: router}
			s := New(Options{DC: 2, LayerRPC: counting})
			scheduler := newInboundRPCScheduler(1, 1, 1<<30)
			s.rpcScheduler = scheduler

			target := &Conn{authKeyID: [8]byte{8, 1}, sessionID: 81, metrics: NopMetrics{}}
			target.startInboundRPCScheduler(scheduler, 1, 1, time.Second)
			holder := target
			if test.fillGlobal {
				holder = &Conn{authKeyID: [8]byte{8, 2}, sessionID: 82, metrics: NopMetrics{}}
				holder.startInboundRPCScheduler(scheduler, 1, 1, time.Second)
			}
			occupied, err := holder.reserveInboundRPCBatch(context.Background(), []inboundRPCSpec{{method: "occupied", size: 1}})
			if err != nil {
				t.Fatal(err)
			}
			defer occupied.abort()

			body := exactLayerRPCBody(t, &tg.InvokeWithLayerRequest{Layer: 225, Query: &tg.HelpGetConfigRequest{}})
			plan := &inboundPlan{items: []inboundItem{{kind: inboundItemRPC, msgID: 100, body: body}}}
			defer plan.close()
			if err := s.prepareInboundLayerRPCBatch(context.Background(), target, plan); err != nil {
				t.Fatal(err)
			}
			if got := counting.decodeCalls.Load(); got != 0 {
				t.Fatalf("typed decoder entered %d times after capacity rejection", got)
			}
			if plan.items[0].kind != inboundItemCapacityError || plan.rpcReservation != nil || len(plan.rpcTasks) != 0 {
				t.Fatalf("rejected plan = kind:%d reservation:%v tasks:%d", plan.items[0].kind, plan.rpcReservation != nil, len(plan.rpcTasks))
			}
		})
	}
}

func TestLayerRPCAdmissionExpandsTDLibNestedGZIPUnderTransferredBudget(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router, Logger: zaptest.NewLogger(t)})
	c := &Conn{authKeyID: [8]byte{8, 21}, sessionID: 821, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	body, expandedBytes := tdlibNestedGZIPBody(t, tlprofile.Profile228, &tg.HelpGetConfigRequest{})
	plan := &inboundPlan{items: []inboundItem{{kind: inboundItemRPC, msgID: 100, body: body}}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.items[0].kind != inboundItemRPC || plan.rpcReservation == nil || len(plan.rpcTasks) != 1 {
		t.Fatalf("admitted nested gzip plan = kind:%d reservation:%v tasks:%d", plan.items[0].kind, plan.rpcReservation != nil, len(plan.rpcTasks))
	}
	wantCharge := int64(layerRPCAdmissionReservationSize(len(body) + expandedBytes))
	if got := c.inflightRPCBytes.Load(); got != wantCharge {
		t.Fatalf("nested gzip connection charge = %d, want %d", got, wantCharge)
	}
	if got := plan.rpcReservation.entries[0].size; int64(got) != wantCharge {
		t.Fatalf("nested gzip reservation charge = %d, want %d", got, wantCharge)
	}
	if got := plan.gzipExpandedBytes; got != expandedBytes {
		t.Fatalf("nested gzip cumulative expansion = %d, want %d", got, expandedBytes)
	}
	if got := s.frameBudget.usedBytes(); got != 0 {
		t.Fatalf("nested gzip temporary frame budget retained after materialization: %d", got)
	}
}

func TestLayerRPCAdmissionAdmitsTDLibUploadParts(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		body        bin.Object
		withoutGZIP bool
		bare        bool
	}{
		{
			name:   "pixel_9a_small_file_part_negative_file_id",
			method: "upload.saveFilePart",
			body: &tg.UploadSaveFilePartRequest{
				FileID:   -3596058967254453060,
				FilePart: 0,
				Bytes:    make([]byte, 1071),
			},
		},
		{
			name:   "pixel_9a_big_file_part_gzip",
			method: "upload.saveBigFilePart",
			body: &tg.UploadSaveBigFilePartRequest{
				FileID:         92,
				FilePart:       0,
				FileTotalParts: 364,
				Bytes:          make([]byte, 64<<10),
			},
		},
		{
			name:        "pixel_9a_big_file_part_plain",
			method:      "upload.saveBigFilePart",
			withoutGZIP: true,
			body: &tg.UploadSaveBigFilePartRequest{
				FileID:         93,
				FilePart:       7,
				FileTotalParts: 364,
				Bytes:          make([]byte, 64<<10),
			},
		},
		{
			name:   "pixel_9a_big_file_part_bare_upload_session",
			method: "upload.saveBigFilePart",
			bare:   true,
			body: &tg.UploadSaveBigFilePartRequest{
				FileID:         94,
				FilePart:       15,
				FileTotalParts: 364,
				Bytes:          make([]byte, 64<<10),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
			s := New(Options{DC: 2, LayerRPC: router, Logger: zaptest.NewLogger(t)})
			c := &Conn{authKeyID: [8]byte{8, 31}, sessionID: 831, metrics: NopMetrics{}}
			c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
			defer func() {
				c.closeInboundRPCScheduler()
				s.rpcScheduler.stop(time.Second)
			}()

			var (
				body          []byte
				expandedBytes int
			)
			if tc.bare {
				body = exactOutboundLayerRPCBody(t, tlprofile.Profile228, tc.body)
			} else if tc.withoutGZIP {
				body = tdlibWrappedBody(t, tlprofile.Profile228, tc.body)
			} else {
				body, expandedBytes = tdlibNestedGZIPBody(t, tlprofile.Profile228, tc.body)
			}
			plan := &inboundPlan{items: []inboundItem{{kind: inboundItemRPC, msgID: 100, body: body}}}
			defer plan.close()
			if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
				t.Fatal(err)
			}
			if plan.items[0].kind != inboundItemRPC || plan.rpcReservation == nil || len(plan.rpcTasks) != 1 {
				t.Fatalf("admitted nested gzip upload plan = kind:%d reservation:%v tasks:%d payload:%v",
					plan.items[0].kind, plan.rpcReservation != nil, len(plan.rpcTasks), plan.items[0].payload)
			}
			if got := plan.gzipExpandedBytes; got != expandedBytes {
				t.Fatalf("nested gzip upload cumulative expansion = %d, want %d", got, expandedBytes)
			}

			// Admission alone is insufficient: the prepared wrapper chain must remain
			// executable after the temporary gzip expansion buffer has been released.
			// With no Files dependency configured, reaching the upload handler has the
			// stable terminal NOT_IMPLEMENTED; INPUT_REQUEST_INVALID means the wrapper or
			// prepared-call boundary corrupted the otherwise valid request.
			requestBody := &bin.Buffer{Buf: append([]byte(nil), body...)}
			admitted, err := router.AdmitLayerWithOptions(tlprofile.Profile228, requestBody, tlprofile.AdmissionOptions{
				Limits: inboundLayerDecodeLimits,
				ExpandGZIP: func(wire []byte, limit int) ([]byte, func(), error) {
					return s.decodeGZIPWithGlobalBudgetLimit(&bin.Buffer{Buf: wire}, limit)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, method, err := router.DispatchAdmitted(
				rpc.WithUserID(context.Background(), 42),
				[8]byte{8, 31},
				831,
				100,
				1,
				admitted,
			)
			if method != tc.method || !tgerr.Is(err, "NOT_IMPLEMENTED") {
				t.Fatalf("nested gzip upload dispatch = method:%q err:%v, want %s/NOT_IMPLEMENTED", method, err, tc.method)
			}
		})
	}
}

func TestLayerRPCAdmissionRejectionLogsBoundedMetadataWithoutUploadBody(t *testing.T) {
	const marker = "DO_NOT_LOG_UPLOAD_BODY_MARKER"
	payload := make([]byte, 1071)
	copy(payload, marker)
	body := tdlibWrappedBody(t, tlprofile.Profile228, &tg.UploadSaveFilePartRequest{
		FileID:   -3596058967254453060,
		FilePart: 0,
		Bytes:    payload,
	})
	// Remove the final TL padding byte. Exact admission must reject the malformed
	// wire while the edge still records its explicit wrapper and bounded cause.
	body = body[:len(body)-1]

	core, logs := observer.New(zap.DebugLevel)
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zap.New(core), clock.System)
	s := New(Options{DC: 2, LayerRPC: router, Logger: zap.New(core)})
	c := &Conn{
		authKeyID:  [8]byte{8, 34},
		authKeyHex: "0822000000000000",
		sessionID:  834,
		metrics:    NopMetrics{},
	}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 2, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	for attempt := 0; attempt < 2; attempt++ {
		plan := &inboundPlan{items: []inboundItem{{
			kind:   inboundItemRPC,
			msgID:  int64(100 + attempt*4),
			typeID: tg.InvokeWithLayerRequestTypeID,
			body:   body,
		}}}
		if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
			plan.close()
			t.Fatal(err)
		}
		if item := plan.items[0]; item.kind != inboundItemRPCAdmissionError {
			plan.close()
			t.Fatalf("malformed upload attempt %d kind = %d, want admission error", attempt, item.kind)
		}
		plan.close()
	}

	entries := logs.FilterMessage("RPC exact admission rejected").All()
	if len(entries) != 2 {
		t.Fatalf("admission rejection log count = %d, want 2", len(entries))
	}
	if entries[0].Level != zap.InfoLevel || entries[1].Level != zap.DebugLevel {
		t.Fatalf("admission rejection levels = %s/%s, want info/debug", entries[0].Level, entries[1].Level)
	}
	fields := entries[0].ContextMap()
	for key, want := range map[string]any{
		"method":                  "invokeWithLayer#da9b0d0d",
		"auth_key_id":             "0822000000000000",
		"session_id":              int64(834),
		"msg_id":                  int64(100),
		"top_level_wire_id":       uint32(tg.InvokeWithLayerRequestTypeID),
		"wire_bytes":              int64(len(body)),
		"profile_origin":          "unknown",
		"explicit_layer_selector": true,
		"rpc_error_code":          int64(400),
		"rpc_error_message":       "INPUT_REQUEST_INVALID",
	} {
		if got := fields[key]; got != want {
			t.Fatalf("admission rejection field %q = %#v (%T), want %#v (%T); all=%#v", key, got, got, want, want, fields)
		}
	}
	for _, entry := range entries {
		if strings.Contains(entry.Message, marker) || strings.Contains(entry.ContextMap()["error"].(string), marker) {
			t.Fatalf("admission rejection leaked upload body marker: %#v", entry.ContextMap())
		}
		if _, ok := entry.ContextMap()["body"]; ok {
			t.Fatalf("admission rejection exposed body field: %#v", entry.ContextMap())
		}
	}
}

func TestLayerRPCAdmissionAdmitsTDLibFirstUploadContainer(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router, Logger: zaptest.NewLogger(t)})
	c := &Conn{authKeyID: [8]byte{8, 32}, sessionID: 832, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 8, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	// A newly opened TDLib upload Session applies its invokeWithLayer /
	// initConnection header to every query in the first MTProto container. The
	// Pixel 9a trace contains eight gzip-packed 64 KiB saveBigFilePart requests
	// in that first container, all with the same known total and distinct
	// parts/message IDs. The fixture is an ELF and its first chunks compress to
	// roughly 11-14 KiB each, yielding a 129 KiB encrypted write.
	plan, legacyGenericCharge := tdlibFirstUploadPlan(t)
	defer plan.close()
	if legacyGenericCharge <= maxInflightRPCBytes {
		t.Fatalf("test fixture generic charge = %d, must exceed old connection ceiling %d", legacyGenericCharge, maxInflightRPCBytes)
	}

	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.rpcReservation == nil || len(plan.rpcTasks) != len(plan.items) {
		t.Fatalf("first upload container reservation/tasks = %v/%d, want retained/%d",
			plan.rpcReservation != nil, len(plan.rpcTasks), len(plan.items))
	}
	if got := plan.rpcReservation.totalSize; got <= 0 || got >= legacyGenericCharge || got > maxInflightRPCBytes {
		t.Fatalf("first upload container retained charge = %d, want 0 < charge < legacy %d and <= %d",
			got, legacyGenericCharge, maxInflightRPCBytes)
	}
	for index := range plan.items {
		item := &plan.items[index]
		if item.kind != inboundItemRPC {
			t.Fatalf("first upload container item %d kind = %d, payload = %v", index, item.kind, item.payload)
		}
		if method := plan.rpcTasks[index].method; method != "upload.saveBigFilePart" {
			t.Fatalf("first upload container task %d method = %q, want upload.saveBigFilePart", index, method)
		}
	}
}

func TestLayerRPCAdmissionTDLibFirstUploadContainerRequiresFlatBytesCapability(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	// The wrapper deliberately exposes exact admission but not the optional
	// flat-bytes sizing capability. The edge must therefore retain the generic
	// worst-case charge and reject the whole oversized batch atomically.
	handler := &countingLayerRPCAdmission{LayerRPCHandler: router}
	s := New(Options{DC: 2, LayerRPC: handler, Logger: zaptest.NewLogger(t)})
	c := &Conn{authKeyID: [8]byte{8, 33}, sessionID: 833, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 8, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	plan, _ := tdlibFirstUploadPlan(t)
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	for index := range plan.items {
		if plan.items[index].kind != inboundItemCapacityError {
			t.Fatalf("item %d kind = %d, want capacity error", index, plan.items[index].kind)
		}
	}
	if plan.rpcReservation != nil || len(plan.rpcTasks) != 0 {
		t.Fatalf("generic fallback retained reservation/tasks = %v/%d", plan.rpcReservation != nil, len(plan.rpcTasks))
	}
	if got := c.inflightRPCBytes.Load(); got != 0 {
		t.Fatalf("generic fallback leaked connection charge %d", got)
	}
	if tasks, bytes := s.rpcScheduler.budgetSnapshot(); tasks != 0 || bytes != 0 {
		t.Fatalf("generic fallback leaked global budget %d/%d", tasks, bytes)
	}
}

func tdlibFirstUploadPlan(t *testing.T) (*inboundPlan, int64) {
	t.Helper()
	plan := &inboundPlan{items: make([]inboundItem, 8)}
	var legacyGenericCharge int64
	for index := range plan.items {
		payload := make([]byte, 64<<10)
		state := uint32(index + 1)
		for byteIndex := 0; byteIndex < 13<<10; byteIndex++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			payload[byteIndex] = byte(state)
		}
		body, expandedBytes := tdlibNestedGZIPBody(t, tlprofile.Profile228, &tg.UploadSaveBigFilePartRequest{
			FileID:         95,
			FilePart:       index,
			FileTotalParts: 364,
			Bytes:          payload,
		})
		plan.items[index] = inboundItem{
			kind:  inboundItemRPC,
			msgID: int64(100 + index*4),
			body:  body,
		}
		legacyGenericCharge += int64(layerRPCAdmissionReservationSize(len(body) + expandedBytes))
	}
	return plan, legacyGenericCharge
}

func TestLayerRPCAdmissionAdmitsTDLibWrappedBindTempAuthKey(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router, Logger: zaptest.NewLogger(t)})
	c := &Conn{authKeyID: [8]byte{8, 41}, sessionID: -7940676790771565328, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	request := &tg.AuthBindTempAuthKeyRequest{
		PermAuthKeyID:    9179421154451858694,
		Nonce:            5318578202586482454,
		ExpiresAt:        1785817822,
		EncryptedMessage: make([]byte, 104),
	}
	body := tdlibWrappedBody(t, tlprofile.Profile228, request)
	plan := &inboundPlan{items: []inboundItem{{kind: inboundItemRPC, msgID: 100, body: body}}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.items[0].kind != inboundItemRPC || plan.rpcReservation == nil || len(plan.rpcTasks) != 1 {
		t.Fatalf("admitted TDLib bind plan = kind:%d reservation:%v tasks:%d payload:%v",
			plan.items[0].kind, plan.rpcReservation != nil, len(plan.rpcTasks), plan.items[0].payload)
	}
	requestBody := &bin.Buffer{Buf: append([]byte(nil), body...)}
	admitted, err := router.AdmitUnprofiled(requestBody, inboundLayerDecodeLimits)
	if err != nil {
		t.Fatal(err)
	}
	result, method, err := router.DispatchAdmitted(
		context.Background(),
		c.authKeyID,
		c.sessionID,
		100,
		1,
		admitted,
	)
	if err != nil || method != "auth.bindTempAuthKey" || result == nil || !result.WireInvariant() {
		t.Fatalf("TDLib wrapped bind dispatch = method:%q result:%T invariant:%v err:%v",
			method, result, result != nil && result.WireInvariant(), err)
	}
}

func TestLayerRPCAdmissionNestedGZIPGrowFailureRejectsWholeBatch(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router, Logger: zaptest.NewLogger(t)})
	first, _ := tdlibNestedGZIPBody(t, tlprofile.Profile228, &tg.HelpGetConfigRequest{})
	second, _ := tdlibNestedGZIPBody(t, tlprofile.Profile228, &tg.HelpGetNearestDCRequest{})
	initialCharge := int64(layerRPCAdmissionReservationSize(len(first)) + layerRPCAdmissionReservationSize(len(second)))
	s.rpcScheduler = newInboundRPCScheduler(1, 4, initialCharge)
	c := &Conn{authKeyID: [8]byte{8, 22}, sessionID: 822, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	plan := &inboundPlan{items: []inboundItem{
		{kind: inboundItemRPC, msgID: 100, body: first},
		{kind: inboundItemRPC, msgID: 104, body: second},
	}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	for index := range plan.items {
		if plan.items[index].kind != inboundItemCapacityError {
			t.Fatalf("item %d kind = %d, want capacity error", index, plan.items[index].kind)
		}
	}
	if plan.rpcReservation != nil || len(plan.rpcTasks) != 0 {
		t.Fatalf("capacity plan retained reservation/tasks = %v/%d", plan.rpcReservation != nil, len(plan.rpcTasks))
	}
	if got := c.inflightRPCBytes.Load(); got != 0 {
		t.Fatalf("grow failure leaked connection charge %d", got)
	}
	if tasks, bytes := s.rpcScheduler.budgetSnapshot(); tasks != 0 || bytes != 0 {
		t.Fatalf("grow failure leaked global budget %d/%d", tasks, bytes)
	}
	if got := s.frameBudget.usedBytes(); got != 0 {
		t.Fatalf("grow failure leaked temporary frame budget %d", got)
	}
}

func TestLayerRPCAdmissionNestedGZIPSiblingsShareFrameExpansionLimit(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router, Logger: zaptest.NewLogger(t)})
	c := &Conn{authKeyID: [8]byte{8, 23}, sessionID: 823, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
	defer func() {
		c.closeInboundRPCScheduler()
		s.rpcScheduler.stop(time.Second)
	}()

	first, expandedBytes := tdlibNestedGZIPBody(t, tlprofile.Profile228, &tg.HelpGetConfigRequest{})
	second, _ := tdlibNestedGZIPBody(t, tlprofile.Profile228, &tg.HelpGetNearestDCRequest{})
	plan := &inboundPlan{
		gzipExpandedBytes: maxDispatchExpandedBytes - expandedBytes,
		items: []inboundItem{
			{kind: inboundItemRPC, msgID: 100, body: first},
			{kind: inboundItemRPC, msgID: 104, body: second},
		},
	}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	for index := range plan.items {
		if plan.items[index].kind != inboundItemCapacityError {
			t.Fatalf("item %d kind = %d, want capacity error", index, plan.items[index].kind)
		}
	}
	if got := plan.gzipExpandedBytes; got != maxDispatchExpandedBytes {
		t.Fatalf("shared cumulative expansion = %d, want %d", got, maxDispatchExpandedBytes)
	}
	if got := c.inflightRPCBytes.Load(); got != 0 {
		t.Fatalf("shared-limit rejection leaked connection charge %d", got)
	}
	if got := s.frameBudget.usedBytes(); got != 0 {
		t.Fatalf("shared-limit rejection leaked temporary frame budget %d", got)
	}
}

func TestLayerRPCAdmissionNestedGZIPReDecodeReusesMaterializationCharge(t *testing.T) {
	handler := newAdmissionOnlyLayerRPC()
	s := New(Options{DC: 2, LayerRPC: handler, Logger: zaptest.NewLogger(t)})
	s.rpcResults = newRPCExecutionLedgerForServerTest(s, time.Now, 8)
	scheduler := newInboundRPCScheduler(1, 4, 1<<30)
	s.rpcScheduler = scheduler
	authKeyID := [8]byte{8, 24}
	const sessionID = int64(824)
	c225 := &Conn{authKeyID: authKeyID, sessionID: sessionID, metrics: NopMetrics{}}
	c227 := &Conn{authKeyID: authKeyID, sessionID: sessionID, metrics: NopMetrics{}}
	c225.startInboundRPCScheduler(scheduler, 1, 2, time.Second)
	c227.startInboundRPCScheduler(scheduler, 1, 2, time.Second)
	defer func() {
		c225.closeInboundRPCScheduler()
		c227.closeInboundRPCScheduler()
		scheduler.stop(time.Second)
	}()

	terminal := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.MessagesGetHistoryRequest{
		Peer: &tg.InputPeerSelf{}, Limit: 1,
	})
	body := exactLayerRPCBody(t, &tg.InvokeWithoutUpdatesRequest{Query: &proto.GZIP{Data: terminal}})
	initialCharge := layerRPCAdmissionReservationSize(len(body))
	reservation225, err := c225.reserveInboundRPCBatch(context.Background(), []inboundRPCSpec{{method: "messages.getHistory", size: initialCharge}})
	if err != nil {
		t.Fatal(err)
	}
	defer reservation225.abort()
	reservation227, err := c227.reserveInboundRPCBatch(context.Background(), []inboundRPCSpec{{method: "messages.getHistory", size: initialCharge}})
	if err != nil {
		t.Fatal(err)
	}
	defer reservation227.abort()

	plan225 := &inboundPlan{}
	budget225 := &layerRPCGZIPExpansionBudget{
		server: s, plan: plan225, reservation: reservation225,
		baseCharge: initialCharge, chargedSize: initialCharge,
	}
	options225 := tlprofile.AdmissionOptions{Limits: inboundLayerDecodeLimits, ExpandGZIP: budget225.expand}
	item225 := inboundItem{msgID: 100, body: body}
	item225.admitted, item225.method, err = s.decodeInboundLayerRPCWithOptions(
		LayerProfileSnapshot{Profile: tlprofile.Profile225, Origin: LayerProfileInherited}, body, options225,
	)
	if err != nil {
		t.Fatal(err)
	}

	plan227 := &inboundPlan{}
	budget227 := &layerRPCGZIPExpansionBudget{
		server: s, plan: plan227, reservation: reservation227,
		baseCharge: initialCharge, chargedSize: initialCharge,
	}
	options227 := tlprofile.AdmissionOptions{Limits: inboundLayerDecodeLimits, ExpandGZIP: budget227.expand}
	item227 := inboundItem{msgID: 100, body: body}
	item227.admitted, item227.method, err = s.decodeInboundLayerRPCWithOptions(
		LayerProfileSnapshot{Profile: tlprofile.Profile227, Origin: LayerProfileInherited}, body, options227,
	)
	if err != nil {
		t.Fatal(err)
	}
	if item225.admitted.Prepared().Identity() == item227.admitted.Prepared().Identity() {
		t.Fatal("test request identity is invariant; need authoritative-profile re-decode")
	}

	winner, err := s.acquireAdmittedLayerRPC(c225, &item225, nil, options225, budget225)
	if err != nil || winner.state != rpcResultAcquireOwner || winner.owner == nil {
		t.Fatalf("winner = state:%d err:%v", winner.state, err)
	}
	defer winner.owner.Abort()
	loser, err := s.acquireAdmittedLayerRPC(c227, &item227, nil, options227, budget227)
	if err != nil || loser.state != rpcResultAcquirePending {
		t.Fatalf("loser = state:%d err:%v", loser.state, err)
	}
	if got := item227.admitted.Call().Profile(); got != tlprofile.Profile225 {
		t.Fatalf("loser re-admitted profile = %d, want 225", got)
	}
	wantCharge := layerRPCAdmissionReservationSize(len(body) + len(terminal))
	if got := reservation227.entries[0].size; got != wantCharge {
		t.Fatalf("re-decode reservation charge = %d, want single-graph maximum %d", got, wantCharge)
	}
	if got := plan227.gzipExpandedBytes; got != 2*len(terminal) {
		t.Fatalf("re-decode cumulative work = %d, want %d", got, 2*len(terminal))
	}
	if got := s.frameBudget.usedBytes(); got != 0 {
		t.Fatalf("re-decode leaked temporary frame budget %d", got)
	}
}

func TestLayerRPCAdmissionTransfersOriginalReservationToFreshOwner(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router})
	c := &Conn{authKeyID: [8]byte{8, 3}, sessionID: 83, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
	if err := c.FreezeLayerProfile(tlprofile.Profile225); err != nil {
		t.Fatal(err)
	}

	bad := make([]byte, bin.Word)
	bad[0], bad[1], bad[2], bad[3] = 0x04, 0x03, 0x02, 0x01
	fresh := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetConfigRequest{})
	plan := &inboundPlan{items: []inboundItem{
		{kind: inboundItemRPC, msgID: 100, body: bad},
		{kind: inboundItemRPC, msgID: 104, body: fresh},
	}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.items[0].kind != inboundItemRPCAdmissionError || len(plan.rpcTasks) != 1 || plan.rpcReservation == nil {
		t.Fatalf("classified plan = bad:%d tasks:%d reservation:%v", plan.items[0].kind, len(plan.rpcTasks), plan.rpcReservation != nil)
	}
	wantCharge := int64(layerRPCAdmissionReservationSize(len(fresh)))
	if got := c.inflightRPCBytes.Load(); got != wantCharge {
		t.Fatalf("connection retained bytes = %d, want %d", got, wantCharge)
	}
	s.rpcScheduler.budgetMu.Lock()
	globalTasks, globalBytes := s.rpcScheduler.tasks, s.rpcScheduler.bytes
	s.rpcScheduler.budgetMu.Unlock()
	if globalTasks != 1 || globalBytes != wantCharge {
		t.Fatalf("global retained budget = %d/%d, want 1/%d", globalTasks, globalBytes, wantCharge)
	}
	plan.close()
	if got := c.inflightRPCBytes.Load(); got != 0 {
		t.Fatalf("plan abort leaked %d connection bytes", got)
	}
	s.rpcScheduler.budgetMu.Lock()
	globalTasks, globalBytes = s.rpcScheduler.tasks, s.rpcScheduler.bytes
	s.rpcScheduler.budgetMu.Unlock()
	if globalTasks != 0 || globalBytes != 0 {
		t.Fatalf("plan abort leaked global budget %d/%d", globalTasks, globalBytes)
	}
}

func tdlibNestedGZIPBody(t *testing.T, profile tlprofile.Profile, terminal bin.Object) ([]byte, int) {
	t.Helper()
	terminalWire := exactOutboundLayerRPCBody(t, profile, terminal)
	return tdlibWrappedBody(t, profile, zlibPackedObjectForTest(t, terminalWire)), len(terminalWire)
}

func tdlibWrappedBody(t *testing.T, profile tlprofile.Profile, terminal bin.Object) []byte {
	t.Helper()
	request := &tg.InvokeWithLayerRequest{
		Layer: int(profile),
		Query: &tg.InitConnectionRequest{
			APIID:          1,
			DeviceModel:    "android",
			SystemVersion:  "test",
			AppVersion:     "1.0",
			SystemLangCode: "en",
			LangPack:       "",
			LangCode:       "en",
			Params: &tg.JSONObject{Value: []tg.JSONObjectValue{{
				Key:   "tz_offset",
				Value: &tg.JSONNumber{Value: 8 * 60 * 60},
			}}},
			Query: terminal,
		},
	}
	return exactLayerRPCBody(t, request)
}

func TestLayerRPCAdmissionPendingReplayReleasesProvisionalEntry(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router})
	c := &Conn{authKeyID: [8]byte{8, 4}, sessionID: 84, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 4, time.Second)
	if err := c.FreezeLayerProfile(tlprofile.Profile225); err != nil {
		t.Fatal(err)
	}

	pendingBody := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetConfigRequest{})
	identityBuffer := &bin.Buffer{Buf: append([]byte(nil), pendingBody...)}
	pendingRequest, err := router.AdmitLayer(tlprofile.Profile225, identityBuffer, tlprofile.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.rpcResults.AcquireIdentified(c.authKeyID, c.sessionID, 100, pendingRequest.Prepared().Identity())
	if err != nil || pending.owner == nil {
		t.Fatalf("pending owner = %v, %v", pending.owner, err)
	}

	freshBody := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetNearestDCRequest{})
	plan := &inboundPlan{items: []inboundItem{
		{kind: inboundItemRPC, msgID: 100, body: pendingBody},
		{kind: inboundItemRPC, msgID: 104, body: freshBody},
	}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.items[0].kind != inboundItemRewrappedRPC || len(plan.rewrapAliases) != 1 || len(plan.rpcTasks) != 1 {
		t.Fatalf("pending/fresh classification = kind:%d aliases:%d tasks:%d", plan.items[0].kind, len(plan.rewrapAliases), len(plan.rpcTasks))
	}
	wantCharge := int64(layerRPCAdmissionReservationSize(len(freshBody)))
	if got := c.inflightRPCBytes.Load(); got != wantCharge {
		t.Fatalf("pending replay retained bytes = %d, want only fresh %d", got, wantCharge)
	}
	plan.close()
	if !pending.owner.Abort() {
		t.Fatal("plan cleanup aborted the pre-existing pending replay owner")
	}
}

func TestLayerRPCAdmissionCompletedReplayReleasesWholeProvisionalBatch(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router})
	c := &Conn{authKeyID: [8]byte{8, 8}, sessionID: 88, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 2, time.Second)
	if err := c.FreezeLayerProfile(tlprofile.Profile225); err != nil {
		t.Fatal(err)
	}
	body := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetConfigRequest{})
	identityBuffer := &bin.Buffer{Buf: append([]byte(nil), body...)}
	request, err := router.AdmitLayer(tlprofile.Profile225, identityBuffer, tlprofile.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.rpcResults.AcquireIdentified(c.authKeyID, c.sessionID, 100, request.Prepared().Identity())
	if err != nil || claim.owner == nil {
		t.Fatalf("completed replay owner = %v, %v", claim.owner, err)
	}
	if !claim.owner.CompleteExecution(true) {
		t.Fatal("complete replay business outcome failed")
	}
	storeLogicalRPCResultForTest(t, s, c, 100, &encodedOutboundMessage{body: []byte{1, 2, 3, 4}})

	plan := &inboundPlan{items: []inboundItem{{kind: inboundItemRPC, msgID: 100, body: body}}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.items[0].kind != inboundItemReplayRPC || plan.rpcReservation != nil || len(plan.rpcTasks) != 0 {
		t.Fatalf("completed replay plan = kind:%d reservation:%v tasks:%d", plan.items[0].kind, plan.rpcReservation != nil, len(plan.rpcTasks))
	}
	if got := c.inflightRPCBytes.Load(); got != 0 || c.rpcReserved != 0 {
		t.Fatalf("completed replay leaked connection budget bytes:%d tasks:%d", got, c.rpcReserved)
	}
	s.rpcScheduler.budgetMu.Lock()
	globalTasks, globalBytes := s.rpcScheduler.tasks, s.rpcScheduler.bytes
	s.rpcScheduler.budgetMu.Unlock()
	if globalTasks != 0 || globalBytes != 0 {
		t.Fatalf("completed replay leaked global budget %d/%d", globalTasks, globalBytes)
	}
}

func TestLayerRPCAdmissionTransferredBatchClosesWithoutLeak(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router})
	c := &Conn{authKeyID: [8]byte{8, 5}, sessionID: 85, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 2, time.Second)
	if err := c.FreezeLayerProfile(tlprofile.Profile225); err != nil {
		t.Fatal(err)
	}
	plan := &inboundPlan{items: []inboundItem{{
		kind: inboundItemRPC, msgID: 100,
		body: exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetConfigRequest{}),
	}}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	c.beginCloseInboundRPCScheduler()
	if err := plan.commitRPCBatch(); err != ErrConnClosed {
		t.Fatalf("commit after connection close = %v, want ErrConnClosed", err)
	}
	plan.close()
	if !c.waitInboundShutdown(time.Second) {
		t.Fatal("connection close did not converge after transferred reservation failed commit")
	}
	if got := c.inflightRPCBytes.Load(); got != 0 {
		t.Fatalf("connection close leaked %d admission bytes", got)
	}
	s.rpcScheduler.budgetMu.Lock()
	globalTasks, globalBytes := s.rpcScheduler.tasks, s.rpcScheduler.bytes
	s.rpcScheduler.budgetMu.Unlock()
	if globalTasks != 0 || globalBytes != 0 {
		t.Fatalf("connection close leaked global budget %d/%d", globalTasks, globalBytes)
	}
}

func TestLayerRPCAdmissionTransferredBatchCommitsConservativeCharge(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router})
	c := &Conn{authKeyID: [8]byte{8, 6}, sessionID: 86, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 2, time.Second)
	if err := c.FreezeLayerProfile(tlprofile.Profile225); err != nil {
		t.Fatal(err)
	}
	body := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetConfigRequest{})
	plan := &inboundPlan{items: []inboundItem{{kind: inboundItemRPC, msgID: 100, body: body}}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if err := plan.commitRPCBatch(); err != nil {
		t.Fatal(err)
	}
	wantCharge := layerRPCAdmissionReservationSize(len(body))
	c.rpcMu.Lock()
	queued := len(c.rpcQueue)
	gotCharge := 0
	if queued == 1 {
		gotCharge = c.rpcQueue[0].size
	}
	c.rpcMu.Unlock()
	if queued != 1 || gotCharge != wantCharge {
		t.Fatalf("committed queue = len:%d charge:%d, want 1/%d", queued, gotCharge, wantCharge)
	}
	c.beginCloseInboundRPCScheduler()
	if got := c.inflightRPCBytes.Load(); got != 0 {
		t.Fatalf("queued exact task close leaked %d bytes", got)
	}
	s.rpcScheduler.budgetMu.Lock()
	globalTasks, globalBytes := s.rpcScheduler.tasks, s.rpcScheduler.bytes
	s.rpcScheduler.budgetMu.Unlock()
	if globalTasks != 0 || globalBytes != 0 {
		t.Fatalf("queued exact task close leaked global budget %d/%d", globalTasks, globalBytes)
	}
}

func TestLayerRPCAdmissionLocalDuplicateConsumesNoProvisionalEntry(t *testing.T) {
	router := rpc.New(rpc.Config{DC: 2}, rpc.Deps{}, zaptest.NewLogger(t), clock.System)
	s := New(Options{DC: 2, LayerRPC: router})
	c := &Conn{authKeyID: [8]byte{8, 7}, sessionID: 87, metrics: NopMetrics{}}
	c.startInboundRPCScheduler(s.rpcScheduler, 1, 2, time.Second)
	if err := c.FreezeLayerProfile(tlprofile.Profile225); err != nil {
		t.Fatal(err)
	}
	body := exactOutboundLayerRPCBody(t, tlprofile.Profile225, &tg.HelpGetConfigRequest{})
	plan := &inboundPlan{items: []inboundItem{
		{kind: inboundItemDuplicate, msgID: 96, body: body},
		{kind: inboundItemRPC, msgID: 100, body: body},
	}}
	defer plan.close()
	if err := s.prepareInboundLayerRPCBatch(context.Background(), c, plan); err != nil {
		t.Fatal(err)
	}
	if plan.items[0].kind != inboundItemDuplicate || len(plan.rpcTasks) != 1 || c.rpcReserved != 1 {
		t.Fatalf("duplicate/fresh admission = duplicate:%d tasks:%d reserved:%d", plan.items[0].kind, len(plan.rpcTasks), c.rpcReserved)
	}
}

func TestTakeInboundRPCFIFOAdvancesSliceHead(t *testing.T) {
	scheduler := newInboundRPCScheduler(1, 8, 1<<20)
	c := &Conn{metrics: NopMetrics{}}
	c.startInboundRPCScheduler(scheduler, 1, 8, time.Second)
	c.rpcQueue = []inboundRPC{{method: "first"}, {method: "second"}, {method: "third"}}
	c.rpcReady = true
	oldSecond := &c.rpcQueue[1]
	task, ok, _ := c.takeInboundRPC()
	if !ok || task.method != "first" {
		t.Fatalf("take = (%q,%v), want first", task.method, ok)
	}
	if len(c.rpcQueue) != 2 || &c.rpcQueue[0] != oldSecond {
		t.Fatal("FIFO take copied the queue instead of advancing its slice head")
	}
	c.finishInboundRPC(task)
	c.beginCloseInboundRPCScheduler()
}

func BenchmarkLayerRPCAdmissionReservationSize(b *testing.B) {
	for _, size := range []int{64, appfiles.MaxUploadPartBytes + 24} {
		b.Run(time.Duration(size).String(), func(b *testing.B) {
			b.ReportAllocs()
			var charge int
			for i := 0; i < b.N; i++ {
				charge = layerRPCAdmissionReservationSize(size)
			}
			if charge == 0 {
				b.Fatal("zero admission charge")
			}
		})
	}
}
