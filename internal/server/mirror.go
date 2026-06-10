package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/guyweissman/agentstore/internal/canonical"
	"github.com/guyweissman/agentstore/internal/identity"
	applog "github.com/guyweissman/agentstore/internal/log"
	"github.com/guyweissman/agentstore/internal/protocol"
	"github.com/guyweissman/agentstore/internal/store"
)

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func repoDirPath(dataDir, name string) string {
	return filepath.Join(dataDir, name)
}

// handleExport handles GET /<repo>/export — the access control state (grants + roles) that
// ordinary clone does not deliver. Admin only; an admin's clone is the complete,
// migration-ready copy.
func (srv *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	rh, caller, ok := srv.repoAndCaller(w, r)
	if !ok {
		return
	}
	if !srv.requireAdmin(w, rh, caller) {
		return
	}
	grants, err := rh.s.AllGrants()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	roles, err := rh.s.AllRoles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	out := ExportResponse{}
	for _, g := range grants {
		out.Grants = append(out.Grants, MirrorGrant{PrincipalID: g.PrincipalID, PathPattern: g.PathPattern, Permission: g.Permission, GrantedBy: g.GrantedBy})
	}
	for _, ro := range roles {
		out.Roles = append(out.Roles, MirrorRole{PrincipalID: ro.PrincipalID, Role: ro.Role, GrantedBy: ro.GrantedBy})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMirror handles POST /<repo>/mirror — bootstrap a new repo from a full
// mirror. The signer must be registered in this server's directory (via
// agentstore register) before mirroring; authentication is verified against the
// live directory, not the payload roster.
func (srv *Server) handleMirror(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("repo")
	if !validRepoName(name) {
		writeError(w, http.StatusBadRequest, "invalid_repo_name", "invalid repo name", nil)
		return
	}

	// Structural pre-check: a mirror request with no principal header is always
	// invalid. The full authentication (key→directory lookup) happens in
	// verifyMirror after parsing — the public key is in the payload, so we cannot
	// look it up before reading the body.
	if r.Header.Get(protocol.HeaderPrincipal) == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing principal header", nil)
		return
	}

	// Objects ride inline as base64, so allow ~2× the repo-size limit plus
	// headroom; fall back to a hard cap when no repo limit is set.
	maxBody := srv.cfg.Limits.MaxRepoSizeBytes
	if maxBody <= 0 {
		maxBody = 1 << 31 // 2 GiB
	}
	maxBody = maxBody*2 + 1<<20
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_error", err.Error(), nil)
		return
	}
	var req MirrorRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}

	// Self-authenticate against the payload roster + roles.
	principalID, err := srv.verifyMirror(r, body, req, name)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error(), nil)
		return
	}

	// Reserve the target name — refuses a non-empty or mid-creation target and
	// guarantees exclusive use of the repo directory for this import.
	if err := srv.reserveRepo(name); err != nil {
		writeError(w, http.StatusConflict, "repo_exists", "mirror target must be empty", nil)
		return
	}
	defer srv.releaseRepo(name)

	// Reject any non-canonical file path before ingesting (a crafted or old mirror
	// could otherwise seed traversal paths into server state).
	for _, c := range req.Commits {
		for _, f := range c.Files {
			if !store.ValidPath(f.Path) {
				writeError(w, http.StatusBadRequest, "invalid_path",
					fmt.Sprintf("invalid file path %q in commit %s", f.Path, c.ID), nil)
				return
			}
		}
		// Seq must be positive — zero means unconfirmed and negative means absorbed;
		// both are local-only states that must never appear in a mirrored payload.
		if c.Seq <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("commit seq must be positive, got %d for commit %s", c.Seq, c.ID), nil)
			return
		}
	}

	// Only "admin" is a valid role in v0.1; reject anything else before writing.
	for _, role := range req.Roles {
		if role.Role != "admin" {
			writeError(w, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("invalid role %q: only \"admin\" is valid", role.Role), nil)
			return
		}
	}

	// Enforce the TARGET server's limits on the incoming objects before writing
	// any state — a repo valid on its source may violate this server's limits.
	if code, msg := srv.checkMirrorLimits(req); code != 0 {
		writeError(w, code, "limit_exceeded", msg, nil)
		return
	}

	renames, err := srv.applyMirror(name, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "mirror_failed", err.Error(), nil)
		return
	}

	applog.Audit(principalID, "push --mirror", name)
	// Report the signer's resulting identity (principal_id preserved; username may
	// have been auto-renamed) so the client can bind local config without guessing.
	signerName, err := srv.directoryUsername(principalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "mirror_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, MirrorResponse{
		Repo:        name,
		PrincipalID: principalID,
		Username:    signerName,
		Renames:     renames,
	})
}

// verifyMirror self-authenticates the mirror against the payload alone: it checks
// the request signature against the signer's public key in the payload roster and
// confirms that principal holds an admin role in the payload. No prior directory
// registration is required (the target must be empty).
func (srv *Server) verifyMirror(r *http.Request, body []byte, req MirrorRequest, repo string) (string, error) {
	if r.Header.Get(protocol.HeaderProto) != protocol.Version {
		return "", errors.New("unsupported protocol version")
	}
	principalID := r.Header.Get(protocol.HeaderPrincipal)
	ts, err := strconv.ParseInt(r.Header.Get(protocol.HeaderTimestamp), 10, 64)
	if err != nil {
		return "", errors.New("invalid timestamp")
	}
	if !srv.freshTimestamp(ts) {
		return "", errors.New("timestamp outside the acceptable window")
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get(protocol.HeaderSignature))
	if err != nil {
		return "", errors.New("invalid signature encoding")
	}

	// Find the signer's public key in the payload roster (needed for verification
	// because the signer's principal_id is server-specific — it was assigned by the
	// source server and will differ from any principal_id on this server).
	var pubLine string
	for _, p := range req.Principals {
		if p.ID == principalID {
			pubLine = p.PublicKey
			break
		}
	}
	if pubLine == "" {
		return "", errors.New("signer not present in mirror roster")
	}
	pub, err := identity.ParsePublicKey(pubLine)
	if err != nil {
		return "", errors.New("roster public key invalid")
	}

	bodyHash := sha256Sum(body)
	preimage := canonical.RequestPreimageBytes(canonical.RequestContent{
		PrincipalID:   principalID,
		Method:        http.MethodPost,
		RequestTarget: r.URL.RequestURI(),
		Timestamp:     ts,
		BodySHA256:    bodyHash,
	})
	if !identity.Verify(pub, preimage, sig) {
		return "", errors.New("signature verification failed")
	}

	// Trust is bootstrapped entirely from the payload: the signature proves the
	// caller holds the private key for a principal in the roster, and the roles
	// below prove that principal is a repo admin. No prior directory registration
	// is required — pre-registering would mint a second principal_id for the same
	// key, leaving the directory with two ids per key and breaking the member's
	// re-clone. The target must be empty, so a self-declared admin can only create
	// a brand-new repo it owns (the same authority `init` already grants); it can
	// never touch an existing repo. The roster is seeded verbatim afterwards, so
	// the signer's preserved principal_id becomes the directory identity.

	// The signer must be an admin in the mirrored repo.
	isAdmin := false
	for _, role := range req.Roles {
		if role.PrincipalID == principalID && role.Role == "admin" {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		return "", errors.New("mirror must be signed by a repo admin")
	}
	return principalID, nil
}

// checkMirrorLimits validates incoming objects against this server's limits.
// Returns (0, "") if all pass, or an HTTP status + message on the first failure.
func (srv *Server) checkMirrorLimits(req MirrorRequest) (int, string) {
	var total int64
	for _, o := range req.Objects {
		size := int64(len(o.Content))
		if m := srv.cfg.Limits.MaxFileSizeBytes; m > 0 && size > m {
			return http.StatusRequestEntityTooLarge,
				fmt.Sprintf("object %s exceeds max file size %d", o.Hash, m)
		}
		if srv.textOnly() && !isText(o.Content) {
			return http.StatusUnsupportedMediaType,
				fmt.Sprintf("object %s is not text", o.Hash)
		}
		total += size
	}
	if m := srv.cfg.Limits.MaxRepoSizeBytes; m > 0 && total > m {
		return http.StatusRequestEntityTooLarge,
			fmt.Sprintf("mirror size %d exceeds max repo size %d", total, m)
	}
	return 0, ""
}

// applyMirror writes the mirrored state into a new repo store and seeds the
// server directory from the roster. The caller must hold the name reservation
// (see reserveRepo) for exclusive use of the repo directory. On any failure the
// partial directory is removed so it can't be picked up by loadExistingRepos.
func (srv *Server) applyMirror(name string, req MirrorRequest) ([]MirrorRename, error) {
	repoDir := repoDirPath(srv.dataDir, name)
	s, err := store.InitBare(repoDir)
	if err != nil {
		return nil, fmt.Errorf("create repo: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			s.Close()
			os.RemoveAll(repoDir)
		}
	}()

	// Roster first (FK target for commits/grants/roles).
	for _, p := range req.Principals {
		if err := s.AddPrincipal(store.Principal{ID: p.ID, Username: p.Username, PublicKey: p.PublicKey}); err != nil {
			return nil, err
		}
	}

	// Objects before commit metadata (object-before-metadata).
	for _, o := range req.Objects {
		stored, err := s.Objects.WriteObject(o.Content)
		if err != nil {
			return nil, err
		}
		if stored != o.Hash {
			return nil, fmt.Errorf("object hash mismatch: got %s want %s", stored, o.Hash)
		}
	}

	// Commits verbatim, in seq order, preserving id and seq.
	for _, c := range req.Commits {
		files := make([]store.CommitFileRecord, len(c.Files))
		for i, f := range c.Files {
			files[i] = store.CommitFileRecord{
				Path: f.Path, ObjectHash: f.ObjectHash, Size: f.Size,
				ChangeType: f.ChangeType, BasedOnCommitID: f.BasedOnCommitID,
			}
		}
		// "" recomputes the canonical ID; the mismatch check below verifies it equals
		// the source ID — a corrupt-mirror guard. A mirror always carries the complete
		// file set, so recompute is valid here (unlike a permission-filtered clone).
		gotID, err := s.WriteRemoteCommit(store.CommitRecord{
			Message: c.Message, AuthorID: c.AuthorID, CreatedAt: c.CreatedAt,
			Parents: c.Parents, Files: files,
		}, c.Seq, "")
		if err != nil {
			return nil, fmt.Errorf("write commit %s: %w", c.ID, err)
		}
		if gotID != c.ID {
			return nil, fmt.Errorf("commit id mismatch: recomputed %s, source %s (corrupt mirror)", gotID, c.ID)
		}
	}

	// Grants and roles.
	for _, g := range req.Grants {
		if err := s.SetGrant(g.PrincipalID, g.PathPattern, g.Permission, g.GrantedBy); err != nil {
			return nil, err
		}
	}
	for _, role := range req.Roles {
		if err := s.AddRole(role.PrincipalID, role.Role, role.GrantedBy); err != nil {
			return nil, err
		}
	}

	// Seed the server directory from the roster (merge by principal_id; a username
	// that collides with a DIFFERENT principal is auto-renamed).
	renames, err := srv.seedDirectory(req.Principals)
	if err != nil {
		return nil, err
	}

	srv.registerRepo(name, newRepoHandle(s))
	ok = true
	return renames, nil
}

// seedDirectory merges roster principals into the server directory, returning any
// username auto-renames (a roster username that collided with a DIFFERENT
// principal already present). principal_ids are preserved verbatim.
func (srv *Server) seedDirectory(roster []PrincipalJSON) ([]MirrorRename, error) {
	ctx := context.Background()
	var renames []MirrorRename
	for _, p := range roster {
		// Already present by principal_id? Leave as is.
		var existing string
		err := srv.serverDB.QueryRowContext(ctx,
			`SELECT principal_id FROM directory WHERE principal_id = ?`, p.ID).Scan(&existing)
		if err == nil {
			continue
		}
		// Resolve a non-colliding username.
		username := p.Username
		for i := 2; ; i++ {
			var other string
			err := srv.serverDB.QueryRowContext(ctx,
				`SELECT principal_id FROM directory WHERE username = ?`, username).Scan(&other)
			if err != nil { // not found → free
				break
			}
			username = fmt.Sprintf("%s-%d", p.Username, i)
		}
		if _, err := srv.serverDB.ExecContext(ctx,
			`INSERT INTO directory (principal_id, username, public_key, created_at) VALUES (?, ?, ?, ?)`,
			p.ID, username, p.PublicKey, time.Now().UnixMilli()); err != nil {
			return nil, err
		}
		if username != p.Username {
			renames = append(renames, MirrorRename{PrincipalID: p.ID, From: p.Username, To: username})
		}
	}
	return renames, nil
}
