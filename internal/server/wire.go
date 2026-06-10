package server

// Wire types shared between handlers and the client package.

// PushRequest is the body of POST /<repo>/commits.
type PushRequest struct {
	Message   string     `json:"message"`
	CreatedAt int64      `json:"created_at"`
	Parents   []string   `json:"parents"`
	Files     []PushFile `json:"files"`
}

// PushFile is one file entry in a PushRequest.
type PushFile struct {
	Path            string `json:"path"`
	ChangeType      string `json:"change_type"`
	ObjectHash      string `json:"object_hash,omitempty"`
	Size            int64  `json:"size,omitempty"`
	BasedOnCommitID string `json:"based_on_commit_id,omitempty"`
}

// PushResponse is the body of a successful 200 response to a push.
type PushResponse struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}

// ErrorResponse is the body of any non-2xx response.
type ErrorResponse struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  *ConflictDetail `json:"detail,omitempty"`
}

// ConflictDetail is included in a push_conflict ErrorResponse.
type ConflictDetail struct {
	Conflicts []ConflictFile `json:"conflicts"`
}

// ConflictFile names a file that blocked a push and its current server head.
type ConflictFile struct {
	Path                string `json:"path"`
	CurrentHeadCommitID string `json:"current_head_commit_id"`
}

// CommitJSON is the serialised form of a commit in GET /<repo>/commits.
// A fully-inaccessible commit is delivered as a stub: {seq, redacted:true}.
type CommitJSON struct {
	ID        string           `json:"id,omitempty"`
	Seq       int64            `json:"seq"`
	Message   string           `json:"message,omitempty"`
	AuthorID  string           `json:"author_id,omitempty"`
	CreatedAt int64            `json:"created_at,omitempty"`
	Parents   []string         `json:"parents,omitempty"`
	Files     []CommitFileJSON `json:"files,omitempty"`
	Redacted  bool             `json:"redacted,omitempty"`
}

// CommitFileJSON is one file entry in a CommitJSON.
type CommitFileJSON struct {
	Path            string `json:"path"`
	ObjectHash      string `json:"object_hash,omitempty"`
	Size            int64  `json:"size,omitempty"`
	ChangeType      string `json:"change_type"`
	BasedOnCommitID string `json:"based_on_commit_id,omitempty"`
}

// Event types, all derived from accepted commits.
const (
	EventFileCreated  = "file.created"
	EventFileModified = "file.modified"
	EventFileDeleted  = "file.deleted"
	EventCommitPushed = "commit.pushed"
)

// EventJSON is a single event delivered over the watch WebSocket. A file.* event
// carries Path/ObjectHash/Size; a commit.pushed carries Message/Paths. All events
// of one commit share the same Cursor (the commit's seq).
type EventJSON struct {
	Cursor     int64    `json:"cursor"`
	Type       string   `json:"type"`
	Timestamp  int64    `json:"timestamp"`
	CommitID   string   `json:"commit_id"`
	AuthorID   string   `json:"author_id"`
	Path       string   `json:"path,omitempty"`
	ObjectHash string   `json:"object_hash,omitempty"`
	Size       int64    `json:"size,omitempty"`
	Message    string   `json:"message,omitempty"`
	Paths      []string `json:"paths,omitempty"`
}

// AuthzResponse reports the caller's repo-wide authority (used by the client to
// gate whole-repo operations like `checkout .`).
type AuthzResponse struct {
	Admin     bool `json:"admin"`
	RootOwner bool `json:"root_owner"`
}

// PrincipalJSON is one entry in the GET /<repo>/principals roster response.
type PrincipalJSON struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	PublicKey string `json:"public_key"`
}

// HeadJSON is one entry in the GET /<repo>/heads response.
type HeadJSON struct {
	Path       string `json:"path"`
	CommitID   string `json:"commit_id"`
	ObjectHash string `json:"object_hash,omitempty"` // "" if deleted
	ChangeType string `json:"change_type"`
}

// CreateRepoResponse is returned by POST /<repo>.
type CreateRepoResponse struct {
	Repo string `json:"repo"`
}

// RegisterRequest is the body of POST /register (the open endpoint).
type RegisterRequest struct {
	Username  string `json:"username"`
	PublicKey string `json:"public_key"`
}

// RegisterResponse returns the server-assigned principal_id.
type RegisterResponse struct {
	PrincipalID string `json:"principal_id"`
}

// WhoAmIResponse is the body of GET /whoami.
type WhoAmIResponse struct {
	PrincipalID string `json:"principal_id"`
	Username    string `json:"username"`
}

// RekeyRequest is the body of POST /rekey.
type RekeyRequest struct {
	PublicKey string `json:"public_key"`
}

// MirrorGrant / MirrorRole carry access control rows verbatim in a mirror or export.
type MirrorGrant struct {
	PrincipalID string `json:"principal_id"`
	PathPattern string `json:"path_pattern"`
	Permission  string `json:"permission"`
	GrantedBy   string `json:"granted_by"`
}

type MirrorRole struct {
	PrincipalID string `json:"principal_id"`
	Role        string `json:"role"`
	GrantedBy   string `json:"granted_by"`
}

// ExportResponse is the body of GET /<repo>/export (admin only): the access control state
// not delivered by ordinary clone (grants + roles).
type ExportResponse struct {
	Grants []MirrorGrant `json:"grants"`
	Roles  []MirrorRole  `json:"roles"`
}

// MirrorObject is one content object carried inline in a mirror.
type MirrorObject struct {
	Hash    string `json:"hash"`
	Content []byte `json:"content"` // base64 in JSON
}

// MirrorRequest is the full repo state uploaded by `push --mirror` to an empty
// target. Commits are in seq order; ids and seqs are preserved verbatim.
type MirrorRequest struct {
	Principals []PrincipalJSON `json:"principals"` // roster (also seeds the target directory)
	Objects    []MirrorObject  `json:"objects"`
	Commits    []CommitJSON    `json:"commits"`
	Grants     []MirrorGrant   `json:"grants"`
	Roles      []MirrorRole    `json:"roles"`
}

// MirrorRename records a roster principal whose username collided with a
// different principal already in the target directory and was auto-renamed
// during seeding (e.g. "alice" -> "alice-2").
type MirrorRename struct {
	PrincipalID string `json:"principal_id"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// MirrorResponse is returned to the admin who performed the move. It reports the
// signer's resulting identity on the new server (principal_id is preserved; the
// username may have been auto-renamed) plus the full set of roster renames, so
// the admin can tell affected members which username to `bind`.
type MirrorResponse struct {
	Repo        string         `json:"repo"`
	PrincipalID string         `json:"principal_id"`
	Username    string         `json:"username"`
	Renames     []MirrorRename `json:"renames,omitempty"`
}

// DirectoryEntryResponse is one entry from the open GET /_directory plane: the
// single-row body of a ?username= lookup (used by `bind` to resolve a username
// to its principal_id + public key) and, in a list, one row of the no-param
// browse (used by `principals list --remote`). The directory is public, so the
// endpoint is unauthenticated.
type DirectoryEntryResponse struct {
	PrincipalID string `json:"principal_id"`
	Username    string `json:"username"`
	PublicKey   string `json:"public_key"`
}
