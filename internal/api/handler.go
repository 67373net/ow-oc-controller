package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/67373net/ow-oc-controller/internal/clash"
	"github.com/67373net/ow-oc-controller/internal/config"
	"github.com/67373net/ow-oc-controller/internal/connlog"
	"github.com/67373net/ow-oc-controller/internal/logger"
	"github.com/67373net/ow-oc-controller/internal/openwrt"
	"github.com/67373net/ow-oc-controller/internal/subscription"
)

type Handler struct {
	cfg               *config.Config
	clash             *clash.Client
	openwrt           *openwrt.Client
	log               *logger.MemoryLogger
	sub               *subscription.Manager
	connLog           *connlog.Manager
	defaultEnvExample string
}

func NewHandler(
	cfg *config.Config,
	clashClient *clash.Client,
	openwrtClient *openwrt.Client,
	l *logger.MemoryLogger,
	subMgr *subscription.Manager,
	connMgr *connlog.Manager,
	defaultEnvExample string,
) *Handler {
	return &Handler{
		cfg:               cfg,
		clash:             clashClient,
		openwrt:           openwrtClient,
		log:               l,
		sub:               subMgr,
		connLog:           connMgr,
		defaultEnvExample: defaultEnvExample,
	}
}

type SystemStatusResponse struct {
	ClashOnline           bool   `json:"clash_online"`
	CoreVersion           string `json:"core_version"`
	Mode                  string `json:"mode"`
	IsEnabled             bool   `json:"is_enabled"`
	OpenWrtSSHConfigured  bool   `json:"openwrt_ssh_configured"`
	OpenWrtServiceRunning bool   `json:"openwrt_service_running"`
	ActiveProfile         string `json:"active_profile"`
	PrimaryGroup          string `json:"primary_group"`
	PrimaryNode           string `json:"primary_node"`
	GroupsCount           int    `json:"groups_count"`
	NodesCount            int    `json:"nodes_count"`
	RouterHost            string `json:"router_host"`
	PowerToggleMode       string `json:"power_toggle_mode"`
	PollInterval          int    `json:"poll_interval"`
	ConnLogRetentionDays  int    `json:"conn_log_retention_days"`
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("JSON encode error: %v", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}

// GetStatus returns aggregated real-time status of OpenClash & OpenWrt
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	retention := 8
	if h.connLog != nil {
		retention = h.connLog.GetRetentionDays()
	}

	status := SystemStatusResponse{
		RouterHost:           h.cfg.OpenWrtHost,
		PowerToggleMode:      h.cfg.PowerToggleMode,
		PollInterval:         h.cfg.PollInterval,
		OpenWrtSSHConfigured: h.openwrt.IsConfigured(),
		ConnLogRetentionDays: retention,
	}

	// 1. Clash Core probe
	ver, err := h.clash.GetVersion(ctx)
	if err == nil && ver != nil {
		status.ClashOnline = true
		status.CoreVersion = ver.Version
	}

	// 2. Clash Configs probe
	if status.ClashOnline {
		if cfgResp, err := h.clash.GetConfigs(ctx); err == nil && cfgResp != nil {
			status.Mode = cfgResp.Mode
			status.IsEnabled = strings.ToLower(cfgResp.Mode) != "direct"
		} else {
			status.IsEnabled = true
		}
	} else {
		status.IsEnabled = false
	}

	// 3. OpenWrt SSH probe (if configured)
	if h.openwrt.IsConfigured() {
		running, err := h.openwrt.IsOpenClashRunning(ctx)
		if err == nil {
			status.OpenWrtServiceRunning = running
			// If Clash core is down (offline), the service cannot be running or routing traffic
			if !status.ClashOnline {
				status.IsEnabled = false
			} else if h.cfg.PowerToggleMode == "service" {
				status.IsEnabled = running
			}
		} else {
			if !status.ClashOnline {
				status.IsEnabled = false
			}
		}

		_, activeFile, err := h.openwrt.ListProfiles(ctx)
		if err == nil && activeFile != "" {
			status.ActiveProfile = strings.TrimSuffix(activeFile, ".yaml")
			status.ActiveProfile = strings.TrimSuffix(status.ActiveProfile, ".yml")
		}
	} else {
		if !status.ClashOnline {
			status.IsEnabled = false
		}
	}

	// 4. Proxies overview
	if status.ClashOnline {
		if proxiesResp, err := h.clash.GetProxies(ctx); err == nil && proxiesResp != nil {
			var candidateGroups = []string{"Proxy", "PROXY", "节点选择", "GLOBAL", "Global"}
			for _, gName := range candidateGroups {
				if item, exists := proxiesResp.Proxies[gName]; exists && len(item.All) > 0 {
					status.PrimaryGroup = item.Name
					status.PrimaryNode = item.Now
					break
				}
			}

			if status.PrimaryGroup == "" {
				for name, item := range proxiesResp.Proxies {
					if item.Type == "Selector" && len(item.All) > 0 {
						status.PrimaryGroup = name
						status.PrimaryNode = item.Now
						break
					}
				}
			}

			for _, item := range proxiesResp.Proxies {
				if item.Type == "Selector" || item.Type == "URLTest" || item.Type == "Fallback" {
					status.GroupsCount++
				} else if item.Type != "Direct" && item.Type != "Reject" && item.Type != "Compatible" {
					status.NodesCount++
				}
			}
		}
	}

	h.writeJSON(w, http.StatusOK, status)
}

type PowerRequest struct {
	Action string `json:"action"` // "toggle", "enable", "disable"
}

// HandlePower handles enabling or disabling OpenClash
func (h *Handler) HandlePower(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var req PowerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Action = "toggle"
	}

	// Determine current state accurately
	currentState := false
	if h.openwrt.IsConfigured() {
		running, err := h.openwrt.IsOpenClashRunning(ctx)
		if err == nil {
			currentState = running
		}
	} else {
		cfgResp, err := h.clash.GetConfigs(ctx)
		if err == nil && cfgResp != nil {
			currentState = strings.ToLower(cfgResp.Mode) != "direct"
		}
	}

	targetEnable := true
	switch strings.ToLower(req.Action) {
	case "enable", "start":
		targetEnable = true
	case "disable", "stop":
		targetEnable = false
	case "toggle":
		targetEnable = !currentState
	default:
		targetEnable = !currentState
	}

	var targetMode string
	if targetEnable {
		h.log.Info("POWER", "触发启动操作: 正在开启 OpenClash...")
		targetMode = "Rule"

		// 1. If OpenWrt SSH is configured, start the service
		if h.openwrt.IsConfigured() {
			out, err := h.openwrt.StartService(ctx)
			if err != nil {
				h.log.Error("POWER", "OpenWrt 启动服务失败", err.Error())
			} else {
				h.log.Success("POWER", "已向 OpenWrt 下发启动命令 (/etc/init.d/openclash start)", out)
			}
		} else {
			h.log.Warn("POWER", "未配置 OpenWrt SSH 凭证 (OPENWRT_SSH_PASS 为空)，仅切换 Clash 模式至 Rule")
		}

		// 2. Watch OpenClash startup health & bind mode
		h.watchOpenClashStartup("POWER", targetMode, "OpenClash 核心初始化完成，已自动激活 Rule 规则模式")

	} else {
		h.log.Info("POWER", "触发停止操作: 正在关闭 OpenClash...")
		targetMode = "Direct"

		// 1. Immediately set Clash mode to Direct so traffic bypasses proxy instantly
		if err := h.clash.SetMode(ctx, targetMode); err != nil {
			h.log.Warn("CLASH", "设置 Clash 核心模式为 Direct 失败", err.Error())
		} else {
			h.log.Success("CLASH", "已将 Clash 核心模式设为 Direct (所有流量直连)")
		}

		// 2. If OpenWrt SSH is configured, perform service-level stop
		if h.openwrt.IsConfigured() {
			out, err := h.openwrt.StopService(ctx)
			if err != nil {
				h.log.Error("POWER", "OpenWrt 停止服务失败", err.Error())
			} else {
				h.log.Success("POWER", "OpenWrt 服务停止命令执行成功 (/etc/init.d/openclash stop)", out)
			}
		} else {
			h.log.Warn("POWER", "【特别提示】未配置 OpenWrt SSH 密码 (OPENWRT_SSH_PASS为空)！已降级为「直连模式 Direct」：所有流量直通不经过代理，但 OpenWrt 上的 OpenClash 进程仍会保持运行。若需要彻底终止 OpenClash 服务进程，请在 .env 文件中填入 OPENWRT_SSH_PASS 密码。")
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"is_enabled": targetEnable,
		"mode":       targetMode,
	})
}

// ListProfiles returns all airport profiles (synced with subscriptions)
func (h *Handler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	profiles := make([]subscription.Item, 0)
	if h.sub != nil {
		if items, err := h.sub.List(ctx); err == nil {
			profiles = items
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"profiles":               profiles,
		"openwrt_ssh_configured": h.openwrt.IsConfigured(),
	})
}

type SwitchProfileRequest struct {
	Filename string `json:"filename"`
}

// SwitchProfile switches active airport profile
func (h *Handler) SwitchProfile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var req SwitchProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Filename == "" {
		h.writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	safeName := filepath.Clean(filepath.Base(req.Filename))
	h.log.Info("AIRPORT", fmt.Sprintf("开始切换机场配置文件: %s", safeName))

	var sshSuccess bool
	if h.openwrt.IsConfigured() {
		out, err := h.openwrt.SwitchProfile(ctx, safeName)
		if err != nil {
			h.log.Error("AIRPORT", "OpenWrt 切换配置文件失败", err.Error())
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sshSuccess = true
		h.log.Success("AIRPORT", fmt.Sprintf("已修改 OpenWrt 配置为 %s 并触发重启", safeName), out)
		h.watchOpenClashStartup("AIRPORT", "Rule", fmt.Sprintf("机场 [%s] 切换完成，OpenClash 核心已成功上线", safeName))
	} else {
		// Only attempt reload via Clash Core API if SSH is NOT configured
		h.log.Warn("AIRPORT", "OpenWrt SSH 凭证未配置，尝试通过 Clash API 热重载配置...")
		configPath := fmt.Sprintf("/etc/openclash/config/%s", safeName)
		if err := h.clash.ReloadConfig(ctx, configPath); err != nil {
			h.log.Warn("CLASH", "Clash 核心重载配置响应: "+err.Error())
		} else {
			h.log.Success("CLASH", fmt.Sprintf("Clash 核心已重载新配置: %s", safeName))
		}
	}

	if !sshSuccess && !h.openwrt.IsConfigured() {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"warning": "SSH未配置，已尝试通过核心API重载",
			"filename": safeName,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"filename": safeName,
	})
}

type UpdateProviderRequest struct {
	Name string `json:"name"`
}

// UpdateProvider triggers a subscription update
func (h *Handler) UpdateProvider(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var req UpdateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "invalid provider name")
		return
	}

	h.log.Info("AIRPORT", fmt.Sprintf("正在拉取更新机场订阅 Provider: %s...", req.Name))
	if err := h.clash.UpdateProvider(ctx, req.Name); err != nil {
		h.log.Error("AIRPORT", fmt.Sprintf("订阅更新失败: %s", req.Name), err.Error())
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.log.Success("AIRPORT", fmt.Sprintf("机场订阅 Provider [%s] 更新成功", req.Name))
	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"name":    req.Name,
	})
}

type ProxyNodeView struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Delay     int    `json:"delay"`
	IsCurrent bool   `json:"is_current"`
	UDP       bool   `json:"udp"`
}

type ProxyGroupView struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Current string          `json:"current"`
	Nodes   []ProxyNodeView `json:"nodes"`
}

// GetProxies returns structured proxy groups and nodes
func (h *Handler) GetProxies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp, err := h.clash.GetProxies(ctx)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "failed to fetch proxies from Clash core: "+err.Error())
		return
	}

	groups := make([]ProxyGroupView, 0)

	for name, item := range resp.Proxies {
		if item.Type == "Selector" || item.Type == "URLTest" || item.Type == "Fallback" {
			gv := ProxyGroupView{
				Name:    name,
				Type:    item.Type,
				Current: item.Now,
				Nodes:   make([]ProxyNodeView, 0, len(item.All)),
			}

			for _, nodeName := range item.All {
				delay := 0
				udp := false
				nodeType := "Unknown"

				if nodeItem, ok := resp.Proxies[nodeName]; ok {
					nodeType = nodeItem.Type
					udp = nodeItem.UDP
					if len(nodeItem.History) > 0 {
						delay = nodeItem.History[len(nodeItem.History)-1].Delay
					}
				}

				gv.Nodes = append(gv.Nodes, ProxyNodeView{
					Name:      nodeName,
					Type:      nodeType,
					Delay:     delay,
					IsCurrent: nodeName == item.Now,
					UDP:       udp,
				})
			}

			groups = append(groups, gv)
		}
	}

	// Deterministic sorting so strategy group tabs never shuffle on refresh
	sort.SliceStable(groups, func(i, j int) bool {
		priority := func(name string) int {
			u := strings.ToUpper(name)
			switch {
			case u == "PROXY" || name == "节点选择":
				return 0
			case u == "GLOBAL":
				return 1
			case strings.Contains(name, "漏网") || strings.Contains(u, "MATCH"):
				return 20
			default:
				return 10
			}
		}
		pi, pj := priority(groups[i].Name), priority(groups[j].Name)
		if pi != pj {
			return pi < pj
		}
		return groups[i].Name < groups[j].Name
	})

	h.writeJSON(w, http.StatusOK, map[string]any{
		"groups": groups,
	})
}

type SelectProxyRequest struct {
	Group string `json:"group"`
	Name  string `json:"name"`
}

// SelectProxy switches the node for a group
func (h *Handler) SelectProxy(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req SelectProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Group == "" || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "invalid group or proxy name")
		return
	}

	h.log.Info("PROXY", fmt.Sprintf("正在切换策略组 [%s] 节点 -> %s", req.Group, req.Name))

	if err := h.clash.SelectProxy(ctx, req.Group, req.Name); err != nil {
		h.log.Error("PROXY", fmt.Sprintf("策略组 [%s] 切换失败", req.Group), err.Error())
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.log.Success("PROXY", fmt.Sprintf("策略组 [%s] 成功切换至: %s", req.Group, req.Name))
	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"group":   req.Group,
		"name":    req.Name,
	})
}

type DelayTestRequest struct {
	Nodes   []string `json:"nodes"`
	URL     string   `json:"url"`
	Timeout int      `json:"timeout"`
}

// TestDelay tests latency for one or multiple nodes concurrently
func (h *Handler) TestDelay(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var req DelayTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Nodes) == 0 {
		h.writeError(w, http.StatusBadRequest, "invalid nodes parameter")
		return
	}

	if req.URL == "" {
		req.URL = h.cfg.TestURL
	}
	if req.Timeout <= 0 {
		req.Timeout = h.cfg.TestTimeout
	}

	h.log.Info("PROXY", fmt.Sprintf("发起测速: 共 %d 个节点 (目标: %s)", len(req.Nodes), req.URL))
	results := h.clash.BatchTestDelay(ctx, req.Nodes, req.URL, req.Timeout)
	
	validCount := 0
	for _, delay := range results {
		if delay > 0 {
			validCount++
		}
	}
	h.log.Success("PROXY", fmt.Sprintf("测速完成: %d/%d 节点有效响应", validCount, len(req.Nodes)))

	h.writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
	})
}

// GetLogs returns system activity logs
func (h *Handler) GetLogs(w http.ResponseWriter, r *http.Request) {
	entries := h.log.GetEntries(100)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"logs": entries,
	})
}

// ClearLogs clears system activity logs
func (h *Handler) ClearLogs(w http.ResponseWriter, r *http.Request) {
	h.log.Clear()
	h.log.Info("SYSTEM", "日志已被用户手动清空")
	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// HealthCheck for container health probe
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// -----------------------------------------------------------------------------
// Subscriptions Management Endpoints
// -----------------------------------------------------------------------------

// ListSubscriptions returns all subscriptions and local profiles
func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if h.sub == nil {
		h.writeError(w, http.StatusServiceUnavailable, "subscription manager not initialized")
		return
	}

	list, err := h.sub.List(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"subscriptions": list,
	})
}

type AddSubscriptionRequest struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	AllowGlobal bool   `json:"allow_global"`
}

// AddSubscription downloads and saves a new subscription
func (h *Handler) AddSubscription(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if h.sub == nil {
		h.writeError(w, http.StatusServiceUnavailable, "subscription manager not initialized")
		return
	}

	var req AddSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		h.writeError(w, http.StatusBadRequest, "订阅链接不能为空")
		return
	}

	item, err := h.sub.Add(ctx, req.Name, req.URL, req.AllowGlobal)
	if err != nil {
		var confirmErr *subscription.ErrGlobalConfirmRequired
		if errors.As(err, &confirmErr) {
			h.writeJSON(w, http.StatusOK, map[string]any{
				"success":             false,
				"need_global_confirm": true,
				"message":             confirmErr.Message,
				"detail":              "常规下载策略均失败（订阅域名疑似被防火墙阻断且未包含在当前分流规则中）。是否允许临时将 OpenClash 切换为全局出海模式（约 2~3 秒）强制下载？切换期间全屋设备流量将短暂走代理出海，完成后自动恢复规则分流。",
			})
			return
		}
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"subscription": item,
	})
}

type SubActionRequest struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	AllowGlobal bool   `json:"allow_global"`
}

// UpdateSubscription updates an existing subscription from its remote URL
func (h *Handler) UpdateSubscription(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if h.sub == nil {
		h.writeError(w, http.StatusServiceUnavailable, "subscription manager not initialized")
		return
	}

	var req SubActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "请求参数解析失败")
		return
	}

	target := req.Filename
	if target == "" {
		target = req.Name
	}
	if target == "" {
		h.writeError(w, http.StatusBadRequest, "订阅名称或文件名不能为空")
		return
	}

	item, err := h.sub.Update(ctx, target, req.AllowGlobal)
	if err != nil {
		var confirmErr *subscription.ErrGlobalConfirmRequired
		if errors.As(err, &confirmErr) {
			h.writeJSON(w, http.StatusOK, map[string]any{
				"success":             false,
				"need_global_confirm": true,
				"message":             confirmErr.Message,
				"detail":              fmt.Sprintf("订阅 [%s] 常规更新策略均失败（订阅域名疑似被防火墙阻断且未包含在当前分流规则中）。是否允许临时将 OpenClash 切换为全局出海模式（约 2~3 秒）强制更新？切换期间全屋设备流量将短暂走代理出海，完成后自动恢复规则分流。", target),
			})
			return
		}
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"subscription": item,
	})
}

// DeleteSubscription removes a subscription from OpenWrt and local manager
func (h *Handler) DeleteSubscription(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if h.sub == nil {
		h.writeError(w, http.StatusServiceUnavailable, "subscription manager not initialized")
		return
	}

	var req SubActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "请求参数解析失败")
		return
	}

	target := req.Filename
	if target == "" {
		target = req.Name
	}
	if target == "" {
		h.writeError(w, http.StatusBadRequest, "订阅名称或文件名不能为空")
		return
	}

	if err := h.sub.Delete(ctx, target); err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// UploadSubscription handles uploading a local yaml config file
func (h *Handler) UploadSubscription(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if h.sub == nil {
		h.writeError(w, http.StatusServiceUnavailable, "subscription manager not initialized")
		return
	}

	// Limit upload size to 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)

	// Check if multipart form
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(10 * 1024 * 1024); err != nil {
			h.writeError(w, http.StatusBadRequest, "解析上传表单失败: "+err.Error())
			return
		}
		file, handler, err := r.FormFile("file")
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "获取上传文件失败: "+err.Error())
			return
		}
		defer file.Close()

		content, err := io.ReadAll(file)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "读取文件失败: "+err.Error())
			return
		}

		item, err := h.sub.Upload(ctx, handler.Filename, content)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		h.writeJSON(w, http.StatusOK, map[string]any{
			"success":      true,
			"subscription": item,
		})
		return
	}

	// Fallback: JSON body { filename, content_base64 }
	type UploadJSONRequest struct {
		Filename      string `json:"filename"`
		ContentBase64 string `json:"content_base64"`
	}
	var jsonReq UploadJSONRequest
	if err := json.NewDecoder(r.Body).Decode(&jsonReq); err != nil || jsonReq.Filename == "" || jsonReq.ContentBase64 == "" {
		h.writeError(w, http.StatusBadRequest, "无效的上传参数")
		return
	}

	content, err := base64.StdEncoding.DecodeString(jsonReq.ContentBase64)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "Base64 解码失败: "+err.Error())
		return
	}

	item, err := h.sub.Upload(ctx, jsonReq.Filename, content)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"subscription": item,
	})
}

// -----------------------------------------------------------------------------
// Connection Logs Endpoints
// -----------------------------------------------------------------------------

// GetConnections returns paginated connection logs with search filtering
func (h *Handler) GetConnections(w http.ResponseWriter, r *http.Request) {
	if h.connLog == nil {
		h.writeError(w, http.StatusServiceUnavailable, "connection log manager not initialized")
		return
	}

	page := 1
	limit := 200
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	query := r.URL.Query().Get("query")

	result := h.connLog.Query(page, limit, query)
	h.writeJSON(w, http.StatusOK, result)
}

type SetConnLogConfigRequest struct {
	RetentionDays int `json:"retention_days"`
}

// SetConnLogConfig updates retention policy days
func (h *Handler) SetConnLogConfig(w http.ResponseWriter, r *http.Request) {
	if h.connLog == nil {
		h.writeError(w, http.StatusServiceUnavailable, "connection log manager not initialized")
		return
	}

	var req SetConnLogConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RetentionDays <= 0 {
		h.writeError(w, http.StatusBadRequest, "保留天数必须为正整数 (>= 1)")
		return
	}

	h.connLog.SetRetentionDays(req.RetentionDays)
	h.log.Info("CONNLOG", fmt.Sprintf("已设置连接日志保留天数为: %d 天", req.RetentionDays))

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"retention_days": req.RetentionDays,
	})
}

// ClearConnections purges all saved connection logs
func (h *Handler) ClearConnections(w http.ResponseWriter, r *http.Request) {
	if h.connLog == nil {
		h.writeError(w, http.StatusServiceUnavailable, "connection log manager not initialized")
		return
	}

	if err := h.connLog.ClearAll(); err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.log.Info("CONNLOG", "本地连接日志已被清空")
	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// GetEnv returns current .env content, existence status, active path and template
func (h *Handler) GetEnv(w http.ResponseWriter, r *http.Request) {
	envPath := config.GetEnvFilePath()
	exists := false
	var content string

	if data, err := os.ReadFile(envPath); err == nil {
		exists = true
		content = string(data)
	}

	example := h.defaultEnvExample
	if example == "" {
		if exPath := config.GetEnvExamplePath(); exPath != "" {
			if exData, err := os.ReadFile(exPath); err == nil {
				example = string(exData)
			}
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"exists":  exists,
		"path":    envPath,
		"content": content,
		"example": example,
	})
}

type SaveEnvRequest struct {
	Content string `json:"content"`
}

// SaveEnv writes updated content to the .env file and hot-reloads configuration in memory
func (h *Handler) SaveEnv(w http.ResponseWriter, r *http.Request) {
	var req SaveEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "无效的 JSON 请求: "+err.Error())
		return
	}

	envPath := config.GetEnvFilePath()
	if err := os.WriteFile(envPath, []byte(req.Content), 0666); err != nil {
		h.log.Error("SYSTEM", "保存环境变量文件失败: "+err.Error())
		h.writeError(w, http.StatusInternalServerError, "写入环境变量文件失败: "+err.Error())
		return
	}

	// Hot-reload configuration in memory
	envMap := config.ParseEnvString(req.Content)
	h.cfg.ApplyEnvMap(envMap)

	// Hot-update clients
	h.clash.UpdateConfig(h.cfg.ClashAPIURL(), h.cfg.OpenClashAPISecret)
	h.openwrt.UpdateConfig(h.cfg.SSHAddress(), h.cfg.OpenWrtSSHUser, h.cfg.OpenWrtSSHPass, h.cfg.OpenWrtSSHKey)
	if h.sub != nil {
		h.sub.UpdateSettings(h.cfg.OpenWrtHost, h.cfg.SubscriptionProxy)
	}

	h.log.Info("SYSTEM", fmt.Sprintf("环境变量已更新并热重载: 主路由=%s, Clash API=%s", h.cfg.OpenWrtHost, h.cfg.ClashAPIURL()))

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "环境变量已保存并热重载生效",
		"path":    envPath,
	})
}

// InitEnv resets/creates .env from the default template
func (h *Handler) InitEnv(w http.ResponseWriter, r *http.Request) {
	example := h.defaultEnvExample
	if example == "" {
		if exPath := config.GetEnvExamplePath(); exPath != "" {
			if exData, err := os.ReadFile(exPath); err == nil {
				example = string(exData)
			}
		}
	}
	if example == "" {
		h.writeError(w, http.StatusInternalServerError, "无法读取默认配置模版")
		return
	}

	envPath := config.GetEnvFilePath()
	if err := os.WriteFile(envPath, []byte(example), 0666); err != nil {
		h.log.Error("SYSTEM", "初始化环境变量文件失败: "+err.Error())
		h.writeError(w, http.StatusInternalServerError, "写入环境变量文件失败: "+err.Error())
		return
	}

	// Hot-reload
	envMap := config.ParseEnvString(example)
	h.cfg.ApplyEnvMap(envMap)
	h.clash.UpdateConfig(h.cfg.ClashAPIURL(), h.cfg.OpenClashAPISecret)
	h.openwrt.UpdateConfig(h.cfg.SSHAddress(), h.cfg.OpenWrtSSHUser, h.cfg.OpenWrtSSHPass, h.cfg.OpenWrtSSHKey)
	if h.sub != nil {
		h.sub.UpdateSettings(h.cfg.OpenWrtHost, h.cfg.SubscriptionProxy)
	}

	h.log.Info("SYSTEM", "环境变量已重置并初始化为默认模版")

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "已成功从模版初始化环境变量",
		"content": example,
		"path":    envPath,
	})
}

// watchOpenClashStartup monitors OpenClash startup, sets mode on success, or captures router logs on failure
func (h *Handler) watchOpenClashStartup(source string, targetMode string, successMsg string) {
	go func() {
		// Wait 2.5 seconds initially for OpenWrt init script to start background daemon
		time.Sleep(2500 * time.Millisecond)

		const maxAttempts = 10
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			// 1. Check if Clash core is already responding
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 3*time.Second)
			ver, err := h.clash.GetVersion(bgCtx)
			if err == nil && ver != nil {
				// Core is up! Apply mode
				_ = h.clash.SetMode(bgCtx, targetMode)
				h.log.Success("CLASH", successMsg)
				bgCancel()
				return
			}
			bgCancel()

			// 2. If SSH is configured and we've waited >= 4.5s (attempt >= 2)
			if h.openwrt.IsConfigured() && attempt >= 2 {
				diagCtx, diagCancel := context.WithTimeout(context.Background(), 5*time.Second)
				running, runErr := h.openwrt.IsOpenClashRunning(diagCtx)
				if runErr == nil && !running {
					// OpenClash process is dead/stopped!
					diag := h.openwrt.DiagnoseFailure(diagCtx)
					diagCancel()
					if diag != "" {
						h.log.Error("OPENCLASH", "OpenClash 核心启动失败并已退出，请检查配置", diag)
					} else {
						h.log.Error("OPENCLASH", "OpenClash 核心启动失败，进程未在运行", "请检查 OpenWrt 系统状态及 /tmp/openclash.log")
					}
					return
				}

				// Check if there is an explicit fatal/panic in the latest log even if still stopping
				if fatalMsg := h.openwrt.DiagnoseFatal(diagCtx); fatalMsg != "" {
					diagCancel()
					h.log.Error("OPENCLASH", "OpenClash 核心启动发生致命错误", fatalMsg)
					return
				}
				diagCancel()
			}

			time.Sleep(2 * time.Second)
		}

		// Reached timeout (20+ seconds) without online
		if h.openwrt.IsConfigured() {
			diagCtx, diagCancel := context.WithTimeout(context.Background(), 5*time.Second)
			diag := h.openwrt.DiagnoseFailure(diagCtx)
			diagCancel()
			if diag != "" {
				h.log.Error("OPENCLASH", "OpenClash 启动超时 (20秒内未上线)", diag)
			} else {
				h.log.Error("OPENCLASH", "OpenClash 启动超时", "Clash 核心外部控制端口(9090)未响应，请检查 OpenWrt 状态")
			}
		} else {
			h.log.Error("CLASH", "Clash 核心启动超时", "外部控制端口(9090)在 20 秒内未响应")
		}
	}()
}

// GetOpenClashLog returns recent log lines from OpenWrt router's /tmp/openclash.log
func (h *Handler) GetOpenClashLog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if !h.openwrt.IsConfigured() {
		h.writeError(w, http.StatusBadRequest, "OpenWrt SSH 未配置，无法读取路由器底层日志")
		return
	}

	content, err := h.openwrt.GetRecentOpenClashLog(ctx, 120)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "获取 OpenClash 日志失败: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"content": content,
	})
}



