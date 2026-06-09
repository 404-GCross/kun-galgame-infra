package service

import "testing"

func TestCheckEmailDomainAllowed(t *testing.T) {
	allowed := []string{
		"user@qq.com",
		"user@gmail.com",
		"user@outlook.com",
		"USER@QQ.COM",       // case-insensitive
		"a.b+tag@gmail.com", // local part with dots/plus
		"x@outlook.com ",    // trailing space on domain tolerated
	}
	for _, e := range allowed {
		if err := checkEmailDomainAllowed(e); err != nil {
			t.Errorf("expected %q allowed, got %v", e, err)
		}
	}

	rejected := []string{
		"user@163.com",
		"user@hotmail.com", // outlook variant NOT auto-allowed
		"user@live.com",
		"user@googlemail.com", // gmail variant NOT auto-allowed
		"user@sub.qq.com",     // subdomain is a different domain
		"user@qq.com.evil.com",
		"user@",
		"noatsign",
		"",
	}
	for _, e := range rejected {
		if err := checkEmailDomainAllowed(e); err == nil {
			t.Errorf("expected %q rejected, got nil", e)
		}
	}
}
