package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ObjectStore is a content-addressed store for file content.
// Objects are stored at objects/<hash[0:2]>/<hash[2:]> and are immutable.
type ObjectStore struct {
	dir string // absolute path to the objects/ directory
}

func newObjectStore(dir string) *ObjectStore {
	return &ObjectStore{dir: dir}
}

// HashContent computes the object hash for the given content without storing it.
// The hash is SHA-256 of "blob <size>\0<content>" — identical to git's object hash with SHA-256.
func HashContent(content []byte) string {
	h := sha256.New()
	header := "blob " + strconv.Itoa(len(content)) + "\x00"
	h.Write([]byte(header))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// WriteObject stores content and returns its hash.
// Idempotent: if the object already exists it is not overwritten.
//
// Crash safety: the content is fsync'd to disk before the rename that makes it
// visible under its final path. This guarantees that any object_hash reference
// in commit_files resolves to readable, complete content — the object-before-
// metadata write ordering required by the architecture.
func (o *ObjectStore) WriteObject(content []byte) (string, error) {
	hash := HashContent(content)
	path := o.objectPath(hash)

	if _, err := os.Stat(path); err == nil {
		return hash, nil // already present; immutability guaranteed
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("write object %s: %w", hash[:8], err)
	}

	// Write to a temp file, fsync for durability, then rename atomically.
	// fsync before rename: guarantees content is on disk before the directory
	// entry (and thus before any commit_files metadata row) can reference it.
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o444)
	if err != nil {
		return "", fmt.Errorf("write object %s: %w", hash[:8], err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("write object %s: %w", hash[:8], err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("fsync object %s: %w", hash[:8], err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write object %s: %w", hash[:8], err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write object %s: %w", hash[:8], err)
	}
	// Sync the containing directory so the rename is durable.
	if df, err := os.Open(dir); err == nil {
		df.Sync()
		df.Close()
	}
	return hash, nil
}

// ValidObjectHash reports whether h is a well-formed object hash: 64 lowercase
// hex characters (the form HashContent produces). Guards the path-slicing in
// objectPath so a malformed hash can never panic a caller.
func ValidObjectHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ReadObject returns the content stored under hash.
func (o *ObjectStore) ReadObject(hash string) ([]byte, error) {
	if !ValidObjectHash(hash) {
		return nil, fmt.Errorf("invalid object hash %q", hash)
	}
	data, err := os.ReadFile(o.objectPath(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("object %s not found", hash[:8])
	}
	return data, err
}

// HasObject reports whether hash exists in the store. A malformed hash is simply
// "not present" (never panics).
func (o *ObjectStore) HasObject(hash string) bool {
	if !ValidObjectHash(hash) {
		return false
	}
	_, err := os.Stat(o.objectPath(hash))
	return err == nil
}

func (o *ObjectStore) objectPath(hash string) string {
	return filepath.Join(o.dir, hash[:2], hash[2:])
}

// TotalSize returns the total bytes stored in the object store. Used for the
// repo-size limit. O(n) over objects; acceptable at v0.1 scale (a maintained
// counter is the optimization at scale).
func (o *ObjectStore) TotalSize() (int64, error) {
	var total int64
	err := filepath.WalkDir(o.dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
