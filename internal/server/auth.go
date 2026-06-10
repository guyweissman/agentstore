package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/guyweissman/agentstore/internal/canonical"
	"github.com/guyweissman/agentstore/internal/identity"
	"github.com/guyweissman/agentstore/internal/protocol"
)

type ctxKey int

const principalKey ctxKey = 0

// maxBodyHeadroom is added to the max file size to bound the signed-request body
// read: object uploads are raw content (≤ max file size); commit/access control bodies are
// small JSON. The headroom covers JSON framing without allowing abuse.
const maxBodyHeadroom = 1 << 16 // 64 KiB

// principalFromContext returns the authenticated principal_id stored by authenticate.
func principalFromContext(ctx context.Context) string {
	id, _ := ctx.Value(principalKey).(string)
	return id
}

// authenticate wraps a handler, verifying the request signature and stashing the
// resolved principal_id in the request context. Rejects unsigned/invalid requests.
func (srv *Server) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principalID, err := srv.verifyRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", err.Error(), nil)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, principalID)
		next(w, r.WithContext(ctx))
	}
}

// verifyRequest validates the auth envelope and returns the principal_id.
// It buffers and restores the request body so the handler can read it again.
func (srv *Server) verifyRequest(r *http.Request) (string, error) {
	proto := r.Header.Get(protocol.HeaderProto)
	if proto != protocol.Version {
		return "", fmt.Errorf("unsupported protocol version %q", proto)
	}
	principalID := r.Header.Get(protocol.HeaderPrincipal)
	if principalID == "" {
		return "", errors.New("missing principal header")
	}
	tsStr := r.Header.Get(protocol.HeaderTimestamp)
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", errors.New("invalid timestamp header")
	}
	sigB64 := r.Header.Get(protocol.HeaderSignature)
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return "", errors.New("invalid signature encoding")
	}

	// Freshness window (two-sided).
	if !srv.freshTimestamp(ts) {
		return "", errors.New("timestamp outside the acceptable window, check your clock")
	}

	// Buffer the body so we can hash it and still let the handler read it.
	// Bound the read so an oversized body can't exhaust memory before the
	// per-handler size limit runs. The largest legitimate signed body is an
	// object upload (≤ max file size); the mirror bootstrap bypasses this path.
	var body []byte
	if r.Body != nil {
		max := srv.cfg.Limits.MaxFileSizeBytes + maxBodyHeadroom
		body, err = io.ReadAll(io.LimitReader(r.Body, max+1))
		if err != nil {
			return "", errors.New("failed to read body")
		}
		if int64(len(body)) > max {
			return "", errors.New("request body too large")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	bodyHash := sha256.Sum256(body)

	// Look up the principal's public key in the directory.
	pubLine, err := srv.directoryPublicKey(principalID)
	if err != nil {
		return "", errors.New("unknown principal")
	}
	pub, err := identity.ParsePublicKey(pubLine)
	if err != nil {
		return "", errors.New("stored public key is invalid")
	}

	preimage := canonical.RequestPreimageBytes(canonical.RequestContent{
		PrincipalID:   principalID,
		Method:        r.Method,
		RequestTarget: r.URL.RequestURI(),
		Timestamp:     ts,
		BodySHA256:    bodyHash[:],
	})
	if !identity.Verify(pub, preimage, sig) {
		return "", errors.New("signature verification failed")
	}
	return principalID, nil
}

// --- directory operations (server.db) ---

// directoryPublicKey returns the OpenSSH public-key line for a principal.
func (srv *Server) directoryPublicKey(principalID string) (string, error) {
	var pub string
	err := srv.serverDB.QueryRowContext(context.Background(),
		`SELECT public_key FROM directory WHERE principal_id = ?`, principalID).Scan(&pub)
	return pub, err
}

// directoryUsername returns the username for a principal.
func (srv *Server) directoryUsername(principalID string) (string, error) {
	var name string
	err := srv.serverDB.QueryRowContext(context.Background(),
		`SELECT username FROM directory WHERE principal_id = ?`, principalID).Scan(&name)
	return name, err
}

// directoryRegister inserts a new principal. Username must be unique.
func (srv *Server) directoryRegister(principalID, username, publicKey string) error {
	_, err := srv.serverDB.ExecContext(context.Background(),
		`INSERT INTO directory (principal_id, username, public_key, created_at) VALUES (?, ?, ?, ?)`,
		principalID, username, publicKey, time.Now().UnixMilli())
	return err
}

// directoryRekey updates a principal's public key.
func (srv *Server) directoryRekey(principalID, publicKey string) error {
	res, err := srv.serverDB.ExecContext(context.Background(),
		`UPDATE directory SET public_key = ? WHERE principal_id = ?`, publicKey, principalID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// directoryEntry holds a principal's directory record.
type directoryEntry struct {
	PrincipalID string
	Username    string
	PublicKey   string
}

// directoryEntryByID returns a principal's full directory record.
func (srv *Server) directoryEntryByID(principalID string) (directoryEntry, error) {
	var e directoryEntry
	err := srv.serverDB.QueryRowContext(context.Background(),
		`SELECT principal_id, username, public_key FROM directory WHERE principal_id = ?`,
		principalID).Scan(&e.PrincipalID, &e.Username, &e.PublicKey)
	return e, err
}

// directoryLookupByUsername finds a principal by username.
func (srv *Server) directoryLookupByUsername(username string) (directoryEntry, error) {
	var e directoryEntry
	err := srv.serverDB.QueryRowContext(context.Background(),
		`SELECT principal_id, username, public_key FROM directory WHERE username = ?`,
		username).Scan(&e.PrincipalID, &e.Username, &e.PublicKey)
	return e, err
}

// directoryList returns every registered principal, ordered by username. Backs
// the open directory-browse endpoint; public fields only.
func (srv *Server) directoryList() ([]directoryEntry, error) {
	rows, err := srv.serverDB.QueryContext(context.Background(),
		`SELECT principal_id, username, public_key FROM directory ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []directoryEntry
	for rows.Next() {
		var e directoryEntry
		if err := rows.Scan(&e.PrincipalID, &e.Username, &e.PublicKey); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// freshTimestamp reports whether a request timestamp (Unix ms) is within the
// configured two-sided freshness window. It compares the attacker-controlled
// timestamp against precomputed bounds rather than subtracting it, so no crafted
// value can overflow int64 (or trip abs64(math.MinInt64)) and wrap the check.
func (srv *Server) freshTimestamp(tsMillis int64) bool {
	now := time.Now().UnixMilli()
	windowMs := int64(srv.cfg.Auth.RequestFreshnessSeconds) * 1000
	// now ± windowMs cannot overflow for any sane configured window.
	return tsMillis >= now-windowMs && tsMillis <= now+windowMs
}
