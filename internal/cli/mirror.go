package cli

import (
	"fmt"

	"github.com/guyweissman/agentstore/internal/client"
	"github.com/guyweissman/agentstore/internal/server"
)

// RunMirror relocates a repo from sourceURL to an empty targetURL. The caller
// must be an admin (only an admin's view is complete). It reads the full source
// state — roster, history, objects, grants, roles — and uploads it verbatim to
// the target, preserving commit ids and seqs. The mirror request is signed with
// the admin's source key and self-authenticated against the roster it carries.
// It returns the signer's resulting identity on the target (principal_id
// preserved; username may be auto-renamed) plus any roster renames.
func RunMirror(sourceURL, targetURL string, sourceID *client.Identity) (server.MirrorResponse, error) {
	var empty server.MirrorResponse
	src, err := client.New(sourceURL, sourceID)
	if err != nil {
		return empty, err
	}
	// Target client signs with the SAME (source) key; the target verifies it
	// against the roster in the payload, since its directory is still empty.
	dst, err := client.New(targetURL, sourceID)
	if err != nil {
		return empty, err
	}

	roster, err := src.GetPrincipals()
	if err != nil {
		return empty, fmt.Errorf("read roster: %w", err)
	}
	commits, err := src.GetAllCommits(0)
	if err != nil {
		return empty, fmt.Errorf("read commits: %w", err)
	}
	export, err := src.Export()
	if err != nil {
		return empty, fmt.Errorf("read grants/roles (are you an admin?): %w", err)
	}

	// Collect and download every referenced object (deduped).
	seen := map[string]bool{}
	var objects []server.MirrorObject
	for _, c := range commits {
		if c.Redacted {
			// An admin should see no stubs; a stub here means an incomplete view.
			return empty, fmt.Errorf("source view is incomplete (redacted commit %d); mirror requires admin", c.Seq)
		}
		for _, f := range c.Files {
			if f.ObjectHash == "" || seen[f.ObjectHash] {
				continue
			}
			seen[f.ObjectHash] = true
			data, err := src.DownloadObject(f.ObjectHash)
			if err != nil {
				return empty, fmt.Errorf("download object %s: %w", f.ObjectHash[:8], err)
			}
			objects = append(objects, server.MirrorObject{Hash: f.ObjectHash, Content: data})
		}
	}

	req := server.MirrorRequest{
		Principals: roster,
		Objects:    objects,
		Commits:    commits,
		Grants:     export.Grants,
		Roles:      export.Roles,
	}
	resp, err := dst.Mirror(req)
	if err != nil {
		return empty, fmt.Errorf("upload mirror: %w", err)
	}
	return resp, nil
}
