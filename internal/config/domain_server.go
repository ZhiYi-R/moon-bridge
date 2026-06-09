package config

// ServerConfig is the config domain model for the server layer.
// Note: This is distinct from the global ServerFileConfig sub-type used in YAML loading.
type ServerConfig struct {
	Addr        string
	AuthToken   string
	AuthType    AuthType
	Mode        string
	MaxSessions int
	SessionTTL  string
}

// ServerFromGlobalConfig extracts server-relevant fields from the global config.
func ServerFromGlobalConfig(cfg *Config) ServerConfig {
	return ServerConfig{
		Addr:        cfg.Addr,
		AuthToken:   cfg.AuthToken,
		AuthType:    cfg.AuthType,
		Mode:        string(cfg.Mode),
		MaxSessions: cfg.MaxSessions,
		SessionTTL:  cfg.SessionTTL,
	}
}
