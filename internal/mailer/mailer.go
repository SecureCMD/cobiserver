// Package mailer sends password-reset emails over SMTP when configured.
// When no SMTP host is set, Mailer is nil-safe and simply reports that it
// is disabled so callers can fall back to a server-log-based recovery flow.
package mailer

import (
	"fmt"
	"net/smtp"
)

type Mailer struct {
	host     string
	port     string
	user     string
	pass     string
	from     string
	startTLS bool
}

func New(host, port, user, pass, from string, startTLS bool) *Mailer {
	if host == "" {
		return nil
	}
	return &Mailer{host: host, port: port, user: user, pass: pass, from: from, startTLS: startTLS}
}

func (m *Mailer) Enabled() bool { return m != nil }

func (m *Mailer) SendPasswordReset(to, resetURL string) error {
	if m == nil {
		return fmt.Errorf("mailer disabled")
	}
	subject := "Password reset for your KOReader sync server"
	body := fmt.Sprintf(
		"Someone (hopefully you) requested a password reset.\r\n\r\n"+
			"Reset your password using this link (valid for 1 hour):\r\n%s\r\n\r\n"+
			"If you did not request this, you can safely ignore this email.\r\n",
		resetURL,
	)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		m.from, to, subject, body)

	addr := m.host + ":" + m.port
	var auth smtp.Auth
	if m.user != "" {
		auth = smtp.PlainAuth("", m.user, m.pass, m.host)
	}
	return smtp.SendMail(addr, auth, m.from, []string{to}, []byte(msg))
}
