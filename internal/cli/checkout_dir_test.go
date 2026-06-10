package cli

import (
	"path/filepath"
	"testing"
)

func TestCheckoutDir(t *testing.T) {
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatalf("abs %q: %v", p, err)
		}
		return a
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"derives from URL last segment", []string{"http://127.0.0.1:8080/team"}, abs("team")},
		{"ignores trailing slash", []string{"http://127.0.0.1:8080/team/"}, abs("team")},
		{"explicit relative directory", []string{"http://127.0.0.1:8080/team", "work"}, abs("work")},
		{"explicit absolute directory", []string{"http://127.0.0.1:8080/team", "/tmp/work"}, "/tmp/work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkoutDir(tc.args)
			if err != nil {
				t.Fatalf("checkoutDir(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Errorf("checkoutDir(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}

	if _, err := checkoutDir([]string{""}); err == nil {
		t.Error("expected error when the URL has no repo name and no directory is given")
	}
}
