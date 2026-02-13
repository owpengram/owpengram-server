/*
 * Created from 'scheme.tl' by 'mtprotoc'
 *
 * Copyright (c) 2021-present,  Teamgram Studio (https://teamgram.io).
 *  All rights reserved.
 *
 * Author: teamgramio (teamgram.io@gmail.com)
 */

package core

import (
	"github.com/teamgram/proto/mtproto"
	"github.com/teamgram/teamgram-server/app/interface/session/session"
	"github.com/teamgram/teamgram-server/app/messenger/sync/sync"
	"github.com/teamgram/teamgram-server/app/service/status/status"
)

// SyncPushUpdatesIfNot
// sync.pushUpdatesIfNot user_id:long excludes:Vector<int64> updates:Updates = Void;
func (c *SyncCore) SyncPushUpdatesIfNot(in *sync.TLSyncPushUpdatesIfNot) (*mtproto.Void, error) {
	var (
		userId   = in.GetUserId()
		updates  = in.GetUpdates()
		excludes = in.GetExcludes()
	)

	notification, err := c.processUpdates(syncTypeUser, userId, false, updates)
	if err != nil {
		c.Logger.Errorf("sync.pushUpdatesIfNot - processUpdates error: %v", err)
		return nil, err
	}

	excludeSet := make(map[int64]struct{}, len(excludes))
	for _, key := range excludes {
		excludeSet[key] = struct{}{}
	}

	statusList, _ := c.svcCtx.Dao.StatusClient.StatusGetUserOnlineSessions(c.ctx, &status.TLStatusGetUserOnlineSessions{
		UserId: userId,
	})
	serverIdKeyIdList := make(map[string][]int64)
	for _, sess := range statusList.GetUserSessions() {
		if _, skip := excludeSet[sess.PermAuthKeyId]; skip {
			continue
		}
		if _, skip := excludeSet[sess.AuthKeyId]; skip {
			continue
		}
		serverIdKeyIdList[sess.Gateway] = append(serverIdKeyIdList[sess.Gateway], sess.AuthKeyId)
	}

	for serverId, keyIdList := range serverIdKeyIdList {
		for _, keyId := range keyIdList {
			_ = c.svcCtx.Dao.PushUpdatesToSession(
				c.ctx,
				serverId,
				&session.TLSessionPushUpdatesData{
					PermAuthKeyId: keyId,
					Notification:  notification,
					Updates:       updates,
				})
		}
	}

	return mtproto.EmptyVoid, nil
}
