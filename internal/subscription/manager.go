package subscription

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/67373net/ow-oc-controller/internal/clash"
	"github.com/67373net/ow-oc-controller/internal/logger"
	"github.com/67373net/ow-oc-controller/internal/openwrt"
)

// Item represents a subscription or profile configuration
type Item struct {
	Name      string `json:"name"`
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	Domain    string `json:"domain,omitempty"`
	UpdatedAt string `json:"updated_at"`
	IsActive  bool   `json:"is_active"`
	Type      string `json:"type"` // "subscription" or "local"
	Size      int64  `json:"size,omitempty"`
	Upload    int64  `json:"upload,omitempty"`
	Download  int64  `json:"download,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Expire    int64  `json:"expire,omitempty"`
	Remaining int64  `json:"remaining,omitempty"`
	UsageText string `json:"usage_text,omitempty"`
}

// Manager manages subscriptions, downloading, updating, and synchronizing with OpenWrt
type Manager struct {
	dataDir    string
	metaFile   string
	localDir   string
	routerHost string
	subProxy   string
	mu         sync.RWMutex
	items      map[string]Item
	log        *logger.MemoryLogger
	openwrt    *openwrt.Client
	clash      *clash.Client
	httpClient *http.Client
}

// NewManager creates a new Subscription Manager
func NewManager(dataDir string, log *logger.MemoryLogger, openwrtClient *openwrt.Client, clashClient *clash.Client, routerHost, subProxy string) *Manager {
	_ = os.MkdirAll(dataDir, 0755)
	localDir := filepath.Join(dataDir, "profiles")
	_ = os.MkdirAll(localDir, 0755)
	metaFile := filepath.Join(dataDir, "subscriptions.json")

	m := &Manager{
		dataDir:    dataDir,
		metaFile:   metaFile,
		localDir:   localDir,
		routerHost: routerHost,
		subProxy:   subProxy,
		items:      make(map[string]Item),
		log:        log,
		openwrt:    openwrtClient,
		clash:      clashClient,
		httpClient: &http.Client{Timeout: 45 * time.Second},
	}

	m.loadMeta()
	return m
}

// UpdateSettings updates router host and subscription proxy for hot-reloading
func (m *Manager) UpdateSettings(routerHost, subProxy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routerHost = routerHost
	m.subProxy = subProxy
}

func (m *Manager) loadMeta() {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.metaFile)
	if err != nil {
		return
	}

	var list []Item
	if err := json.Unmarshal(data, &list); err == nil {
		for _, item := range list {
			if item.Filename != "" {
				m.items[item.Filename] = item
			}
		}
	}
}

func (m *Manager) saveMeta() {
	list := make([]Item, 0, len(m.items))
	for _, it := range m.items {
		list = append(list, it)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err == nil {
		_ = os.WriteFile(m.metaFile, data, 0644)
	}
}

func cleanFilename(name string) (cleanName, filename string) {
	cleanName = strings.TrimSpace(name)
	filename = filepath.Clean(filepath.Base(cleanName))
	filename = strings.TrimSuffix(filename, ".yaml")
	filename = strings.TrimSuffix(filename, ".yml")
	cleanName = filename
	filename = filename + ".yaml"
	return cleanName, filename
}

func extractDomain(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return ""
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// parseSubscriptionUserInfo extracts upload, download, total, expire and remaining text from header
func parseSubscriptionUserInfo(headerVal string) (upload, download, total, expire, remaining int64, usageText string) {
	if headerVal == "" {
		return
	}
	parts := strings.Split(headerVal, ";")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "upload":
			upload = v
		case "download":
			download = v
		case "total":
			total = v
		case "expire":
			expire = v
		}
	}
	if total > 0 {
		remaining = total - (upload + download)
		if remaining < 0 {
			remaining = 0
		}
		usageText = fmt.Sprintf("剩余: %s / %s", formatBytes(remaining), formatBytes(total))
	}
	return
}

// extractUsageFromYAML scans yaml content for special usage/expiry pseudo-nodes
func extractUsageFromYAML(content []byte) string {
	lines := strings.Split(string(content), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.Contains(trimmed, "name:") {
			nameVal := strings.TrimPrefix(trimmed, "name:")
			nameVal = strings.Trim(nameVal, " '\"")
			if strings.Contains(nameVal, "剩余流量") || strings.Contains(nameVal, "重置") || strings.Contains(nameVal, "到期") {
				return nameVal
			}
		}
	}
	return ""
}

// extractNameFromHeadersOrURL automatically derives subscription title
func extractNameFromHeadersOrURL(subURL string, header http.Header, content []byte) string {
	// 1. Profile-Title header
	if pt := header.Get("profile-title"); pt != "" {
		pt = strings.TrimSpace(pt)
		if strings.HasPrefix(strings.ToLower(pt), "base64:") {
			b64 := strings.TrimPrefix(pt, pt[:7])
			if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil && len(decoded) > 0 {
				return strings.TrimSpace(string(decoded))
			}
		}
		if len(pt) > 0 {
			return pt
		}
	}
	if pt := header.Get("subscription-title"); pt != "" {
		return strings.TrimSpace(pt)
	}

	// 2. Content-Disposition filename
	if cd := header.Get("content-disposition"); cd != "" {
		if idx := strings.Index(cd, "filename*="); idx != -1 {
			fn := cd[idx+10:]
			if qIdx := strings.Index(fn, ";"); qIdx != -1 {
				fn = fn[:qIdx]
			}
			fn = strings.Trim(fn, " '\"")
			if strings.HasPrefix(strings.ToLower(fn), "utf-8''") {
				fn = fn[7:]
				if unescaped, err := url.PathUnescape(fn); err == nil {
					fn = unescaped
				}
			}
			if fn != "" {
				clean, _ := cleanFilename(fn)
				if clean != "" {
					return clean
				}
			}
		}
		if idx := strings.Index(cd, "filename="); idx != -1 {
			fn := cd[idx+9:]
			if qIdx := strings.Index(fn, ";"); qIdx != -1 {
				fn = fn[:qIdx]
			}
			fn = strings.Trim(fn, " '\"")
			if fn != "" {
				clean, _ := cleanFilename(fn)
				if clean != "" {
					return clean
				}
			}
		}
	}

	// 3. YAML comments
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineCount := 0
	for scanner.Scan() && lineCount < 25 {
		lineCount++
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "name:") || strings.Contains(lower, "title:") || strings.Contains(lower, "机场:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
					clean, _ := cleanFilename(parts[1])
					if clean != "" {
						return clean
					}
				}
			}
		}
	}

	// 4. Derive from URL domain or path
	if u, err := url.Parse(subURL); err == nil {
		host := u.Hostname()
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			domainName := parts[len(parts)-2]
			if len(domainName) > 2 {
				return domainName
			}
		}
		if host != "" {
			return host
		}
	}

	return fmt.Sprintf("sub_%d", time.Now().Unix()%10000)
}

// List returns all subscriptions merged with OpenWrt file profiles and Clash core providers
func (m *Manager) List(ctx context.Context) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	activeFile := ""
	openwrtFiles := make(map[string]bool)

	// 1. If OpenWrt SSH is configured, fetch files from /etc/openclash/config/
	if m.openwrt.IsConfigured() {
		files, act, err := m.openwrt.ListProfiles(ctx)
		if err == nil {
			activeFile = act
			for _, f := range files {
				openwrtFiles[f.Filename] = true
				// If not in managed subscriptions, add as local profile
				if _, exists := m.items[f.Filename]; !exists {
					m.items[f.Filename] = Item{
						Name:     f.Name,
						Filename: f.Filename,
						URL:      "",
						Type:     "local",
					}
				}
			}
		}
	}

	result := make([]Item, 0, len(m.items))
	for fn, item := range m.items {
		// If OpenWrt is configured and this item doesn't exist on OpenWrt (and has no URL), skip
		if m.openwrt.IsConfigured() && len(openwrtFiles) > 0 {
			if !openwrtFiles[fn] && item.URL == "" {
				continue
			}
		}
		// Determine active status
		item.IsActive = (fn == activeFile)
		// Check local file size if present
		localPath := filepath.Join(m.localDir, fn)
		if fi, err := os.Stat(localPath); err == nil {
			item.Size = fi.Size()
		}
		// Extract domain if URL present
		if item.URL != "" && item.Domain == "" {
			item.Domain = extractDomain(item.URL)
		}
		// Format usage if total > 0
		if item.UsageText == "" && item.Total > 0 {
			item.UsageText = fmt.Sprintf("剩余: %s / %s", formatBytes(item.Remaining), formatBytes(item.Total))
		}
		// If still empty and local file exists, check yaml
		if item.UsageText == "" {
			if data, err := os.ReadFile(localPath); err == nil {
				item.UsageText = extractUsageFromYAML(data)
			}
		}

		result = append(result, item)
	}

	// 2. Only if OpenWrt SSH is NOT configured and no subscriptions exist, check Clash Core HTTP providers
	if !m.openwrt.IsConfigured() && len(result) == 0 && m.clash != nil {
		providersResp, err := m.clash.GetProviders(ctx)
		if err == nil && providersResp != nil {
			for name, p := range providersResp.Providers {
				if strings.ToUpper(p.VehicleType) != "HTTP" {
					continue
				}
				u := strings.ToUpper(name)
				if u == "DEFAULT" || u == "GLOBAL" || u == "PROXY" || u == "DIRECT" || u == "REJECT" ||
					strings.Contains(name, "自动") || strings.Contains(name, "选择") || strings.Contains(name, "节点") ||
					strings.Contains(name, "漏网") || strings.Contains(u, "MATCH") || strings.Contains(u, "FALLBACK") {
					continue
				}

				result = append(result, Item{
					Name:      name,
					Filename:  name,
					Type:      "provider",
					UpdatedAt: p.UpdatedAt,
					IsActive:  true,
				})
			}
		}
	}

	// If no active item was determined and we have results, set first as active
	hasActive := false
	for _, it := range result {
		if it.IsActive {
			hasActive = true
			break
		}
	}
	if !hasActive && len(result) > 0 {
		result[0].IsActive = true
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsActive != result[j].IsActive {
			return result[i].IsActive
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func downloadViaHTTP(ctx context.Context, subURL string, proxyURLStr string, timeout time.Duration) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("User-Agent", "Clash/1.18.0 (ClashMeta/v1.18.0)")
	req.Header.Set("Accept", "*/*")

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	if proxyURLStr != "" {
		pURL, err := url.Parse(proxyURLStr)
		if err != nil {
			return nil, nil, fmt.Errorf("代理地址格式错误: %w", err)
		}
		transport.Proxy = http.ProxyURL(pURL)
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("网络下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("服务器返回状态异常: HTTP %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("读取数据流失败: %w", err)
	}

	if len(content) < 30 {
		return nil, nil, fmt.Errorf("下载内容过短，非有效订阅配置")
	}

	return content, resp.Header, nil
}

func parseCurlResponse(data []byte) ([]byte, http.Header, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	var lastHeaders http.Header
	var statusCode int

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "HTTP/") {
			parts := strings.Split(trimmed, " ")
			if len(parts) >= 2 {
				statusCode, _ = strconv.Atoi(parts[1])
			}
			lastHeaders = make(http.Header)
			for {
				hLine, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				hTrimmed := strings.TrimRight(hLine, "\r\n")
				if hTrimmed == "" {
					break
				}
				if colon := strings.Index(hTrimmed, ":"); colon > 0 {
					k := strings.TrimSpace(hTrimmed[:colon])
					v := strings.TrimSpace(hTrimmed[colon+1:])
					lastHeaders.Add(k, v)
				}
			}

			// If another HTTP status line follows (e.g. redirect), continue to next block
			peekBytes, _ := reader.Peek(5)
			if string(peekBytes) == "HTTP/" {
				continue
			}
			break
		}
	}

	body, _ := io.ReadAll(reader)
	if statusCode != 0 && (statusCode < 200 || statusCode >= 300) {
		return nil, lastHeaders, fmt.Errorf("HTTP %d", statusCode)
	}
	if len(body) < 30 {
		return nil, lastHeaders, fmt.Errorf("下载内容过短，非有效订阅配置")
	}
	return body, lastHeaders, nil
}

func ensureClashFlag(subURL string) string {
	if strings.Contains(subURL, "flag=") {
		return subURL
	}
	if strings.Contains(subURL, "?") {
		return subURL + "&flag=clash"
	}
	return subURL + "?flag=clash"
}

// withGlobalProxy temporarily switches Clash core mode to "Global" and ensures an active proxy node/group
// is selected in GLOBAL, so requests to airport subscription domains (which typically don't have explicit rules
// in airport configs and fall through to MATCH,DIRECT) are forced through overseas proxy nodes, completely
// avoiding GFW SNI blocking and EOF connection resets.
func (m *Manager) withGlobalProxy(ctx context.Context, fn func() ([]byte, http.Header, error)) ([]byte, http.Header, error) {
	if m.clash == nil {
		return fn()
	}

	cfg, err := m.clash.GetConfigs(ctx)
	if err != nil || cfg == nil {
		return fn()
	}

	origMode := cfg.Mode
	var origGlobalNow string

	// Check GLOBAL group
	if pResp, err := m.clash.GetProxies(ctx); err == nil && pResp != nil {
		if gItem, ok := pResp.Proxies["GLOBAL"]; ok {
			origGlobalNow = gItem.Now
			// If GLOBAL is currently set to DIRECT or Direct or empty, point it to an active proxy group/node
			if strings.EqualFold(gItem.Now, "DIRECT") || gItem.Now == "" {
				candidateTarget := ""
				for _, candidate := range []string{"Proxy", "PROXY", "节点选择", "AUTO", "Auto"} {
					if _, has := pResp.Proxies[candidate]; has {
						candidateTarget = candidate
						break
					}
				}
				if candidateTarget == "" {
					for _, candidate := range gItem.All {
						if !strings.EqualFold(candidate, "DIRECT") && !strings.EqualFold(candidate, "REJECT") && !strings.EqualFold(candidate, "COMPATIBLE") {
							candidateTarget = candidate
							break
						}
					}
				}
				if candidateTarget != "" {
					_ = m.clash.SelectProxy(ctx, "GLOBAL", candidateTarget)
				}
			}
		}
	}

	// Switch to Global mode
	if !strings.EqualFold(origMode, "Global") {
		m.log.Info("SUBSCRIPTION", "临时切换 Clash 核心为全局 (Global) 模式以使订阅域名强制走代理出海...")
		_ = m.clash.SetMode(ctx, "Global")
	}

	defer func() {
		// Restore original mode
		if origMode != "" && !strings.EqualFold(origMode, "Global") {
			_ = m.clash.SetMode(context.Background(), origMode)
			m.log.Info("SUBSCRIPTION", fmt.Sprintf("已恢复 Clash 核心原始运行模式 (%s)", origMode))
		}
		// Restore GLOBAL target if changed
		if origGlobalNow != "" {
			_ = m.clash.SelectProxy(context.Background(), "GLOBAL", origGlobalNow)
		}
	}()

	return fn()
}

// ErrGlobalConfirmRequired indicates that normal download attempts failed and explicit user confirmation
// is required to temporarily switch Clash core to Global mode.
type ErrGlobalConfirmRequired struct {
	Message string
	Errors  []string
}

func (e *ErrGlobalConfirmRequired) Error() string {
	if len(e.Errors) > 0 {
		return fmt.Sprintf("%s (%s)", e.Message, strings.Join(e.Errors, "; "))
	}
	return e.Message
}

func (m *Manager) downloadViaSSH(ctx context.Context, subURL string, proxyPort int) ([]byte, http.Header, error) {
	if m.openwrt == nil || !m.openwrt.IsConfigured() {
		return nil, nil, fmt.Errorf("OpenWrt SSH 未配置")
	}

	escapedURL := strings.ReplaceAll(subURL, "'", "'\\''")
	proxies := []string{fmt.Sprintf("http://127.0.0.1:%d", proxyPort)}
	if proxyPort != 7890 {
		proxies = append(proxies, "http://127.0.0.1:7890")
	}
	proxiesStr := "'" + strings.Join(proxies, "' '") + "'"

	cmd := fmt.Sprintf(`URL='%s'
UA='Clash/1.18.0 (ClashMeta/v1.18.0)'
for p in %s; do
  out=$(curl -sL -k -m 10 -x "$p" -H "User-Agent: $UA" -H "Accept: */*" -i "$URL" 2>/dev/null)
  if echo "$out" | grep -q 'HTTP/[123]'; then
    echo "$out"
    exit 0
  fi
done
exit 1`, escapedURL, proxiesStr)

	out, err := m.openwrt.RunCommand(ctx, cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH curl 失败: %w", err)
	}

	return parseCurlResponse([]byte(out))
}

func (m *Manager) download(ctx context.Context, subURL string, allowGlobal bool) ([]byte, http.Header, error) {
	subURL = ensureClashFlag(subURL)
	var errs []string

	// Strategy 0: Custom SUBSCRIPTION_PROXY if configured
	if m.subProxy != "" {
		m.log.Info("SUBSCRIPTION", fmt.Sprintf("尝试通过配置的代理 (%s) 下载订阅...", m.subProxy))
		proxyCtx, cancelProxy := context.WithTimeout(ctx, 20*time.Second)
		body, header, err := downloadViaHTTP(proxyCtx, subURL, m.subProxy, 20*time.Second)
		cancelProxy()
		if err == nil {
			m.log.Success("SUBSCRIPTION", "通过指定代理成功下载订阅")
			return body, header, nil
		}
		m.log.Warn("SUBSCRIPTION", fmt.Sprintf("通过指定代理下载失败: %v", err))
		errs = append(errs, fmt.Sprintf("指定代理: %v", err))
	}

	// Strategy 1: Direct download with 5-second timeout
	m.log.Info("SUBSCRIPTION", "尝试直接连接下载订阅...")
	directCtx, cancelDirect := context.WithTimeout(ctx, 5*time.Second)
	body, header, err := downloadViaHTTP(directCtx, subURL, "", 5*time.Second)
	cancelDirect()
	if err == nil {
		m.log.Success("SUBSCRIPTION", "直接下载订阅成功")
		return body, header, nil
	}
	m.log.Warn("SUBSCRIPTION", fmt.Sprintf("直连下载不可达 (%v)，准备通过常规代理通道尝试...", err))
	errs = append(errs, fmt.Sprintf("直连: %v", err))

	// Detect proxy port
	proxyPort := 7890
	if m.clash != nil {
		if cfg, errClash := m.clash.GetConfigs(ctx); errClash == nil && cfg != nil {
			if cfg.MixedPort > 0 {
				proxyPort = cfg.MixedPort
			} else if cfg.Port > 0 {
				proxyPort = cfg.Port
			}
		}
	}

	// Strategy 2: OpenClash router core proxy in NORMAL (Rule) mode (without changing Clash mode)
	if m.routerHost != "" {
		routerProxyURL := fmt.Sprintf("http://%s:%d", m.routerHost, proxyPort)
		m.log.Info("SUBSCRIPTION", fmt.Sprintf("尝试通过 OpenClash 核心代理 (%s, 规则分流模式) 下载订阅...", routerProxyURL))
		pCtx, cancelP := context.WithTimeout(ctx, 10*time.Second)
		b, h, err := downloadViaHTTP(pCtx, subURL, routerProxyURL, 10*time.Second)
		cancelP()
		if err == nil {
			m.log.Success("SUBSCRIPTION", fmt.Sprintf("通过核心代理 (%s) 成功下载订阅", routerProxyURL))
			return b, h, nil
		}
		m.log.Warn("SUBSCRIPTION", fmt.Sprintf("规则分流模式下核心代理下载未成功: %v", err))
		errs = append(errs, fmt.Sprintf("核心代理: %v", err))
	}

	// Strategy 3: OpenWrt router SSH curl in NORMAL (Rule) mode (without changing Clash mode)
	if m.openwrt != nil && m.openwrt.IsConfigured() {
		m.log.Info("SUBSCRIPTION", "尝试在 OpenWrt 路由器上通过 SSH curl (规则分流模式) 下载订阅...")
		sshCtx, cancelSSH := context.WithTimeout(ctx, 12*time.Second)
		b, h, err := m.downloadViaSSH(sshCtx, subURL, proxyPort)
		cancelSSH()
		if err == nil {
			m.log.Success("SUBSCRIPTION", "通过 OpenWrt 路由器 SSH 成功下载订阅")
			return b, h, nil
		}
		m.log.Warn("SUBSCRIPTION", fmt.Sprintf("规则分流模式下路由器 SSH 下载未成功: %v", err))
		errs = append(errs, fmt.Sprintf("路由器SSH: %v", err))
	}

	// All non-intrusive safe strategies failed.
	// If the user has NOT authorized temporary global mode, STOP here and return ErrGlobalConfirmRequired.
	if !allowGlobal {
		m.log.Warn("SUBSCRIPTION", "常规下载策略均失败（订阅域名疑似被防火墙拦截且未包含在规则分流中），等待用户确认是否临时启用全局出海模式...")
		return nil, nil, &ErrGlobalConfirmRequired{
			Message: "常规下载方式均失败（订阅域名疑似被防火墙阻断且未包含在规则分流中）",
			Errors:  errs,
		}
	}

	// Strategy 4: Fallback inside withGlobalProxy to force overseas proxy routing (EXPLICIT USER CONSENT)
	m.log.Info("SUBSCRIPTION", "用户已授权出海下载，临时切换 OpenClash 为全局模式以强制拉取订阅...")
	proxyBody, proxyHeader, proxyErr := m.withGlobalProxy(ctx, func() ([]byte, http.Header, error) {
		// Try via router core proxy first
		if m.routerHost != "" {
			routerProxyURL := fmt.Sprintf("http://%s:%d", m.routerHost, proxyPort)
			m.log.Info("SUBSCRIPTION", fmt.Sprintf("全局出海模式下尝试核心代理 (%s)...", routerProxyURL))
			pCtx, cancelP := context.WithTimeout(ctx, 15*time.Second)
			b, h, err := downloadViaHTTP(pCtx, subURL, routerProxyURL, 15*time.Second)
			cancelP()
			if err == nil {
				return b, h, nil
			}
			m.log.Warn("SUBSCRIPTION", fmt.Sprintf("全局模式下核心代理下载失败: %v", err))
		}

		// Try via OpenWrt SSH curl
		if m.openwrt != nil && m.openwrt.IsConfigured() {
			m.log.Info("SUBSCRIPTION", "全局出海模式下尝试路由器 SSH curl...")
			sshCtx, cancelSSH := context.WithTimeout(ctx, 20*time.Second)
			b, h, err := m.downloadViaSSH(sshCtx, subURL, proxyPort)
			cancelSSH()
			if err == nil {
				return b, h, nil
			}
			m.log.Warn("SUBSCRIPTION", fmt.Sprintf("全局模式下路由器 SSH 下载失败: %v", err))
		}

		return nil, nil, fmt.Errorf("全局模式下代理下载未成功")
	})

	if proxyErr == nil && proxyBody != nil {
		m.log.Success("SUBSCRIPTION", "全局出海代理模式下成功拉取订阅内容")
		return proxyBody, proxyHeader, nil
	}

	errs = append(errs, fmt.Sprintf("全局出海模式: %v", proxyErr))
	return nil, nil, fmt.Errorf("所有下载方式均失败: %s", strings.Join(errs, "; "))
}

// Add downloads a new subscription and syncs to OpenWrt
func (m *Manager) Add(ctx context.Context, name, subURL string, allowGlobal bool) (*Item, error) {
	subURL = strings.TrimSpace(subURL)
	if subURL == "" {
		return nil, fmt.Errorf("订阅链接不能为空")
	}

	m.log.Info("SUBSCRIPTION", fmt.Sprintf("开始下载订阅来源: %s", subURL))

	content, header, err := m.download(ctx, subURL, allowGlobal)
	if err != nil {
		m.log.Error("SUBSCRIPTION", "下载订阅失败", err.Error())
		return nil, err
	}

	// If name was not provided, automatically extract from headers or URL
	name = strings.TrimSpace(name)
	if name == "" {
		name = extractNameFromHeadersOrURL(subURL, header, content)
	}

	cleanName, filename := cleanFilename(name)
	if cleanName == "" {
		cleanName = fmt.Sprintf("sub_%d", time.Now().Unix()%10000)
		filename = cleanName + ".yaml"
	}

	// Parse subscription userinfo for usage
	upload, download, total, expire, remaining, usageText := parseSubscriptionUserInfo(header.Get("subscription-userinfo"))
	if usageText == "" {
		usageText = extractUsageFromYAML(content)
	}

	// Save to local cache
	localPath := filepath.Join(m.localDir, filename)
	if err := os.WriteFile(localPath, content, 0644); err != nil {
		m.log.Warn("SUBSCRIPTION", fmt.Sprintf("本地缓存文件写入失败: %v", err))
	}

	// Sync to OpenWrt /etc/openclash/config/
	if m.openwrt.IsConfigured() {
		remotePath := fmt.Sprintf("/etc/openclash/config/%s", filename)
		if err := m.openwrt.WriteFile(ctx, remotePath, content); err != nil {
			m.log.Error("SUBSCRIPTION", fmt.Sprintf("向 OpenWrt 写入配置文件 [%s] 失败", filename), err.Error())
			return nil, fmt.Errorf("向 OpenWrt 写入失败: %w", err)
		}
		m.log.Success("SUBSCRIPTION", fmt.Sprintf("已成功同步配置文件到 OpenWrt: %s", remotePath))
	}

	nowStr := time.Now().Format("060102-150405")
	item := Item{
		Name:      cleanName,
		Filename:  filename,
		URL:       subURL,
		Domain:    extractDomain(subURL),
		UpdatedAt: nowStr,
		Type:      "subscription",
		Size:      int64(len(content)),
		Upload:    upload,
		Download:  download,
		Total:     total,
		Expire:    expire,
		Remaining: remaining,
		UsageText: usageText,
	}

	m.mu.Lock()
	m.items[filename] = item
	m.saveMeta()
	m.mu.Unlock()

	m.log.Success("SUBSCRIPTION", fmt.Sprintf("订阅 [%s] 添加完成 (大小: %d 字节, %s)", cleanName, len(content), usageText))
	return &item, nil
}

// Update refreshes an existing subscription from its remote URL
func (m *Manager) Update(ctx context.Context, nameOrFile string, allowGlobal bool) (*Item, error) {
	m.mu.RLock()
	_, filename := cleanFilename(nameOrFile)
	item, ok := m.items[filename]
	if !ok {
		for _, it := range m.items {
			if it.Name == nameOrFile {
				item = it
				ok = true
				filename = it.Filename
				break
			}
		}
	}
	m.mu.RUnlock()

	if !ok {
		// If it's a provider from Clash Core, update via Clash API!
		if m.clash != nil {
			m.log.Info("SUBSCRIPTION", fmt.Sprintf("通过 Clash API 更新代理 Provider: [%s]...", nameOrFile))
			if err := m.clash.UpdateProvider(ctx, nameOrFile); err == nil {
				nowStr := time.Now().Format("060102-150405")
				m.log.Success("SUBSCRIPTION", fmt.Sprintf("代理 Provider [%s] 更新完成", nameOrFile))
				return &Item{
					Name:      nameOrFile,
					Filename:  nameOrFile,
					Type:      "provider",
					UpdatedAt: nowStr,
					IsActive:  true,
				}, nil
			}
		}
		return nil, fmt.Errorf("未找到名为 [%s] 的订阅", nameOrFile)
	}

	if item.URL == "" {
		return nil, fmt.Errorf("订阅 [%s] 没有配置远程更新地址", item.Name)
	}

	m.log.Info("SUBSCRIPTION", fmt.Sprintf("正在拉取最新订阅: [%s]...", item.Name))

	content, header, err := m.download(ctx, item.URL, allowGlobal)
	if err != nil {
		m.log.Error("SUBSCRIPTION", fmt.Sprintf("更新订阅 [%s] 失败", item.Name), err.Error())
		return nil, err
	}

	// Parse subscription userinfo for usage
	upload, download, total, expire, remaining, usageText := parseSubscriptionUserInfo(header.Get("subscription-userinfo"))
	if usageText == "" {
		usageText = extractUsageFromYAML(content)
	}

	// Save to local cache
	localPath := filepath.Join(m.localDir, filename)
	_ = os.WriteFile(localPath, content, 0644)

	// Sync to OpenWrt
	if m.openwrt.IsConfigured() {
		remotePath := fmt.Sprintf("/etc/openclash/config/%s", filename)
		if err := m.openwrt.WriteFile(ctx, remotePath, content); err != nil {
			m.log.Error("SUBSCRIPTION", fmt.Sprintf("同步更新到 OpenWrt 失败: %s", filename), err.Error())
			return nil, fmt.Errorf("同步更新到 OpenWrt 失败: %w", err)
		}
		m.log.Success("SUBSCRIPTION", fmt.Sprintf("已成功更新 OpenWrt 配置文件: %s", remotePath))
	}

	nowStr := time.Now().Format("060102-150405")
	item.UpdatedAt = nowStr
	item.Size = int64(len(content))
	item.Domain = extractDomain(item.URL)
	if total > 0 {
		item.Upload = upload
		item.Download = download
		item.Total = total
		item.Expire = expire
		item.Remaining = remaining
		item.UsageText = usageText
	} else if usageText != "" {
		item.UsageText = usageText
	}

	m.mu.Lock()
	m.items[filename] = item
	m.saveMeta()
	m.mu.Unlock()

	m.log.Success("SUBSCRIPTION", fmt.Sprintf("订阅 [%s] 更新完成 (大小: %d 字节, %s)", item.Name, len(content), item.UsageText))
	return &item, nil
}

// Upload handles uploading a local yaml config file
func (m *Manager) Upload(ctx context.Context, filename string, content []byte) (*Item, error) {
	cleanName, cleanFile := cleanFilename(filename)
	if cleanName == "" {
		return nil, fmt.Errorf("文件名不能为空")
	}
	if len(content) < 10 {
		return nil, fmt.Errorf("上传文件内容为空")
	}

	m.log.Info("SUBSCRIPTION", fmt.Sprintf("接收到本地 YAML 配置文件上传: [%s] (大小: %d 字节)", cleanFile, len(content)))

	// Save to local cache
	localPath := filepath.Join(m.localDir, cleanFile)
	if err := os.WriteFile(localPath, content, 0644); err != nil {
		m.log.Warn("SUBSCRIPTION", fmt.Sprintf("本地缓存文件写入失败: %v", err))
	}

	// Sync to OpenWrt /etc/openclash/config/
	if m.openwrt.IsConfigured() {
		remotePath := fmt.Sprintf("/etc/openclash/config/%s", cleanFile)
		if err := m.openwrt.WriteFile(ctx, remotePath, content); err != nil {
			m.log.Error("SUBSCRIPTION", fmt.Sprintf("向 OpenWrt 写入配置文件 [%s] 失败", cleanFile), err.Error())
			return nil, fmt.Errorf("向 OpenWrt 写入失败: %w", err)
		}
		m.log.Success("SUBSCRIPTION", fmt.Sprintf("已成功同步上传文件到 OpenWrt: %s", remotePath))
	}

	nowStr := time.Now().Format("060102-150405")
	usage := extractUsageFromYAML(content)
	item := Item{
		Name:      cleanName,
		Filename:  cleanFile,
		URL:       "",
		Domain:    "",
		UpdatedAt: nowStr,
		Type:      "local",
		Size:      int64(len(content)),
		UsageText: usage,
	}

	m.mu.Lock()
	m.items[cleanFile] = item
	m.saveMeta()
	m.mu.Unlock()

	m.log.Success("SUBSCRIPTION", fmt.Sprintf("本地配置文件 [%s] 上传成功", cleanName))
	return &item, nil
}

// Delete removes a subscription from OpenWrt and local manager
func (m *Manager) Delete(ctx context.Context, nameOrFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, filename := cleanFilename(nameOrFile)
	_, ok := m.items[filename]
	if !ok {
		for _, it := range m.items {
			if it.Name == nameOrFile {
				ok = true
				filename = it.Filename
				break
			}
		}
	}

	if !ok {
		filename = nameOrFile
		if !strings.HasSuffix(filename, ".yaml") {
			filename += ".yaml"
		}
	}

	m.log.Info("SUBSCRIPTION", fmt.Sprintf("正在删除配置文件/订阅: %s", filename))

	if m.openwrt.IsConfigured() {
		_, err := m.openwrt.DeleteProfile(ctx, filename)
		if err != nil {
			m.log.Warn("SUBSCRIPTION", fmt.Sprintf("从 OpenWrt 删除配置文件返回: %v", err))
		}
	}

	// Remove local cache
	_ = os.Remove(filepath.Join(m.localDir, filename))

	delete(m.items, filename)
	m.saveMeta()

	m.log.Success("SUBSCRIPTION", fmt.Sprintf("配置文件 [%s] 已删除", filename))
	return nil
}
