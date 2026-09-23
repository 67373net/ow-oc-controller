package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port int

	// OpenWrt Router
	OpenWrtHost string

	// OpenClash RESTful API
	OpenClashAPIPort   int
	OpenClashAPISecret string

	// OpenWrt SSH Credentials
	OpenWrtSSHPort int
	OpenWrtSSHUser string
	OpenWrtSSHPass string
	OpenWrtSSHKey  string

	// Operational settings
	PowerToggleMode      string // "smart", "service", "mode"
	TestURL              string
	TestTimeout          int // milliseconds
	PollInterval         int // seconds
	ConnLogRetentionDays int // days to keep connection logs, default 8
	SubscriptionProxy    string // optional http proxy, e.g. http://127.0.0.1:7890 or http://192.168.1.1:7890
}

// LoadConfig loads environment variables with .env support and defaults
func LoadConfig() *Config {
	loadDotEnv(GetEnvFilePath())

	cfg := &Config{
		Port:                 getEnvInt("PORT", 8080),
		OpenWrtHost:          getEnv("OPENWRT_HOST", "192.168.1.1"),
		OpenClashAPIPort:     getEnvInt("OPENCLASH_API_PORT", 9090),
		OpenClashAPISecret:   getEnv("OPENCLASH_API_SECRET", ""),
		OpenWrtSSHPort:       getEnvInt("OPENWRT_SSH_PORT", 22),
		OpenWrtSSHUser:       getEnv("OPENWRT_SSH_USER", "root"),
		OpenWrtSSHPass:       getEnv("OPENWRT_SSH_PASS", ""),
		OpenWrtSSHKey:        getEnv("OPENWRT_SSH_KEY", ""),
		PowerToggleMode:      getEnv("POWER_TOGGLE_MODE", "smart"),
		TestURL:              getEnv("TEST_URL", "http://www.gstatic.com/generate_204"),
		TestTimeout:          getEnvInt("TEST_TIMEOUT", 3000),
		PollInterval:         getEnvInt("POLL_INTERVAL", 5),
		ConnLogRetentionDays: getEnvInt("CONN_LOG_RETENTION_DAYS", 8),
		SubscriptionProxy:    getEnv("SUBSCRIPTION_PROXY", ""),
	}

	return cfg
}

func (c *Config) ClashAPIURL() string {
	return fmt.Sprintf("http://%s:%d", c.OpenWrtHost, c.OpenClashAPIPort)
}

func (c *Config) SSHAddress() string {
	return fmt.Sprintf("%s:%d", c.OpenWrtHost, c.OpenWrtSSHPort)
}

func (c *Config) HasSSHConfig() bool {
	return c.OpenWrtSSHPass != "" || c.OpenWrtSSHKey != ""
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return i
		}
	}
	return fallback
}

// Simple .env parser to avoid heavy external dependencies
func loadDotEnv(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

// GetEnvFilePath locates the active .env file path
func GetEnvFilePath() string {
	if _, err := os.Stat("/app/host_project"); err == nil {
		return "/app/host_project/.env"
	}
	if _, err := os.Stat(".env"); err == nil {
		return ".env"
	}
	if _, err := os.Stat("/app/.env"); err == nil {
		return "/app/.env"
	}
	if _, err := os.Stat("data"); err == nil {
		return "data/.env"
	}
	return ".env"
}

// GetEnvExamplePath locates the .env.example template file
func GetEnvExamplePath() string {
	if _, err := os.Stat("/app/host_project/.env.example"); err == nil {
		return "/app/host_project/.env.example"
	}
	if _, err := os.Stat(".env.example"); err == nil {
		return ".env.example"
	}
	if _, err := os.Stat("/app/.env.example"); err == nil {
		return "/app/.env.example"
	}
	return ""
}

// ParseEnvString parses raw .env content into key-value pairs
func ParseEnvString(content string) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			result[key] = val
		}
	}
	return result
}

// ApplyEnvMap hot-updates Config struct fields and os.Environ from parsed key-value pairs
func (c *Config) ApplyEnvMap(envMap map[string]string) {
	for k, v := range envMap {
		os.Setenv(k, v)
	}
	if v, ok := envMap["PORT"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			c.Port = i
		}
	}
	if v, ok := envMap["OPENWRT_HOST"]; ok {
		c.OpenWrtHost = v
	}
	if v, ok := envMap["OPENCLASH_API_PORT"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			c.OpenClashAPIPort = i
		}
	}
	if v, ok := envMap["OPENCLASH_API_SECRET"]; ok {
		c.OpenClashAPISecret = v
	}
	if v, ok := envMap["OPENWRT_SSH_PORT"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			c.OpenWrtSSHPort = i
		}
	}
	if v, ok := envMap["OPENWRT_SSH_USER"]; ok {
		c.OpenWrtSSHUser = v
	}
	if v, ok := envMap["OPENWRT_SSH_PASS"]; ok {
		c.OpenWrtSSHPass = v
	}
	if v, ok := envMap["OPENWRT_SSH_KEY"]; ok {
		c.OpenWrtSSHKey = v
	}
	if v, ok := envMap["POWER_TOGGLE_MODE"]; ok {
		c.PowerToggleMode = v
	}
	if v, ok := envMap["TEST_URL"]; ok {
		c.TestURL = v
	}
	if v, ok := envMap["TEST_TIMEOUT"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			c.TestTimeout = i
		}
	}
	if v, ok := envMap["POLL_INTERVAL"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			c.PollInterval = i
		}
	}
	if v, ok := envMap["CONN_LOG_RETENTION_DAYS"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			c.ConnLogRetentionDays = i
		}
	}
	if v, ok := envMap["SUBSCRIPTION_PROXY"]; ok {
		c.SubscriptionProxy = v
	}
}
