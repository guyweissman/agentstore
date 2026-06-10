package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	applog "github.com/guyweissman/agentstore/internal/log"
	"github.com/guyweissman/agentstore/internal/store"
)

const (
	maxCommitMessageLen = 4096
	maxCommitParents    = 3
)

// validObjectHash reports whether s is a valid SHA-256 object hash: exactly 64
// lowercase hex characters. Prevents objectPath from panicking on short inputs
// and ensures only well-formed hashes reach the object store.
func validObjectHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// handleCreateRepo handles POST /<repo>. The authenticated caller becomes the
// repo's first admin and owner of /*.
func (srv *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("repo")
	if !validRepoName(name) {
		writeError(w, http.StatusBadRequest, "invalid_repo_name", "invalid repo name", nil)
		return
	}
	principalID := principalFromContext(r.Context())
	if err := srv.createRepo(name, principalID); err != nil {
		writeError(w, http.StatusConflict, "repo_exists", err.Error(), nil)
		return
	}
	applog.Audit(principalID, "init", name)
	writeJSON(w, http.StatusCreated, CreateRepoResponse{Repo: name})
}

// handleUploadObject handles PUT /<repo>/objects/<hash>.
func (srv *Server) handleUploadObject(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}
	caller := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, caller) {
		return
	}
	// Object upload is a write operation that counts against repo quota — require
	// the caller to be able to write somewhere, so read-only members can't store blobs.
	if ok, err := rh.s.HasAnyWrite(caller); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, "forbidden", "no write permission in this repo", nil)
		return
	}
	hash := r.PathValue("hash")
	// Validate the hash format before touching the object store — objectPath
	// panics on strings shorter than 2 chars, and non-hex hashes are never valid.
	if !validObjectHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid_hash",
			"object hash must be a 64-character lowercase hex string", nil)
		return
	}
	if rh.s.Objects.HasObject(hash) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, int64(srv.cfg.Limits.MaxFileSizeBytes)+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_error", err.Error(), nil)
		return
	}
	if int64(len(data)) > srv.cfg.Limits.MaxFileSizeBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("max %d bytes", srv.cfg.Limits.MaxFileSizeBytes), nil)
		return
	}
	// Validate content hash matches the URL before writing — prevents orphaned
	// objects from accumulating when a caller supplies a wrong hash.
	if store.HashContent(data) != hash {
		writeError(w, http.StatusBadRequest, "hash_mismatch",
			"object content does not match the supplied hash", nil)
		return
	}
	// File-type limit: v0.1 is text-only. Reject content that isn't valid UTF-8
	// text (a NUL byte or invalid encoding marks it binary).
	if srv.textOnly() && !isText(data) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_type",
			"only text files are allowed", nil)
		return
	}
	// Repo-size limit: refuse an upload that would push the repo past the cap.
	if max := srv.cfg.Limits.MaxRepoSizeBytes; max > 0 {
		cur, err := rh.s.Objects.TotalSize()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "size_error", err.Error(), nil)
			return
		}
		if cur+int64(len(data)) > max {
			writeError(w, http.StatusRequestEntityTooLarge, "repo_too_large",
				fmt.Sprintf("repo size limit %d bytes exceeded", max), nil)
			return
		}
	}
	if _, err := rh.s.Objects.WriteObject(data); err != nil {
		writeError(w, http.StatusInternalServerError, "write_error", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDownloadObject handles GET /<repo>/objects/<hash>. The caller may only
// fetch an object they can reach: one referenced by some path they can read.
func (srv *Server) handleDownloadObject(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}
	caller := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, caller) {
		return
	}
	hash := r.PathValue("hash")
	if !validObjectHash(hash) {
		// Indistinguishable from not found — don't reveal the validation failure.
		writeError(w, http.StatusNotFound, "object_not_found", "object not found", nil)
		return
	}

	ok, err := srv.callerCanReadObject(rh, caller, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	if !ok {
		// Indistinguishable from "not found" — don't reveal existence.
		writeError(w, http.StatusNotFound, "object_not_found", "object not found", nil)
		return
	}

	data, err := rh.s.Objects.ReadObject(hash)
	if err != nil {
		writeError(w, http.StatusNotFound, "object_not_found", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// callerCanReadObject reports whether the caller can read at least one path that
// references the object hash anywhere in history.
func (srv *Server) callerCanReadObject(rh *repoHandle, caller, hash string) (bool, error) {
	paths, err := rh.s.PathsReferencingObject(hash)
	if err != nil {
		return false, err
	}
	for _, p := range paths {
		ok, err := rh.s.CanRead(caller, p)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// handlePush handles POST /<repo>/commits — the OCC core.
func (srv *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}

	var req PushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "commit must contain at least one file", nil)
		return
	}
	if len(req.Message) > maxCommitMessageLen {
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("commit message too long (max %d bytes)", maxCommitMessageLen), nil)
		return
	}
	if len(req.Parents) > maxCommitParents {
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("too many parents (max %d)", maxCommitParents), nil)
		return
	}
	// Reject malformed paths before anything else: a non-canonical or escaping
	// path would traverse out of a victim's working tree when later materialized.
	for _, f := range req.Files {
		if !store.ValidPath(f.Path) {
			writeError(w, http.StatusBadRequest, "invalid_path",
				fmt.Sprintf("invalid file path %q", f.Path), nil)
			return
		}
	}

	principalID := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, principalID) {
		return
	}

	// Object-before-metadata: every non-deleted file's object must already be in
	// the store. Reject otherwise — never write metadata pointing at missing content.
	for _, f := range req.Files {
		if f.ChangeType == "deleted" {
			continue
		}
		if f.ObjectHash == "" {
			writeError(w, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("%s: %s requires an object_hash", f.Path, f.ChangeType), nil)
			return
		}
		if !store.ValidObjectHash(f.ObjectHash) {
			writeError(w, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("%s: malformed object_hash", f.Path), nil)
			return
		}
		if !rh.s.Objects.HasObject(f.ObjectHash) {
			writeError(w, http.StatusBadRequest, "missing_object",
				fmt.Sprintf("object %s for %s not uploaded", f.ObjectHash, f.Path), nil)
			return
		}
	}

	// Serialise all writes to this repo — WAL handles concurrent reads freely.
	rh.mu.Lock()
	defer rh.mu.Unlock()

	// Authorization inside the write lock: the permission check and the commit
	// write now share the same serialization boundary, so a concurrent revoke
	// cannot slip between the check and the write.
	for _, f := range req.Files {
		ok, err := rh.s.CanWrite(principalID, f.Path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
			return
		}
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden",
				fmt.Sprintf("no write permission on %s", f.Path), nil)
			return
		}
	}

	id, seq, conflicts, err := applyPush(r.Context(), rh.s, req, principalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	if len(conflicts) > 0 {
		detail := &ConflictDetail{Conflicts: conflicts}
		writeError(w, http.StatusConflict, "push_conflict",
			fmt.Sprintf("%d file(s) changed since your base; pull and retry", len(conflicts)),
			detail)
		return
	}

	// Build the event group once (from the accepted commit, already in memory)
	// and publish it AFTER the SQLite commit succeeded. The hub fans out, filters
	// per subscriber, and never blocks this path.
	rh.hub.Publish(buildEventGroup(id, seq, req, principalID))

	writeJSON(w, http.StatusOK, PushResponse{ID: id, Seq: seq})
}

// buildEventGroup constructs a commit's full event group: one file.* event per
// file, then a terminal commit.pushed, all sharing the commit's seq as cursor.
func buildEventGroup(commitID string, seq int64, req PushRequest, authorID string) []EventJSON {
	ts := time.Now().UnixMilli() // server clock — the time the event is emitted
	group := make([]EventJSON, 0, len(req.Files)+1)
	paths := make([]string, 0, len(req.Files))
	for _, f := range req.Files {
		ev := EventJSON{
			Cursor:    seq,
			Type:      fileEventType(f.ChangeType),
			Timestamp: ts,
			CommitID:  commitID,
			AuthorID:  authorID,
			Path:      f.Path,
		}
		if f.ChangeType != "deleted" {
			ev.ObjectHash = f.ObjectHash
			ev.Size = f.Size
		}
		group = append(group, ev)
		paths = append(paths, f.Path)
	}
	group = append(group, EventJSON{
		Cursor:    seq,
		Type:      EventCommitPushed,
		Timestamp: ts,
		CommitID:  commitID,
		AuthorID:  authorID,
		Message:   req.Message,
		Paths:     paths,
	})
	return group
}

// validRepoName restricts repo names to a narrow allowlist so they are safe to
// use as a directory under the data dir — never relying on router path cleaning
// for filesystem safety.
func validRepoName(name string) bool {
	if name == "" || len(name) > 64 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// textOnly reports whether uploads must be text. v0.1 can only validate text, so
// any restrictive allowlist enforces text; only an explicit allow-all ("*/*")
// disables the check, and an empty/unset allowlist means no limit configured.
// (A real per-MIME-type allowlist is post-v0.1; configuring a non-text type in
// v0.1 still enforces text, since that is all the server can verify.)
func (srv *Server) textOnly() bool {
	types := srv.cfg.Limits.AllowedFileTypes
	if len(types) == 0 {
		return false
	}
	for _, t := range types {
		if t == "*/*" {
			return false
		}
	}
	return true
}

// isText reports whether data is valid UTF-8 text with no NUL bytes.
func isText(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	return utf8.Valid(data)
}

func fileEventType(changeType string) string {
	switch changeType {
	case "added":
		return EventFileCreated
	case "deleted":
		return EventFileDeleted
	default:
		return EventFileModified
	}
}

// applyPush performs the OCC check and writes the commit atomically.
// Returns conflicts if any file's base doesn't match the current head.
func applyPush(ctx context.Context, s *store.Store, req PushRequest, authorID string) (id string, seq int64, conflicts []ConflictFile, err error) {
	// Build the store.CommitRecord from the push request.
	files := make([]store.CommitFileRecord, len(req.Files))
	for i, f := range req.Files {
		files[i] = store.CommitFileRecord{
			Path:            f.Path,
			ObjectHash:      f.ObjectHash,
			Size:            f.Size,
			ChangeType:      f.ChangeType,
			BasedOnCommitID: f.BasedOnCommitID,
		}
	}

	// OCC check: for each file, verify based_on_commit_id matches current head.
	for _, f := range req.Files {
		head, err := s.FileHead(f.Path)
		if err != nil {
			return "", 0, nil, fmt.Errorf("file head %s: %w", f.Path, err)
		}
		currentHead := ""
		if head != nil {
			currentHead = head.CommitID
		}
		if currentHead != f.BasedOnCommitID {
			conflicts = append(conflicts, ConflictFile{
				Path:                f.Path,
				CurrentHeadCommitID: currentHead,
			})
		}
	}
	if len(conflicts) > 0 {
		return "", 0, conflicts, nil
	}

	// All checks passed — write commit with server-assigned seq.
	createdAt := req.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().UnixMilli()
	}
	id, err = s.WriteRemoteCommit(store.CommitRecord{
		Message:   req.Message,
		AuthorID:  authorID, // the authenticated signer
		CreatedAt: createdAt,
		Parents:   req.Parents,
		Files:     files,
	}, 0, "") // seq=0 auto-assigns; "" recomputes the canonical ID from the full pushed file set
	if err != nil {
		return "", 0, nil, err
	}
	seq, err = s.SeqForCommit(id)
	return id, seq, nil, err
}

// handleGetCommits handles GET /<repo>/commits, permission-filtered per caller.
func (srv *Server) handleGetCommits(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}
	caller := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, caller) {
		return
	}

	q := r.URL.Query()
	var since int64
	if s := q.Get("since"); s != "" {
		since, _ = strconv.ParseInt(s, 10, 64)
	}
	var limit int
	if l := q.Get("limit"); l != "" {
		limit64, _ := strconv.ParseInt(l, 10, 64)
		limit = int(limit64)
	}

	commits, err := rh.s.LogCommits(store.LogFilter{
		Cursor:  since,
		Limit:   limit,
		Reverse: true, // oldest-first for incremental sync
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}

	out := make([]CommitJSON, 0, len(commits))
	for _, c := range commits {
		cj, err := srv.filterCommit(rh, caller, c)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
			return
		}
		out = append(out, cj)
	}
	writeJSON(w, http.StatusOK, out)
}

// filterCommit returns a permission-filtered view of a commit for the caller:
// only readable files are included; a commit with no readable files becomes a
// stub {seq, redacted:true}.
func (srv *Server) filterCommit(rh *repoHandle, caller string, c *store.Commit) (CommitJSON, error) {
	var readable []store.CommitFile
	for _, f := range c.Files {
		ok, err := rh.s.CanRead(caller, f.Path)
		if err != nil {
			return CommitJSON{}, err
		}
		if ok {
			readable = append(readable, f)
		}
	}
	if len(readable) == 0 {
		return CommitJSON{Seq: c.Seq, Redacted: true}, nil
	}
	filtered := *c
	filtered.Files = readable
	return commitToJSON(&filtered), nil
}

// handleGetHeads handles GET /<repo>/heads.
func (srv *Server) handleGetHeads(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}

	caller := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, caller) {
		return
	}
	heads, err := rh.s.AllFileHeads()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	filtered := make([]map[string]string, 0, len(heads))
	for _, h := range heads {
		ok, err := rh.s.CanRead(caller, h["path"])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
			return
		}
		if ok {
			filtered = append(filtered, h)
		}
	}
	writeJSON(w, http.StatusOK, filtered)
}

// handleGetPrincipals handles GET /<repo>/principals — the member roster.
func (srv *Server) handleGetPrincipals(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}
	if !srv.requireMember(w, rh, principalFromContext(r.Context())) {
		return
	}
	principals, err := rh.s.ListPrincipals()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	out := make([]PrincipalJSON, len(principals))
	for i, p := range principals {
		out[i] = PrincipalJSON{ID: p.ID, Username: p.Username, PublicKey: p.PublicKey}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAuthz handles GET /<repo>/authz — the caller's repo-wide authority.
func (srv *Server) handleAuthz(w http.ResponseWriter, r *http.Request) {
	rh, err := srv.repo(r.PathValue("repo"))
	if err != nil {
		writeError(w, http.StatusNotFound, "repo_not_found", err.Error(), nil)
		return
	}
	caller := principalFromContext(r.Context())
	if !srv.requireMember(w, rh, caller) {
		return
	}
	admin, err := rh.s.IsAdmin(caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	rootOwner, err := rh.s.IsAdminOrRootOwner(caller)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, AuthzResponse{Admin: admin, RootOwner: rootOwner})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, detail *ConflictDetail) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: msg, Detail: detail})
}

func commitToJSON(c *store.Commit) CommitJSON {
	files := make([]CommitFileJSON, len(c.Files))
	for i, f := range c.Files {
		files[i] = CommitFileJSON{
			Path:            f.Path,
			ObjectHash:      f.ObjectHash,
			Size:            f.Size,
			ChangeType:      f.ChangeType,
			BasedOnCommitID: f.BasedOnCommitID,
		}
	}
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return CommitJSON{
		ID: c.ID, Seq: c.Seq, Message: c.Message,
		AuthorID: c.AuthorID, CreatedAt: c.CreatedAt,
		Parents: parents, Files: files,
	}
}
