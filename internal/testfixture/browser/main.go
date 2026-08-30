// Command browser is an isolated regression fixture, not an application server.
// It never opens the user's databases, constructs providers, runs schedules or
// sends notifications. Only research-thesis writes reach its temporary SQLite.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"sec_monitor/internal/api/handler"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	db, err := gorm.Open(sqlite.Open("file:browser-main?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	must(err)
	research, err := gorm.Open(sqlite.Open("file:browser-research?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	must(err)
	for _, d := range []*gorm.DB{db, research} {
		sqlDB, e := d.DB()
		must(e)
		sqlDB.SetMaxOpenConns(1)
	}
	must(db.AutoMigrate(&model.ResearchThesis{}, &model.ResearchThesisRevision{}, &model.OperationLog{}, &model.Filing{}, &model.AIAnalysis{}, &model.InAppNotification{}))
	must(research.AutoMigrate(&discovery.Security{}, &discovery.Listing{}, &discovery.SecurityBatchIdentity{}, &discovery.InsiderTransactionSnapshot{}))
	h := &handler.AppHandler{DB: db, DiscoveryDB: research}
	r := gin.New()
	r.Use(gin.Recovery())
	var mu sync.Mutex
	scenario := ""
	unexpected := []string{}
	r.GET("/__test/reset", func(c *gin.Context) {
		mu.Lock()
		defer mu.Unlock()
		scenario = c.Query("scenario")
		unexpected = []string{}
		for _, table := range []string{"research_thesis_revisions", "research_theses", "operation_logs", "filings", "in_app_notifications"} {
			must(db.Exec("DELETE FROM " + table).Error)
		}
		for _, ticker := range []string{"TEST", "ALT"} {
			must(db.Create(&model.Filing{ID: map[string]uint{"TEST": 1, "ALT": 2}[ticker], Ticker: ticker, FilingID: ticker + "-fixture", FilingType: "8-K", Title: ticker + " 原文证据", FilingURL: "https://www.sec.gov/Archives/fixture", CreatedAt: time.Now().UTC().Add(-time.Hour)}).Error)
		}
		if scenario == "due" {
			past := time.Now().UTC().Add(-time.Hour)
			must(db.Create(&model.ResearchThesis{Ticker: "ALT", Version: 1, Status: "active", Rationale: "待复核的测试观点", Invalidation: "订单下降", NextCheck: "核对收入", NextReviewAt: &past, ReviewedAt: &past, Evidence: []model.ThesisEvidence{}}).Error)
			must(db.Create(&model.InAppNotification{Ticker: "ALT", EventKey: "new-evidence", Title: "待复核新证据", CreatedAt: time.Now().UTC()}).Error)
		}
		c.Data(200, "text/html; charset=utf-8", []byte("<h1>隔离回归数据已重置</h1>"))
	})
	r.GET("/__test/status", func(c *gin.Context) {
		mu.Lock()
		defer mu.Unlock()
		var revisions, audits int64
		db.Model(&model.ResearchThesisRevision{}).Count(&revisions)
		db.Model(&model.OperationLog{}).Count(&audits)
		c.JSON(200, gin.H{"unexpected_requests": unexpected, "revisions": revisions, "audits": audits})
	})
	r.GET("/api/research-theses", h.ListDueResearchTheses)
	r.GET("/api/research-theses/:ticker", func(c *gin.Context) {
		mu.Lock()
		fail := scenario == "read-error"
		mu.Unlock()
		if fail {
			c.JSON(503, gin.H{"message": "fixture unavailable"})
			return
		}
		h.GetResearchThesis(c)
	})
	r.GET("/api/research-theses/:ticker/sources", h.ListThesisSources)
	r.PUT("/api/research-theses/:ticker", h.SaveResearchThesis)
	r.GET("/api/system-configs", func(c *gin.Context) { handler.OK(c, []any{}) })
	r.GET("/api/in-app-notifications/unread-count", func(c *gin.Context) { handler.OK(c, gin.H{"unread_count": 1}) })
	r.GET("/api/in-app-notifications", func(c *gin.Context) {
		handler.OK(c, gin.H{"items": []gin.H{{"id": 1, "ticker": "ALT", "title": "回归通知：核对 ALT", "source": "technical_signal", "link": "/ticker-workspace?ticker=ALT", "created_at": time.Now().UTC(), "read_at": time.Now().UTC()}}, "total": 1})
	})
	r.GET("/api/ticker-evaluations", func(c *gin.Context) { handler.OK(c, gin.H{"items": []any{}, "total": 0}) })
	r.GET("/api/filings", func(c *gin.Context) {
		handler.OK(c, gin.H{"items": []gin.H{{"ticker": c.Query("ticker"), "title": c.Query("ticker") + " 原文证据", "filing_type": "8-K", "filing_date": "2026-08-28", "filing_url": "https://www.sec.gov/Archives/fixture"}}, "total": 1})
	})
	r.GET("/api/insider-transactions", func(c *gin.Context) {
		handler.OK(c, gin.H{"items": []any{}, "total": 0, "summary": gin.H{"transactions": 0}})
	})
	r.GET("/api/ai/analyses", func(c *gin.Context) { handler.OK(c, gin.H{"items": []any{}, "total": 0}) })
	r.GET("/api/watch-targets", func(c *gin.Context) {
		id := 1
		if c.Query("ticker") == "ALT" {
			id = 2
		}
		handler.OK(c, gin.H{"items": []gin.H{{"id": id, "ticker": c.Query("ticker"), "company_name": c.Query("ticker") + " Fixture Inc."}}, "total": 1})
	})
	r.GET("/api/watch-targets/:id/technical-history", func(c *gin.Context) {
		price := 12.34
		if c.Param("id") == "2" {
			price = 45.67
		}
		handler.OK(c, gin.H{"technical": gin.H{"status": "ready", "trade_date": "2026-08-28", "close_usd": price, "liquidity_status": "normal"}, "history": []any{}})
	})
	r.GET("/api/discovery/institutional-holdings/:ticker", func(c *gin.Context) {
		mu.Lock()
		fail := scenario == "partial"
		mu.Unlock()
		if fail {
			c.JSON(503, gin.H{"message": "fixture unavailable"})
			return
		}
		handler.OK(c, gin.H{"institutional_holders": []any{}, "fund_holders": []any{}})
	})
	dist := os.Getenv("BROWSER_FIXTURE_DIST")
	if dist == "" {
		dist = "dist"
	}
	dist, err = filepath.Abs(dist)
	must(err)
	if _, err = os.Stat(filepath.Join(dist, "index.html")); err != nil {
		log.Fatal("先执行 npm run build: ", err)
	}
	files := http.FileServer(http.Dir(dist))
	r.NoRoute(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 5 && c.Request.URL.Path[:5] == "/api/" {
			mu.Lock()
			unexpected = append(unexpected, c.Request.Method+" "+c.Request.URL.Path)
			mu.Unlock()
			c.JSON(501, gin.H{"message": "unexpected fixture API: provider/scheduler access is forbidden"})
			return
		}
		if filepath.Ext(c.Request.URL.Path) != "" {
			files.ServeHTTP(c.Writer, c.Request)
			return
		}
		http.ServeFile(c.Writer, c.Request, filepath.Join(dist, "index.html"))
	})
	// Deliberately fixed loopback-only port: never proxy or target :9090.
	fmt.Println("Browser fixture ready at http://127.0.0.1:19090")
	must(http.ListenAndServe("127.0.0.1:19090", r))
}
func must(err error) {
	if err != nil {
		raw, _ := json.Marshal(err.Error())
		log.Fatal(string(raw))
	}
}
