package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// OfficialWelcomeMessage builds the account-visible incoming message sent
// from the official system account on every completed sign-in (SignUp and
// every subsequent SignIn/SignInWithEmail), regardless of delivery channel.
// Unlike OfficialLoginCodeMessage this never embeds a secret, so it is safe
// to send unconditionally — it exists to give the account owner (and, on a
// self-hosted single-admin server, that's usually also "the admin") a
// visible record of every session start.
//
// body is the already-resolved, already-{{server_name}}-substituted message
// text (see ResolveWelcomeMessageTemplate / RenderWelcomeMessageTemplate in
// login_welcome_template.go) -- resolving it requires the identity store and
// config, both of which live above this package, so callers (internal/app/auth)
// do that and pass the final text in here.
func OfficialWelcomeMessage(userID int64, body string, date int) (Message, error) {
	body = strings.TrimSpace(body)
	if userID <= 0 || IsSystemUserID(userID) || body == "" || date < 0 || date > math.MaxInt32 {
		return Message{}, fmt.Errorf("%w: user=%d date=%d", ErrLoginCodeDeliveryInvalid, userID, date)
	}
	return Message{
		OwnerUserID: userID,
		Peer:        Peer{Type: PeerTypeUser, ID: OfficialSystemUserID},
		From:        Peer{Type: PeerTypeUser, ID: OfficialSystemUserID},
		Date:        date,
		Body:        body,
	}, nil
}

// SignInMethodLabel returns the human-readable method name embedded in
// OfficialWelcomeMessage. Email-signup accounts are identified by
// SignupEmail (see NewEmailSignupDisplayPhone) rather than by their stored
// Phone, which — once assigned — is an ordinary-looking short number that
// carries no information about the signup method.
func SignInMethodLabel(u User) string {
	if u.SignupEmail != "" {
		return "email"
	}
	return "phone number"
}

const (
	MaxWelcomeMessagesPerPeer     = 5
	MaxWelcomeMessageContentBytes = 4 << 20
	InitialWelcomeRevision        = int64(1)
	WelcomeMessageDeliveryTTL     = 24 * time.Hour
)

var (
	ErrWelcomeMessageInvalid          = errors.New("welcome message invalid")
	ErrWelcomeMessagePeerInvalid      = errors.New("welcome message peer invalid")
	ErrWelcomeMessageForbidden        = errors.New("welcome message forbidden")
	ErrWelcomeMessageNotFound         = errors.New("welcome message not found")
	ErrWelcomeMessageNotModified      = errors.New("welcome message not modified")
	ErrWelcomeMessageLimit            = errors.New("welcome message limit exceeded")
	ErrWelcomeMessageRandomIDConflict = errors.New("welcome message random id conflict")
	ErrWelcomeMessageRevisionOverflow = errors.New("welcome message revision overflow")
)

// WelcomeMessageContent is a durable peer template. It intentionally does not
// contain transient receiver, device, callback, report, TTL or reply fields.
type WelcomeMessageContent struct {
	Message     string
	Entities    []MessageEntity
	Media       *MessageMedia
	ReplyMarkup *MessageReplyMarkup
	RichMessage *MessageRichMessage
	InvertMedia bool
	NoForwards  bool
}

func (c WelcomeMessageContent) Validate() error {
	if err := ValidateEphemeralContent(EphemeralContent{
		Message: c.Message, Entities: c.Entities, Media: c.Media,
		ReplyMarkup: c.ReplyMarkup, RichMessage: c.RichMessage,
	}); err != nil {
		return ErrWelcomeMessageInvalid
	}
	if c.InvertMedia && c.Media == nil {
		return ErrWelcomeMessageInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxWelcomeMessageContentBytes {
		return ErrWelcomeMessageInvalid
	}
	return nil
}

type WelcomeMessage struct {
	ID                int
	Peer              Peer
	CreatorUserID     int64
	Date              int
	EditDate          int
	RandomID          int64
	Content           WelcomeMessageContent
	CreateFingerprint [32]byte
	Version           uint64
}

func (m WelcomeMessage) ValidateStored() error {
	if m.ID <= 0 || m.ID > MaxMessageBoxID || m.Peer.Type != PeerTypeChannel || m.Peer.ID <= 0 ||
		m.CreatorUserID <= 0 || m.Date <= 0 || m.RandomID == 0 || m.Version == 0 ||
		(m.EditDate != 0 && m.EditDate < m.Date) || m.CreateFingerprint == ([32]byte{}) {
		return ErrWelcomeMessageInvalid
	}
	return m.Content.Validate()
}

type CreateWelcomeMessageRequest struct {
	Peer              Peer
	CreatorUserID     int64
	Date              int
	RandomID          int64
	Content           WelcomeMessageContent
	CreateFingerprint [32]byte
}

func (r CreateWelcomeMessageRequest) Validate() error {
	if r.Peer.Type != PeerTypeChannel || r.Peer.ID <= 0 || r.CreatorUserID <= 0 ||
		r.Date <= 0 || r.RandomID == 0 || r.CreateFingerprint == ([32]byte{}) {
		return ErrWelcomeMessageInvalid
	}
	return r.Content.Validate()
}

type WelcomeMessageEditFields struct {
	SetMessage     bool
	Message        string
	SetEntities    bool
	Entities       []MessageEntity
	SetMedia       bool
	Media          *MessageMedia
	SetReplyMarkup bool
	ReplyMarkup    *MessageReplyMarkup
	SetRichMessage bool
	RichMessage    *MessageRichMessage
	SetInvertMedia bool
	InvertMedia    bool
}

func (f WelcomeMessageEditFields) Empty() bool {
	return !f.SetMessage && !f.SetEntities && !f.SetMedia && !f.SetReplyMarkup &&
		!f.SetRichMessage && !f.SetInvertMedia
}

func (f WelcomeMessageEditFields) Apply(current WelcomeMessageContent) (WelcomeMessageContent, error) {
	if f.Empty() {
		return WelcomeMessageContent{}, ErrWelcomeMessageInvalid
	}
	if f.SetMessage {
		current.Message = f.Message
	}
	if f.SetEntities {
		current.Entities = f.Entities
	}
	if f.SetMedia {
		current.Media = f.Media
	}
	if f.SetReplyMarkup {
		current.ReplyMarkup = f.ReplyMarkup
	}
	if f.SetRichMessage {
		current.RichMessage = f.RichMessage
	}
	if f.SetInvertMedia {
		current.InvertMedia = f.InvertMedia
	}
	if err := current.Validate(); err != nil {
		return WelcomeMessageContent{}, err
	}
	return current, nil
}

type EditWelcomeMessageRequest struct {
	Peer     Peer
	ID       int
	EditDate int
	Fields   WelcomeMessageEditFields
}

func (r EditWelcomeMessageRequest) Validate() error {
	if r.Peer.Type != PeerTypeChannel || r.Peer.ID <= 0 || r.ID <= 0 ||
		r.ID > MaxMessageBoxID || r.EditDate <= 0 || r.Fields.Empty() {
		return ErrWelcomeMessageInvalid
	}
	return nil
}

type WelcomeMessageList struct {
	Hash        int64
	Messages    []WelcomeMessage
	NotModified bool
}

// WelcomeMessageDelivery is a short-lived, non-PTS snapshot created in the
// same transaction as one inactive->active membership transition. It is not a
// message-history row and must be physically removed no later than ExpiresAt.
type WelcomeMessageDelivery struct {
	ID           int64
	JoinEventID  int64
	ChannelID    int64
	TargetUserID int64
	TemplateID   int
	EphemeralID  int
	JoinedAt     int
	Content      WelcomeMessageContent
	AttemptCount int
	ExpiresAt    time.Time
}

func (d WelcomeMessageDelivery) ValidateStored(now time.Time) error {
	if d.ID <= 0 || d.JoinEventID <= 0 || d.ChannelID <= 0 || d.TargetUserID <= 0 ||
		d.TemplateID <= 0 || d.EphemeralID <= 0 || d.EphemeralID > MaxMessageBoxID ||
		d.JoinedAt <= 0 || d.AttemptCount <= 0 || d.ExpiresAt.IsZero() || !d.ExpiresAt.After(now) {
		return ErrWelcomeMessageInvalid
	}
	return d.Content.Validate()
}

func WelcomeCreateFingerprint(peer Peer, creatorUserID, randomID int64, content WelcomeMessageContent) ([32]byte, error) {
	raw, err := json.Marshal(struct {
		Peer          Peer
		CreatorUserID int64
		RandomID      int64
		Content       WelcomeMessageContent
	}{peer, creatorUserID, randomID, content})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func NextWelcomeRevision(current int64) (int64, error) {
	if current < InitialWelcomeRevision || current == math.MaxInt64 {
		return 0, ErrWelcomeMessageRevisionOverflow
	}
	return current + 1, nil
}
