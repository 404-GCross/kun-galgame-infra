package handler

import "testing"

func TestMayManage(t *testing.T) {
	owner := uint(7)
	other := uint(8)

	cases := []struct {
		name       string
		managesAll bool
		callerID   uint
		createdBy  *uint
		want       bool
	}{
		{"ren sees an own row", true, owner, &owner, true},
		{"ren sees someone else's row", true, owner, &other, true},
		{"ren sees a pre-ownership row", true, owner, nil, true},
		{"admin sees an own row", false, owner, &owner, true},
		{"admin is refused someone else's row", false, owner, &other, false},
		{"admin is refused a pre-ownership row", false, owner, nil, false},
		{"an unauthenticated caller is refused even a NULL-owner match", false, 0, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayManage(tc.managesAll, tc.callerID, tc.createdBy); got != tc.want {
				t.Errorf("mayManage(%v, %d, %v) = %v, want %v", tc.managesAll, tc.callerID, tc.createdBy, got, tc.want)
			}
		})
	}
}
