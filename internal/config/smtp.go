// Package config 从文件或环境变量加载 SMTP 等配置。
package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// 配置路径与 SMTP 环境变量名
const (
	defaultConfigPath = "config.json"
	envConfigPath    = "CONFIG_PATH"
	envSMTPServer    = "SMTP_SERVER"
	envSMTPPort      = "SMTP_PORT"
	envSMTPUser      = "SMTP_USER"
	envSMTPPassword  = "SMTP_PASSWORD"
	envSMTPAuthCode  = "SMTP_AUTH_CODE"
	envSMTPFrom      = "SMTP_FROM"
	envSMTPTo        = "SMTP_TO"
)

type SMTP struct {
	Server   string `json:"smtp_server"`
	Port     int    `json:"smtp_port"`
	User     string `json:"smtp_user"`
	Password string `json:"smtp_password"`
	From     string `json:"smtp_from"`
	To       string `json:"smtp_to"`
}

// LoadSMTP 先读 envConfigPath 指定文件（默认 config.json），再被环境变量覆盖。
func LoadSMTP() *SMTP {
	cfg := &SMTP{}
	configPath := os.Getenv(envConfigPath)
	if configPath == "" {
		configPath = defaultConfigPath
	}
	if b, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(b, cfg)
	}
	if v := os.Getenv(envSMTPServer); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv(envSMTPPort); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}
	if v := os.Getenv(envSMTPUser); v != "" {
		cfg.User = v
	}
	if v := os.Getenv(envSMTPPassword); v != "" {
		cfg.Password = v
	}
	if v := os.Getenv(envSMTPAuthCode); v != "" {
		cfg.Password = v
	}
	if v := os.Getenv(envSMTPFrom); v != "" {
		cfg.From = v
	}
	if v := os.Getenv(envSMTPTo); v != "" {
		cfg.To = v
	}

	if cfg.From == "" && cfg.User != "" {
		cfg.From = cfg.User
	}

	return cfg
}

func (s *SMTP) Enabled() bool {
	srv := strings.TrimSpace(s.Server)
	from := strings.TrimSpace(s.From)
	to := strings.TrimSpace(s.To)
	return srv != "" && from != "" && to != ""
}
