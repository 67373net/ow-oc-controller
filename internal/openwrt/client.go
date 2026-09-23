package openwrt

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ModernClashUA is the universally accepted modern Clash/Meta User-Agent
const ModernClashUA = "clash-verge/v1.7.7 (Mihomo/v1.18.10) ClashMeta/v1.18.10"

type Client struct {
	addr      string
	user      string
	pass      string
	keyPath   string
	hasConfig bool
	sshConfig *ssh.ClientConfig
	clientMu  sync.Mutex
}

type ProfileInfo struct {
	Name     string `json:"name"`
	Filename string `json:"filename"`
	IsActive bool   `json:"is_active"`
}

func NewClient(addr, user, pass, keyPath string) *Client {
	c := &Client{
		addr:      addr,
		user:      user,
		pass:      pass,
		keyPath:   keyPath,
		hasConfig: (pass != "" || keyPath != "") && addr != "",
	}

	if !c.hasConfig {
		return c
	}

	authMethods := make([]ssh.AuthMethod, 0)
	if pass != "" {
		authMethods = append(authMethods, ssh.Password(pass))
	}

	if keyPath != "" {
		keyBytes, err := os.ReadFile(keyPath)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(keyBytes)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	c.sshConfig = &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Local LAN trusted connection
		Timeout:         10 * time.Second,
	}

	return c
}

func (c *Client) IsConfigured() bool {
	return c.hasConfig
}

// UpdateConfig updates connection address and credentials for hot-reloading
func (c *Client) UpdateConfig(addr, user, pass, keyPath string) {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()

	c.addr = addr
	c.user = user
	c.pass = pass
	c.keyPath = keyPath
	c.hasConfig = (pass != "" || keyPath != "") && addr != ""

	if !c.hasConfig {
		c.sshConfig = nil
		return
	}

	authMethods := make([]ssh.AuthMethod, 0)
	if pass != "" {
		authMethods = append(authMethods, ssh.Password(pass))
	}

	if keyPath != "" {
		keyBytes, err := os.ReadFile(keyPath)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(keyBytes)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	c.sshConfig = &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
}

// RunCommand executes a command on OpenWrt and returns stdout and error
func (c *Client) RunCommand(ctx context.Context, cmd string) (string, error) {
	if !c.hasConfig || c.sshConfig == nil {
		return "", fmt.Errorf("ssh credentials not configured (OPENWRT_SSH_PASS is empty)")
	}

	c.clientMu.Lock()
	defer c.clientMu.Unlock()

	client, err := ssh.Dial("tcp", c.addr, c.sshConfig)
	if err != nil {
		return "", fmt.Errorf("SSH connection to %s failed: %w", c.addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case err := <-done:
		outStr := strings.TrimSpace(stdout.String())
		errStr := strings.TrimSpace(stderr.String())
		if err != nil {
			if errStr != "" {
				return outStr, fmt.Errorf("%v (stderr: %s)", err, errStr)
			}
			return outStr, err
		}
		return outStr, nil
	}
}

// IsOpenClashRunning checks if OpenClash core process or init script is active
func (c *Client) IsOpenClashRunning(ctx context.Context) (bool, error) {
	if !c.hasConfig {
		return false, fmt.Errorf("ssh not configured")
	}

	// Check core binary with pidof (avoids self-matching) or check active PID file
	cmd := "if (pidof clash clash_meta clash_tun mihomo >/dev/null 2>&1 || [ -f /var/run/openclash.pid -a -d \"/proc/$(cat /var/run/openclash.pid 2>/dev/null)\" ]); then echo 'RUNNING'; else echo 'STOPPED'; fi"
	out, err := c.RunCommand(ctx, cmd)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "RUNNING"), nil
}

// StartService starts OpenClash service
func (c *Client) StartService(ctx context.Context) (string, error) {
	// Enable in UCI, then start via init script (fallback to restart if start fails)
	cmd := "uci set openclash.config.enable=1 && uci commit openclash && (/etc/init.d/openclash start || /etc/init.d/openclash restart) 2>&1"
	return c.RunCommand(ctx, cmd)
}

// StopService stops OpenClash service completely and kills core process
func (c *Client) StopService(ctx context.Context) (string, error) {
	// 1. Mark disabled in UCI
	// 2. Call init.d stop
	// 3. Fallback killall clash to ensure no lingering core
	// 4. Clean up any pid files
	cmd := "uci set openclash.config.enable=0 && uci commit openclash && /etc/init.d/openclash stop 2>&1; killall -9 clash clash_meta clash_tun mihomo 2>/dev/null || true; rm -f /var/run/openclash.pid /tmp/openclash.pid 2>/dev/null || true; echo 'STOPPED'"
	return c.RunCommand(ctx, cmd)
}

// RestartService restarts OpenClash service
func (c *Client) RestartService(ctx context.Context) (string, error) {
	cmd := "(/etc/init.d/openclash restart || /etc/init.d/openclash start) 2>&1"
	return c.RunCommand(ctx, cmd)
}

// ListProfiles retrieves all airport profile configs and checks which one is active
func (c *Client) ListProfiles(ctx context.Context) ([]ProfileInfo, string, error) {
	if !c.hasConfig {
		return nil, "", fmt.Errorf("ssh not configured")
	}

	// 1. Get current config path from UCI
	activePath, _ := c.RunCommand(ctx, "uci get openclash.config.config_path 2>/dev/null")
	activeFilename := filepath.Base(strings.TrimSpace(activePath))

	// 2. List all yaml configs in /etc/openclash/config/
	cmd := "ls -1 /etc/openclash/config/ 2>/dev/null | grep -iE '\\.(yaml|yml)$'"
	output, err := c.RunCommand(ctx, cmd)
	if err != nil {
		return nil, "", err
	}

	lines := strings.Split(output, "\n")
	profiles := make([]ProfileInfo, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		name := strings.TrimSuffix(trimmed, filepath.Ext(trimmed))
		isActive := trimmed == activeFilename
		profiles = append(profiles, ProfileInfo{
			Name:     name,
			Filename: trimmed,
			IsActive: isActive,
		})
	}

	return profiles, activeFilename, nil
}

// SwitchProfile switches OpenClash active config to the target yaml and restarts
func (c *Client) SwitchProfile(ctx context.Context, filename string) (string, error) {
	if !c.hasConfig {
		return "", fmt.Errorf("ssh not configured")
	}

	safeName := filepath.Clean(filepath.Base(filename))
	cmd := fmt.Sprintf("uci set openclash.config.config_path='/etc/openclash/config/%s' && uci commit openclash && (/etc/init.d/openclash restart || /etc/init.d/openclash start) 2>&1", safeName)
	return c.RunCommand(ctx, cmd)
}

// DownloadProfile downloads a remote subscription to /etc/openclash/config/
func (c *Client) DownloadProfile(ctx context.Context, filename, subURL string) (string, error) {
	if !c.hasConfig {
		return "", fmt.Errorf("ssh not configured")
	}
	safeName := filepath.Clean(filepath.Base(filename))
	if !strings.HasSuffix(safeName, ".yaml") && !strings.HasSuffix(safeName, ".yml") {
		safeName += ".yaml"
	}

	cmd := fmt.Sprintf("curl -sL -k -m 60 -H 'User-Agent: %s' -o '/etc/openclash/config/%s' '%s' 2>&1", ModernClashUA, safeName, subURL)
	return c.RunCommand(ctx, cmd)
}

// DeleteProfile removes a subscription file from /etc/openclash/config/
func (c *Client) DeleteProfile(ctx context.Context, filename string) (string, error) {
	if !c.hasConfig {
		return "", fmt.Errorf("ssh not configured")
	}
	safeName := filepath.Clean(filepath.Base(filename))
	cmd := fmt.Sprintf("rm -f '/etc/openclash/config/%s'", safeName)
	return c.RunCommand(ctx, cmd)
}

// SaveProfileContent writes yaml content directly to /etc/openclash/config/
func (c *Client) SaveProfileContent(ctx context.Context, filename, base64Content string) (string, error) {
	if !c.hasConfig {
		return "", fmt.Errorf("ssh not configured")
	}
	safeName := filepath.Clean(filepath.Base(filename))
	cmd := fmt.Sprintf("echo '%s' | base64 -d > '/etc/openclash/config/%s'", base64Content, safeName)
	return c.RunCommand(ctx, cmd)
}

// WriteFile writes arbitrary content directly to a remote path via SSH stdin
func (c *Client) WriteFile(ctx context.Context, remotePath string, content []byte) error {
	if !c.hasConfig || c.sshConfig == nil {
		return fmt.Errorf("ssh not configured")
	}

	c.clientMu.Lock()
	defer c.clientMu.Unlock()

	client, err := ssh.Dial("tcp", c.addr, c.sshConfig)
	if err != nil {
		return fmt.Errorf("SSH connection to %s failed: %w", c.addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	session.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	session.Stderr = &stderr

	cmd := fmt.Sprintf("cat > '%s'", remotePath)
	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w: %s", err, stderr.String())
		}
		return nil
	}
}

// GetRecentOpenClashLog reads the recent log lines from OpenWrt /tmp/openclash.log or logread
func (c *Client) GetRecentOpenClashLog(ctx context.Context, lines int) (string, error) {
	if !c.hasConfig {
		return "", fmt.Errorf("ssh not configured")
	}
	if lines <= 0 {
		lines = 50
	}
	cmd := fmt.Sprintf("if [ -f /tmp/openclash.log ]; then tail -n %d /tmp/openclash.log; else logread -e 'OpenClash' | tail -n %d; fi", lines, lines)
	return c.RunCommand(ctx, cmd)
}

// DiagnoseFailure extracts fatal error or core failure messages from recent OpenClash log
func (c *Client) DiagnoseFailure(ctx context.Context) string {
	logContent, err := c.GetRecentOpenClashLog(ctx, 40)
	if err != nil || strings.TrimSpace(logContent) == "" {
		return ""
	}
	lines := strings.Split(logContent, "\n")
	var errLines []string
	for _, l := range lines {
		lTrim := strings.TrimSpace(l)
		if lTrim == "" {
			continue
		}
		lower := strings.ToLower(lTrim)
		if strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "level=error") ||
			strings.Contains(lower, "[error]") ||
			strings.Contains(lower, "core start failed") ||
			strings.Contains(lower, "parse config error") ||
			strings.Contains(lower, "not found") ||
			strings.Contains(lower, "panic:") {
			errLines = append(errLines, lTrim)
		}
	}
	if len(errLines) > 0 {
		if len(errLines) > 6 {
			errLines = errLines[len(errLines)-6:]
		}
		return strings.Join(errLines, "\n")
	}
	// Fallback to last 6 lines of log
	if len(lines) > 6 {
		return strings.Join(lines[len(lines)-6:], "\n")
	}
	return logContent
}

// DiagnoseFatal quickly checks if there is an explicit fatal/panic in the latest log
func (c *Client) DiagnoseFatal(ctx context.Context) string {
	logContent, err := c.GetRecentOpenClashLog(ctx, 25)
	if err != nil || strings.TrimSpace(logContent) == "" {
		return ""
	}
	lines := strings.Split(logContent, "\n")
	var fatalLines []string
	for _, l := range lines {
		lTrim := strings.TrimSpace(l)
		if lTrim == "" {
			continue
		}
		lower := strings.ToLower(lTrim)
		if strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "core start failed") ||
			strings.Contains(lower, "parse config error") ||
			strings.Contains(lower, "panic:") {
			fatalLines = append(fatalLines, lTrim)
		}
	}
	if len(fatalLines) > 0 {
		if len(fatalLines) > 4 {
			fatalLines = fatalLines[len(fatalLines)-4:]
		}
		return strings.Join(fatalLines, "\n")
	}
	return ""
}
