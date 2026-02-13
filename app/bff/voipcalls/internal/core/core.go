package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
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
	if call.State >= svc.CallStateAccepted &&
		((call.AcceptedByAuth != 0 && call.AcceptedByAuth != acceptedByAuth) ||
			(call.AcceptedByPerm != 0 && call.AcceptedByPerm != acceptedByPerm)) {
		c.svcCtx.Mu.Unlock()
		return nil, mtproto.ErrCallPeerInvalid
	}
	call.State = svc.CallStateAccepted
	call.AcceptedByAuth = acceptedByAuth
	call.AcceptedByPerm = acceptedByPerm
	call.GB = in.GB
	call.Protocol = c.getOrDefaultProtocol(in.Protocol)
	accepted := mtproto.MakeTLPhoneCallAccepted(&mtproto.PhoneCall{
		Video:         call.Video,
		Id:            call.ID,
		AccessHash:    call.AccessHash,
		Date:          call.Date,
		AdminId:       call.AdminID,
		ParticipantId: call.ParticipantID,
		GB:            call.GB,
		Protocol:      call.Protocol,
	}).To_PhoneCall()
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

	c.pushCallUpdate(call.AdminID, accepted)
	excludeAuthIDs := make([]int64, 0, 2)
	if call.AcceptedByAuth != 0 {
		excludeAuthIDs = append(excludeAuthIDs, call.AcceptedByAuth)
	}
	if call.AcceptedByPerm != 0 && call.AcceptedByPerm != call.AcceptedByAuth {
		excludeAuthIDs = append(excludeAuthIDs, call.AcceptedByPerm)
	}
	if len(excludeAuthIDs) > 0 {
		busyOnOtherDevices := mtproto.MakeTLPhoneCallDiscarded(&mtproto.PhoneCall{
			Video:    call.Video,
			Id:       call.ID,
			Reason:   mtproto.MakeTLPhoneCallDiscardReasonBusy(nil).To_PhoneCallDiscardReason(),
			Duration: wrapperspb.Int32(0),
		}).To_PhoneCall()
		c.pushCallUpdateIfNot(call.ParticipantID, excludeAuthIDs, busyOnOtherDevices)
	}
	return mtproto.MakeTLPhonePhoneCall(&mtproto.Phone_PhoneCall{
		PhoneCall: waiting,
		Users:     c.getUsersForResponseFor(c.MD.UserId, call.AdminID, call.ParticipantID),
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
	call.State = svc.CallStateEstablished
	call.GA = in.GA
	call.KeyFingerprint = in.KeyFingerprint
	call.Protocol = c.getOrDefaultProtocol(in.Protocol)
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
