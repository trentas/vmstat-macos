//go:build darwin

package main

import (
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		interval time.Duration
		count    int
		wantErr  bool
	}{
		{name: "no args means a single since-boot line", args: nil, interval: 0, count: 1},
		{name: "delay only runs forever", args: []string{"1"}, interval: time.Second, count: 0},
		{name: "delay and count", args: []string{"2", "5"}, interval: 2 * time.Second, count: 5},
		{name: "fractional delay", args: []string{"0.5"}, interval: 500 * time.Millisecond, count: 0},
		{name: "zero delay rejected", args: []string{"0"}, wantErr: true},
		{name: "negative delay rejected", args: []string{"-1"}, wantErr: true},
		{name: "non-numeric delay rejected", args: []string{"abc"}, wantErr: true},
		{name: "zero count rejected", args: []string{"1", "0"}, wantErr: true},
		{name: "non-numeric count rejected", args: []string{"1", "x"}, wantErr: true},
		{name: "too many args rejected", args: []string{"1", "2", "3"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interval, count, err := parseArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got interval=%v count=%d", interval, count)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if interval != tc.interval {
				t.Errorf("interval = %v, want %v", interval, tc.interval)
			}
			if count != tc.count {
				t.Errorf("count = %d, want %d", count, tc.count)
			}
		})
	}
}
