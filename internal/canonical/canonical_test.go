package canonical_test

import (
	"encoding/hex"
	"testing"

	"github.com/guyweissman/agentstore/internal/canonical"
)

// knownHash is a fixed 32-byte object hash used as test data.
var knownHash = mustDecodeHex("abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

func mustDecodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestCommitIDGolden pins the exact byte encoding of the commit preimage.
// If this test breaks, the commit ID algorithm has changed — all existing IDs are invalidated.
func TestCommitIDGolden(t *testing.T) {
	c := canonical.CommitContent{
		Message:   "Initial commit",
		AuthorID:  "principal_00000000000000000000000000000001",
		CreatedAt: 1717286400000,
		Parents:   nil,
		Files: []canonical.CommitFile{
			{Path: "/strategy/icp.md", ObjectHash: knownHash},
		},
	}

	gotID := canonical.CommitID(c)
	gotBytes := canonical.CommitPreimageBytes(c)

	// Golden values — pinned; any change here means commit IDs across the system are invalidated.
	const wantID = "10a25303e33d36ea8fd5816f3461e30a4f09743c63fb3d27ef3207336455cfea"
	const wantBytesHex = "6167656e7473746f72652d636f6d6d69742d76310a0000000e496e697469616c20636f6d6d69740000002a7072696e636970616c5f30303030303030303030303030303030303030303030303030303030303030310000018fd63ef0000000000000000001000000102f73747261746567792f6963702e6d6401abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	if gotID != wantID {
		t.Errorf("CommitID\n got  %s\n want %s", gotID, wantID)
	}
	if hex.EncodeToString(gotBytes) != wantBytesHex {
		t.Errorf("CommitPreimageBytes\n got  %s\n want %s", hex.EncodeToString(gotBytes), wantBytesHex)
	}
}

// TestCommitIDSortOrder verifies that file order in the input does not affect the commit ID.
func TestCommitIDSortOrder(t *testing.T) {
	base := canonical.CommitContent{
		Message:   "test",
		AuthorID:  "principal_test",
		CreatedAt: 0,
		Files: []canonical.CommitFile{
			{Path: "/b.md", ObjectHash: knownHash},
			{Path: "/a.md", ObjectHash: knownHash},
		},
	}
	reversed := canonical.CommitContent{
		Message:   "test",
		AuthorID:  "principal_test",
		CreatedAt: 0,
		Files: []canonical.CommitFile{
			{Path: "/a.md", ObjectHash: knownHash},
			{Path: "/b.md", ObjectHash: knownHash},
		},
	}
	if canonical.CommitID(base) != canonical.CommitID(reversed) {
		t.Error("CommitID must be stable regardless of file input order")
	}
}

// TestCommitIDDeletion verifies that a deleted file (nil ObjectHash) produces a different
// preimage than a set operation.
func TestCommitIDDeletion(t *testing.T) {
	set := canonical.CommitContent{
		Message: "set", AuthorID: "p", CreatedAt: 0,
		Files: []canonical.CommitFile{{Path: "/f.md", ObjectHash: knownHash}},
	}
	del := canonical.CommitContent{
		Message: "del", AuthorID: "p", CreatedAt: 0,
		Files: []canonical.CommitFile{{Path: "/f.md", ObjectHash: nil}},
	}
	if canonical.CommitID(set) == canonical.CommitID(del) {
		t.Error("set and delete operations must produce different commit IDs")
	}
}

// TestRequestPreimageGolden pins the exact byte encoding of the request preimage.
// If this test breaks, all signatures are invalidated.
func TestRequestPreimageGolden(t *testing.T) {
	bodyHash := mustDecodeHex("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	r := canonical.RequestContent{
		PrincipalID:   "principal_00000000000000000000000000000001",
		Method:        "POST",
		RequestTarget: "/my-repo/commits",
		Timestamp:     1717286400000,
		BodySHA256:    bodyHash,
	}

	got := canonical.RequestPreimageBytes(r)

	// Golden value — pinned; any change here means all request signatures are invalidated.
	const wantHex = "6167656e7473746f72652d726571756573742d76310a0000002a7072696e636970616c5f303030303030303030303030303030303030303030303030303030303030303100000004504f5354000000102f6d792d7265706f2f636f6d6d6974730000018fd63ef000e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	if hex.EncodeToString(got) != wantHex {
		t.Errorf("RequestPreimageBytes\n got  %s\n want %s", hex.EncodeToString(got), wantHex)
	}
}
