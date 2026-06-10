// Package server implements the AgentStore HTTP server.
// One server process serves multiple repos; each repo is an independent store
// under the data directory.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/config"
	"github.com/guyweissman/agentstore/internal/storage/sqlite"
	"github.com/guyweissman/agentstore/internal/store"
)

// Server is the AgentStore HTTP server.
type Server struct {
	dataDir  string
	cfg      config.ServerConfig
	http     *http.Server
	serverDB *sql.DB // server-level state: the identity directory

	mu       sync.RWMutex
	repos    map[string]*repoHandle
	creating map[string]bool // repo names being created/mirrored, reserved under mu
}

// repoHandle wraps a store with a write mutex for OCC serialization and the
// repo's in-memory event hub.
type repoHandle struct {
	s   *store.Store
	mu  sync.Mutex // serializes push transactions
	hub *hub
}

// newRepoHandle wraps a store, starts its event hub, and returns the handle.
func newRepoHandle(s *store.Store) *repoHandle {
	rh := &repoHandle{s: s}
	rh.hub = newHub(func(principalID, path string) bool {
		ok, err := s.CanRead(principalID, path)
		return err == nil && ok
	})
	go rh.hub.run()
	return rh
}

// New creates a Server rooted at dataDir, loading config from server.toml if present.
func New(dataDir string) (*Server, error) {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	cfg, err := config.LoadServer(dataDir)
	if err != nil {
		return nil, fmt.Errorf("load server config: %w", err)
	}
	srv := &Server{
		dataDir:  dataDir,
		cfg:      cfg,
		repos:    make(map[string]*repoHandle),
		creating: make(map[string]bool),
	}
	// Initialise server-level state (the identity directory, empty until M3).
	if err := srv.initServerDB(); err != nil {
		return nil, fmt.Errorf("init server db: %w", err)
	}
	// Re-open any repos that already exist on disk.
	if err := srv.loadExistingRepos(); err != nil {
		return nil, err
	}
	return srv, nil
}

// Start binds to addr and serves, blocking until the server is shut down.
// addr overrides server.toml if non-empty.
func (srv *Server) Start(addr string) error {
	if addr == "" {
		addr = srv.cfg.Server.Addr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return srv.Serve(ln)
}

// Serve serves on an already-bound listener, blocking until the server is shut
// down. Start binds the listener for callers; tests bind their own so they can
// learn the chosen port (e.g. an ephemeral "0.0.0.0:0").
func (srv *Server) Serve(ln net.Listener) error {
	addr := ln.Addr().String()
	if !isLoopbackAddr(addr) {
		fmt.Fprintf(os.Stderr, "WARNING: agentstore is serving on %s without TLS. "+
			"Use a TLS-terminating reverse proxy for any internet-facing deployment.\n", addr)
	}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	srv.http = &http.Server{
		Handler: mux,
		// Bound the header read to blunt slow-header (Slowloris) connections.
		// No ReadTimeout/WriteTimeout: those would sever the long-lived watch
		// WebSocket, whose request is intentionally open for its lifetime.
		ReadHeaderTimeout: 30 * time.Second,
	}
	// Write pidfile.
	pidPath := filepath.Join(srv.dataDir, brand.PIDFile)
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)

	fmt.Fprintf(os.Stderr, "agentstore server listening on %s\n", addr)
	err := srv.http.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully drains the server.
func (srv *Server) Shutdown(ctx context.Context) error {
	if srv.http == nil {
		return nil
	}
	err := srv.http.Shutdown(ctx)
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	for _, rh := range srv.repos {
		rh.hub.Stop()
		rh.s.Close()
	}
	if srv.serverDB != nil {
		srv.serverDB.Close()
	}
	os.Remove(filepath.Join(srv.dataDir, brand.PIDFile))
	return err
}

// Handler returns an http.Handler suitable for use with httptest.NewServer.
func (srv *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return mux
}

// registerRoutes wires URL patterns to handlers. All endpoints require a valid
// signed request EXCEPT /register, which is the open identity-establishment call.
func (srv *Server) registerRoutes(mux *http.ServeMux) {
	auth := srv.authenticate

	// Open endpoints — the identity directory is public.
	mux.HandleFunc("POST /register", srv.handleRegister)
	mux.HandleFunc("GET /_directory", srv.handleDirectory)

	// Mirror bootstrap — self-authenticated against the roster it carries (the
	// target directory is empty until this seeds it), so it is NOT behind the
	// standard auth middleware. Admin-gated by the roles in the payload.
	mux.HandleFunc("POST /{repo}/mirror", srv.handleMirror)

	// Identity (signed).
	mux.HandleFunc("GET /whoami", auth(srv.handleWhoAmI))
	mux.HandleFunc("POST /rekey", auth(srv.handleRekey))

	// Repo + data plane (signed).
	mux.HandleFunc("POST /{repo}", auth(srv.handleCreateRepo))
	mux.HandleFunc("PUT /{repo}/objects/{hash}", auth(srv.handleUploadObject))
	mux.HandleFunc("GET /{repo}/objects/{hash}", auth(srv.handleDownloadObject))
	mux.HandleFunc("POST /{repo}/commits", auth(srv.handlePush))
	mux.HandleFunc("GET /{repo}/commits", auth(srv.handleGetCommits))
	mux.HandleFunc("GET /{repo}/heads", auth(srv.handleGetHeads))
	mux.HandleFunc("GET /{repo}/principals", auth(srv.handleGetPrincipals))
	mux.HandleFunc("GET /{repo}/authz", auth(srv.handleAuthz))
	mux.HandleFunc("GET /{repo}/export", auth(srv.handleExport))
	mux.HandleFunc("GET /{repo}/watch", auth(srv.handleWatch))

	// Access control plane (signed).
	mux.HandleFunc("POST /{repo}/grants", auth(srv.handleGrant))
	mux.HandleFunc("DELETE /{repo}/grants", auth(srv.handleRevoke))
	mux.HandleFunc("GET /{repo}/permissions", auth(srv.handlePermissions))
	mux.HandleFunc("POST /{repo}/members", auth(srv.handleAddMember))
	mux.HandleFunc("DELETE /{repo}/members", auth(srv.handleRemoveMember))
	mux.HandleFunc("GET /{repo}/admins", auth(srv.handleListAdmins))
	mux.HandleFunc("POST /{repo}/admins", auth(srv.handleAddAdmin))
	mux.HandleFunc("DELETE /{repo}/admins", auth(srv.handleRevokeAdmin))
}

// repo returns the repoHandle for name, or an error if it doesn't exist.
func (srv *Server) repo(name string) (*repoHandle, error) {
	srv.mu.RLock()
	rh := srv.repos[name]
	srv.mu.RUnlock()
	if rh == nil {
		return nil, fmt.Errorf("repo %q not found", name)
	}
	return rh, nil
}

// reserveRepo claims a repo name for creation (create or mirror), failing if it
// already exists or is mid-creation. Pairs with releaseRepo and guarantees only
// one creator touches a given repo directory at a time.
func (srv *Server) reserveRepo(name string) error {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if _, exists := srv.repos[name]; exists {
		return fmt.Errorf("repo %q already exists", name)
	}
	if srv.creating[name] {
		return fmt.Errorf("repo %q is already being created", name)
	}
	srv.creating[name] = true
	return nil
}

func (srv *Server) releaseRepo(name string) {
	srv.mu.Lock()
	delete(srv.creating, name)
	srv.mu.Unlock()
}

func (srv *Server) registerRepo(name string, rh *repoHandle) {
	srv.mu.Lock()
	srv.repos[name] = rh
	srv.mu.Unlock()
}

// createRepo creates a new repo seeded with the caller as first admin + owner of /*.
// On any failure after the store directory is created, the directory is removed so
// no partial repo lingers (and can't be picked up by loadExistingRepos on restart).
func (srv *Server) createRepo(name, principalID string) error {
	if err := srv.reserveRepo(name); err != nil {
		return err
	}
	defer srv.releaseRepo(name)

	entry, err := srv.directoryEntryByID(principalID)
	if err != nil {
		return fmt.Errorf("caller not in directory: %w", err)
	}

	repoDir := filepath.Join(srv.dataDir, name)
	s, err := store.InitBare(repoDir)
	if err != nil {
		return fmt.Errorf("init repo %q: %w", name, err)
	}
	ok := false
	defer func() {
		if !ok {
			s.Close()
			os.RemoveAll(repoDir)
		}
	}()

	// Seed the caller as the first member, admin, and owner of /*.
	if err := s.AddPrincipal(store.Principal{ID: entry.PrincipalID, Username: entry.Username, PublicKey: entry.PublicKey}); err != nil {
		return err
	}
	if err := s.AddRole(principalID, "admin", principalID); err != nil {
		return err
	}
	if err := s.SetGrant(principalID, "/*", store.PermOwner, principalID); err != nil {
		return err
	}

	srv.registerRepo(name, newRepoHandle(s))
	ok = true
	return nil
}

// initServerDB opens server.db (kept open for the server's lifetime) and applies
// the server-level schema: the identity directory.
func (srv *Server) initServerDB() error {
	db, err := sqlite.Open(filepath.Join(srv.dataDir, brand.ServerDB))
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS directory (
			principal_id TEXT PRIMARY KEY,
			username     TEXT NOT NULL UNIQUE,
			public_key   TEXT NOT NULL DEFAULT '',
			created_at   INTEGER NOT NULL
		)`); err != nil {
		db.Close()
		return err
	}
	srv.serverDB = db
	return nil
}

func (srv *Server) loadExistingRepos() error {
	entries, err := os.ReadDir(srv.dataDir)
	if err != nil {
		return nil // data dir might be empty on first run
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		storeDB := filepath.Join(srv.dataDir, e.Name(), brand.StoreDir, brand.StoreDB)
		if _, err := os.Stat(storeDB); err != nil {
			continue
		}
		s, err := store.Open(filepath.Join(srv.dataDir, e.Name()))
		if err != nil {
			return fmt.Errorf("re-open repo %q: %w", e.Name(), err)
		}
		srv.repos[e.Name()] = newRepoHandle(s)
	}
	return nil
}

// DrainTimeout is the maximum time Shutdown waits for in-flight requests.
const DrainTimeout = 30 * time.Second

// isLoopbackAddr reports whether addr (host:port) binds only to the loopback interface.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
