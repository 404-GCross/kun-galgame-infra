package service

import "testing"

func TestCheckEmailDomainAllowed(t *testing.T) {
	allowed := []string{
		"user@qq.com",
		"user@foxmail.com",
		"user@163.com",
		"user@126.com",
		"user@yeah.net",
		"user@sina.com",
		"user@sina.cn",
		"user@sohu.com",
		"user@aliyun.com",
		"user@139.com",
		"user@189.cn",
		"user@gmail.com",
		"user@googlemail.com",
		"user@outlook.com",
		"user@hotmail.com",
		"user@live.com",
		"user@msn.com",
		"user@icloud.com",
		"user@me.com",
		"user@mac.com",
		"user@yahoo.com",
		"user@yahoo.co.jp",
		"user@proton.me",
		"user@protonmail.com",
		"user@pm.me",
		"USER@QQ.COM",
		"a.b+tag@gmail.com",
		"x@outlook.com ",
	}
	for _, e := range allowed {
		if err := checkEmailDomainAllowed(e); err != nil {
			t.Errorf("expected %q allowed, got %v", e, err)
		}
	}

	rejected := []string{
		"user@example.com",
		"user@mail.qq.com",
		"user@qq.com.evil.com",
		"user@10minutemail.com",
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
