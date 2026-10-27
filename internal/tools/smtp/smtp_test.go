package smtp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wmentor/go-magnetar/internal/config"
)

func newTestConfig(t *testing.T, extraYAML string) *config.Config {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	content := "version: \"1.0\"\n" + extraYAML
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func TestSendMailPlainTextAndHTML(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t, `
smtp:
  enable: true
  from:
    name: "John Doe"
    email: "noreply@example.com"
  server:
    host: "smtp.example.com"
    port: 465
    username: "user1"
    password: "secret"
    tls: true
`)

	tools, err := New(cfg)
	if err != nil {
		t.Fatalf("New unexpected error: %v", err)
	}

	var capturedSrv ServerConfig
	var capturedFrom string
	var capturedTo []string
	var capturedMsg string

	tools.sender = func(_ context.Context, srv ServerConfig, from string, to []string, msg []byte) error {
		capturedSrv = srv
		capturedFrom = from
		capturedTo = to
		capturedMsg = string(msg)
		return nil
	}

	res := tools.Dispatch("send_mail", `{"to":"alice@example.com, bob@example.com","subject":"Weekly Report","body":"<h1>Hello</h1><p>Status green.</p>"}`)
	if !strings.Contains(res, "Email sent successfully") {
		t.Fatalf("unexpected dispatch response: %s", res)
	}

	if capturedSrv.Host != "smtp.example.com" || capturedSrv.Port != 465 || !capturedSrv.TLS {
		t.Errorf("unexpected server config: %+v", capturedSrv)
	}
	if capturedFrom != "noreply@example.com" {
		t.Errorf("expected from noreply@example.com, got %q", capturedFrom)
	}
	if len(capturedTo) != 2 || capturedTo[0] != "alice@example.com" || capturedTo[1] != "bob@example.com" {
		t.Errorf("unexpected recipients: %+v", capturedTo)
	}
	if !strings.Contains(capturedMsg, "Content-Type: text/html; charset=\"utf-8\"") {
		t.Errorf("expected HTML Content-Type in message:\n%s", capturedMsg)
	}
	if !strings.Contains(capturedMsg, `"John Doe" <noreply@example.com>`) {
		t.Errorf("expected formatted From header in message:\n%s", capturedMsg)
	}
}

func TestSendMailWithAttachments(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t, `
smtp:
  enable: true
  from:
    email: "bot@example.com"
  server:
    host: "smtp.example.com"
    port: 587
    tls: false
`)

	tools, err := New(cfg)
	if err != nil {
		t.Fatalf("New unexpected error: %v", err)
	}

	attFile := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(attFile, []byte("attachment payload"), 0o644); err != nil {
		t.Fatalf("write attachment: %v", err)
	}

	var capturedMsg string
	tools.sender = func(_ context.Context, _ ServerConfig, _ string, _ []string, msg []byte) error {
		capturedMsg = string(msg)
		return nil
	}

	err = tools.SendMail(context.Background(), []string{"team@example.com"}, "Report Attached", "Plain body text", false, []string{attFile})
	if err != nil {
		t.Fatalf("SendMail with attachment failed: %v", err)
	}

	if !strings.Contains(capturedMsg, "Content-Type: multipart/mixed;") {
		t.Errorf("expected multipart/mixed Content-Type:\n%s", capturedMsg)
	}
	if !strings.Contains(capturedMsg, `filename="report.txt"`) {
		t.Errorf("expected attachment filename in message:\n%s", capturedMsg)
	}
}

func TestSendMailValidationErrors(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t, `
smtp:
  enable: true
  from:
    email: "bot@example.com"
  server:
    host: "smtp.example.com"
`)
	tools, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := tools.SendMail(context.Background(), nil, "Subj", "Body", false, nil); err == nil {
		t.Error("expected error for missing recipient")
	}
	if err := tools.SendMail(context.Background(), []string{"not-an-email"}, "Subj", "Body", false, nil); err == nil {
		t.Error("expected error for invalid recipient email")
	}
	if err := tools.SendMail(context.Background(), []string{"valid@example.com"}, "   ", "Body", false, nil); err == nil {
		t.Error("expected error for empty subject")
	}
}
