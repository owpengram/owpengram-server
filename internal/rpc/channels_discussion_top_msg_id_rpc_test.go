package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"
	"go.uber.org/zap/zaptest"

	appchannels "telesrv/internal/app/channels"
	appdialogs "telesrv/internal/app/dialogs"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

// forumGeneralTopMsgID is what stock tdesktop puts in top_msg_id when it has no
// topic to name -- Data::ForumTopic::kGeneralId. Attaching a file to a comment
// on a channel post takes that path even though a linked discussion group is a
// plain megagroup, so the client sends reply_to_msg_id=<root> together with
// top_msg_id=1 while the same comment typed as text sends top_msg_id=<root>.
const forumGeneralTopMsgID = 1

// A comment carrying a top_msg_id that disagrees with the thread the reply
// target belongs to used to be refused with REPLY_MESSAGE_ID_INVALID, which
// made every media comment on a channel post fail while text went through.
// Outside a forum that field selects nothing -- the root is whatever the reply
// target belongs to -- so it is now normalised instead of refused.
func TestCommentAcceptsMismatchedTopMsgIDOutsideForum(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserStore()
	owner, _ := users.Create(ctx, domain.User{AccessHash: 501, Phone: "15550005001", FirstName: "Owner"})
	channelStore := memory.NewChannelStore()
	channels := appchannels.NewService(channelStore, appchannels.WithBotProfileResolver(emptyDiscussionBotProfiles{}))
	dialogs := appdialogs.NewService(memory.NewDialogStore(), channelStore)
	r := New(Config{}, Deps{Users: appusers.NewService(users), Channels: channels, Dialogs: dialogs}, zaptest.NewLogger(t), clock.System)

	broadcast, err := channels.CreateChannel(ctx, owner.ID, domain.CreateChannelRequest{Title: "Channel", Broadcast: true, Date: 1700005001})
	if err != nil {
		t.Fatalf("create broadcast: %v", err)
	}
	group, err := channels.CreateMegagroupFromCreateChat(ctx, owner.ID, domain.CreateChannelRequest{Title: "Comments", Date: 1700005002})
	if err != nil {
		t.Fatalf("create discussion group: %v", err)
	}
	if ok, err := r.onChannelsSetDiscussionGroup(WithUserID(ctx, owner.ID), &tg.ChannelsSetDiscussionGroupRequest{
		Broadcast: &tg.InputChannel{ChannelID: broadcast.Channel.ID, AccessHash: broadcast.Channel.AccessHash},
		Group:     &tg.InputChannel{ChannelID: group.Channel.ID, AccessHash: group.Channel.AccessHash},
	}); err != nil || !ok {
		t.Fatalf("link discussion group = %v %v", ok, err)
	}
	postUpdates, err := r.onMessagesSendMessage(WithUserID(ctx, owner.ID), &tg.MessagesSendMessageRequest{
		Peer:    &tg.InputPeerChannel{ChannelID: broadcast.Channel.ID, AccessHash: broadcast.Channel.AccessHash},
		Message: "post", RandomID: 5001,
	})
	if err != nil {
		t.Fatalf("send post: %v", err)
	}
	post := postUpdates.(*tg.Updates).Updates[1].(*tg.UpdateNewChannelMessage).Message.(*tg.Message)
	discussion, err := r.onMessagesGetDiscussionMessage(WithUserID(ctx, owner.ID), &tg.MessagesGetDiscussionMessageRequest{
		Peer: &tg.InputPeerChannel{ChannelID: broadcast.Channel.ID, AccessHash: broadcast.Channel.AccessHash}, MsgID: post.ID,
	})
	if err != nil || len(discussion.Messages) != 1 {
		t.Fatalf("get discussion message: messages=%d err=%v", len(discussion.Messages), err)
	}
	root := discussion.Messages[0].(*tg.Message)
	if root.ID == forumGeneralTopMsgID {
		t.Fatalf("discussion root id is %d, which makes the mismatch untestable", root.ID)
	}
	groupPeer := &tg.InputPeerChannel{ChannelID: group.Channel.ID, AccessHash: group.Channel.AccessHash}

	// What the client sends when a file is attached: the right reply target,
	// the wrong thread root.
	mediaReq := &tg.MessagesSendMediaRequest{
		Peer:     groupPeer,
		Media:    &tg.InputMediaContact{PhoneNumber: "15550009999", FirstName: "Someone"},
		Message:  "file caption",
		RandomID: 5002,
	}
	mediaReq.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: root.ID, TopMsgID: forumGeneralTopMsgID})
	if _, err := r.onMessagesSendMedia(WithUserID(ctx, owner.ID), mediaReq); err != nil {
		t.Fatalf("media comment with top_msg_id=%d: %v", forumGeneralTopMsgID, err)
	}

	// The same mismatch over the text path, so the fix is not mistaken for
	// something specific to sendMedia.
	textReq := &tg.MessagesSendMessageRequest{Peer: groupPeer, Message: "text comment", RandomID: 5003}
	textReq.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: root.ID, TopMsgID: forumGeneralTopMsgID})
	if _, err := r.onMessagesSendMessage(WithUserID(ctx, owner.ID), textReq); err != nil {
		t.Fatalf("text comment with top_msg_id=%d: %v", forumGeneralTopMsgID, err)
	}

	// Both land in the thread the reply target belongs to, not in thread 1.
	replies, err := r.onMessagesGetReplies(WithUserID(ctx, owner.ID), &tg.MessagesGetRepliesRequest{
		Peer: groupPeer, MsgID: root.ID, Limit: 20,
	})
	if err != nil {
		t.Fatalf("get replies: %v", err)
	}
	page, ok := replies.(*tg.MessagesChannelMessages)
	if !ok || len(page.Messages) != 2 {
		t.Fatalf("thread of root %d = %T with %d messages, want both comments", root.ID, replies, len(page.Messages))
	}
}

// Inside a forum top_msg_id really does pick a topic, so a value that disagrees
// with the reply target's thread stays a client error.
func TestForumReplyStillRefusesMismatchedTopMsgID(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserStore()
	owner, _ := users.Create(ctx, domain.User{AccessHash: 502, Phone: "15550005010", FirstName: "ForumOwner"})
	channelStore := memory.NewChannelStore()
	channels := appchannels.NewService(channelStore, appchannels.WithBotProfileResolver(emptyDiscussionBotProfiles{}))
	dialogs := appdialogs.NewService(memory.NewDialogStore(), channelStore)
	r := New(Config{}, Deps{Users: appusers.NewService(users), Channels: channels, Dialogs: dialogs}, zaptest.NewLogger(t), clock.System)

	forum, err := channels.CreateMegagroupFromCreateChat(ctx, owner.ID, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Forum", Forum: true, Date: 1700005010,
	})
	if err != nil {
		t.Fatalf("create forum: %v", err)
	}
	forumPeer := &tg.InputPeerChannel{ChannelID: forum.Channel.ID, AccessHash: forum.Channel.AccessHash}
	sent, err := r.onMessagesSendMessage(WithUserID(ctx, owner.ID), &tg.MessagesSendMessageRequest{
		Peer: forumPeer, Message: "in general", RandomID: 5011,
	})
	if err != nil {
		t.Fatalf("send into forum: %v", err)
	}
	target := sent.(*tg.Updates).Updates[1].(*tg.UpdateNewChannelMessage).Message.(*tg.Message)

	req := &tg.MessagesSendMessageRequest{Peer: forumPeer, Message: "wrong topic", RandomID: 5012}
	req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: target.ID, TopMsgID: target.ID + 1000})
	_, err = r.onMessagesSendMessage(WithUserID(ctx, owner.ID), req)
	if !tgerr.Is(err, "REPLY_MESSAGE_ID_INVALID") {
		t.Fatalf("forum reply with a mismatched top_msg_id err = %v, want REPLY_MESSAGE_ID_INVALID", err)
	}
}
