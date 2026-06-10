package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/guyweissman/agentstore/internal/identity"
	applog "github.com/guyweissman/agentstore/internal/log"
)

// handleRegister handles POST /register — the open identity-establishment call.
func (srv *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// Open endpoint: cap the body — a registration is a username + a public key.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if req.Username == "" || req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "username and public_key required", nil)
		return
	}
	// Validate the public key parses as ed25519 before storing.
	if _, err := identity.ParsePublicKey(req.PublicKey); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_key", err.Error(), nil)
		return
	}

	principalID := "principal_" + uuid.NewString()
	if err := srv.directoryRegister(principalID, req.Username, req.PublicKey); err != nil {
		// Most likely a username uniqueness collision.
		writeError(w, http.StatusConflict, "username_taken", err.Error(), nil)
		return
	}
	applog.Audit(principalID, "register", req.Username)
	writeJSON(w, http.StatusCreated, RegisterResponse{PrincipalID: principalID})
}

// handleDirectory handles GET /_directory — the open directory plane. With a
// ?username= it resolves a single principal (used by `bind`); without one it
// enumerates the whole directory (used by `principals list --remote`). The
// directory is public, so this is unauthenticated; it leaks nothing a
// registration didn't already make public.
func (srv *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("username") == "" {
		srv.handleDirectoryList(w, r)
		return
	}
	srv.handleDirectoryLookup(w, r)
}

// handleDirectoryLookup resolves a single username to its principal_id + public
// key so `bind` can point a client at an existing identity (e.g. after a repo
// move seeds the directory).
func (srv *Server) handleDirectoryLookup(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	entry, err := srv.directoryLookupByUsername(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_principal",
			fmt.Sprintf("no principal named %q on this server", username), nil)
		return
	}
	writeJSON(w, http.StatusOK, DirectoryEntryResponse(entry))
}

// handleDirectoryList enumerates every registered principal — the directory
// browse backing `principals list --remote`. Public fields only.
func (srv *Server) handleDirectoryList(w http.ResponseWriter, r *http.Request) {
	entries, err := srv.directoryList()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "directory_error", err.Error(), nil)
		return
	}
	out := make([]DirectoryEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, DirectoryEntryResponse(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWhoAmI handles GET /whoami (signed).
func (srv *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	principalID := principalFromContext(r.Context())
	username, err := srv.directoryUsername(principalID)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_principal", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, WhoAmIResponse{PrincipalID: principalID, Username: username})
}

// handleRekey handles POST /rekey (signed) — rotate the caller's own public key.
func (srv *Server) handleRekey(w http.ResponseWriter, r *http.Request) {
	principalID := principalFromContext(r.Context())
	var req RekeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if _, err := identity.ParsePublicKey(req.PublicKey); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_key", err.Error(), nil)
		return
	}
	if err := srv.directoryRekey(principalID, req.PublicKey); err != nil {
		writeError(w, http.StatusInternalServerError, "rekey_failed", err.Error(), nil)
		return
	}
	applog.Audit(principalID, "rekey", principalID)
	w.WriteHeader(http.StatusOK)
}
