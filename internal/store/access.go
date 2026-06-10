package store

import (
	"context"
	"fmt"
	"strings"
)

// Permission levels, ordered read < write < owner.
const (
	PermRead  = "read"
	PermWrite = "write"
	PermOwner = "owner"
)

func permRank(p string) int {
	switch p {
	case PermRead:
		return 1
	case PermWrite:
		return 2
	case PermOwner:
		return 3
	}
	return 0
}

// Principal is a member of the repo (snapshot of the directory identity).
type Principal struct {
	ID        string
	Username  string
	PublicKey string
}

// AddPrincipal inserts (or replaces) a member snapshot in the repo.
func (s *Store) AddPrincipal(p Principal) error {
	_, err := s.DB.ExecContext(context.Background(), `
		INSERT INTO principals (id, username, public_key, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET username = excluded.username, public_key = excluded.public_key`,
		p.ID, p.Username, p.PublicKey, nowMS())
	return err
}

// HasPrincipal reports whether a principal is a member of the repo.
func (s *Store) HasPrincipal(principalID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM principals WHERE id = ?`, principalID).Scan(&n)
	return n > 0, err
}

// PrincipalIDByUsername resolves a repo member's principal_id from their username.
func (s *Store) PrincipalIDByUsername(username string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT id FROM principals WHERE username = ?`, username).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("no member %q", username)
	}
	return id, nil
}

// UsernameByPrincipalID resolves a member's username from their principal_id.
func (s *Store) UsernameByPrincipalID(principalID string) (string, error) {
	var name string
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT username FROM principals WHERE id = ?`, principalID).Scan(&name)
	return name, err
}

// ListPrincipals returns all repo members.
func (s *Store) ListPrincipals() ([]Principal, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT id, username, public_key FROM principals ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Principal
	for rows.Next() {
		var p Principal
		if err := rows.Scan(&p.ID, &p.Username, &p.PublicKey); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RemovePrincipal removes a member, cascading their grants and roles, in a single
// transaction. Refuses to remove the last admin (which would orphan the repo).
// Historical attributions (commit author_id, grant/role granted_by) are not FKs,
// so they persist after removal — authorship and provenance are permanent facts.
func (s *Store) RemovePrincipal(principalID string) error {
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Last-admin protection, inside the transaction.
	var isAdmin bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM repo_roles WHERE principal_id = ? AND role = 'admin')`,
		principalID).Scan(&isAdmin); err != nil {
		return err
	}
	if isAdmin {
		var adminCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM repo_roles WHERE role = 'admin'`).Scan(&adminCount); err != nil {
			return err
		}
		if adminCount <= 1 {
			return fmt.Errorf("cannot remove the last admin")
		}
	}

	for _, q := range []string{
		`DELETE FROM grants WHERE principal_id = ?`,
		`DELETE FROM repo_roles WHERE principal_id = ?`,
		`DELETE FROM principals WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, principalID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- roles ---

// AddRole grants a repo-level role (admin) to a principal.
func (s *Store) AddRole(principalID, role, grantedBy string) error {
	_, err := s.DB.ExecContext(context.Background(), `
		INSERT INTO repo_roles (principal_id, role, granted_by, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(principal_id, role) DO NOTHING`,
		principalID, role, grantedBy, nowMS())
	return err
}

// IsAdmin reports whether a principal holds the admin role.
func (s *Store) IsAdmin(principalID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM repo_roles WHERE principal_id = ? AND role = 'admin'`,
		principalID).Scan(&n)
	return n > 0, err
}

// ListAdmins returns the principal IDs holding the admin role.
func (s *Store) ListAdmins() ([]string, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT principal_id FROM repo_roles WHERE role = 'admin' ORDER BY principal_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RevokeAdmin removes the admin role, refusing to remove the last admin.
// The last-admin check and the DELETE run in a single transaction so concurrent
// revoke calls cannot both pass the check and leave the repo with zero admins.
func (s *Store) RevokeAdmin(principalID string) error {
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var isAdmin bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM repo_roles WHERE principal_id = ? AND role = 'admin')`,
		principalID).Scan(&isAdmin); err != nil {
		return err
	}
	if !isAdmin {
		return fmt.Errorf("%s is not an admin", principalID)
	}
	var adminCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM repo_roles WHERE role = 'admin'`).Scan(&adminCount); err != nil {
		return err
	}
	if adminCount <= 1 {
		return fmt.Errorf("cannot remove the last admin")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM repo_roles WHERE principal_id = ? AND role = 'admin'`, principalID); err != nil {
		return err
	}
	return tx.Commit()
}

// --- grants ---

// Grant is a single access control grant row.
type Grant struct {
	PrincipalID string
	PathPattern string
	Permission  string
	GrantedBy   string
}

// SetGrant sets (creating or replacing) a grant on an exact path or prefix pattern.
func (s *Store) SetGrant(principalID, pathPattern, permission, grantedBy string) error {
	if permRank(permission) == 0 {
		return fmt.Errorf("invalid permission %q", permission)
	}
	_, err := s.DB.ExecContext(context.Background(), `
		INSERT INTO grants (principal_id, path_pattern, permission, granted_by, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(principal_id, path_pattern) DO UPDATE SET permission = excluded.permission`,
		principalID, pathPattern, permission, grantedBy, nowMS())
	return err
}

// RevokeGrant removes a principal's grant on the exact pattern given.
func (s *Store) RevokeGrant(principalID, pathPattern string) error {
	res, err := s.DB.ExecContext(context.Background(),
		`DELETE FROM grants WHERE principal_id = ? AND path_pattern = ?`, principalID, pathPattern)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no grant for %s on %s", principalID, pathPattern)
	}
	return nil
}

// GrantsForPath returns all (principal, permission) grants whose pattern matches path.
func (s *Store) GrantsForPath(path string) ([]Grant, error) {
	all, err := s.allGrants()
	if err != nil {
		return nil, err
	}
	var out []Grant
	for _, g := range all {
		if patternMatches(g.PathPattern, path) {
			out = append(out, g)
		}
	}
	return out, nil
}

// AllGrants returns every grant in the repo (for mirror/export).
func (s *Store) AllGrants() ([]Grant, error) {
	return s.allGrants()
}

// Role is a repo-level role assignment.
type Role struct {
	PrincipalID string
	Role        string
	GrantedBy   string
}

// AllRoles returns every role assignment in the repo (for mirror/export).
func (s *Store) AllRoles() ([]Role, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT principal_id, role, granted_by FROM repo_roles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.PrincipalID, &r.Role, &r.GrantedBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) allGrants() ([]Grant, error) {
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT principal_id, path_pattern, permission, granted_by FROM grants`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.PrincipalID, &g.PathPattern, &g.Permission, &g.GrantedBy); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// EffectivePermission returns a principal's effective permission level on path:
// the maximum level across all grants whose pattern matches the path or an
// ancestor. Admins always have owner-level access. "" means no access.
func (s *Store) EffectivePermission(principalID, path string) (string, error) {
	admin, err := s.IsAdmin(principalID)
	if err != nil {
		return "", err
	}
	if admin {
		return PermOwner, nil
	}
	rows, err := s.DB.QueryContext(context.Background(),
		`SELECT path_pattern, permission FROM grants WHERE principal_id = ?`, principalID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	best := ""
	for rows.Next() {
		var pattern, perm string
		if err := rows.Scan(&pattern, &perm); err != nil {
			return "", err
		}
		if patternMatches(pattern, path) && permRank(perm) > permRank(best) {
			best = perm
		}
	}
	return best, rows.Err()
}

// CanRead / CanWrite are convenience thresholds over EffectivePermission.
func (s *Store) CanRead(principalID, path string) (bool, error) {
	p, err := s.EffectivePermission(principalID, path)
	return permRank(p) >= permRank(PermRead), err
}

func (s *Store) CanWrite(principalID, path string) (bool, error) {
	p, err := s.EffectivePermission(principalID, path)
	return permRank(p) >= permRank(PermWrite), err
}

// HasAnyWrite reports whether a principal can write somewhere in the repo: an
// admin, or the holder of a write/owner grant on any path. Gates object upload
// so a purely read-only member cannot consume repo storage.
func (s *Store) HasAnyWrite(principalID string) (bool, error) {
	admin, err := s.IsAdmin(principalID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}
	var n int
	err = s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM grants WHERE principal_id = ? AND permission IN ('write','owner')`,
		principalID).Scan(&n)
	return n > 0, err
}

// CanGrant reports whether a principal may grant/revoke on a path: admins always,
// and owners of the path (or an ancestor).
func (s *Store) CanGrant(principalID, path string) (bool, error) {
	p, err := s.EffectivePermission(principalID, path)
	return permRank(p) >= permRank(PermOwner), err
}

// patternMatches reports whether a grant pattern covers a concrete path.
// A prefix pattern ("/strategy/*") matches everything beneath it; an exact
// pattern ("/strategy/icp.md") matches only that path.
func patternMatches(pattern, path string) bool {
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "*") // keep trailing slash
		return strings.HasPrefix(path, prefix)
	}
	return pattern == path
}
