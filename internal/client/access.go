package client

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/guyweissman/agentstore/internal/server"
)

// Grant sets a grant on the remote.
func (c *Client) Grant(principal, permission, path string) error {
	body, _ := json.Marshal(server.GrantRequest{Principal: principal, Permission: permission, Path: path})
	return c.noContent(http.MethodPost, c.repoPath("grants"), body)
}

// Revoke removes a grant on the remote.
func (c *Client) Revoke(principal, path string) error {
	body, _ := json.Marshal(server.RevokeRequest{Principal: principal, Path: path})
	return c.noContent(http.MethodDelete, c.repoPath("grants"), body)
}

// Permissions lists the grants matching a path.
func (c *Client) Permissions(path string) ([]server.PermissionEntry, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("permissions")+"?path="+url.QueryEscape(path), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var out []server.PermissionEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddMember adds a directory principal to the repo (admin only).
func (c *Client) AddMember(username string) error {
	body, _ := json.Marshal(server.MemberRequest{Username: username})
	return c.noContent(http.MethodPost, c.repoPath("members"), body)
}

// RemoveMember removes a member from the repo (admin only).
func (c *Client) RemoveMember(username string) error {
	body, _ := json.Marshal(server.MemberRequest{Username: username})
	return c.noContent(http.MethodDelete, c.repoPath("members"), body)
}

// ListMembers returns the repo roster.
func (c *Client) ListMembers() ([]server.PrincipalJSON, error) {
	return c.GetPrincipals()
}

// ListAdmins returns the usernames of repo admins.
func (c *Client) ListAdmins() ([]string, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("admins"), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var out []string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddAdmin grants the admin role (admin only).
func (c *Client) AddAdmin(username string) error {
	body, _ := json.Marshal(server.MemberRequest{Username: username})
	return c.noContent(http.MethodPost, c.repoPath("admins"), body)
}

// RevokeAdmin revokes the admin role (admin only).
func (c *Client) RevokeAdmin(username string) error {
	body, _ := json.Marshal(server.MemberRequest{Username: username})
	return c.noContent(http.MethodDelete, c.repoPath("admins"), body)
}

// Export fetches the repo's access control state (grants + roles) — admin only.
func (c *Client) Export() (server.ExportResponse, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("export"), nil, "")
	if err != nil {
		return server.ExportResponse{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return server.ExportResponse{}, err
	}
	var out server.ExportResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return server.ExportResponse{}, err
	}
	return out, nil
}

// Mirror uploads a full repo state to an empty target (bootstrap). The request
// is self-authenticated against the roster carried in the payload. It returns the
// signer's resulting identity on the target (principal_id preserved; username may
// be auto-renamed) plus any roster renames.
func (c *Client) Mirror(req server.MirrorRequest) (server.MirrorResponse, error) {
	var out server.MirrorResponse
	body, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	resp, err := c.do(http.MethodPost, c.repoPath("mirror"), body, "application/json")
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return out, readError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// LookupDirectory resolves a username to its directory entry on the remote. The
// directory is public, so this is unauthenticated (the client may have no
// identity yet — this is how `bind` discovers an existing principal_id).
func (c *Client) LookupDirectory(username string) (server.DirectoryEntryResponse, error) {
	var out server.DirectoryEntryResponse
	resp, err := c.do(http.MethodGet, c.baseURL+"/_directory?username="+url.QueryEscape(username), nil, "")
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return out, err
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// ListDirectory enumerates every principal registered in the remote's directory.
// Like LookupDirectory it hits the public directory plane, so it is
// unauthenticated (no repo or identity context required).
func (c *Client) ListDirectory() ([]server.DirectoryEntryResponse, error) {
	resp, err := c.do(http.MethodGet, c.baseURL+"/_directory", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var out []server.DirectoryEntryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Authz returns the caller's repo-wide authority.
func (c *Client) Authz() (server.AuthzResponse, error) {
	resp, err := c.do(http.MethodGet, c.repoPath("authz"), nil, "")
	if err != nil {
		return server.AuthzResponse{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return server.AuthzResponse{}, err
	}
	var out server.AuthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return server.AuthzResponse{}, err
	}
	return out, nil
}

func (c *Client) noContent(method, url string, body []byte) error {
	resp, err := c.do(method, url, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return readError(resp)
}
