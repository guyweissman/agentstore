package protocol

// Protocol identifiers — frozen at first release.
// These appear in hash preimages and wire headers; a rename must NOT touch them.
// They are separate from the brand package for this reason.
const (
	// CommitV1Tag is the domain/version tag prepended to every commit preimage.
	CommitV1Tag = "agentstore-commit-v1\n"

	// RequestV1Tag is the domain/version tag prepended to every request-signing preimage.
	RequestV1Tag = "agentstore-request-v1\n"

	// Wire headers carried on every HTTP request and the WebSocket handshake.
	HeaderProto     = "X-AgentStore-Proto"
	HeaderPrincipal = "X-AgentStore-Principal"
	HeaderTimestamp = "X-AgentStore-Timestamp"
	HeaderSignature = "X-AgentStore-Signature"

	// Version is the protocol version declared in HeaderProto.
	Version = "1"
)
