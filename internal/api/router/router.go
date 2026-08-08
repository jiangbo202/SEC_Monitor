package router

import (
	"context"
	"fmt"
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
	Config      config.Config
	DB          *gorm.DB
	DiscoveryDB *gorm.DB
	SEC         sec.Client
	Notifier    telegram.Notifier
	WebDistDir  string
}

func New(deps Dependencies) (*gin.Engine, error) {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	audit := service.NewAuditService(deps.DB)
	configs := service.NewConfigService(deps.DB, audit, deps.Config.System)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure system config defaults: %w", err)
	}
	if err := configs.MigrateEncryptedValues(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate encrypted configuration values: %w", err)
	}
	runtimeConfig := deps.Config
	if userAgent, ok, err := configs.GetValue(context.Background(), "sec.user_agent"); err != nil {
		return nil, fmt.Errorf("load SEC user agent configuration: %w", err)
	} else if ok && strings.TrimSpace(userAgent) != "" {
		runtimeConfig.SEC.UserAgent = strings.TrimSpace(userAgent)
		runtimeConfig.Discovery.UserAgent = strings.TrimSpace(userAgent)
	}
	tasks := service.NewTaskConfigService(deps.DB, audit)
	if err := tasks.EnsureDefault(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure task defaults: %w", err)
	}
	secClient := deps.SEC
	if secClient == nil {
		secClient = newSECClient(runtimeConfig)
	}
	notifier := deps.Notifier
	if notifier == nil {
		notifier = telegramNotifier{configs: configs}
	}
	notificationBatches := service.NewNotificationBatchService(deps.DB, notifier, configs)
	candidateNotifications := service.NewCandidateNotificationService(deps.DB, deps.DiscoveryDB, notifier, configs)
	analystRatingNotifications := service.NewAnalystRatingNotificationService(deps.DB, deps.DiscoveryDB, notifier, configs)
	tradeSetupNotifications := service.NewTradeSetupNotificationService(deps.DB, deps.DiscoveryDB, notifier, configs)
	tradePlanSimulations := service.NewTradePlanSimulationService(deps.DB, deps.DiscoveryDB)
	discoverySync := service.NewDiscoverySyncService(deps.DiscoveryDB, runtimeConfig.Discovery).WithConfigService(configs).WithWatchTargetDB(deps.DB).WithAnalystRatingNotifications(analystRatingNotifications)
	backup := service.NewSQLiteBackupService(deps.DB, deps.DiscoveryDB, runtimeConfig.Database.DSN, runtimeConfig.Discovery.Database.DSN, configs)
	lifecycle := service.NewLifecycleService(deps.DB, deps.DiscoveryDB, configs)
	operationalHealth := service.NewOperationalHealthService(deps.DB, deps.DiscoveryDB, notifier, configs).WithBackup(backup)
	macroCalendar := service.NewMacroCalendarService(deps.DB)
	earningsPreview := service.NewEarningsPreviewService(deps.DB, runtimeConfig.Discovery, configs, notifier).WithDiscoveryDB(deps.DiscoveryDB)
	if recovered, err := tasks.RecoverInterrupted(context.Background()); err != nil {
		return nil, fmt.Errorf("recover interrupted task states: %w", err)
	} else if recovered > 0 {
		log.Printf("cleared %d interrupted scheduler task state(s)", recovered)
	}
	if recovered, err := discoverySync.RecoverInterruptedRuns(context.Background()); err != nil {
		return nil, fmt.Errorf("recover interrupted discovery sync runs: %w", err)
	} else if recovered > 0 {
		log.Printf("recovered %d stale discovery sync run(s)", recovered)
	}
	filings := service.NewFilingService(deps.DB, secClient, notifier, configs).WithDiscoveryDB(deps.DiscoveryDB)
	currentFilingsClient, ok := secClient.(sec.CurrentFilingsClient)
	if !ok {
		currentFilingsClient = newSECClient(runtimeConfig)
	}
	ipoRadar := service.NewIPORadarService(deps.DB, currentFilingsClient, notifier, configs)
	sched := scheduler.New(tasks, filings, configs, ipoRadar, candidateNotifications, tradeSetupNotifications, discoverySync, notificationBatches, backup, lifecycle, operationalHealth, macroCalendar, earningsPreview)
	if err := sched.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("start scheduler: %w", err)
	}
	app := &handler.AppHandler{
		Runtime:                runtimeConfig,
		DB:                     deps.DB,
		DiscoveryDB:            deps.DiscoveryDB,
		Targets:                service.NewWatchTargetService(deps.DB, audit),
		Configs:                configs,
		Tasks:                  tasks,
		Filings:                filings,
		IPO:                    ipoRadar,
		SEC:                    secClient,
		Audit:                  audit,
		Notification:           service.NewNotificationService(deps.DB),
		NotificationBatch:      notificationBatches,
		CandidateNotification:  candidateNotifications,
		TradeSetupNotification: tradeSetupNotifications,
		TradePlanSimulation:    tradePlanSimulations,
		DiscoverySync:          discoverySync,
		Backup:                 backup,
		Lifecycle:              lifecycle,
		OperationalHealth:      operationalHealth,
		Macro:                  macroCalendar,
		EarningsPreview:        earningsPreview,
		Scheduler:              sched,
	}

	r.GET("/healthz", handler.Health)

	api := r.Group("/api")
	{
		api.GET("/sec/tickers/:ticker", app.LookupTicker)
		api.GET("/macro/releases", app.ListMacroReleases)
		api.POST("/macro/releases/sync", app.SyncMacroReleases)
		api.GET("/discovery/candidates/notification-preview", app.PreviewDiscoveryCandidateNotification)
		api.POST("/discovery/candidates/notification-send", app.SendDiscoveryCandidateNotification)
		api.GET("/watch-targets/trade-setup-notification-preview", app.PreviewTradeSetupNotification)
		api.POST("/watch-targets/trade-setup-notification-send", app.SendTradeSetupNotification)
		api.GET("/watch-targets/trade-plan-simulations", app.GetTradePlanSimulations)
		api.POST("/watch-targets/trade-plan-simulations/rebuild", app.RebuildTradePlanSimulations)
		api.POST("/discovery/candidates/refresh", app.RefreshDiscoveryCandidates)
		api.POST("/discovery/candidates/market-refresh-force", app.ForceRefreshDiscoveryMarketPrices)
		api.POST("/discovery/candidates/technical-history-backfill", app.BackfillDiscoveryCandidateTechnicalHistory)
		api.GET("/discovery/candidates/criteria", app.GetDiscoveryCandidateCriteria)
		api.GET("/discovery/candidates/health", app.GetDiscoveryCandidateHealth)
		api.GET("/discovery/candidates/overview", app.GetDiscoveryCandidateOverview)
		api.GET("/discovery/candidates/report", app.GetDiscoveryCandidateReport)
		api.GET("/discovery/candidates/effectiveness", app.GetDiscoveryCandidateEffectiveness)
		api.GET("/exports/candidates.csv", app.ExportDiscoveryCandidatesCSV)
		api.GET("/discovery/candidates/summary", app.PreviewDiscoveryCandidateSummary)
		api.POST("/discovery/candidates/eligibility-check", app.CheckDiscoverySmallCapEligibility)
		api.GET("/discovery/candidates/eligibility-checks", app.ListDiscoverySmallCapEligibilityChecks)
		api.GET("/discovery/candidate-watches", app.ListCandidateWatches)
		api.GET("/discovery/candidate-review-queue", app.GetCandidateReviewQueue)
		api.GET("/discovery/candidates/market-price-recovery-queue", app.ListDiscoveryMarketPriceRecoveryQueue)
		api.POST("/discovery/candidates/:ticker/market-history-refresh", app.RefreshDiscoveryCandidateMarketHistory)
		api.POST("/discovery/candidate-watches", app.UpsertCandidateWatch)
		api.DELETE("/discovery/candidate-watches/:id", app.DeleteCandidateWatch)
		api.GET("/discovery/research-positions", app.ListCandidateResearchPositions)
		api.POST("/discovery/research-positions", app.UpsertCandidateResearchPosition)
		api.DELETE("/discovery/research-positions/:id", app.DeleteCandidateResearchPosition)
		api.GET("/discovery/profit-history/:ticker", app.GetDiscoveryProfitHistory)
		api.GET("/discovery/company-profiles/recovery-queue", app.ListDiscoveryCompanyProfileRecoveryQueue)
		api.POST("/discovery/company-profiles/recovery-queue/retry", app.RetryDiscoveryCompanyProfileQueue)
		api.GET("/discovery/company-profiles/:ticker", app.GetDiscoveryCompanyProfile)
		api.POST("/discovery/company-profiles/:ticker/refresh", app.RefreshDiscoveryCompanyProfile)
		api.POST("/discovery/company-profiles/:ticker/retry", app.RetryDiscoveryCompanyProfile)
		api.POST("/discovery/providers/longbridge/probe", app.ProbeDiscoveryLongbridgeQuote)
		api.GET("/discovery/analyst-ratings/:ticker", app.GetDiscoveryAnalystRating)
		api.POST("/discovery/analyst-ratings/:ticker/refresh", app.RefreshDiscoveryAnalystRating)
		api.POST("/discovery/candidates/:ticker/business-model", app.UpsertDiscoveryCandidateBusinessModel)
		api.GET("/discovery/candidates/:ticker/detail", app.GetDiscoveryCandidateDetail)
		api.GET("/discovery/candidates", app.ListDiscoveryCandidates)
		api.GET("/discovery/batches", app.ListDiscoveryBatches)
		api.GET("/discovery/provider-runs", app.ListDiscoveryProviderRuns)
		api.GET("/discovery/sync-status", app.GetDiscoverySyncStatus)
		api.GET("/discovery/sync-runs", app.ListDiscoverySyncRuns)
		api.GET("/discovery/sync-runs/:id/steps", app.ListDiscoverySyncSteps)
		api.GET("/discovery/storage-health", app.GetDiscoveryStorageHealth)
		api.GET("/discovery/storage/cache-cleanup-preview", app.PreviewDiscoveryCacheCleanup)
		api.POST("/discovery/storage/cache-cleanup", app.CleanupDiscoveryCache)
		api.GET("/discovery/provider-health", app.ListDiscoveryProviderHealth)
		api.GET("/discovery/provider-observability", app.GetDiscoveryProviderObservability)

		api.GET("/watch-targets", app.ListWatchTargets)
		api.GET("/watch-targets/earnings-previews", app.ListWatchTargetEarningsPreviews)
		api.POST("/watch-targets", app.CreateWatchTarget)
		api.GET("/watch-targets/:id", app.GetWatchTarget)
		api.GET("/watch-targets/:id/technical-history", app.GetWatchTargetTechnicalHistory)
		api.GET("/watch-targets/:id/earnings-preview", app.GetWatchTargetEarningsPreview)
		api.POST("/watch-targets/:id/earnings-preview/refresh", app.RefreshWatchTargetEarningsPreview)
		api.PUT("/watch-targets/:id", app.UpdateWatchTarget)
		api.DELETE("/watch-targets/:id", app.DeleteWatchTarget)
		api.PATCH("/watch-targets/:id/status", app.SetWatchTargetStatus)
		api.POST("/watch-targets/:id/sync", app.SyncWatchTarget)
		api.POST("/watch-targets/:id/technical-history-backfill", app.BackfillWatchTargetTechnicalHistory)
		api.GET("/watch-targets/:id/sync-details", app.ListWatchTargetSyncDetails)

		api.GET("/filings", app.ListFilings)
		api.POST("/filings/refresh", app.RefreshFilings)
		api.GET("/ipo-health", app.GetIPORadarHealth)
		api.GET("/ipo-companies", app.ListIPOCompanies)
		api.GET("/ipo-companies/:cik/offerings", app.ListIPOOfferingEvents)
		api.PUT("/ipo-companies/:cik/override", app.UpdateIPOCompanyOverride)
		api.GET("/ipo-filings", app.ListIPORadarFilings)
		api.POST("/ipo-filings/refresh", app.RefreshIPORadar)
		api.GET("/filings/cleanup-preview", app.PreviewFilingCleanup)
		api.POST("/filings/cleanup", app.CleanupFilings)
		api.GET("/system/lifecycle-cleanup-preview", app.PreviewLifecycleCleanup)
		api.POST("/system/lifecycle-cleanup", app.CleanupLifecycle)
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
		api.GET("/notification-batches", app.ListNotificationBatches)
		api.GET("/notification-batches/:id/items", app.ListNotificationBatchItems)
		api.POST("/notification-batches/:id/retry", app.RequeueNotificationBatch)

		api.GET("/system-health", app.ListHealth)
		api.GET("/operational-health", app.GetOperationalHealth)
		api.POST("/operational-health/notify", app.NotifyOperationalHealth)
		api.POST("/system/backups/verify", app.VerifyLatestSQLiteBackup)
		api.POST("/system/backups/recovery-check", app.CheckSQLiteRecoveryReadiness)
		api.POST("/system/databases/compact", app.CompactSQLiteDatabases)
		api.GET("/system/databases/latest-compaction", app.GetLatestSQLiteCompaction)
		api.GET("/exports/filings.csv", app.ExportFilingsCSV)
		api.GET("/exports/ipo-companies.csv", app.ExportIPOCompaniesCSV)
		api.GET("/exports/ipo-filings.csv", app.ExportIPORadarFilingsCSV)
		api.GET("/exports/watch-targets.csv", app.ExportTargetsCSV)
		api.GET("/exports/configs.json", app.ExportConfigsJSON)
		api.GET("/exports/backup.json", app.ExportBackupJSON)
	}

	configureWebApp(r, deps.WebDistDir)
	return r, nil
}

func newSECClient(runtimeConfig config.Config) *sec.HTTPClient {
	return sec.NewHTTPClientWithPolicy(
		runtimeConfig.SEC.BaseURL,
		runtimeConfig.SEC.UserAgent,
		time.Duration(runtimeConfig.SEC.TimeoutMS)*time.Millisecond,
		sec.RequestPolicy{
			RequestsPerSecond: runtimeConfig.SEC.RequestsPerSecond,
			MaxRetries:        runtimeConfig.SEC.MaxRetries,
		},
	)
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
	notifier := telegram.NewHTTPNotifier(cfg.BotToken, cfg.ChatID, 10*time.Second)
	notifier.BaseURL = cfg.APIBaseURL
	return notifier.Send(ctx, message)
}
