package server_test

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/guyweissman/agentstore/internal/cli"
	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/server"
	"github.com/guyweissman/agentstore/internal/store"
)

func hashOf(data []byte) string { return store.HashContent(data) }

func writeServerTOML(t *testing.T, dir string, cfg config.ServerConfig) {
	t.Helper()
	toml := fmt.Sprintf(`[limits]
max_file_size_bytes = %d
max_repo_size_bytes = %d
allowed_file_types = ["%s"]
`, cfg.Limits.MaxFileSizeBytes, cfg.Limits.MaxRepoSizeBytes, firstOr(cfg.Limits.AllowedFileTypes, "text/*"))
	if err := os.WriteFile(filepath.Join(dir, "server.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write server.toml: %v", err)
	}
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

// testServerWithConfig starts a server with a custom config (for limits).
func testServerWithConfig(t *testing.T, cfg config.ServerConfig) string {
	t.Helper()
	dir := t.TempDir()
	// Write server.toml so server.New picks up the limits.
	writeServerTOML(t, dir, cfg)
	srv, err := server.New(dir)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// TestLimitFileTooLarge verifies oversized files are rejected at upload.
func TestLimitFileTooLarge(t *testing.T) {
	url := testServerWithConfig(t, config.ServerConfig{
		Limits: config.LimitsSection{MaxFileSizeBytes: 16, AllowedFileTypes: []string{"text/*"}},
	})
	alice := registerUser(t, url, "alice")
	repo := t.TempDir()
	if err := cli.RunInit(repo, url+"/r", alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	cl, _ := client.New(url+"/r", alice)
	big := make([]byte, 64)
	for i := range big {
		big[i] = 'a'
	}
	if err := cl.UploadObject(hashOf(big), big); err == nil {
		t.Error("upload over the file-size limit should be rejected")
	}
}

// TestLimitBinaryRejected verifies non-text content is rejected when text-only.
func TestLimitBinaryRejected(t *testing.T) {
	url := testServerWithConfig(t, config.ServerConfig{
		Limits: config.LimitsSection{MaxFileSizeBytes: 1024, AllowedFileTypes: []string{"text/*"}},
	})
	alice := registerUser(t, url, "alice")
	repo := t.TempDir()
	if err := cli.RunInit(repo, url+"/r", alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	cl, _ := client.New(url+"/r", alice)
	binary := []byte{'o', 'k', 0x00, 0xff} // NUL byte → not text
	if err := cl.UploadObject(hashOf(binary), binary); err == nil {
		t.Error("binary upload should be rejected under text/* limit")
	}
	// A text file of the same size is accepted.
	text := []byte("hello\n")
	if err := cl.UploadObject(hashOf(text), text); err != nil {
		t.Errorf("text upload should be accepted: %v", err)
	}
}

// TestLimitAllowAllPermitsBinary verifies an explicit "*/*" allowlist disables
// the text-only check.
func TestLimitAllowAllPermitsBinary(t *testing.T) {
	url := testServerWithConfig(t, config.ServerConfig{
		Limits: config.LimitsSection{MaxFileSizeBytes: 1024, AllowedFileTypes: []string{"*/*"}},
	})
	alice := registerUser(t, url, "alice")
	repo := t.TempDir()
	if err := cli.RunInit(repo, url+"/r", alice); err != nil {
		t.Fatalf("init: %v", err)
	}
	cl, _ := client.New(url+"/r", alice)
	binary := []byte{0x00, 0x01, 0x02, 0xff}
	if err := cl.UploadObject(hashOf(binary), binary); err != nil {
		t.Errorf("binary upload should be accepted under */* allowlist: %v", err)
	}
}
