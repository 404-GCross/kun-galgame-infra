package main

import "testing"

func TestOriginalFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"toolset/123/45_某工具_a1b2c3d.zip", "某工具.zip"},
		{"toolset/123/27872_BGI_Hazuki_YgB9w0S.zip", "BGI_Hazuki.zip"},
		{"toolset/1/1665_批量解压-密码匹配_7gkJgDR.zip", "批量解压-密码匹配.zip"},
		{"toolset/9/7_My_Tool_v2_0f1e2d3.rar", "My_Tool_v2.rar"},
		{"toolset/1/2_x_abcdef0.7z", "x.7z"},
		{"toolset/1/weirdkey.zip", "weirdkey.zip"},
		{"toolset/1/45_工具_short.zip", "45_工具_short.zip"},
	}
	for _, c := range cases {
		if got := originalFilename(c.in); got != c.want {
			t.Errorf("originalFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
