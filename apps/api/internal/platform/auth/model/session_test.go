package model

import (
	"testing"
	"time"
)

func TestSession_PrevTokenWithinGrace(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name      string
		rotatedAt *time.Time
		want      bool
	}{
		{
			name:      "never rotated → not within grace",
			rotatedAt: nil,
			want:      false,
		},
		{
			name:      "rotated just now → within grace",
			rotatedAt: ptr(now),
			want:      true,
		},
		{
			name:      "rotated 1 min ago (< 2 min window) → within grace",
			rotatedAt: ptr(now.Add(-1 * time.Minute)),
			want:      true,
		},
		{
			name:      "rotated exactly at window edge → within grace",
			rotatedAt: ptr(now.Add(-RefreshGraceWindow + time.Second)),
			want:      true,
		},
		{
			name:      "rotated 5 min ago (> 2 min window) → reuse, not within grace",
			rotatedAt: ptr(now.Add(-5 * time.Minute)),
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{RotatedAt: tc.rotatedAt}
			if got := s.PrevTokenWithinGrace(); got != tc.want {
				t.Fatalf("PrevTokenWithinGrace() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSession_IsExpired(t *testing.T) {
	past := &Session{ExpiresAt: time.Now().Add(-time.Hour)}
	if !past.IsExpired() {
		t.Fatal("expected past ExpiresAt to be expired")
	}
	future := &Session{ExpiresAt: time.Now().Add(time.Hour)}
	if future.IsExpired() {
		t.Fatal("expected future ExpiresAt to not be expired")
	}
}

func ptr(t time.Time) *time.Time { return &t }
