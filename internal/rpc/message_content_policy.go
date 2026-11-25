package rpc

import (
	"context"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// checkMessageTextPolicy applies the anti-abuse text policy to outgoing
// message text and media captions (domain.ClassifyMessageText): known
// abuse-client protocol markers and bare base64 public keys are refused with
// MESSAGE_CONTENT_BLOCKED; armored keys and messages dominated by an opaque
// random-looking token are only logged ("message content policy hit") so
// moderators can spot accounts that exchange key material or ciphertext.
// Text content is never logged.
func (r *Router) checkMessageTextPolicy(ctx context.Context, method, text string) error {
	verdict := domain.ClassifyMessageText(text)
	if verdict == domain.MessageTextOK {
		return nil
	}
	if r != nil && r.log != nil {
		userID, _ := UserIDFrom(ctx)
		fields := append([]zap.Field{
			zap.String("method", method),
			zap.String("verdict", verdict.String()),
			zap.Bool("blocked", verdict.Blocks()),
			zap.Int64("user_id", userID),
			zap.Int("text_len", utf8.RuneCountInString(text)),
		}, r.contextLogFields(ctx)...)
		r.log.Warn("message content policy hit", fields...)
	}
	if r != nil && r.deps.ContentPolicyReporter != nil {
		userID, _ := UserIDFrom(ctx)
		r.deps.ContentPolicyReporter.ReportContentPolicyHit(ctx, domain.ContentPolicyHit{
			UserID:  userID,
			Source:  "message",
			Kind:    verdict.String(),
			Blocked: verdict.Blocks(),
			Size:    int64(len(text)),
			At:      time.Now(),
		})
	}
	if verdict.Blocks() {
		return messageContentBlockedErr()
	}
	return nil
}
