package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Embedder EmbedderConfig `yaml:"embedder,omitempty"`
	LLM      LLMConfig      `yaml:"llm,omitempty"`
	Search   SearchConfig   `yaml:"search,omitempty"`
	Git      GitConfig      `yaml:"git,omitempty"`
	Server   ServerConfig   `yaml:"server,omitempty"`
	Auth     AuthConfig     `yaml:"auth,omitempty"`
	Limits   LimitsConfig   `yaml:"limits,omitempty"`
}

type EmbedderConfig struct {
	Type       string `yaml:"type,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty"`
	Model      string `yaml:"model,omitempty"`
	Dimensions int    `yaml:"dimensions,omitempty"`
	APIKeyEnv  string `yaml:"api_key_env,omitempty"`
}

type LLMConfig struct {
	BaseURL   string `yaml:"base_url,omitempty"`
	Model     string `yaml:"model,omitempty"`
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
}

type SearchConfig struct {
	Fusion     string  `yaml:"fusion,omitempty"`
	FTSWeight  float64 `yaml:"fts_weight,omitempty"`
	VecWeight  float64 `yaml:"vec_weight,omitempty"`
	Rerank     bool    `yaml:"rerank,omitempty"`
	RerankTopK int     `yaml:"rerank_top_k,omitempty"`
}

type GitConfig struct {
	Branch           string `yaml:"branch,omitempty"`
	RemoteName       string `yaml:"remote_name,omitempty"`
	Push             string `yaml:"push,omitempty"`
	DebounceInterval string `yaml:"debounce_interval,omitempty"`
	AuthorName       string `yaml:"author_name,omitempty"`
	AuthorEmail      string `yaml:"author_email,omitempty"`
}

type IndexMode string

const (
	IndexModeAuto   IndexMode = "auto"
	IndexModeManual IndexMode = "manual"
)

type ServerConfig struct {
	HTTPPort     int       `yaml:"http_port,omitempty"`
	MCPTransport string    `yaml:"mcp_transport,omitempty"`
	IndexMode    IndexMode `yaml:"index_mode,omitempty"`
}

type AuthConfig struct {
	APIKeys []string `yaml:"api_keys,omitempty"`
}

type LimitsConfig struct {
	MaxResponseChars int `yaml:"max_response_chars,omitempty"`
}

func Default() Config {
	return Config{
		Search: SearchConfig{
			Fusion:     "rrf",
			FTSWeight:  0.5,
			VecWeight:  0.5,
			Rerank:     true,
			RerankTopK: 10,
		},
		Git: GitConfig{
			Branch:           "main",
			RemoteName:       "origin",
			Push:             "off",
			DebounceInterval: "5m",
			AuthorName:       "kvt",
			AuthorEmail:      "kvt@local",
		},
		Server: ServerConfig{
			HTTPPort:     8200,
			MCPTransport: "stdio",
			IndexMode:    IndexModeAuto,
		},
		Auth: AuthConfig{
			APIKeys: []string{},
		},
		Limits: LimitsConfig{
			MaxResponseChars: 16000,
		},
	}
}

func Load(vaultPath string, explicitPath string) (Config, error) {
	cfg := Default()
	path := explicitPath
	if path == "" {
		path = filepath.Join(vaultPath, ".kvt", "config.yaml")
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return cfg, nil
			}
			return Config{}, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	from := ""
	if explicitPath != "" {
		from = explicitPath
	} else {
		from = filepath.Join(vaultPath, ".kvt", "config.yaml")
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", from, err)
	}
	return cfg, nil
}
