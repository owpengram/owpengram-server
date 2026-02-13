package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/teamgram/proto/mtproto"
	"github.com/teamgram/proto/mtproto/rpc/metadata"
	"github.com/teamgram/teamgram-server/app/bff/voipcalls/internal/svc"
	"github.com/teamgram/teamgram-server/app/messenger/msg/msg/msg"
	"github.com/teamgram/teamgram-server/app/messenger/sync/sync"
	userpb "github.com/teamgram/teamgram-server/app/service/biz/user/user"
)

type VoipCallsCore struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
	MD *metadata.RpcMetadata
}

func New(ctx context.Context, svcCtx *svc.ServiceContext) *VoipCallsCore {
	return &VoipCallsCore{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
		MD:     metadata.RpcMetadataFromIncoming(ctx),
	}
}

func (c *VoipCallsCore) nowUnix() int32 {
	return int32(time.Now().Unix())
}

func (c *VoipCallsCore) randomInt64() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	v := int64(binary.LittleEndian.Uint64(b[:]))
	if v < 0 {
		v = -v
	}
	return v
}

func (c *VoipCallsCore) userPairKey(a, b int64) string {
	if a < b {
		return fmt.Sprintf("%d:%d", a, b)
	}
	return fmt.Sprintf("%d:%d", b, a)
}

func (c *VoipCallsCore) getUsersForResponseFor(viewerUserID int64, userIDs ...int64) []*mtproto.User {
	mUsers, err := c.svcCtx.Dao.UserClient.UserGetMutableUsers(c.ctx, &userpb.TLUserGetMutableUsers{
		Id: userIDs,
		To: []int64{},
	})
	if err != nil || mUsers == nil {
		return []*mtproto.User{}
	}
	return mUsers.GetUserListByIdList(viewerUserID, userIDs...)
}

func (c *VoipCallsCore) getOrDefaultProtocol(protocol *mtproto.PhoneCallProtocol) *mtproto.PhoneCallProtocol {
	if protocol == nil {
		protocol = mtproto.MakeTLPhoneCallProtocol(&mtproto.PhoneCallProtocol{
			UdpP2P:          true,
			UdpReflector:    true,
			MinLayer:        65,
			MaxLayer:        200,
			LibraryVersions: []string{"2.4.4"},
		}).To_PhoneCallProtocol()
	}
	normalized := mtproto.MakeTLPhoneCallProtocol(&mtproto.PhoneCallProtocol{
		UdpP2P:          protocol.GetUdpP2P(),
		UdpReflector:    protocol.GetUdpReflector(),
		MinLayer:        protocol.GetMinLayer(),
		MaxLayer:        protocol.GetMaxLayer(),
		LibraryVersions: append([]string(nil), protocol.GetLibraryVersions()...),
	}).To_PhoneCallProtocol()
	if normalized.MinLayer == 0 {
		normalized.MinLayer = 65
	}
	if normalized.MaxLayer == 0 {
		normalized.MaxLayer = 200
	}
	if normalized.MaxLayer < normalized.MinLayer {
		normalized.MaxLayer = normalized.MinLayer
	}
	if len(normalized.LibraryVersions) == 0 {
		normalized.LibraryVersions = []string{"2.4.4"}
	}
	return normalized
}

// parseSemVer parses a "major.minor.patch" version string into three integers.
// Returns (0,0,0) if parsing fails.
func parseSemVer(version string) (int, int, int) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) != 3 {
		return 0, 0, 0
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0
	}
	return major, minor, patch
}

// compareSemVer compares two semantic version strings.
// Returns >0 if a > b, <0 if a < b, 0 if equal.
func compareSemVer(a, b string) int {
	aMaj, aMin, aPat := parseSemVer(a)
	bMaj, bMin, bPat := parseSemVer(b)
	if aMaj != bMaj {
		return aMaj - bMaj
	}
	if aMin != bMin {
		return aMin - bMin
	}
	return aPat - bPat
}

// negotiateProtocol computes a negotiated protocol from the admin's and
// participant's protocols. It finds the intersection of LibraryVersions,
// sorts them by semantic version (highest first), and negotiates the
// layer range and UDP flags.
func (c *VoipCallsCore) negotiateProtocol(
	adminProto, participantProto *mtproto.PhoneCallProtocol,
) *mtproto.PhoneCallProtocol {
	adminProto = c.getOrDefaultProtocol(adminProto)
	if participantProto == nil {
		return adminProto
	}

	// Build a set of participant versions for fast lookup.
	participantSet := make(map[string]struct{}, len(participantProto.GetLibraryVersions()))
	for _, v := range participantProto.GetLibraryVersions() {
		participantSet[v] = struct{}{}
	}

	// Intersect: keep only versions present in both lists.
	var intersection []string
	for _, v := range adminProto.GetLibraryVersions() {
		if _, ok := participantSet[v]; ok {
			intersection = append(intersection, v)
		}
	}

	// Sort by semantic version descending (highest version first).
	sort.Slice(intersection, func(i, j int) bool {
		return compareSemVer(intersection[i], intersection[j]) > 0
	})

	// Fallback: if no common versions, use admin's list as-is.
	if len(intersection) == 0 {
		intersection = append([]string(nil), adminProto.GetLibraryVersions()...)
	}

	// Negotiate layer range.
	minLayer := adminProto.GetMinLayer()
	if participantProto.GetMinLayer() > minLayer {
		minLayer = participantProto.GetMinLayer()
	}
	maxLayer := adminProto.GetMaxLayer()
	if participantProto.GetMaxLayer() < maxLayer {
		maxLayer = participantProto.GetMaxLayer()
	}
	if maxLayer < minLayer {
		maxLayer = minLayer
	}

	return mtproto.MakeTLPhoneCallProtocol(&mtproto.PhoneCallProtocol{
		UdpP2P:          adminProto.GetUdpP2P() && participantProto.GetUdpP2P(),
		UdpReflector:    adminProto.GetUdpReflector() && participantProto.GetUdpReflector(),
		MinLayer:        minLayer,
		MaxLayer:        maxLayer,
		LibraryVersions: intersection,
	}).To_PhoneCallProtocol()
}

func (c *VoipCallsCore) pushCallUpdate(userID int64, phoneCall *mtproto.PhoneCall) {
	update := mtproto.MakeTLUpdatePhoneCall(&mtproto.Update{
		PhoneCall: phoneCall,
	}).To_Update()
	push := mtproto.MakeUpdatesByUpdates(update)
	push.Users = c.getUsersForResponseFor(
		userID,
		phoneCall.GetAdminId(),
		phoneCall.GetParticipantId(),
	)
	_, _ = c.svcCtx.Dao.SyncClient.SyncPushUpdates(c.ctx, &sync.TLSyncPushUpdates{
		UserId:  userID,
		Updates: push,
	})
}

func (c *VoipCallsCore) pushSignalingUpdate(userID int64, callID int64, data []byte) {
	update := mtproto.MakeTLUpdatePhoneCallSignalingData(&mtproto.Update{
		PhoneCallId:    callID,
		Data_FLAGBYTES: data,
	}).To_Update()
	_, _ = c.svcCtx.Dao.SyncClient.SyncPushUpdates(c.ctx, &sync.TLSyncPushUpdates{
		UserId:  userID,
		Updates: mtproto.MakeUpdatesByUpdates(update),
	})
}

func (c *VoipCallsCore) pushCallUpdateIfNot(userID int64, excludeAuthIDs []int64, phoneCall *mtproto.PhoneCall) {
	update := mtproto.MakeTLUpdatePhoneCall(&mtproto.Update{
		PhoneCall: phoneCall,
	}).To_Update()
	push := mtproto.MakeUpdatesByUpdates(update)
	push.Users = c.getUsersForResponseFor(
		userID,
		phoneCall.GetAdminId(),
		phoneCall.GetParticipantId(),
	)
	_, _ = c.svcCtx.Dao.SyncClient.SyncPushUpdatesIfNot(c.ctx, &sync.TLSyncPushUpdatesIfNot{
		UserId:   userID,
		Excludes: excludeAuthIDs,
		Updates:  push,
	})
}

func (c *VoipCallsCore) relayConnections() []*mtproto.PhoneConnection {
	if len(c.svcCtx.Config.VoipRelayEndpoints) == 0 {
		return []*mtproto.PhoneConnection{
			mtproto.MakeTLPhoneConnection(&mtproto.PhoneConnection{
				Id:      1,
				Ip:      "127.0.0.1",
				Ipv6:    "",
				Port:    3478,
				PeerTag: svc.MakePeerTag(),
			}).To_PhoneConnection(),
		}
	}

	connections := make([]*mtproto.PhoneConnection, 0, len(c.svcCtx.Config.VoipRelayEndpoints))
	for idx, endpoint := range c.svcCtx.Config.VoipRelayEndpoints {
		id := endpoint.Id
		if id == 0 {
			id = int64(idx + 1)
		}
		connections = append(connections, mtproto.MakeTLPhoneConnection(&mtproto.PhoneConnection{
			Tcp:      endpoint.Tcp,
			Id:       id,
			Ip:       endpoint.Ip,
			Ipv6:     endpoint.Ipv6,
			Port:     endpoint.Port,
			PeerTag:  svc.DecodePeerTag(endpoint.PeerTag),
			Turn:     endpoint.Turn,
			Stun:     endpoint.Stun,
			Username: endpoint.Username,
			Password: endpoint.Password,
		}).To_PhoneConnection())
	}
	return connections
}

func (c *VoipCallsCore) pushCallHistoryMessage(actorID, peerID int64, callID int64, video bool, reason *mtproto.PhoneCallDiscardReason, duration int32) {
	serviceMessage := mtproto.MakeTLMessageService(&mtproto.Message{
		Out:         true,
		Mentioned:   false,
		MediaUnread: false,
		Silent:      false,
		Id:          0,
		FromId:      mtproto.MakePeerUser(actorID),
		PeerId:      mtproto.MakePeerUser(peerID),
		Date:        c.nowUnix(),
		Action: mtproto.MakeTLMessageActionPhoneCall(&mtproto.MessageAction{
			Video:    video,
			CallId:   callID,
			Reason:   reason,
			Duration: wrapperspb.Int32(duration),
		}).To_MessageAction(),
	}).To_Message()

	_, _ = c.svcCtx.Dao.MsgClient.MsgPushUserMessage(c.ctx, &msg.TLMsgPushUserMessage{
		UserId:    actorID,
		AuthKeyId: 0,
		PeerType:  mtproto.PEER_USER,
		PeerId:    peerID,
		PushType:  0,
		Message: msg.MakeTLOutboxMessage(&msg.OutboxMessage{
			NoWebpage:    false,
			Background:   false,
			RandomId:     mrand.Int63(),
			Message:      serviceMessage,
			ScheduleDate: nil,
		}).To_OutboxMessage(),
	})
}

func (c *VoipCallsCore) PhoneGetCallConfig(_ *mtproto.TLPhoneGetCallConfig) (*mtproto.DataJSON, error) {
	data := c.svcCtx.Config.VoipCallConfigJSON
	if data == "" {
		data = `{"udp_p2p":true,"udp_reflector":true}`
	}
	return mtproto.MakeTLDataJSON(&mtproto.DataJSON{Data: data}).To_DataJSON(), nil
}

func (c *VoipCallsCore) PhoneRequestCall(in *mtproto.TLPhoneRequestCall) (*mtproto.Phone_PhoneCall, error) {
	participantID := mtproto.FromInputUser(c.MD.UserId, in.UserId).PeerId
	if participantID == 0 || participantID == c.MD.UserId {
		return nil, mtproto.ErrUserIdInvalid
	}

	now := c.nowUnix()
	call := &svc.PrivateCallSession{
		ID:            c.randomInt64(),
		AccessHash:    c.randomInt64(),
		AdminID:       c.MD.UserId,
		ParticipantID: participantID,
		Video:         in.Video,
		Date:          now,
		State:         svc.CallStateRequested,
		Protocol:      c.getOrDefaultProtocol(in.Protocol),
		GAHash:        in.GAHash,
	}

	key := c.userPairKey(call.AdminID, call.ParticipantID)
	c.svcCtx.Mu.Lock()
	c.svcCtx.CallsByID[call.ID] = call
	c.svcCtx.CallsByUserKey[key] = call.ID
	c.svcCtx.Mu.Unlock()

	requested := mtproto.MakeTLPhoneCallRequested(&mtproto.PhoneCall{
		Video:         call.Video,
		Id:            call.ID,
		AccessHash:    call.AccessHash,
		Date:          call.Date,
		AdminId:       call.AdminID,
		ParticipantId: call.ParticipantID,
		GAHash:        call.GAHash,
		Protocol:      call.Protocol,
	}).To_PhoneCall()
	c.pushCallUpdate(call.ParticipantID, requested)

	waiting := mtproto.MakeTLPhoneCallWaiting(&mtproto.PhoneCall{
		Video:         call.Video,
		Id:            call.ID,
		AccessHash:    call.AccessHash,
		Date:          call.Date,
		AdminId:       call.AdminID,
		ParticipantId: call.ParticipantID,
		Protocol:      call.Protocol,
	}).To_PhoneCall()

	return mtproto.MakeTLPhonePhoneCall(&mtproto.Phone_PhoneCall{
		PhoneCall: waiting,
		Users:     c.getUsersForResponseFor(c.MD.UserId, call.AdminID, call.ParticipantID),
	}).To_Phone_PhoneCall(), nil
}

func (c *VoipCallsCore) PhoneAcceptCall(in *mtproto.TLPhoneAcceptCall) (*mtproto.Phone_PhoneCall, error) {
	callID := in.Peer.GetId()
	accessHash := in.Peer.GetAccessHash()

	c.svcCtx.Mu.Lock()
	call, ok := c.svcCtx.CallsByID[callID]
	if !ok || call.AccessHash != accessHash || call.ParticipantID != c.MD.UserId {
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	acceptedByAuth := c.MD.GetAuthId()
	acceptedByPerm := c.MD.GetPermAuthKeyId()
	if acceptedByAuth == 0 {
		acceptedByAuth = acceptedByPerm
	}
	// Defensive check: don't process if call is already discarded
	if call.State == svc.CallStateDiscarded {
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	// Idempotency: if already accepted by the same device, return current state
	if call.State >= svc.CallStateAccepted {
		if (call.AcceptedByAuth != 0 && call.AcceptedByAuth == acceptedByAuth) ||
			(call.AcceptedByPerm != 0 && call.AcceptedByPerm == acceptedByPerm) {
			// Already accepted by this device, return current state
			waiting := mtproto.MakeTLPhoneCallWaiting(&mtproto.PhoneCall{
				Video:         call.Video,
				Id:            call.ID,
				AccessHash:    call.AccessHash,
				Date:          call.Date,
				AdminId:       call.AdminID,
				ParticipantId: call.ParticipantID,
				Protocol:      call.Protocol,
			}).To_PhoneCall()
			c.svcCtx.Mu.Unlock()
			return mtproto.MakeTLPhonePhoneCall(&mtproto.Phone_PhoneCall{
				PhoneCall: waiting,
				Users:     c.getUsersForResponseFor(c.MD.UserId, call.AdminID, call.ParticipantID),
			}).To_Phone_PhoneCall(), nil
		}
		// Accepted by different device, reject
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	call.State = svc.CallStateAccepted
	call.AcceptedByAuth = acceptedByAuth
	call.AcceptedByPerm = acceptedByPerm
	call.GB = in.GB
	call.ParticipantProtocol = c.getOrDefaultProtocol(in.Protocol)
	accepted := mtproto.MakeTLPhoneCallAccepted(&mtproto.PhoneCall{
		Video:         call.Video,
		Id:            call.ID,
		AccessHash:    call.AccessHash,
		Date:          call.Date,
		AdminId:       call.AdminID,
		ParticipantId: call.ParticipantID,
		GB:            call.GB,
		Protocol:      call.ParticipantProtocol,
	}).To_PhoneCall()
	waiting := mtproto.MakeTLPhoneCallWaiting(&mtproto.PhoneCall{
		Video:         call.Video,
		Id:            call.ID,
		AccessHash:    call.AccessHash,
		Date:          call.Date,
		AdminId:       call.AdminID,
		ParticipantId: call.ParticipantID,
		Protocol:      call.ParticipantProtocol,
	}).To_PhoneCall()
	// Store values needed after unlock
	adminID := call.AdminID
	participantID := call.ParticipantID
	storedAcceptedByAuth := call.AcceptedByAuth
	storedAcceptedByPerm := call.AcceptedByPerm
	callVideo := call.Video
	storedCallID := call.ID
	c.svcCtx.Mu.Unlock()

	// Send phoneCallAccepted to admin first to ensure proper state transition
	// This must be received before any discarded updates to prevent "Unexpected state" errors
	// Use pushCallUpdate helper which uses SyncPushUpdates internally
	c.pushCallUpdate(adminID, accepted)

	// Small delay to ensure phoneCallAccepted is processed before any subsequent updates
	// This is especially important when admin and participant are on different network paths
	// (e.g., server on localhost, desktop on same machine, android on different IP)
	time.Sleep(100 * time.Millisecond)

	// Defensive check: only send busy updates if call is still in Accepted state
	// Re-acquire lock to check current state
	c.svcCtx.Mu.RLock()
	callCheck, ok := c.svcCtx.CallsByID[storedCallID]
	callState := svc.CallStateDiscarded
	if ok {
		callState = callCheck.State
	}
	c.svcCtx.Mu.RUnlock()

	// Only send busy updates if call is still accepted (not discarded or established)
	if callState == svc.CallStateAccepted {
		excludeAuthIDs := make([]int64, 0, 2)
		if storedAcceptedByAuth != 0 {
			excludeAuthIDs = append(excludeAuthIDs, storedAcceptedByAuth)
		}
		if storedAcceptedByPerm != 0 && storedAcceptedByPerm != storedAcceptedByAuth {
			excludeAuthIDs = append(excludeAuthIDs, storedAcceptedByPerm)
		}
		if len(excludeAuthIDs) > 0 {
			busyOnOtherDevices := mtproto.MakeTLPhoneCallDiscarded(&mtproto.PhoneCall{
				Video:    callVideo,
				Id:       storedCallID,
				Reason:   mtproto.MakeTLPhoneCallDiscardReasonBusy(nil).To_PhoneCallDiscardReason(),
				Duration: wrapperspb.Int32(0),
			}).To_PhoneCall()
			c.pushCallUpdateIfNot(participantID, excludeAuthIDs, busyOnOtherDevices)
		}
	}
	return mtproto.MakeTLPhonePhoneCall(&mtproto.Phone_PhoneCall{
		PhoneCall: waiting,
		Users:     c.getUsersForResponseFor(c.MD.UserId, adminID, participantID),
	}).To_Phone_PhoneCall(), nil
}

func (c *VoipCallsCore) PhoneConfirmCall(in *mtproto.TLPhoneConfirmCall) (*mtproto.Phone_PhoneCall, error) {
	callID := in.Peer.GetId()
	accessHash := in.Peer.GetAccessHash()

	c.svcCtx.Mu.Lock()
	call, ok := c.svcCtx.CallsByID[callID]
	if !ok || call.AccessHash != accessHash || call.AdminID != c.MD.UserId {
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	// Defensive check: don't process if call is already discarded
	if call.State == svc.CallStateDiscarded {
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	// Idempotency: if already established, return current state
	if call.State == svc.CallStateEstablished {
		connections := c.relayConnections()
		established := mtproto.MakeTLPhoneCall(&mtproto.PhoneCall{
			Video:               call.Video,
			P2PAllowed:          true,
			ConferenceSupported: false,
			Id:                  call.ID,
			AccessHash:          call.AccessHash,
			Date:                call.Date,
			AdminId:             call.AdminID,
			ParticipantId:       call.ParticipantID,
			GAOrB:               call.GA,
			KeyFingerprint:      call.KeyFingerprint,
			Protocol:            call.Protocol,
			Connections:         connections,
			StartDate:           call.StartDate,
		}).To_PhoneCall()
		c.svcCtx.Mu.Unlock()
		return mtproto.MakeTLPhonePhoneCall(&mtproto.Phone_PhoneCall{
			PhoneCall: established,
			Users:     c.getUsersForResponseFor(c.MD.UserId, call.AdminID, call.ParticipantID),
		}).To_Phone_PhoneCall(), nil
	}
	// Defensive check: can only confirm if call is in Accepted state
	if call.State != svc.CallStateAccepted {
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	call.State = svc.CallStateEstablished
	call.GA = in.GA
	call.KeyFingerprint = in.KeyFingerprint
	call.Protocol = c.negotiateProtocol(in.Protocol, call.ParticipantProtocol)
	call.StartDate = c.nowUnix()
	connections := c.relayConnections()
	established := mtproto.MakeTLPhoneCall(&mtproto.PhoneCall{
		Video:               call.Video,
		P2PAllowed:          true,
		ConferenceSupported: false,
		Id:                  call.ID,
		AccessHash:          call.AccessHash,
		Date:                call.Date,
		AdminId:             call.AdminID,
		ParticipantId:       call.ParticipantID,
		GAOrB:               call.GA,
		KeyFingerprint:      call.KeyFingerprint,
		Protocol:            call.Protocol,
		Connections:         connections,
		StartDate:           call.StartDate,
	}).To_PhoneCall()
	c.svcCtx.Mu.Unlock()

	// Send Established to BOTH admin and participant
	// This ensures both sides receive the update in the correct order
	// Small delay to ensure phoneCallAccepted is processed first by clients
	time.Sleep(100 * time.Millisecond)
	c.pushCallUpdate(call.AdminID, established)
	c.pushCallUpdate(call.ParticipantID, established)
	return mtproto.MakeTLPhonePhoneCall(&mtproto.Phone_PhoneCall{
		PhoneCall: established,
		Users:     c.getUsersForResponseFor(c.MD.UserId, call.AdminID, call.ParticipantID),
	}).To_Phone_PhoneCall(), nil
}

func (c *VoipCallsCore) PhoneReceivedCall(in *mtproto.TLPhoneReceivedCall) (*mtproto.Bool, error) {
	callID := in.Peer.GetId()
	c.svcCtx.Mu.Lock()
	call, ok := c.svcCtx.CallsByID[callID]
	if ok && call.ParticipantID == c.MD.UserId {
		call.State = svc.CallStateWaitingIncoming
		receiveDate := c.nowUnix()
		call.Date = call.Date
		waiting := mtproto.MakeTLPhoneCallWaiting(&mtproto.PhoneCall{
			Video:         call.Video,
			Id:            call.ID,
			AccessHash:    call.AccessHash,
			Date:          call.Date,
			AdminId:       call.AdminID,
			ParticipantId: call.ParticipantID,
			Protocol:      call.Protocol,
			ReceiveDate:   wrapperspb.Int32(receiveDate),
		}).To_PhoneCall()
		c.svcCtx.Mu.Unlock()
		c.pushCallUpdate(call.AdminID, waiting)
		return mtproto.BoolTrue, nil
	}
	c.svcCtx.Mu.Unlock()
	return mtproto.BoolFalse, nil
}

func (c *VoipCallsCore) PhoneDiscardCall(in *mtproto.TLPhoneDiscardCall) (*mtproto.Updates, error) {
	callID := in.Peer.GetId()
	accessHash := in.Peer.GetAccessHash()

	c.svcCtx.Mu.Lock()
	call, ok := c.svcCtx.CallsByID[callID]
	if !ok || call.AccessHash != accessHash {
		c.svcCtx.Mu.Unlock()
		return mtproto.MakeUpdatesByUpdates(), nil
	}
	callerAuth := c.MD.GetAuthId()
	callerPerm := c.MD.GetPermAuthKeyId()
	if callerAuth == 0 {
		callerAuth = callerPerm
	}
	if call.State >= svc.CallStateAccepted &&
		c.MD.UserId == call.ParticipantID &&
		((call.AcceptedByAuth != 0 && call.AcceptedByAuth != callerAuth) ||
			(call.AcceptedByPerm != 0 && call.AcceptedByPerm != callerPerm)) {
		c.svcCtx.Mu.Unlock()
		return mtproto.MakeUpdatesByUpdates(), nil
	}
	call.State = svc.CallStateDiscarded
	call.LastReason = in.Reason
	call.LastDuration = in.Duration

	discarded := mtproto.MakeTLPhoneCallDiscarded(&mtproto.PhoneCall{
		NeedRating: in.Reason.GetPredicateName() == mtproto.Predicate_phoneCallDiscardReasonHangup,
		NeedDebug:  false,
		Video:      call.Video,
		Id:         call.ID,
		Reason:     in.Reason,
		Duration:   wrapperspb.Int32(in.Duration),
	}).To_PhoneCall()
	delete(c.svcCtx.CallsByID, call.ID)
	delete(c.svcCtx.CallsByUserKey, c.userPairKey(call.AdminID, call.ParticipantID))
	c.svcCtx.Mu.Unlock()

	c.pushCallUpdate(call.AdminID, discarded)
	c.pushCallUpdate(call.ParticipantID, discarded)
	// Always persist call history from call initiator perspective to keep direction stable.
	c.pushCallHistoryMessage(call.AdminID, call.ParticipantID, call.ID, call.Video, in.Reason, in.Duration)
	return mtproto.MakeUpdatesByUpdates(mtproto.MakeTLUpdatePhoneCall(&mtproto.Update{
		PhoneCall: discarded,
	}).To_Update()), nil
}

func (c *VoipCallsCore) PhoneSetCallRating(_ *mtproto.TLPhoneSetCallRating) (*mtproto.Updates, error) {
	return mtproto.MakeUpdatesByUpdates(), nil
}

func (c *VoipCallsCore) PhoneSaveCallDebug(_ *mtproto.TLPhoneSaveCallDebug) (*mtproto.Bool, error) {
	return mtproto.BoolTrue, nil
}

func (c *VoipCallsCore) PhoneSendSignalingData(in *mtproto.TLPhoneSendSignalingData) (*mtproto.Bool, error) {
	callID := in.Peer.GetId()
	accessHash := in.Peer.GetAccessHash()
	c.svcCtx.Mu.RLock()
	call, ok := c.svcCtx.CallsByID[callID]
	c.svcCtx.Mu.RUnlock()
	if !ok || call.AccessHash != accessHash {
		return mtproto.BoolFalse, nil
	}
	toUserID := call.AdminID
	if c.MD.UserId == call.AdminID {
		toUserID = call.ParticipantID
	}
	c.pushSignalingUpdate(toUserID, call.ID, in.Data)
	return mtproto.BoolTrue, nil
}

func (c *VoipCallsCore) PhoneSaveCallLog(_ *mtproto.TLPhoneSaveCallLog) (*mtproto.Bool, error) {
	return mtproto.BoolTrue, nil
}

func (c *VoipCallsCore) MessagesDeletePhoneCallHistory(in *mtproto.TLMessagesDeletePhoneCallHistory) (*mtproto.Messages_AffectedFoundMessages, error) {
	r, err := c.svcCtx.Dao.MsgClient.MsgDeletePhoneCallHistory(c.ctx, &msg.TLMsgDeletePhoneCallHistory{
		UserId:    c.MD.UserId,
		AuthKeyId: c.MD.AuthId,
		Revoke:    in.Revoke,
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}
