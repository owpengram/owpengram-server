package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"go.uber.org/zap/zaptest"
	"telesrv/internal/domain"
)

type failingReadUsers struct{ mapUsersService }

func (failingReadUsers) BaseUsersByIDs(context.Context, []int64) ([]domain.User, error) {
	return nil, errors.New("identity unavailable")
}

func TestMessageReadInputScope(t *testing.T) {
	users := &countingMapUsersService{mapUsersService: mapUsersService{users: map[int64]domain.User{22: {ID: 22, AccessHash: 77}}}}
	r := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
	ctx := context.Background()
	invalid := []tg.InputPeerClass{nil, (*tg.InputPeerUser)(nil), &tg.InputPeerEmpty{}, &tg.InputPeerUser{UserID: 0}, &tg.InputPeerUser{UserID: -22}, &tg.InputPeerUser{UserID: 22, AccessHash: 0}, &tg.InputPeerUser{UserID: 22, AccessHash: 78}, &tg.InputPeerUser{UserID: 23, AccessHash: 77}, &tg.InputPeerUserFromMessage{Peer: &tg.InputPeerSelf{}, MsgID: 1, UserID: 22}}
	for i, p := range invalid {
		for _, sender := range []bool{false, true} {
			_, err := r.checkedMessageReadPeer(ctx, 11, p, sender)
			want := "PEER_ID_INVALID"
			if sender {
				want = "FROM_PEER_INVALID"
			}
			if !tgerr.Is(err, want) {
				t.Fatalf("invalid[%d], sender=%t err=%v", i, sender, err)
			}
		}
	}
	for _, p := range []tg.InputPeerClass{&tg.InputPeerEmpty{}, &tg.InputPeerSelf{}, &tg.InputPeerUser{UserID: 22, AccessHash: 77}} {
		req := &tg.MessagesSearchRequest{Peer: p, FromID: &tg.InputPeerUser{UserID: 22, AccessHash: 77}, Filter: &tg.InputMessagesFilterEmpty{}}
		f, err := r.messageFilterFromSearchRequest(ctx, 11, req)
		_, global := p.(*tg.InputPeerEmpty)
		if err != nil || f.HasPeer == global || f.SenderUserID != 22 || !f.CountOnly {
			t.Fatalf("scope=%T filter=%+v err=%v", p, f, err)
		}
	}
	if users.byIDCalls != 0 || users.byIDsCalls != 0 || users.selfCalls != 0 || users.baseByIDsCalls == 0 {
		t.Fatalf("read validation projected users: %+v", users)
	}
	for _, deps := range []Deps{{}, {Users: failingReadUsers{}}} {
		broken := New(Config{}, deps, zaptest.NewLogger(t), clock.System)
		_, err := broken.checkedMessageReadPeer(ctx, 11, &tg.InputPeerUser{UserID: 22, AccessHash: 77}, false)
		if !tgerr.Is(err, "INTERNAL_SERVER_ERROR") {
			t.Fatalf("missing identity boundary err=%v", err)
		}
	}
}

func TestMessageReadBoundsAndCaps(t *testing.T) {
	r := New(Config{}, Deps{}, zaptest.NewLogger(t), clock.System)
	ctx := context.Background()
	for _, tt := range []struct {
		limit, offset, max, min int
		want                    string
	}{{-1, 0, 0, 0, "LIMIT_INVALID"}, {0, -1, 0, 0, "MSG_ID_INVALID"}, {0, 0, -1, 0, "MSG_ID_INVALID"}, {0, 0, 0, -1, "MSG_ID_INVALID"}, {0, domain.MaxMessageBoxID + 1, 0, 0, "MSG_ID_INVALID"}, {0, 0, domain.MaxMessageBoxID + 1, 0, "MSG_ID_INVALID"}} {
		_, se := r.messageFilterFromSearchRequest(ctx, 11, &tg.MessagesSearchRequest{Peer: &tg.InputPeerSelf{}, Limit: tt.limit, OffsetID: tt.offset, MaxID: tt.max, MinID: tt.min})
		_, he := r.messageFilterFromHistoryRequest(ctx, 11, &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerSelf{}, Limit: tt.limit, OffsetID: tt.offset, MaxID: tt.max, MinID: tt.min})
		if !tgerr.Is(se, tt.want) || !tgerr.Is(he, tt.want) {
			t.Fatalf("bounds %+v search=%v history=%v", tt, se, he)
		}
	}
	s, err := r.messageFilterFromSearchRequest(ctx, 11, &tg.MessagesSearchRequest{Peer: &tg.InputPeerSelf{}, Limit: 501, AddOffset: -2})
	if err != nil || s.Limit != 500 || s.AddOffset != -2 || s.CountOnly {
		t.Fatalf("search %+v %v", s, err)
	}
	h, err := r.messageFilterFromHistoryRequest(ctx, 11, &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerSelf{}, Limit: 501, AddOffset: -2})
	if err != nil || h.Limit != 50 || h.AddOffset != -2 || h.CountOnly {
		t.Fatalf("history %+v %v", h, err)
	}
	h, err = r.messageFilterFromHistoryRequest(ctx, 11, &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerSelf{}})
	if err != nil || h.CountOnly {
		t.Fatalf("default history %+v %v", h, err)
	}
}
