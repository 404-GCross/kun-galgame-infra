package mail

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"

	"api/pkg/config"
)

// Mailer handles email sending
type Mailer struct {
	cfg config.MailConfig
}

// NewMailer creates a new Mailer
func NewMailer(cfg config.MailConfig) *Mailer {
	return &Mailer{cfg: cfg}
}

// SendEmail sends an email
func (m *Mailer) SendEmail(to, subject, htmlBody string) error {
	if m.cfg.Host == "" {
		return fmt.Errorf("mail host not configured")
	}

	// Setup authentication
	auth := smtp.PlainAuth("", m.cfg.Account, m.cfg.Password, m.cfg.Host)

	// Build email headers
	headers := make(map[string]string)
	headers["From"] = fmt.Sprintf("%s <%s>", m.cfg.From, m.cfg.Account)
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"

	// Build message
	var msg bytes.Buffer
	for k, v := range headers {
		msg.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	// Connect to SMTP server
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)

	// Use TLS for port 587
	if m.cfg.Port == 587 {
		return m.sendWithTLS(addr, auth, to, msg.Bytes())
	}

	// Plain SMTP
	return smtp.SendMail(addr, auth, m.cfg.Account, []string{to}, msg.Bytes())
}

// sendWithTLS sends email using STARTTLS
func (m *Mailer) sendWithTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	// Connect
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer client.Close()

	// STARTTLS
	tlsConfig := &tls.Config{
		ServerName: m.cfg.Host,
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("failed to start TLS: %w", err)
	}

	// Authenticate
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	// Set sender
	if err := client.Mail(m.cfg.Account); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	// Set recipient
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("failed to set recipient: %w", err)
	}

	// Send message
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	return client.Quit()
}

// SendPasswordResetEmail sends a password reset email
func (m *Mailer) SendPasswordResetEmail(to, name, resetLink string) error {
	subject := "Reset Your Password - KUN Visual Novel"

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Reset Your Password</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); padding: 30px; text-align: center; border-radius: 10px 10px 0 0;">
        <h1 style="color: white; margin: 0;">KUN Visual Novel</h1>
    </div>
    <div style="background: #f9f9f9; padding: 30px; border-radius: 0 0 10px 10px;">
        <h2 style="color: #333;">Password Reset Request</h2>
        <p>Hello <strong>%s</strong>,</p>
        <p>We received a request to reset your password. Click the button below to create a new password:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s" style="background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 15px 30px; text-decoration: none; border-radius: 5px; font-weight: bold; display: inline-block;">Reset Password</a>
        </div>
        <p style="color: #666; font-size: 14px;">This link will expire in 1 hour.</p>
        <p style="color: #666; font-size: 14px;">If you didn't request this, please ignore this email. Your password will remain unchanged.</p>
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #999; font-size: 12px; text-align: center;">
            &copy; KUN Visual Novel. All rights reserved.
        </p>
    </div>
</body>
</html>
`, name, resetLink)

	return m.SendEmail(to, subject, htmlBody)
}

// SendVerificationEmail sends an email verification email
func (m *Mailer) SendVerificationEmail(to, name, verifyLink string) error {
	subject := "Verify Your Email - KUN Visual Novel"

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Verify Your Email</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); padding: 30px; text-align: center; border-radius: 10px 10px 0 0;">
        <h1 style="color: white; margin: 0;">KUN Visual Novel</h1>
    </div>
    <div style="background: #f9f9f9; padding: 30px; border-radius: 0 0 10px 10px;">
        <h2 style="color: #333;">Welcome to KUN Visual Novel!</h2>
        <p>Hello <strong>%s</strong>,</p>
        <p>Thank you for registering. Please verify your email address by clicking the button below:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s" style="background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 15px 30px; text-decoration: none; border-radius: 5px; font-weight: bold; display: inline-block;">Verify Email</a>
        </div>
        <p style="color: #666; font-size: 14px;">This link will expire in 24 hours.</p>
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #999; font-size: 12px; text-align: center;">
            &copy; KUN Visual Novel. All rights reserved.
        </p>
    </div>
</body>
</html>
`, name, verifyLink)

	return m.SendEmail(to, subject, htmlBody)
}

// SendWithTemplate sends an email using a custom template
func (m *Mailer) SendWithTemplate(to, subject, tmplStr string, data any) error {
	tmpl, err := template.New("email").Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return m.SendEmail(to, subject, buf.String())
}
