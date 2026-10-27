package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/wmentor/go-magnetar/internal/common"
	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/printer"
)

const smtpDefaultTimeout = 30 * time.Second

var (
	ErrInvalidConfig    = errors.New("smtp: invalid config")
	ErrMissingHost      = errors.New("smtp: server host is not configured")
	ErrMissingFromEmail = errors.New("smtp: sender email (smtp.from.email) is not configured")
	ErrMissingRecipient = errors.New("smtp: recipient (to) is required")
	ErrMissingSubject   = errors.New("smtp: subject is required")
)

// SenderFunc sends a raw RFC 5322 message via an SMTP server.
type SenderFunc func(ctx context.Context, cfg ServerConfig, from string, to []string, msg []byte) error

// ServerConfig holds SMTP connection settings resolved from configuration.
type ServerConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      bool
}

// EmailMessage represents an outgoing email to be formatted and sent.
type EmailMessage struct {
	FromName    string
	FromEmail   string
	To          []string
	Subject     string
	Body        string
	IsHTML      bool
	Attachments []string
}

// SMTPTools provides the send_mail LLM tool for sending emails via SMTP.
type SMTPTools struct {
	cfg    *config.Config
	sender SenderFunc
}

// New creates a new SMTPTools instance.
func New(cfg *config.Config) (*SMTPTools, error) {
	if cfg == nil {
		return nil, ErrInvalidConfig
	}
	return &SMTPTools{
		cfg:    cfg,
		sender: defaultSendMail,
	}, nil
}

// StaticDefinition returns the OpenAI tool schema for send_mail.
func StaticDefinition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "send_mail",
			Description: "Send an email via the configured SMTP server (supports plain text, HTML bodies, and file attachments)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to": map[string]any{
						"type":        "string",
						"description": "Recipient email address(es), comma-separated if multiple",
					},
					"subject": map[string]any{
						"type":        "string",
						"description": "Email subject line",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Email body content (plain text or HTML)",
					},
					"attachments": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Optional list of local file paths to attach to the email",
					},
				},
				"required": []string{"to", "subject", "body"},
			},
		},
	}
}

// Definition returns the OpenAI tool schema for send_mail.
func (s *SMTPTools) Definition() openai.Tool {
	return StaticDefinition()
}

type sendMailParams struct {
	To          any    `json:"to"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	Attachments any    `json:"attachments,omitempty"`
	HTML        *bool  `json:"html,omitempty"`
}

// Dispatch handles a tool call by name, parsing JSON args and returning the result string.
func (s *SMTPTools) Dispatch(name string, args string) string {
	switch name {
	case "send_mail", "send_email":
		var params sendMailParams
		if err := json.Unmarshal([]byte(args), &params); err != nil {
			printer.ToolCall(printer.IconError, "send_mail: failed to parse args", "args", args, "err", err)
			return "error: failed to parse arguments"
		}

		recipients := parseStringList(params.To)
		attachments := parseStringList(params.Attachments)
		isHTML := detectHTML(params.Body)
		if params.HTML != nil {
			isHTML = *params.HTML
		}

		if err := s.SendMail(context.Background(), recipients, params.Subject, params.Body, isHTML, attachments); err != nil {
			printer.Error("send_mail failed", "to", strings.Join(recipients, ","), "err", err)
			return fmt.Sprintf("error: %v", err)
		}

		return fmt.Sprintf("Email sent successfully to %s", strings.Join(recipients, ", "))
	default:
		return "error: unknown tool " + name
	}
}

// SendMail validates configuration, builds the MIME message, and sends it via the SMTP server.
func (s *SMTPTools) SendMail(
	ctx context.Context,
	to []string,
	subject string,
	body string,
	isHTML bool,
	attachments []string,
) error {
	host := strings.TrimSpace(s.cfg.String("smtp.server.host"))
	if host == "" {
		return ErrMissingHost
	}

	port := s.cfg.Int("smtp.server.port")
	if port <= 0 {
		port = 465
	}

	fromEmail := strings.TrimSpace(s.cfg.String("smtp.from.email"))
	if fromEmail == "" {
		return ErrMissingFromEmail
	}
	fromName := strings.TrimSpace(s.cfg.String("smtp.from.name"))

	rcptAddrs, formattedTo, err := parseRecipients(to)
	if err != nil {
		return err
	}

	if strings.TrimSpace(subject) == "" {
		return ErrMissingSubject
	}

	printer.ToolCall(printer.IconTool, "send_mail", "to", strings.Join(rcptAddrs, ","), "subject", subject)

	msgBytes, err := BuildMessage(EmailMessage{
		FromName:    fromName,
		FromEmail:   fromEmail,
		To:          formattedTo,
		Subject:     subject,
		Body:        body,
		IsHTML:      isHTML,
		Attachments: attachments,
	})
	if err != nil {
		return err
	}

	srvCfg := ServerConfig{
		Host:     host,
		Port:     port,
		Username: s.cfg.String("smtp.server.username"),
		Password: s.cfg.String("smtp.server.password"),
		TLS:      s.cfg.Bool("smtp.server.tls"),
	}

	return s.sender(ctx, srvCfg, fromEmail, rcptAddrs, msgBytes)
}

// BuildMessage constructs a compliant RFC 5322 / MIME email payload.
func BuildMessage(email EmailMessage) ([]byte, error) {
	var buf bytes.Buffer

	fromHeader := email.FromEmail
	if email.FromName != "" {
		addr := mail.Address{Name: email.FromName, Address: email.FromEmail}
		fromHeader = addr.String()
	}

	fmt.Fprintf(&buf, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(email.To, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", email.Subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	contentType := "text/plain; charset=\"utf-8\""
	if email.IsHTML {
		contentType = "text/html; charset=\"utf-8\""
	}

	if len(email.Attachments) == 0 {
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n\r\n")
		writeBase64Lines(&buf, []byte(email.Body))
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mw.Boundary())

	bodyHeader := make(textproto.MIMEHeader)
	bodyHeader.Set("Content-Type", contentType)
	bodyHeader.Set("Content-Transfer-Encoding", "base64")
	bodyPart, err := mw.CreatePart(bodyHeader)
	if err != nil {
		return nil, fmt.Errorf("smtp: failed to create body part: %w", err)
	}
	writeBase64Lines(bodyPart, []byte(email.Body))

	for _, rawPath := range email.Attachments {
		cleanPath := common.ExpandHome(strings.TrimSpace(rawPath))
		if cleanPath == "" {
			continue
		}
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("smtp: failed to read attachment %q: %w", cleanPath, err)
		}

		baseName := filepath.Base(cleanPath)
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(baseName)))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		partHeader := make(textproto.MIMEHeader)
		partHeader.Set("Content-Type", mimeType)
		partHeader.Set("Content-Transfer-Encoding", "base64")
		partHeader.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", baseName))

		attPart, err := mw.CreatePart(partHeader)
		if err != nil {
			return nil, fmt.Errorf("smtp: failed to create attachment part for %q: %w", baseName, err)
		}
		writeBase64Lines(attPart, data)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("smtp: failed to finalize multipart message: %w", err)
	}

	return buf.Bytes(), nil
}

func writeBase64Lines(w interface{ Write([]byte) (int, error) }, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	const lineLen = 76
	for len(encoded) > lineLen {
		_, _ = w.Write([]byte(encoded[:lineLen] + "\r\n"))
		encoded = encoded[lineLen:]
	}
	if len(encoded) > 0 {
		_, _ = w.Write([]byte(encoded + "\r\n"))
	}
}

func parseStringList(raw any) []string {
	var out []string
	switch v := raw.(type) {
	case string:
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						out = append(out, trimmed)
					}
				}
			}
		}
	case []string:
		for _, s := range v {
			for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
	}
	return out
}

func parseRecipients(raw []string) ([]string, []string, error) {
	var addrs []string
	var formatted []string
	for _, entry := range raw {
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool { return r == ',' || r == ';' }) {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			parsed, err := mail.ParseAddress(trimmed)
			if err != nil {
				return nil, nil, fmt.Errorf("smtp: invalid recipient address %q: %w", trimmed, err)
			}
			addrs = append(addrs, parsed.Address)
			formatted = append(formatted, parsed.String())
		}
	}
	if len(addrs) == 0 {
		return nil, nil, ErrMissingRecipient
	}
	return addrs, formatted, nil
}

func detectHTML(body string) bool {
	lower := strings.ToLower(strings.TrimSpace(body))
	htmlMarkers := []string{"<!doctype html", "<html", "<body", "<p>", "<div", "<br", "<table", "<h1", "<h2", "<h3", "<ul", "<ol", "<a href="}
	for _, marker := range htmlMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func defaultSendMail(ctx context.Context, srv ServerConfig, from string, to []string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, smtpDefaultTimeout)
	defer cancel()

	addr := net.JoinHostPort(srv.Host, fmt.Sprintf("%d", srv.Port))
	tlsCfg := &tls.Config{
		ServerName: srv.Host,
		MinVersion: tls.VersionTLS12,
	}

	var conn net.Conn
	var err error
	implicitTLS := srv.TLS && srv.Port == 465

	if implicitTLS {
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: smtpDefaultTimeout},
			Config:    tlsCfg,
		}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	} else {
		dialer := &net.Dialer{Timeout: smtpDefaultTimeout}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp: failed to connect to %s: %w", addr, err)
	}
	defer conn.Close()

	client, err := netsmtp.NewClient(conn, srv.Host)
	if err != nil {
		return fmt.Errorf("smtp: failed to create client for %s: %w", addr, err)
	}
	defer client.Close()

	if srv.TLS && !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp: STARTTLS failed: %w", err)
			}
		}
	}

	if srv.Username != "" || srv.Password != "" {
		auth := netsmtp.PlainAuth("", srv.Username, srv.Password, srv.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: authentication failed: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL FROM failed: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp: RCPT TO %q failed: %w", rcpt, err)
		}
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA command failed: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp: writing message body failed: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp: closing DATA stream failed: %w", err)
	}

	return client.Quit()
}
