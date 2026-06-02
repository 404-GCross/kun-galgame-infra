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

// ──────────────────────────────────────────
// Email layout
// ──────────────────────────────────────────
//
// Brand colors come from the project design system: primary #006FEE (鲲
// Galgame blue) for the header / CTA / code chip, and #ff4ecd (pink accent)
// for the thin rule under the header. Deliberately NO gradients and only a
// modest 6–8px radius so the mail matches the app's clean, flat KunUI look
// instead of the old generic purple-gradient / big-pill template.
//
// Layout uses a <table> + inline styles (not flexbox / external CSS) because
// that is the only thing rendered consistently across email clients
// (Gmail / Outlook / Apple Mail strip <style> blocks and modern CSS).

const (
	kunBrandPrimary = "#006FEE"
	kunBrandAccent  = "#ff4ecd"
)

// kunEmailShell wraps the per-mail inner HTML in the shared branded layout.
func kunEmailShell(heading, inner string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
</head>
<body style="margin:0; padding:24px 12px; background-color:#f4f5f7; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Helvetica,Arial,sans-serif; color:#1f2d3d;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:480px; margin:0 auto; background-color:#ffffff; border:1px solid #e6e8eb; border-radius:8px; overflow:hidden;">
<tr><td style="background-color:%s; padding:20px 28px;"><span style="color:#ffffff; font-size:18px; font-weight:700; letter-spacing:0.5px;">鲲 Galgame</span></td></tr>
<tr><td style="height:3px; line-height:0; font-size:0; background-color:%s;">&nbsp;</td></tr>
<tr><td style="padding:28px;">
<h2 style="margin:0 0 16px; font-size:18px; font-weight:600; color:#1f2d3d;">%s</h2>
%s
</td></tr>
<tr><td style="padding:16px 28px; border-top:1px solid #eef0f2; background-color:#fafbfc;">
<p style="margin:0; color:#9aa5b1; font-size:12px; text-align:center;">&copy; 鲲 Galgame · 本邮件由系统自动发送，请勿直接回复</p>
</td></tr>
</table>
</body>
</html>`, heading, kunBrandPrimary, kunBrandAccent, heading, inner)
}

// codeChip renders a centered verification-code block in the primary color.
func codeChip(code string) string {
	return fmt.Sprintf(`<div style="margin:24px 0; text-align:center;">
<span style="display:inline-block; padding:14px 28px; font-size:30px; font-weight:700; letter-spacing:8px; color:%s; background-color:#eef5ff; border:1px solid #cfe2ff; border-radius:8px; font-family:'SFMono-Regular',Consolas,Menlo,monospace;">%s</span>
</div>`, kunBrandPrimary, code)
}

// ctaButton renders a centered solid-primary call-to-action button.
func ctaButton(href, label string) string {
	return fmt.Sprintf(`<div style="margin:24px 0; text-align:center;">
<a href="%s" style="display:inline-block; padding:12px 30px; background-color:%s; color:#ffffff; text-decoration:none; font-size:14px; font-weight:600; border-radius:6px;">%s</a>
</div>`, href, kunBrandPrimary, label)
}

const (
	emailTextPara = `<p style="margin:0 0 10px; font-size:14px; color:#3e4c59;">%s</p>`
	emailHintPara = `<p style="margin:0; font-size:13px; color:#7b8794;">%s</p>`
)

// SendPasswordResetEmail sends a password reset email
func (m *Mailer) SendPasswordResetEmail(to, name, resetLink string) error {
	subject := "重置密码 - 鲲 Galgame"
	inner := fmt.Sprintf(emailTextPara, fmt.Sprintf("你好 <strong>%s</strong>，", name)) +
		fmt.Sprintf(emailTextPara, "我们收到了重置你账号密码的请求。点击下方按钮设置新密码：") +
		ctaButton(resetLink, "重置密码") +
		fmt.Sprintf(`<p style="margin:0 0 6px; font-size:13px; color:#7b8794;">%s</p>`, "该链接 1 小时内有效。") +
		fmt.Sprintf(emailHintPara, "如果你没有发起此请求，请忽略本邮件，你的密码不会发生变化。")
	return m.SendEmail(to, subject, kunEmailShell("重置密码", inner))
}

// SendVerificationEmail sends an email verification email
func (m *Mailer) SendVerificationEmail(to, name, verifyLink string) error {
	subject := "验证邮箱 - 鲲 Galgame"
	inner := fmt.Sprintf(emailTextPara, fmt.Sprintf("你好 <strong>%s</strong>，", name)) +
		fmt.Sprintf(emailTextPara, "感谢注册 鲲 Galgame！请点击下方按钮验证你的邮箱地址：") +
		ctaButton(verifyLink, "验证邮箱") +
		fmt.Sprintf(emailHintPara, "该链接 24 小时内有效。")
	return m.SendEmail(to, subject, kunEmailShell("验证邮箱", inner))
}

// SendRegisterCodeEmail sends a 6-digit verification code to a soon-to-be
// registered email address. Unlike SendEmailChangeCodeEmail (which targets
// the OLD address to defend against JWT theft), this one targets the NEW
// address itself — the whole point is to verify ownership of the proposed
// registration email before any account is created. `name` is the
// requested-but-not-yet-created username, used as a salutation.
// `ttlMinutes` is the same TTL the caller wrote into Redis, so the body
// text matches the actual code lifetime even if config changes.
func (m *Mailer) SendRegisterCodeEmail(to, name, code string, ttlMinutes int) error {
	subject := "注册验证码 - 鲲 Galgame"
	inner := fmt.Sprintf(emailTextPara, fmt.Sprintf("你好 <strong>%s</strong>，", name)) +
		fmt.Sprintf(emailTextPara, "欢迎注册 鲲 Galgame！请使用以下验证码完成注册：") +
		codeChip(code) +
		fmt.Sprintf(`<p style="margin:0 0 6px; font-size:13px; color:#7b8794;">验证码 %d 分钟内有效。</p>`, ttlMinutes) +
		fmt.Sprintf(emailHintPara, "如果你没有发起此操作，请忽略本邮件——你的邮箱不会被注册到任何账号。")
	return m.SendEmail(to, subject, kunEmailShell("完成账号注册", inner))
}

// SendEmailChangeCodeEmail sends an email change verification code to the user's current email.
// `ttlMinutes` mirrors what the caller wrote into Redis so body text == reality.
func (m *Mailer) SendEmailChangeCodeEmail(to, name, code string, ttlMinutes int) error {
	subject := "邮箱变更验证码 - 鲲 Galgame"
	inner := fmt.Sprintf(emailTextPara, fmt.Sprintf("你好 <strong>%s</strong>，", name)) +
		fmt.Sprintf(emailTextPara, "你正在修改账号的邮箱地址，请使用以下验证码完成操作：") +
		codeChip(code) +
		fmt.Sprintf(`<p style="margin:0 0 6px; font-size:13px; color:#7b8794;">验证码 %d 分钟内有效。</p>`, ttlMinutes) +
		fmt.Sprintf(emailHintPara, "如果你没有发起此操作，请忽略本邮件，你的账号信息不会发生变化。")
	return m.SendEmail(to, subject, kunEmailShell("邮箱变更验证", inner))
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
