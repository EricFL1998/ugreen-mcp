package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	Port         int      `json:"port"`
	Host         string   `json:"host"`
	AuthToken    string   `json:"auth_token"`
	ReadOnly     bool     `json:"read_only"`
	AllowedPaths []string `json:"allowed_paths"`
	AuditLogPath string   `json:"audit_log_path"`
	ConfigPath   string   `json:"-"`
}

var (
	globalConfig *Config
	configLock   sync.RWMutex
)

func GenerateSecureToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("ugreen_mcp_%s", hex.EncodeToString(b))
}

func DefaultConfig() *Config {
	return &Config{
		Port:         8095,
		Host:         "0.0.0.0",
		AuthToken:    GenerateSecureToken(),
		ReadOnly:     true,
		AllowedPaths: []string{"/volume1"},
		AuditLogPath: "/var/log/ugreen-nas-mcp/audit.log",
	}
}

func LoadConfig(path string) (*Config, bool, error) {
	configLock.Lock()
	defer configLock.Unlock()

	cfg := DefaultConfig()
	cfg.ConfigPath = path

	data, err := os.ReadFile(path)
	isNewlyGenerated := false

	if err != nil {
		if os.IsNotExist(err) {
			// Generate new config and persist it
			isNewlyGenerated = true
			_ = os.MkdirAll(filepath.Dir(path), 0755)
			saveData, _ := json.MarshalIndent(cfg, "", "  ")
			_ = os.WriteFile(path, saveData, 0600)
			globalConfig = cfg
			return cfg, isNewlyGenerated, nil
		}
		return nil, false, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, false, err
	}

	// If existing config has no auth_token, generate and save
	if cfg.AuthToken == "" {
		cfg.AuthToken = GenerateSecureToken()
		isNewlyGenerated = true
		saveData, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile(path, saveData, 0600)
	}

	globalConfig = cfg
	return cfg, isNewlyGenerated, nil
}

func GetConfig() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	if globalConfig == nil {
		globalConfig = DefaultConfig()
	}
	return globalConfig
}
