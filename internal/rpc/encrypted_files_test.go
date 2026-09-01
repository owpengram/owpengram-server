package rpc

import (
	"bytes"
	"testing"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"

	"telesrv/internal/domain"
)

// TestSendEncryptedFileFlow：sendEncryptedFile 铸造 EncryptedFile、随消息投递、返回
// SentEncryptedFile{date,file}，且 InputEncryptedFile 复用路径能回查同一文件。
func TestSendEncryptedFileFlow(t *testing.T) {
	f := newEncryptedFixture(t)
	chatID, _ := f.acceptChat(t)
	chat, _, _ := f.store.GetSecretChat(f.ctx, chatID)
	f.sessions.reset()

	sent, err := f.router.onMessagesSendEncryptedFile(f.adminCtx(), &tg.MessagesSendEncryptedFileRequest{
		Peer:     tg.InputEncryptedChat{ChatID: chatID, AccessHash: chat.AdminAccessHash},
		RandomID: 71717,
		Data:     []byte{0x01, 0x02},
		File:     &tg.InputEncryptedFileUploaded{ID: 555, Parts: 1, KeyFingerprint: 7},
	})
	if err != nil {
		t.Fatalf("sendEncryptedFile: %v", err)
	}
	sf, ok := sent.(*tg.MessagesSentEncryptedFile)
	if !ok || sf.Date == 0 {
		t.Fatalf("response = %T %+v, want SentEncryptedFile{date,file}", sent, sent)
	}
	ef, ok := sf.File.(*tg.EncryptedFile)
	if !ok || ef.ID == 0 {
		t.Fatalf("response file = %T, want non-empty EncryptedFile", sf.File)
	}

	// 推送给 participant 的 updateNewEncryptedMessage 携带 file。
	recs := f.sessions.records()
	if len(recs) != 1 {
		t.Fatalf("send push = %d, want 1", len(recs))
	}
	em, ok := encNewMessagePayload(t, recs[0]).Message.(*tg.EncryptedMessage)
	if !ok {
		t.Fatalf("pushed message = %T, want EncryptedMessage", encNewMessagePayload(t, recs[0]).Message)
	}
	if _, ok := em.File.(*tg.EncryptedFile); !ok {
		t.Fatalf("pushed message file = %T, want EncryptedFile", em.File)
	}

	// InputEncryptedFile 复用：按 id+access_hash 回查到同一文件。
	ref, found, err := f.router.deps.SecretChats.GetEncryptedFile(f.ctx, ef.ID, ef.AccessHash)
	if err != nil || !found || ref.ID != ef.ID {
		t.Fatalf("GetEncryptedFile reuse = found %v id %d err %v", found, ref.ID, err)
	}
}

// TestUploadEncryptedFile：uploadEncryptedFile 铸造并返回 EncryptedFile。
func TestUploadEncryptedFile(t *testing.T) {
	f := newEncryptedFixture(t)
	chatID, _ := f.acceptChat(t)
	chat, _, _ := f.store.GetSecretChat(f.ctx, chatID)

	res, err := f.router.onMessagesUploadEncryptedFile(f.adminCtx(), &tg.MessagesUploadEncryptedFileRequest{
		Peer: tg.InputEncryptedChat{ChatID: chatID, AccessHash: chat.AdminAccessHash},
		File: &tg.InputEncryptedFileUploaded{ID: 888, Parts: 1, KeyFingerprint: 9},
	})
	if err != nil {
		t.Fatalf("uploadEncryptedFile: %v", err)
	}
	ef, ok := res.(*tg.EncryptedFile)
	if !ok || ef.ID == 0 {
		t.Fatalf("upload response = %T, want non-empty EncryptedFile", res)
	}
}

// TestEncryptedFileDownloadRequiresCapability：密聊 blob 只有在 id+access_hash 元数据能力
// 校验成功后才会转换为内部 enc:<id> key；错误 hash 不能触达 Files.GetFile。
func TestEncryptedFileDownloadRequiresCapability(t *testing.T) {
	f := newEncryptedFixture(t)
	chatID, _ := f.acceptChat(t)
	chat, _, _ := f.store.GetSecretChat(f.ctx, chatID)
	res, err := f.router.onMessagesUploadEncryptedFile(f.adminCtx(), &tg.MessagesUploadEncryptedFileRequest{
		Peer: tg.InputEncryptedChat{ChatID: chatID, AccessHash: chat.AdminAccessHash},
		File: &tg.InputEncryptedFileUploaded{ID: 888, Parts: 1, KeyFingerprint: 9},
	})
	if err != nil {
		t.Fatalf("uploadEncryptedFile: %v", err)
	}
	ef := res.(*tg.EncryptedFile)
	files := f.router.deps.Files.(*fakeFiles)
	files.getFileFound = true
	files.getFileChunk = domain.FileChunk{MimeType: "application/octet-stream", Bytes: []byte{1, 2, 3}}

	got, err := f.router.onUploadGetFile(f.adminCtx(), &tg.UploadGetFileRequest{
		Location: &tg.InputEncryptedFileLocation{ID: ef.ID, AccessHash: ef.AccessHash},
		Offset:   0,
		Limit:    1024,
	})
	if err != nil {
		t.Fatalf("get encrypted file: %v", err)
	}
	file, ok := got.(*tg.UploadFile)
	if !ok || !bytes.Equal(file.Bytes, []byte{1, 2, 3}) {
		t.Fatalf("download = %T %+v", got, got)
	}
	if files.getFileCalls != 1 || files.getFileRequest.LocationKey != "enc:9001" {
		t.Fatalf("GetFile calls/key = %d/%q", files.getFileCalls, files.getFileRequest.LocationKey)
	}

	_, err = f.router.onUploadGetFile(f.adminCtx(), &tg.UploadGetFileRequest{
		Location: &tg.InputEncryptedFileLocation{ID: ef.ID, AccessHash: ef.AccessHash + 1},
		Offset:   0,
		Limit:    1024,
	})
	if !tgerr.Is(err, "LOCATION_INVALID") {
		t.Fatalf("wrong access hash err = %v", err)
	}
	if files.getFileCalls != 1 {
		t.Fatalf("wrong access hash reached blob store: calls=%d", files.getFileCalls)
	}
}

func TestEncryptedDataLimit(t *testing.T) {
	f := newEncryptedFixture(t)
	chatID, _ := f.acceptChat(t)
	chat, _, _ := f.store.GetSecretChat(f.ctx, chatID)
	tooLong := make([]byte, domain.MaxSecretMessageDataBytes+1)

	_, err := f.router.onMessagesSendEncrypted(f.adminCtx(), &tg.MessagesSendEncryptedRequest{
		Peer: tg.InputEncryptedChat{ChatID: chatID, AccessHash: chat.AdminAccessHash}, RandomID: 1, Data: tooLong,
	})
	if !tgerr.Is(err, "DATA_TOO_LONG") {
		t.Fatalf("sendEncrypted oversized err = %v", err)
	}
	_, err = f.router.onMessagesSendEncryptedFile(f.adminCtx(), &tg.MessagesSendEncryptedFileRequest{
		Peer: tg.InputEncryptedChat{ChatID: chatID, AccessHash: chat.AdminAccessHash}, RandomID: 2, Data: tooLong,
		File: &tg.InputEncryptedFileUploaded{ID: 888, Parts: 1, KeyFingerprint: 9},
	})
	if !tgerr.Is(err, "DATA_TOO_LONG") {
		t.Fatalf("sendEncryptedFile oversized err = %v", err)
	}
}
