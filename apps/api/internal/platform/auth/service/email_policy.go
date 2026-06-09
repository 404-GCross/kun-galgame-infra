package service

import (
	"strings"

	"api/pkg/errors"
)

// allowedEmailDomains is the allowlist of email providers accepted for NEW
// emails — i.e. registration and email-change. It is deliberately NOT applied
// to login, forgot-password, or admin edits, so existing accounts on other
// providers keep working. Keys are lowercase and matched exactly against the
// part after the final "@".
var allowedEmailDomains = map[string]struct{}{
	"qq.com":      {},
	"gmail.com":   {},
	"outlook.com": {},
}

// checkEmailDomainAllowed returns an AppError when email's domain isn't in
// allowedEmailDomains. Returns nil for an accepted domain. The email is
// assumed to already be syntactically valid (DTO `validate:"email"`), so a
// missing "@" is treated as an invalid email rather than a domain rejection.
func checkEmailDomainAllowed(email string) error {
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return errors.NewWithCode(errors.ErrAuthInvalidEmail)
	}
	domain := strings.ToLower(strings.TrimSpace(email[at+1:]))
	if _, ok := allowedEmailDomains[domain]; !ok {
		return errors.NewWithCode(errors.ErrAuthEmailDomainNotAllowed)
	}
	return nil
}
