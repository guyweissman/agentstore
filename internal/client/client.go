// Package client is the HTTP client for the AgentStore server.
package client

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/guyweissman/agentstore/internal/canonical"
	"github.com/guyweissman/agentstore/internal/identity"
	"github.com/guyweissman/agentstore/internal/protocol"
	"github.com/guyweissman/agentstore/internal/server"
)

// Identity signs requests. When nil, requests are sent unsigned (only valid for
// the open `register` endpoint).
type Identity struct {
	PrincipalID string
	PrivateKey  ed25519.PrivateKey
}

// Client communicates with one AgentStore server.
type Client struct {
	baseURL string // scheme+host, e.g. "http://127.0.0.1:8080"
	repo    string // repo name
	http    *http.Client
	id      *Identity
}

// New returns a Client for the given repo URL, signing requests with id (may be nil).
func New(repoURL string, id *Identity) (*Client, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", repoURL, err)
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("URL %q has no repo name in the path", repoURL)
	}
	return &Client{
		baseURL: u.Scheme + "://" + u.Host,
		repo:    parts[0],
		http:    &http.Client{Timeout: 60 * time.Second},
		id:      id,
	}, nil
}

// RepoURL returns the full URL of the repo.
func (c *Client) RepoURL() string { return c.baseURL + "/" + c.repo }

// CreateRepo sends POST /<repo> to create a new repo on the server.
func (c *Client) CreateRepo() error {
	resp, err := c.do(http.MethodPost, c.RepoURL(), nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusCreated)
}

// UploadObject sends PUT /<repo>/objects/<hash> with the raw content.
func (c *Client) UploadObject(hash string, data []byte) error {
	resp, err := c.do(http.MethodPut, c.repoPath("objects/"+hash), data, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return readError(resp)
}

// DownloadObject fetches GET /<repo>/objects/<hash> and returns the raw bytes.
func (c *Client) DownloadObject(hash string) ([]byte, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("objects/"+hash), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}
	return io.ReadAll(resp.Body)
}

// PushResult is the outcome of a Push call.
type PushResult struct {
	ID        string
	Seq       int64
	Conflicts []server.ConflictFile // non-empty on OCC rejection
}

// Push sends POST /<repo>/commits and returns the result.
func (c *Client) Push(req server.PushRequest) (PushResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return PushResult{}, err
	}
	resp, err := c.do(http.MethodPost, c.repoPath("commits"), body, "application/json")
	if err != nil {
		return PushResult{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return PushResult{}, err
	}

	if resp.StatusCode == http.StatusConflict {
		var errResp server.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return PushResult{}, fmt.Errorf("push rejected: %s", respBody)
		}
		var conflicts []server.ConflictFile
		if errResp.Detail != nil {
			conflicts = errResp.Detail.Conflicts
		}
		return PushResult{Conflicts: conflicts}, nil
	}
	if resp.StatusCode != http.StatusOK {
		var errResp server.ErrorResponse
		json.Unmarshal(respBody, &errResp)
		return PushResult{}, fmt.Errorf("push failed (%d): %s", resp.StatusCode, errResp.Message)
	}

	var pushResp server.PushResponse
	if err := json.Unmarshal(respBody, &pushResp); err != nil {
		return PushResult{}, fmt.Errorf("decode push response: %w", err)
	}
	return PushResult{ID: pushResp.ID, Seq: pushResp.Seq}, nil
}

// commitPageLimit is the server-side page size for a single GetCommits call.
// A var (not const) so tests can shrink it to exercise pagination cheaply.
var commitPageLimit = 1000

// SetCommitPageLimitForTest overrides the commit page size; for tests only.
func SetCommitPageLimitForTest(n int) { commitPageLimit = n }

// GetCommits fetches one page of GET /<repo>/commits?since=<seq> (up to
// commitPageLimit commits, oldest-first). Most callers want GetAllCommits.
func (c *Client) GetCommits(since int64) ([]server.CommitJSON, error) {
	u := c.repoPath("commits") +
		"?since=" + strconv.FormatInt(since, 10) +
		"&limit=" + strconv.Itoa(commitPageLimit) +
		"&reverse=true"
	resp, err := c.do(http.MethodGet, u, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var commits []server.CommitJSON
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, err
	}
	return commits, nil
}

// GetAllCommits fetches every commit with seq > since, paginating past the
// per-request page limit. Used by clone, pull, and mirror so large repos are
// never silently truncated. Stubs (redacted commits) carry a seq too, so the
// cursor advances correctly across pages even when some commits are filtered.
func (c *Client) GetAllCommits(since int64) ([]server.CommitJSON, error) {
	var all []server.CommitJSON
	for {
		page, err := c.GetCommits(since)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		since = page[len(page)-1].Seq
		if len(page) < commitPageLimit {
			break
		}
	}
	return all, nil
}

// GetHeads fetches GET /<repo>/heads.
func (c *Client) GetHeads() ([]server.HeadJSON, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("heads"), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var raw []map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]server.HeadJSON, len(raw))
	for i, m := range raw {
		out[i] = server.HeadJSON{
			Path:       m["path"],
			CommitID:   m["commit_id"],
			ObjectHash: m["object_hash"],
			ChangeType: m["change_type"],
		}
	}
	return out, nil
}

// GetPrincipals fetches GET /<repo>/principals — the member roster.
func (c *Client) GetPrincipals() ([]server.PrincipalJSON, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("principals"), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var out []server.PrincipalJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) repoPath(sub string) string {
	return c.baseURL + "/" + c.repo + "/" + sub
}

// do builds, signs (if an identity is set), and sends a request.
func (c *Client) do(method, fullURL string, body []byte, contentType string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, fullURL, r)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.id != nil {
		c.sign(req, body)
	}
	return c.http.Do(req)
}

// sign attaches the AgentStore auth-envelope headers to req.
func (c *Client) sign(req *http.Request, body []byte) {
	for k, v := range c.authHeaders(req.Method, req.URL.RequestURI(), body) {
		req.Header[k] = v
	}
}

// authHeaders computes the auth-envelope headers for a method + request target
// (path + query, exactly as sent) + body. Shared by HTTP requests and the watch
// WebSocket handshake.
func (c *Client) authHeaders(method, requestTarget string, body []byte) http.Header {
	ts := time.Now().UnixMilli()
	bodyHash := sha256.Sum256(body) // sha256 of empty input when body is nil
	preimage := canonical.RequestPreimageBytes(canonical.RequestContent{
		PrincipalID:   c.id.PrincipalID,
		Method:        method,
		RequestTarget: requestTarget,
		Timestamp:     ts,
		BodySHA256:    bodyHash[:],
	})
	sig := identity.Sign(c.id.PrivateKey, preimage)
	h := http.Header{}
	h.Set(protocol.HeaderProto, protocol.Version)
	h.Set(protocol.HeaderPrincipal, c.id.PrincipalID)
	h.Set(protocol.HeaderTimestamp, strconv.FormatInt(ts, 10))
	h.Set(protocol.HeaderSignature, base64.StdEncoding.EncodeToString(sig))
	return h
}

func checkStatus(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	return readError(resp)
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var e server.ErrorResponse
	if err := json.Unmarshal(body, &e); err == nil && e.Message != "" {
		return fmt.Errorf("server error %d [%s]: %s", resp.StatusCode, e.Code, e.Message)
	}
	return fmt.Errorf("server error %d: %s", resp.StatusCode, body)
}
