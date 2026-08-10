package service

import (
	"strings"

	"api/pkg/errors"
)

var allowedEmailDomains = map[string]struct{}{
	"qq.com":         {},
	"foxmail.com":    {},
	"163.com":        {},
	"126.com":        {},
	"yeah.net":       {},
	"sina.com":       {},
	"sina.cn":        {},
	"sohu.com":       {},
	"aliyun.com":     {},
	"139.com":        {},
	"189.cn":         {},
	"gmail.com":      {},
	"googlemail.com": {},
	"outlook.com":    {},
	"hotmail.com":    {},
	"live.com":       {},
	"msn.com":        {},
	"icloud.com":     {},
	"me.com":         {},
	"mac.com":        {},
	"yahoo.com":      {},
	"yahoo.co.jp":    {},
	"proton.me":      {},
	"protonmail.com": {},
	"pm.me":          {},
}

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
