package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/67373net/ow-oc-controller/internal/api"
	"github.com/67373net/ow-oc-controller/internal/clash"
	"github.com/67373net/ow-oc-controller/internal/config"
	"github.com/67373net/ow-oc-controller/internal/connlog"
	"github.com/67373net/ow-oc-controller/internal/logger"
	"github.com/67373net/ow-oc-controller/internal/openwrt"
	"github.com/67373net/ow-oc-controller/internal/subscription"
)

//go:embed web/*
var embeddedWebFS embed.FS

//go:embed .env.example
var defaultDotEnvExample string

func main() {
	cfg := config.LoadConfig()
	appLog := logger.Default()

	// Initialize System Logger persistence
	if err := appLog.InitPersistence("data/logs/system.jsonl"); err != nil {
		log.Printf("[Startup] Failed to init system log persistence: %v", err)
	}
	defer appLog.Close()

	appLog.Info("SYSTEM", fmt.Sprintf("服务正在启动，监听端口 :%d", cfg.Port))
	appLog.Info("SYSTEM", fmt.Sprintf("OpenWrt 路由地址: %s, Clash API: %s", cfg.OpenWrtHost, cfg.ClashAPIURL()))

	log.Printf("[Startup] Initializing OpenClash & OpenWrt Controller on :%d", cfg.Port)
	log.Printf("[Startup] OpenWrt Router Host: %s", cfg.OpenWrtHost)
	log.Printf("[Startup] Clash REST API: %s", cfg.ClashAPIURL())
	if cfg.HasSSHConfig() {
		appLog.Info("SSH", fmt.Sprintf("已配置 OpenWrt SSH 管理凭证 (%s, 用户: %s)", cfg.SSHAddress(), cfg.OpenWrtSSHUser))
		log.Printf("[Startup] OpenWrt SSH enabled on %s (User: %s)", cfg.SSHAddress(), cfg.OpenWrtSSHUser)
	} else {
		appLog.Warn("SSH", "未配置 OpenWrt SSH 密码/密钥 (OPENWRT_SSH_PASS 为空)！已启用纯 Clash REST 模式。若需服务彻底停止与读取 /etc/openclash/config/ 机场，请在 .env 中设置 OPENWRT_SSH_PASS。")
		log.Printf("[Startup] OpenWrt SSH not configured, operating in Clash REST mode (soft toggle/node switch)")
	}

	clashClient := clash.NewClient(cfg.ClashAPIURL(), cfg.OpenClashAPISecret)
	openwrtClient := openwrt.NewClient(cfg.SSHAddress(), cfg.OpenWrtSSHUser, cfg.OpenWrtSSHPass, cfg.OpenWrtSSHKey)

	// Initialize Connection Logger (persistent in ./data/connections, retention default 8 days)
	connLog := connlog.NewManager("data", cfg.ConnLogRetentionDays, appLog, clashClient)
	connLog.Start()
	defer connLog.Stop()

	// Initialize Subscription Manager
	subManager := subscription.NewManager("data", appLog, openwrtClient, clashClient, cfg.OpenWrtHost, cfg.SubscriptionProxy)

	apiHandler := api.NewHandler(cfg, clashClient, openwrtClient, appLog, subManager, connLog, defaultDotEnvExample)

	mux := http.NewServeMux()

	// REST API Routes
	mux.HandleFunc("GET /api/status", apiHandler.GetStatus)
	mux.HandleFunc("POST /api/power", apiHandler.HandlePower)
	mux.HandleFunc("GET /api/profiles", apiHandler.ListProfiles)
	mux.HandleFunc("POST /api/profiles/switch", apiHandler.SwitchProfile)
	mux.HandleFunc("POST /api/profiles/update", apiHandler.UpdateProvider)
	mux.HandleFunc("GET /api/subscriptions", apiHandler.ListSubscriptions)
	mux.HandleFunc("POST /api/subscriptions/add", apiHandler.AddSubscription)
	mux.HandleFunc("POST /api/subscriptions/update", apiHandler.UpdateSubscription)
	mux.HandleFunc("POST /api/subscriptions/upload", apiHandler.UploadSubscription)
	mux.HandleFunc("POST /api/subscriptions/delete", apiHandler.DeleteSubscription)
	mux.HandleFunc("GET /api/proxies", apiHandler.GetProxies)
	mux.HandleFunc("POST /api/proxies/select", apiHandler.SelectProxy)
	mux.HandleFunc("POST /api/proxies/delay", apiHandler.TestDelay)
	mux.HandleFunc("GET /api/connections", apiHandler.GetConnections)
	mux.HandleFunc("POST /api/connections/config", apiHandler.SetConnLogConfig)
	mux.HandleFunc("POST /api/connections/clear", apiHandler.ClearConnections)
	mux.HandleFunc("GET /api/logs", apiHandler.GetLogs)
	mux.HandleFunc("POST /api/logs/clear", apiHandler.ClearLogs)
	mux.HandleFunc("GET /api/openwrt/openclash-log", apiHandler.GetOpenClashLog)
	mux.HandleFunc("GET /api/env", apiHandler.GetEnv)
	mux.HandleFunc("POST /api/env", apiHandler.SaveEnv)
	mux.HandleFunc("POST /api/env/init", apiHandler.InitEnv)
	mux.HandleFunc("GET /api/health", apiHandler.HealthCheck)

	// Embedded static web frontend
	subFS, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("/", fileServer)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 25 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[Ready] Web UI & API listening at http://0.0.0.0:%d", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("[Shutdown] Gracefully shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	log.Println("[Shutdown] Server stopped.")
}
