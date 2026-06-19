package router

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sec_monitor/internal/api/handler"
	"sec_monitor/internal/config"
	"sec_monitor/internal/scheduler"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Dependencies struct {
	Config     config.Config
	DB         *gorm.DB
	SEC        sec.Client
	Notifier   telegram.Notifier
	WebDistDir string
}

func New(deps Dependencies) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	audit := service.NewAuditService(deps.DB)
	configs := service.NewConfigService(deps.DB, audit)
	_ = configs.EnsureDefaults(context.Background())
	tasks := service.NewTaskConfigService(deps.DB, audit)
	_ = tasks.EnsureDefault(context.Background())
	secClient := deps.SEC
	if secClient == nil {
		secClient = sec.NewHTTPClient(deps.Config.SEC.BaseURL, deps.Config.SEC.UserAgent, time.Duration(deps.Config.SEC.TimeoutMS)*time.Millisecond)
	}
	notifier := deps.Notifier
	if notifier == nil {
		notifier = telegramNotifier{configs: configs}
	}
	filings := service.NewFilingService(deps.DB, secClient, notifier, configs)
	currentFilingsClient, ok := secClient.(sec.CurrentFilingsClient)
	if !ok {
		currentFilingsClient = sec.NewHTTPClient(deps.Config.SEC.BaseURL, deps.Config.SEC.UserAgent, time.Duration(deps.Config.SEC.TimeoutMS)*time.Millisecond)
	}
	ipoRadar := service.NewIPORadarService(deps.DB, currentFilingsClient, notifier, configs)
	sched := scheduler.New(tasks, filings, ipoRadar)
	_ = sched.Start(context.Background())
	app := &handler.AppHandler{
		Runtime:      deps.Config,
		DB:           deps.DB,
		Targets:      service.NewWatchTargetService(deps.DB, audit),
		Configs:      configs,
		Tasks:        tasks,
		Filings:      filings,
		IPO:          ipoRadar,
		SEC:          secClient,
		Audit:        audit,
		Notification: service.NewNotificationService(deps.DB),
		Scheduler:    sched,
	}

	r.GET("/healthz", handler.Health)

	api := r.Group("/api")
	{
		api.GET("/sec/tickers/:ticker", app.LookupTicker)

		api.GET("/watch-targets", app.ListWatchTargets)
		api.POST("/watch-targets", app.CreateWatchTarget)
		api.GET("/watch-targets/:id", app.GetWatchTarget)
		api.PUT("/watch-targets/:id", app.UpdateWatchTarget)
		api.DELETE("/watch-targets/:id", app.DeleteWatchTarget)
		api.PATCH("/watch-targets/:id/status", app.SetWatchTargetStatus)
		api.POST("/watch-targets/:id/sync", app.SyncWatchTarget)
		api.GET("/watch-targets/:id/sync-details", app.ListWatchTargetSyncDetails)

		api.GET("/filings", app.ListFilings)
		api.POST("/filings/refresh", app.RefreshFilings)
		api.GET("/ipo-companies", app.ListIPOCompanies)
		api.PUT("/ipo-companies/:cik/override", app.UpdateIPOCompanyOverride)
		api.GET("/ipo-filings", app.ListIPORadarFilings)
		api.POST("/ipo-filings/refresh", app.RefreshIPORadar)
		api.GET("/filings/cleanup-preview", app.PreviewFilingCleanup)
		api.POST("/filings/cleanup", app.CleanupFilings)
		api.GET("/filings/:id", app.GetFiling)
		api.GET("/sync-runs", app.ListSyncRuns)
		api.GET("/sync-runs/:id/details", app.ListSyncRunDetails)

		api.GET("/task-configs", app.ListTaskConfigs)
		api.PUT("/task-configs/:id", app.UpdateTaskConfig)
		api.POST("/task-configs/:id/run", app.RunTask)

		api.GET("/system-configs", app.ListSystemConfigs)
		api.PUT("/system-configs", app.UpdateSystemConfigs)
		api.POST("/system-configs/reload", app.ListSystemConfigs)

		api.GET("/telegram/config", app.GetTelegramConfig)
		api.PUT("/telegram/config", app.UpdateTelegramConfig)
		api.POST("/telegram/test", app.TestTelegram)

		api.GET("/operation-logs", app.ListOperationLogs)
		api.GET("/notification-logs", app.ListNotificationLogs)

		api.GET("/system-health", app.ListHealth)
		api.GET("/exports/filings.csv", app.ExportFilingsCSV)
		api.GET("/exports/ipo-companies.csv", app.ExportIPOCompaniesCSV)
		api.GET("/exports/ipo-filings.csv", app.ExportIPORadarFilingsCSV)
		api.GET("/exports/watch-targets.csv", app.ExportTargetsCSV)
		api.GET("/exports/configs.json", app.ExportConfigsJSON)
		api.GET("/exports/backup.json", app.ExportBackupJSON)
	}

	configureWebApp(r, deps.WebDistDir)
	return r
}

func configureWebApp(r *gin.Engine, webDistDir string) {
	if strings.TrimSpace(webDistDir) == "" {
		webDistDir = strings.TrimSpace(os.Getenv("WEB_DIST_DIR"))
	}
	if strings.TrimSpace(webDistDir) == "" {
		webDistDir = "web/dist"
	}
	indexPath := filepath.Join(webDistDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		log.Printf("web app disabled: index file not found at %s: %v", indexPath, err)
		return
	}
	log.Printf("web app enabled: serving %s", webDistDir)

	serveIndex := func(c *gin.Context) {
		c.File(indexPath)
	}
	r.GET("/", serveIndex)
	r.HEAD("/", serveIndex)
	r.GET("/index.html", serveIndex)
	r.HEAD("/index.html", serveIndex)
	if _, err := os.Stat(filepath.Join(webDistDir, "assets")); err == nil {
		r.StaticFS("/assets", http.Dir(filepath.Join(webDistDir, "assets")))
	}

	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api") || c.Request.URL.Path == "/healthz" {
			c.Status(http.StatusNotFound)
			return
		}
		requestPath := strings.TrimPrefix(filepath.Clean(c.Request.URL.Path), string(filepath.Separator))
		if requestPath != "." && requestPath != "" {
			filePath := filepath.Join(webDistDir, requestPath)
			if strings.HasPrefix(filePath, filepath.Clean(webDistDir)+string(filepath.Separator)) {
				if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
					c.File(filePath)
					return
				}
			}
		}
		serveIndex(c)
	})
}

type telegramNotifier struct {
	configs *service.ConfigService
}

func (n telegramNotifier) Send(ctx context.Context, message telegram.Message) error {
	cfg, err := n.configs.Telegram(ctx)
	if err != nil {
		return err
	}
	return telegram.NewHTTPNotifier(cfg.BotToken, cfg.ChatID, 10*time.Second).Send(ctx, message)
}
