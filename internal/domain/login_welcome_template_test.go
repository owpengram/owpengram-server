package domain

import "testing"

func TestResolveWelcomeMessageTemplatePrecedence(t *testing.T) {
	const panel = "panel override"
	const env = "env default"

	if got := ResolveWelcomeMessageTemplate(LoginMethodPhone, panel, env); got != panel {
		t.Fatalf("panel override should win, got %q", got)
	}
	if got := ResolveWelcomeMessageTemplate(LoginMethodPhone, "", env); got != env {
		t.Fatalf("env default should win when panel unset, got %q", got)
	}
	if got := ResolveWelcomeMessageTemplate(LoginMethodPhone, "  ", env); got != env {
		t.Fatalf("whitespace-only panel override should be treated as unset, got %q", got)
	}
	if got := ResolveWelcomeMessageTemplate(LoginMethodPhone, "", ""); got != DefaultWelcomeMessagePhoneTemplate {
		t.Fatalf("built-in phone default should be the final fallback, got %q", got)
	}
	if got := ResolveWelcomeMessageTemplate(LoginMethodEmail, "", ""); got != DefaultWelcomeMessageEmailTemplate {
		t.Fatalf("built-in email default should be the final fallback, got %q", got)
	}
}

func TestLoginMethodFromLabel(t *testing.T) {
	if LoginMethodFromLabel("email") != LoginMethodEmail {
		t.Fatal("expected email label to map to LoginMethodEmail")
	}
	if LoginMethodFromLabel("phone number") != LoginMethodPhone {
		t.Fatal("expected phone label to map to LoginMethodPhone")
	}
	if LoginMethodFromLabel("") != LoginMethodPhone {
		t.Fatal("expected unknown label to default to LoginMethodPhone")
	}
}

func TestRenderWelcomeMessageTemplateSubstitutesServerName(t *testing.T) {
	SetOfficialSystemUserDisplayName("")
	defer SetOfficialSystemUserDisplayName("")

	got := RenderWelcomeMessageTemplate("Hello from {{server_name}}!")
	if got != "Hello from OwpenGram!" {
		t.Fatalf("expected default branding.ProductName substitution, got %q", got)
	}

	SetOfficialSystemUserDisplayName("Custom Server")
	got = RenderWelcomeMessageTemplate("Hello from {{server_name}}!")
	if got != "Hello from Custom Server!" {
		t.Fatalf("expected custom display name substitution, got %q", got)
	}

	// No placeholder present -- must be a no-op.
	if got := RenderWelcomeMessageTemplate("no placeholder here"); got != "no placeholder here" {
		t.Fatalf("expected no-op when placeholder absent, got %q", got)
	}
}
