package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

type welcomeRPCService struct {
	messages     []domain.WelcomeMessage
	hash         int64
	hasAny       bool
	authorizeErr error
}

func (s *welcomeRPCService) Authorize(context.Context, int64, domain.Peer) error {
	return s.authorizeErr
}

func (s *welcomeRPCService) Create(_ context.Context, userID int64, peer domain.Peer, randomID int64, content domain.WelcomeMessageContent) (domain.WelcomeMessage, bool, error) {
	s.hash++
	message := domain.WelcomeMessage{
		ID: len(s.messages) + 1, Peer: peer, CreatorUserID: userID, Date: 1700000100,
		RandomID: randomID, Content: content, CreateFingerprint: [32]byte{1}, Version: 1,
	}
	s.messages = append(s.messages, message)
	s.hasAny = true
	return message, true, nil
}
func (s *welcomeRPCService) Edit(_ context.Context, _ int64, _ domain.Peer, id int, fields domain.WelcomeMessageEditFields) (domain.WelcomeMessage, error) {
	for index := range s.messages {
		if s.messages[index].ID == id {
			content, err := fields.Apply(s.messages[index].Content)
			if err != nil {
				return domain.WelcomeMessage{}, err
			}
			s.messages[index].Content = content
			s.messages[index].EditDate = 1700000101
			s.messages[index].Version++
			s.hash++
			return s.messages[index], nil
		}
	}
	return domain.WelcomeMessage{}, domain.ErrWelcomeMessageNotFound
}
func (s *welcomeRPCService) List(_ context.Context, _ int64, _ domain.Peer, hash int64) (domain.WelcomeMessageList, error) {
	if hash == s.hash {
		return domain.WelcomeMessageList{Hash: s.hash, NotModified: true}, nil
	}
	return domain.WelcomeMessageList{Hash: s.hash, Messages: append([]domain.WelcomeMessage(nil), s.messages...)}, nil
}
func (s *welcomeRPCService) Delete(_ context.Context, _ int64, _ domain.Peer, id int) (bool, error) {
	for index := range s.messages {
		if s.messages[index].ID == id {
			s.messages = append(s.messages[:index], s.messages[index+1:]...)
			s.hash++
			s.hasAny = len(s.messages) != 0
			return true, nil
		}
	}
	return true, nil
}
func (s *welcomeRPCService) DeleteAll(context.Context, int64, domain.Peer) (bool, error) {
	if len(s.messages) != 0 {
		s.messages = nil
		s.hash++
	}
	s.hasAny = false
	return true, nil
}
func (s *welcomeRPCService) HasAny(context.Context, domain.Peer) (bool, error) {
	return s.hasAny, nil
}

type welcomeRPCChannels struct {
	ChannelsService
	view domain.ChannelView
}

func (s *welcomeRPCChannels) ResolveChannel(context.Context, int64, int64) (domain.ChannelView, error) {
	return s.view, nil
}
func (s *welcomeRPCChannels) GetChannel(context.Context, int64, int64) (domain.ChannelView, error) {
	return s.view, nil
}

type welcomeRPCUsers struct {
	user domain.User
}

func (s *welcomeRPCUsers) Self(context.Context, int64) (domain.User, error) {
	return s.user, nil
}
func (s *welcomeRPCUsers) ByID(context.Context, int64, int64) (domain.User, bool, error) {
	return s.user, true, nil
}
func (s *welcomeRPCUsers) ByIDs(context.Context, int64, []int64) ([]domain.User, error) {
	return []domain.User{s.user}, nil
}

func newWelcomeRPCRouter(t *testing.T) (*Router, *welcomeRPCService, context.Context, *tg.InputPeerChannel) {
	t.Helper()
	const userID, channelID, accessHash = int64(9), int64(77), int64(88)
	service := &welcomeRPCService{hash: domain.InitialWelcomeRevision}
	channels := &welcomeRPCChannels{view: domain.ChannelView{
		Channel: domain.Channel{ID: channelID, AccessHash: accessHash, Title: "Welcome", Megagroup: true, Pts: 1},
		Self: domain.ChannelMember{
			ChannelID: channelID, UserID: userID, Role: domain.ChannelRoleCreator, Status: domain.ChannelMemberActive,
		},
	}}
	users := &welcomeRPCUsers{user: domain.User{ID: userID, AccessHash: 99, FirstName: "Owner"}}
	router := New(Config{DC: 2}, Deps{WelcomeMessages: service, Channels: channels, Users: users}, zaptest.NewLogger(t), clock.System)
	return router, service, WithUserID(context.Background(), userID), &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash}
}

func TestWelcomeMessageRPCTextMediaRichAndCRUD(t *testing.T) {
	router, service, ctx, peer := newWelcomeRPCRouter(t)
	tests := []struct {
		name    string
		request *tg.EphemeralSendMessageRequest
		assert  func(*testing.T, tg.EphemeralMessage)
	}{
		{
			name: "text",
			request: &tg.EphemeralSendMessageRequest{
				Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, Message: "Hello", RandomID: 1001,
			},
			assert: func(t *testing.T, message tg.EphemeralMessage) {
				if message.Message != "Hello" || message.Media != nil || !message.RichMessage.Zero() {
					t.Fatalf("text welcome = %+v", message)
				}
			},
		},
		{
			name: "media",
			request: &tg.EphemeralSendMessageRequest{
				Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, Message: "Contact", RandomID: 1002,
				Media: &tg.InputMediaContact{PhoneNumber: "+10000000000", FirstName: "Guest"},
			},
			assert: func(t *testing.T, message tg.EphemeralMessage) {
				if message.Media == nil {
					t.Fatalf("media welcome = %+v", message)
				}
			},
		},
		{
			name: "rich",
			request: &tg.EphemeralSendMessageRequest{
				Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, RandomID: 1003,
				RichMessage: &tg.InputRichMessage{Blocks: []tg.PageBlockClass{
					&tg.PageBlockParagraph{Text: &tg.TextPlain{Text: "Rich welcome"}},
				}},
			},
			assert: func(t *testing.T, message tg.EphemeralMessage) {
				if message.RichMessage.Zero() || len(message.RichMessage.Blocks) != 1 {
					t.Fatalf("rich welcome = %+v", message)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updates, err := router.onEphemeralSendMessage(ctx, test.request)
			if err != nil {
				t.Fatal(err)
			}
			result, ok := updates.(*tg.Updates)
			if !ok || len(result.Updates) != 1 || result.Seq != 0 {
				t.Fatalf("send updates = %#v", updates)
			}
			created, ok := result.Updates[0].(*tg.UpdateNewEphemeralMessage)
			if !ok || !created.Message.WelcomeTemplate || created.Message.PeerID == nil ||
				created.Message.ReceiverID != 0 || !created.Message.Out {
				t.Fatalf("new welcome update = %#v", result.Updates[0])
			}
			test.assert(t, created.Message)
		})
	}

	edit := &tg.EphemeralEditMessageRequest{
		Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, ID: 1,
	}
	edit.SetMessage("Edited")
	editedUpdates, err := router.onEphemeralEditWelcomeMessage(ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	edited := editedUpdates.(*tg.Updates).Updates[0].(*tg.UpdateEditEphemeralMessage).Message
	if edited.Message != "Edited" || !edited.WelcomeTemplate {
		t.Fatalf("edited welcome = %+v", edited)
	}

	listed, err := router.onEphemeralGetWelcomeMessages(ctx, &tg.EphemeralGetWelcomeMessagesRequest{Peer: peer, Hash: 0})
	if err != nil {
		t.Fatal(err)
	}
	modified, ok := listed.(*tg.EphemeralWelcomeMessages)
	if !ok || len(modified.Messages) != 3 || modified.Hash != service.hash {
		t.Fatalf("listed welcomes = %#v", listed)
	}
	if same, err := router.onEphemeralGetWelcomeMessages(ctx, &tg.EphemeralGetWelcomeMessagesRequest{Peer: peer, Hash: service.hash}); err != nil {
		t.Fatal(err)
	} else if _, ok := same.(*tg.EphemeralWelcomeMessagesNotModified); !ok {
		t.Fatalf("same hash = %#v", same)
	}
	if ok, err := router.onEphemeralDeleteWelcomeMessage(ctx, &tg.EphemeralDeleteWelcomeMessageRequest{Peer: peer, ID: 1}); err != nil || !ok {
		t.Fatalf("delete = %v,%v", ok, err)
	}
	if ok, err := router.onEphemeralDeleteAllWelcomeMessages(ctx, &tg.EphemeralDeleteAllWelcomeMessagesRequest{Peer: peer}); err != nil || !ok {
		t.Fatalf("delete all = %v,%v", ok, err)
	}
}

func TestWelcomeMessageFullChatProjectionAndAdminRight(t *testing.T) {
	router, service, ctx, _ := newWelcomeRPCRouter(t)
	channelFull := &tg.ChannelFull{}
	chatFull := &tg.ChatFull{}
	service.hasAny = true
	if err := router.applyWelcomeMessagesToFullChat(ctx, 77, channelFull); err != nil {
		t.Fatal(err)
	}
	if err := router.applyWelcomeMessagesToFullChat(ctx, 77, chatFull); err != nil {
		t.Fatal(err)
	}
	if !channelFull.HasWelcomeMessages || !chatFull.HasWelcomeMessages {
		t.Fatalf("full projections channel=%v chat=%v", channelFull.HasWelcomeMessages, chatFull.HasWelcomeMessages)
	}
	service.hasAny = false
	if err := router.applyWelcomeMessagesToFullChat(ctx, 77, channelFull); err != nil {
		t.Fatal(err)
	}
	if channelFull.HasWelcomeMessages {
		t.Fatal("deleted templates left stale channelFull.has_welcome_messages")
	}
	rights := tgChatAdminRights(domain.ChannelAdminRights{ManageWelcomeMessages: true})
	if !rights.ManageWelcomeMessages || !domainChannelAdminRights(rights).ManageWelcomeMessages {
		t.Fatalf("manage_welcome_messages conversion = %+v", rights)
	}
}

func TestWelcomeMessageAuthorizationPrecedesRichMaterialization(t *testing.T) {
	router, service, ctx, peer := newWelcomeRPCRouter(t)
	service.authorizeErr = domain.ErrWelcomeMessageForbidden
	request := &tg.EphemeralSendMessageRequest{
		Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, RandomID: 1001,
		// This rich payload is deliberately invalid. CHAT_ADMIN_REQUIRED proves
		// the permission gate ran before rich parsing or media resolution.
		RichMessage: &tg.InputRichMessage{},
	}
	if _, err := router.onEphemeralSendMessage(ctx, request); err == nil || !tgerr.Is(err, "CHAT_ADMIN_REQUIRED") {
		t.Fatalf("unauthorized invalid rich send err=%v, want CHAT_ADMIN_REQUIRED", err)
	}
}

func TestWelcomeMessagesExactLayer229Only(t *testing.T) {
	router := New(Config{DC: 2}, Deps{}, zaptest.NewLogger(t), clock.System)
	peer := &tg.InputPeerChannel{ChannelID: 77, AccessHash: 88}
	methods := []struct {
		name     string
		request  bin.Object
		semantic tlprofile.SemanticID
	}{
		{"send", &tg.EphemeralSendMessageRequest{Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, Message: "Hello", RandomID: 1001}, tlprofile.SemanticMethodEphemeralSendMessage},
		{"edit", &tg.EphemeralEditMessageRequest{Welcome: true, Peer: peer, ReceiverID: &tg.InputUserEmpty{}, ID: 1}, tlprofile.SemanticMethodEphemeralEditMessage},
		{"delete", &tg.EphemeralDeleteWelcomeMessageRequest{Peer: peer, ID: 1}, tlprofile.SemanticMethodEphemeralDeleteWelcomeMessage},
		{"delete-all", &tg.EphemeralDeleteAllWelcomeMessagesRequest{Peer: peer}, tlprofile.SemanticMethodEphemeralDeleteAllWelcomeMessages},
		{"get", &tg.EphemeralGetWelcomeMessagesRequest{Peer: peer, Hash: 0}, tlprofile.SemanticMethodEphemeralGetWelcomeMessages},
	}
	var getAdmission tlprofile.Admission
	hasGetAdmission := false
	for _, method := range methods {
		t.Run(method.name, func(t *testing.T) {
			body := encodeExactLayerRPC(t, tlprofile.Profile229, method.request)
			raw := body.Copy()
			admission, err := router.AdmitLayer(tlprofile.Profile229, &body, tlprofile.Limits{})
			if err != nil || admission.Call().Method() != method.semantic || body.Len() != 0 {
				t.Fatalf("Layer 229 admission method=%#x want=%#x remaining=%d err=%v", admission.Call().Method(), method.semantic, body.Len(), err)
			}
			if method.semantic == tlprofile.SemanticMethodEphemeralGetWelcomeMessages {
				getAdmission = admission
				hasGetAdmission = true
			}
			for _, profile := range []tlprofile.Profile{tlprofile.Profile225, tlprofile.Profile226, tlprofile.Profile227, tlprofile.Profile228} {
				older := bin.Buffer{Buf: append([]byte(nil), raw...)}
				if _, err := router.AdmitLayer(profile, &older, tlprofile.Limits{}); err == nil {
					t.Fatalf("exact Layer %d admitted Layer 229 %s RPC", profile, method.name)
				}
			}
		})
	}
	if !hasGetAdmission {
		t.Fatal("missing Layer 229 getWelcomeMessages admission")
	}

	message := domain.WelcomeMessage{
		ID: 1, Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: 77}, CreatorUserID: 9,
		Date: 1700000100, RandomID: 1001, Content: domain.WelcomeMessageContent{Message: "Hello"},
		CreateFingerprint: [32]byte{1}, Version: 1,
	}
	wire, err := tgWelcomeMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	result := &tg.EphemeralWelcomeMessages{Hash: 2, Messages: []tg.EphemeralMessage{wire}}
	var encoded229 bin.Buffer
	if err := getAdmission.Call().EncodeResult(result, &encoded229); err != nil {
		t.Fatalf("encode Layer 229 welcome result: %v", err)
	}
	for _, profile := range []tlprofile.Profile{tlprofile.Profile225, tlprofile.Profile226, tlprofile.Profile227, tlprofile.Profile228} {
		var older bin.Buffer
		if err := tlprofile.EncodeObject(profile, result, &older); err == nil {
			t.Fatalf("exact Layer %d encoded Layer 229 welcome result", profile)
		}
		for _, update := range []tg.UpdateClass{
			&tg.UpdateNewEphemeralMessage{Message: wire},
			&tg.UpdateEditEphemeralMessage{Message: wire},
		} {
			var olderUpdate bin.Buffer
			if err := tlprofile.EncodeObject(profile, update, &olderUpdate); err == nil {
				t.Fatalf("exact Layer %d encoded Layer 229 welcome update %T", profile, update)
			}
		}
	}
}
