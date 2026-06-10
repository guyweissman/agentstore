package brand

// Brand strings — all freely replaceable before the v0.1 release.
// Reference these constants everywhere; never hardcode the strings.
const (
	AppName       = "store"
	StoreDir      = ".agentstore"
	GlobalDirName = ".agentstore"
	EnvPrefix     = "AGENTSTORE"

	StoreDB    = "store.db"
	IndexDB    = "index.db"
	ObjectsDir = "objects"
	ConfigFile = "config"
	PIDFile    = "server.pid"

	ServerDB     = "server.db"
	ServerConfig = "server.toml"
)
