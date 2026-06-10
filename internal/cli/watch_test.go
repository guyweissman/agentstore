package cli

import (
	"strings"
	"testing"
)

func TestWatchPathPrefix(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "no arg defaults to repo root", args: nil, want: "/"},
		{name: "empty args defaults to repo root", args: []string{}, want: "/"},
		{name: "rooted store path", args: []string{"/strategy"}, want: "/strategy"},
		{name: "nested rooted store path", args: []string{"/customers/sanitized"}, want: "/customers/sanitized"},
		{name: "repo root", args: []string{"/"}, want: "/"},
		{name: "working-tree path rejected", args: []string{"strategy"}, wantErr: true},
		{name: "dot-relative path rejected", args: []string{"./strategy"}, wantErr: true},
		{name: "empty string rejected", args: []string{""}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := watchPathPrefix(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for args %q, got prefix %q", tt.args, got)
				}
				// The error must steer the user toward the /-rooted form.
				if !strings.Contains(err.Error(), `must start with "/"`) {
					t.Errorf("error message not actionable: %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for args %q: %v", tt.args, err)
			}
			if got != tt.want {
				t.Errorf("watchPathPrefix(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
