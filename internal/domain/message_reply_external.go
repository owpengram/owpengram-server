package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"unicode/utf16"
	"unicode/utf8"
)

const MaxMessageReplyExternalBytes = 1 << 20

// MessageReplyExternal retains the source at send time, independently of its
// later edit/deletion. It never contains another reply or owner-local jump IDs.
type MessageReplyExternal struct {
	From     MessageForward  `json:"from"`
	Text     string          `json:"text"`
	Entities []MessageEntity `json:"entities,omitempty"`
	Media    *MessageMedia   `json:"media,omitempty"`
}

// Apply the same validation when this value is nested in an immutable send
// receipt. Otherwise json.Unmarshal there would discard unknown fields or
// accept an invalid author even though the message-box decoder rejects it.
func (v *MessageReplyExternal) UnmarshalJSON(b []byte) error {
	if len(b) > MaxMessageReplyExternalBytes {
		return fmt.Errorf("external reply snapshot exceeds size bound")
	}
	type snapshot MessageReplyExternal
	var out snapshot
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return fmt.Errorf("decode external reply: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("external reply trailing JSON")
	}
	if _, err := EncodeMessageReplyExternal((*MessageReplyExternal)(&out)); err != nil {
		return err
	}
	*v = MessageReplyExternal(out)
	return nil
}

func EncodeMessageReplyExternal(v *MessageReplyExternal) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	if v.From.Date <= 0 || v.From.From.ID <= 0 || (v.From.From.Type != PeerTypeUser && v.From.From.Type != PeerTypeChannel) || v.From.SavedFrom.ID != 0 || v.From.SavedFromMsgID != 0 || len(v.Entities) > MaxMessageEntityCount || !utf8.ValidString(v.Text) || utf8.RuneCountInString(v.Text) > MaxMessageTextLength {
		return nil, fmt.Errorf("invalid external reply snapshot")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode external reply: %w", err)
	}
	if len(b) > MaxMessageReplyExternalBytes {
		return nil, fmt.Errorf("external reply snapshot exceeds size bound")
	}
	return b, nil
}

func DecodeMessageReplyExternal(b []byte) (*MessageReplyExternal, error) {
	if len(b) == 0 || bytes.Equal(bytes.TrimSpace(b), []byte("{}")) {
		return nil, nil
	}
	if len(b) > MaxMessageReplyExternalBytes {
		return nil, fmt.Errorf("external reply snapshot exceeds size bound")
	}
	var out MessageReplyExternal
	if err := out.UnmarshalJSON(b); err != nil {
		return nil, err
	}
	return &out, nil
}

func NewMessageReplyExternal(source Message) (*MessageReplyExternal, error) {
	v := &MessageReplyExternal{From: MessageForward{From: source.From, Date: source.Date}, Text: source.Body, Entities: source.Entities, Media: source.Media}
	b, err := EncodeMessageReplyExternal(v)
	if err != nil {
		return nil, err
	}
	// A codec round trip detaches the source media, including nested slices.
	return DecodeMessageReplyExternal(b)
}

func ValidateExternalReplyQuote(reply *MessageReply, text string) error {
	if reply == nil {
		return nil
	}
	if len(reply.QuoteText) > MaxMessageReplyQuoteLength || !utf8.ValidString(reply.QuoteText) || len(reply.QuoteEntities) > MaxMessageEntityCount {
		return ErrQuoteTextInvalid
	}
	if reply.QuoteText == "" {
		if reply.QuoteOffset != 0 || len(reply.QuoteEntities) != 0 {
			return ErrQuoteTextInvalid
		}
		return nil
	}
	source, quote := utf16.Encode([]rune(text)), utf16.Encode([]rune(reply.QuoteText))
	start := reply.QuoteOffset
	if start < 0 || start > len(source) || len(quote) > len(source)-start {
		return ErrQuoteTextInvalid
	}
	for i, r := range quote {
		if source[start+i] != r {
			return ErrQuoteTextInvalid
		}
	}
	for _, e := range reply.QuoteEntities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset > len(quote) || e.Length > len(quote)-e.Offset {
			return ErrQuoteTextInvalid
		}
	}
	return nil
}

func CloneMessageReply(in *MessageReply) *MessageReply {
	if in == nil {
		return nil
	}
	out := *in
	out.QuoteEntities = append([]MessageEntity(nil), in.QuoteEntities...)
	if in.External != nil {
		x := *in.External
		x.Entities = append([]MessageEntity(nil), x.Entities...)
		if x.Media != nil {
			x.Media = cloneReplyData(reflect.ValueOf(x.Media)).Interface().(*MessageMedia)
		}
		out.External = &x
	}
	return &out
}

// Clone only our data model. This is not a protocol codec: it neither interprets
// TL fields nor converts bytes. Copying nested pointer/slice fields keeps media
// added to MessageMedia from silently becoming shared between owner snapshots.
func cloneReplyData(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneReplyData(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneReplyData(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneReplyData(iter.Value()))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneReplyData(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				out.Field(i).Set(cloneReplyData(v.Field(i)))
			}
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneReplyData(v.Index(i)))
		}
		return out
	default:
		return v
	}
}
