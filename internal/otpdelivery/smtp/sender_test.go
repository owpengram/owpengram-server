package smtp

import (
	"context"
	"strings"
	"testing"
	"time"

	"telesrv/internal/otpdelivery"
)

func TestSenderRejectsNonEmailChannelBeforeDial(t *testing.T) {
	sender := New(Config{Host: "smtp.example.test", Port: 25, From: "noreply@example.test"})
	_, err := sender.Deliver(context.Background(), otpdelivery.Request{
		DeliveryID: "otp_sms",
		Purpose:    otpdelivery.PurposeLoginSMS,
		Channel:    otpdelivery.ChannelSMS,
		Recipient:  "15550001001",
		Code:       "12345",
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	if err == nil || !strings.Contains(err.Error(), "cannot deliver") {
		t.Fatalf("err = %v", err)
	}
}

func TestHumanTTLRoundsNetworkSkew(t *testing.T) {
	if got := humanTTL(5*time.Minute - 200*time.Millisecond); got != "5 minutes" {
		t.Fatalf("humanTTL = %q", got)
	}
}

// TestEmailContentUsesConfiguredAppName asserts the login-code email is
// branded with the operator's configured product name, not the package's
// internal "telesrv" fallback — the subject/body previously always said
// "Your telesrv login code" regardless of Config.AppName.
func TestEmailContentUsesConfiguredAppName(t *testing.T) {
	subject, body := emailContent("OwpenGram", "12345", 5*time.Minute)
	if subject != "Your OwpenGram login code" {
		t.Fatalf("subject = %q, want %q", subject, "Your OwpenGram login code")
	}
	if !strings.Contains(body, "Your OwpenGram login code is 12345.") {
		t.Fatalf("body missing branded code line: %q", body)
	}
	if strings.Contains(subject, "telesrv") || strings.Contains(body, "telesrv") {
		t.Fatalf("email still mentions telesrv instead of the configured app name:\nsubject=%q\nbody=%q", subject, body)
	}
}

// TestNewDefaultsAppNameToTelesrvWhenUnset asserts the package's own
// self-contained fallback (used only if a caller forgets to pass AppName)
// still works, mirroring FromName/TLSMode/Timeout's existing defaulting.
func TestNewDefaultsAppNameToTelesrvWhenUnset(t *testing.T) {
	sender := New(Config{Host: "smtp.example.test", Port: 25, From: "noreply@example.test"})
	if sender.cfg.AppName != "telesrv" {
		t.Fatalf("cfg.AppName = %q, want default %q", sender.cfg.AppName, "telesrv")
	}
}
