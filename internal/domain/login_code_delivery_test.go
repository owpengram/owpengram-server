package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateLoginCodeMessageTemplate(t *testing.T) {
	if err := ValidateLoginCodeMessageTemplate("Your code is {{code}}."); err != nil {
		t.Fatalf("exactly one {{code}} should be valid, got %v", err)
	}
	if err := ValidateLoginCodeMessageTemplate("No placeholder here."); !errors.Is(err, ErrLoginCodeMessageTemplateMissingCode) {
		t.Fatalf("zero occurrences should be rejected, got %v", err)
	}
	if err := ValidateLoginCodeMessageTemplate("{{code}} and again {{code}}."); !errors.Is(err, ErrLoginCodeMessageTemplateMissingCode) {
		t.Fatalf("two occurrences should be rejected, got %v", err)
	}
	if err := ValidateLoginCodeMessageTemplate(""); !errors.Is(err, ErrLoginCodeMessageTemplateMissingCode) {
		t.Fatalf("empty template should be rejected, got %v", err)
	}
}

func TestResolveLoginCodeMessageTemplatePrecedence(t *testing.T) {
	const panel = "panel override {{code}}"
	const env = "env default {{code}}"

	if got := ResolveLoginCodeMessageTemplate(panel, env); got != panel {
		t.Fatalf("panel override should win, got %q", got)
	}
	if got := ResolveLoginCodeMessageTemplate("", env); got != env {
		t.Fatalf("env default should win when panel unset, got %q", got)
	}
	if got := ResolveLoginCodeMessageTemplate("  ", env); got != env {
		t.Fatalf("whitespace-only panel override should be treated as unset, got %q", got)
	}
	if got := ResolveLoginCodeMessageTemplate("", ""); got != DefaultLoginCodeMessageTemplate {
		t.Fatalf("built-in default should be the final fallback, got %q", got)
	}
}

func TestOfficialLoginCodeMessageDynamicEntityOffset(t *testing.T) {
	SetOfficialSystemUserDisplayName("")
	defer SetOfficialSystemUserDisplayName("")

	// {{code}} is nowhere near a fixed prefix here -- it sits at the end of
	// a sentence, after other text -- proving the entity offset is computed
	// from where the placeholder actually landed, not assumed from a
	// hardcoded "Login code: " prefix the way the old %s-based
	// implementation did.
	template := "Please do not share your one-time code, which is: {{code}} -- thanks!"
	msg, err := OfficialLoginCodeMessage(7, template, "998877", 1700000000)
	if err != nil {
		t.Fatalf("OfficialLoginCodeMessage: %v", err)
	}
	wantBody := "Please do not share your one-time code, which is: 998877 -- thanks!"
	if msg.Body != wantBody {
		t.Fatalf("body = %q, want %q", msg.Body, wantBody)
	}
	if len(msg.Entities) != 1 {
		t.Fatalf("expected exactly one entity, got %d: %+v", len(msg.Entities), msg.Entities)
	}
	entity := msg.Entities[0]
	if entity.Type != MessageEntityBold {
		t.Fatalf("expected bold entity, got %v", entity.Type)
	}
	wantOffset := automaticEntityUTF16Length("Please do not share your one-time code, which is: ")
	if entity.Offset != wantOffset {
		t.Fatalf("offset = %d, want %d", entity.Offset, wantOffset)
	}
	if entity.Length != automaticEntityUTF16Length("998877") {
		t.Fatalf("length = %d, want %d", entity.Length, automaticEntityUTF16Length("998877"))
	}
}

func TestOfficialLoginCodeMessageOffsetShiftsWithServerNameSubstitution(t *testing.T) {
	SetOfficialSystemUserDisplayName("A Much Longer Custom Server Name")
	defer SetOfficialSystemUserDisplayName("")

	// {{server_name}} is substituted BEFORE {{code}}'s position is located,
	// so a longer server name shifts the code's offset. If the offset math
	// were still relying on a fixed/original position (e.g. computed
	// against the raw un-substituted template), this would land on the
	// wrong text.
	template := "Server {{server_name}} says your code is {{code}}."
	msg, err := OfficialLoginCodeMessage(7, template, "42", 1)
	if err != nil {
		t.Fatalf("OfficialLoginCodeMessage: %v", err)
	}
	wantBody := "Server A Much Longer Custom Server Name says your code is 42."
	if msg.Body != wantBody {
		t.Fatalf("body = %q, want %q", msg.Body, wantBody)
	}
	wantOffset := automaticEntityUTF16Length("Server A Much Longer Custom Server Name says your code is ")
	if len(msg.Entities) != 1 || msg.Entities[0].Offset != wantOffset {
		t.Fatalf("entities = %+v, want single bold entity at offset %d", msg.Entities, wantOffset)
	}
}

func TestOfficialLoginCodeMessageFallsBackWhenTemplateInvalid(t *testing.T) {
	SetOfficialSystemUserDisplayName("")
	defer SetOfficialSystemUserDisplayName("")

	// Defense in depth: a template that somehow reaches here without
	// {{code}} (or with it more than once) must never ship a message
	// silently missing the actual OTP -- it falls back to the compiled-in
	// default instead.
	for _, template := range []string{"", "no placeholder", "{{code}} twice {{code}}"} {
		msg, err := OfficialLoginCodeMessage(7, template, "13579", 1)
		if err != nil {
			t.Fatalf("template %q: OfficialLoginCodeMessage: %v", template, err)
		}
		if !strings.Contains(msg.Body, "13579") {
			t.Fatalf("template %q: fallback body missing code: %q", template, msg.Body)
		}
		if len(msg.Entities) != 1 || msg.Entities[0].Length != automaticEntityUTF16Length("13579") {
			t.Fatalf("template %q: unexpected entities: %+v", template, msg.Entities)
		}
	}
}

func TestOfficialLoginCodeMessageValidation(t *testing.T) {
	if _, err := OfficialLoginCodeMessage(0, "{{code}}", "12345", 1); !errors.Is(err, ErrLoginCodeDeliveryInvalid) {
		t.Fatalf("expected invalid user id to be rejected, got %v", err)
	}
	if _, err := OfficialLoginCodeMessage(7, "{{code}}", "", 1); !errors.Is(err, ErrLoginCodeDeliveryInvalid) {
		t.Fatalf("expected empty code to be rejected, got %v", err)
	}
	if _, err := OfficialLoginCodeMessage(OfficialSystemUserID, "{{code}}", "12345", 1); !errors.Is(err, ErrLoginCodeDeliveryInvalid) {
		t.Fatalf("expected system user id to be rejected, got %v", err)
	}
}
