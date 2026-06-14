package storage

import "testing"

func TestPercentEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"a-_.~Z9", "a-_.~Z9"}, // unreserved set passes through
		{"a b", "a%20b"},       // space
		{"file(1).zip", "file%281%29.zip"},
		{"中.zip", "%E4%B8%AD.zip"}, // UTF-8 multibyte, byte-wise
	}
	for _, c := range cases {
		if got := percentEncode(c.in); got != c.want {
			t.Errorf("percentEncode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("战国兰斯.zip")
	want := "attachment; filename*=UTF-8''%E6%88%98%E5%9B%BD%E5%85%B0%E6%96%AF.zip"
	if got != want {
		t.Errorf("contentDisposition = %q, want %q", got, want)
	}
}
