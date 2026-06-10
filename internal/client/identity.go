package client

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/guyweissman/agentstore/internal/server"
)

// Register sends POST /register with a username and public key (the open endpoint).
// Returns the server-assigned principal_id.
func (c *Client) Register(username, publicKey string) (string, error) {
	body, err := json.Marshal(server.RegisterRequest{Username: username, PublicKey: publicKey})
	if err != nil {
		return "", err
	}
	// register is open — never signed.
	resp, err := c.do(http.MethodPost, c.baseURL+"/register", body, "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}
	var out server.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.PrincipalID, nil
}

// WhoAmI sends GET /whoami (signed) and returns the resolved username.
func (c *Client) WhoAmI() (server.WhoAmIResponse, error) {
	resp, err := c.do(http.MethodGet, c.baseURL+"/whoami", nil, "")
	if err != nil {
		return server.WhoAmIResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return server.WhoAmIResponse{}, readError(resp)
	}
	var out server.WhoAmIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return server.WhoAmIResponse{}, err
	}
	return out, nil
}

// Rekey sends POST /rekey (signed) to rotate the caller's public key.
func (c *Client) Rekey(publicKey string) error {
	body, err := json.Marshal(server.RekeyRequest{PublicKey: publicKey})
	if err != nil {
		return err
	}
	resp, err := c.do(http.MethodPost, c.baseURL+"/rekey", body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}
