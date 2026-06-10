package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	applog "github.com/guyweissman/agentstore/internal/log"
	"github.com/guyweissman/agentstore/internal/store"
)

// GrantRequest is the body of POST /<repo>/grants.
type GrantRequest struct {
	Principal  string `json:"principal"`  // username
	Permission string `json:"permission"` // read|write|owner
	Path       string `json:"path"`       // exact path or prefix pattern
}

// RevokeRequest is the body of DELETE /<repo>/grants.
type RevokeRequest struct {
	Principal string `json:"principal"`
	Path      string `json:"path"`
}

// PermissionEntry is one row of the GET /<repo>/permissions response.
type PermissionEntry struct {
	Principal  string `json:"principal"`
	Permission string `json:"permission"`
	Path       string `json:"path"`
}

// MemberRequest is the body of POST/DELETE /<repo>/members and /admins.
type MemberRequest struct {
	Username string `json:"username"`
}

// handleGrant handles POST /<repo>/grants.
func (srv *Server) handleGrant(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	var req GrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if !store.ValidPathPattern(req.Path) {
		writeError(w, http.StatusBadRequest, "invalid_path",
			fmt.Sprintf("invalid path pattern %q", req.Path), nil)
		return
	}
	// The caller must be admin or owner of the target path.
	canGrant, err := rh.s.CanGrant(caller, req.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	if !canGrant {
		writeError(w, http.StatusForbidden, "forbidden", "requires admin or owner of the path", nil)
		return
	}
	target, err := rh.s.PrincipalIDByUsername(req.Principal)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_member",
			fmt.Sprintf("%s is not a member of this repo", req.Principal), nil)
		return
	}
	if err := rh.s.SetGrant(target, req.Path, req.Permission, caller); err != nil {
		writeError(w, http.StatusBadRequest, "grant_failed", err.Error(), nil)
		return
	}
	applog.Audit(caller, "grant", fmt.Sprintf("%s %s %s", req.Principal, req.Permission, req.Path))
	w.WriteHeader(http.StatusNoContent)
}

// handleRevoke handles DELETE /<repo>/grants.
func (srv *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	var req RevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if !store.ValidPathPattern(req.Path) {
		writeError(w, http.StatusBadRequest, "invalid_path",
			fmt.Sprintf("invalid path pattern %q", req.Path), nil)
		return
	}
	canGrant, err := rh.s.CanGrant(caller, req.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	if !canGrant {
		writeError(w, http.StatusForbidden, "forbidden", "requires admin or owner of the path", nil)
		return
	}
	target, err := rh.s.PrincipalIDByUsername(req.Principal)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_member", err.Error(), nil)
		return
	}
	if err := rh.s.RevokeGrant(target, req.Path); err != nil {
		writeError(w, http.StatusNotFound, "revoke_failed", err.Error(), nil)
		return
	}
	applog.Audit(caller, "revoke", fmt.Sprintf("%s %s", req.Principal, req.Path))
	w.WriteHeader(http.StatusNoContent)
}

// handlePermissions handles GET /<repo>/permissions?path=<path>.
func (srv *Server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "path query param required", nil)
		return
	}
	// Reading permissions requires read access to the path.
	canRead, err := rh.s.CanRead(caller, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	if !canRead {
		writeError(w, http.StatusForbidden, "forbidden", "no read access to this path", nil)
		return
	}
	grants, err := rh.s.GrantsForPath(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	out := make([]PermissionEntry, 0, len(grants))
	for _, g := range grants {
		username, _ := rh.s.UsernameByPrincipalID(g.PrincipalID)
		out = append(out, PermissionEntry{Principal: username, Permission: g.Permission, Path: g.PathPattern})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddMember handles POST /<repo>/members — admin adds a directory principal.
func (srv *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	if !srv.requireAdmin(w, rh, caller) {
		return
	}
	var req MemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	// Copy the identity from the remote directory into the repo's roster.
	entry, err := srv.directoryLookupByUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_principal",
			fmt.Sprintf("%s is not registered on this remote", req.Username), nil)
		return
	}
	if err := rh.s.AddPrincipal(store.Principal{ID: entry.PrincipalID, Username: entry.Username, PublicKey: entry.PublicKey}); err != nil {
		writeError(w, http.StatusInternalServerError, "add_failed", err.Error(), nil)
		return
	}
	applog.Audit(caller, "principals add", req.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveMember handles DELETE /<repo>/members — admin removes a member.
func (srv *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	if !srv.requireAdmin(w, rh, caller) {
		return
	}
	var req MemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	target, err := rh.s.PrincipalIDByUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_member", err.Error(), nil)
		return
	}
	// RemovePrincipal cascades grants + roles and refuses the last admin, all in
	// one transaction — no separate admin-revoke step to leave a partial state.
	if err := rh.s.RemovePrincipal(target); err != nil {
		writeError(w, http.StatusConflict, "remove_failed", err.Error(), nil)
		return
	}
	applog.Audit(caller, "principals remove", req.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleListAdmins handles GET /<repo>/admins.
func (srv *Server) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	if !srv.requireMember(w, rh, caller) {
		return
	}
	ids, err := rh.s.ListAdmins()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	usernames := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, err := rh.s.UsernameByPrincipalID(id); err == nil {
			usernames = append(usernames, name)
		}
	}
	writeJSON(w, http.StatusOK, usernames)
}

// handleAddAdmin handles POST /<repo>/admins.
func (srv *Server) handleAddAdmin(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	if !srv.requireAdmin(w, rh, caller) {
		return
	}
	var req MemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	target, err := rh.s.PrincipalIDByUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_member", err.Error(), nil)
		return
	}
	if err := rh.s.AddRole(target, "admin", caller); err != nil {
		writeError(w, http.StatusInternalServerError, "add_admin_failed", err.Error(), nil)
		return
	}
	applog.Audit(caller, "admin add", req.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleRevokeAdmin handles DELETE /<repo>/admins.
func (srv *Server) handleRevokeAdmin(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	if !srv.requireAdmin(w, rh, caller) {
		return
	}
	var req MemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	target, err := rh.s.PrincipalIDByUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_member", err.Error(), nil)
		return
	}
	if err := rh.s.RevokeAdmin(target); err != nil {
		writeError(w, http.StatusConflict, "revoke_admin_failed", err.Error(), nil)
		return
	}
	applog.Audit(caller, "admin revoke", req.Username)
	w.WriteHeader(http.StatusNoContent)
}

// --- shared helpers ---

// repoAndCaller resolves the repo and the authenticated caller, writing an error
// response and returning ok=false on failure.
func (srv *Server) repoAndCaller(w http.ResponseWriter, r *http.Request) (*repoHandle, string, bool) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return nil, "", false
	}
	return rh, principalFromContext(r.Context()), true
}

// requireMember writes a 403 and returns false if the caller is not a repo
// member. Repos are private by default: an identity grants access to nothing
// until a repo admin adds the principal.
func (srv *Server) requireMember(w http.ResponseWriter, rh *repoHandle, caller string) bool {
	isMember, err := rh.s.HasPrincipal(caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return false
	}
	if !isMember {
		writeError(w, http.StatusForbidden, "forbidden", "not a member of this repo", nil)
		return false
	}
	return true
}

// requireAdmin writes a 403 and returns false if the caller is not a repo admin.
func (srv *Server) requireAdmin(w http.ResponseWriter, rh *repoHandle, caller string) bool {
	isAdmin, err := rh.s.IsAdmin(caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return false
	}
	if !isAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "requires repo admin", nil)
		return false
	}
	return true
}
