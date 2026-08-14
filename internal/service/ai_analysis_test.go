package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type aiRoundTripper func(*http.Request) (*http.Response, error)

func (fn aiRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func newAIAnalysisTestServices(t *testing.T) (*gorm.DB, *ConfigService, *AIAnalysisService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemConfig{}, &model.OperationLog{}, &model.AIAnalysis{}, &model.InAppNotification{}); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	configs := NewConfigService(db, NewAuditService(db), config.SystemConfig{EncryptionKey: key})
	return db, configs, NewAIAnalysisService(db, configs, NewAuditService(db))
}

func TestAIProviderConfigMasksAndPreservesSecret(t *testing.T) {
	_, configs, _ := newAIAnalysisTestServices(t)
	ctx := context.Background()
	provider := AIProviderConfig{ID: "deepseek", Name: "DeepSeek", APIBaseURL: "https://api.deepseek.com/v1", APIKey: "secret-value", Model: "deepseek-chat", Enabled: true}
	if err := configs.SaveAIProviders(ctx, []AIProviderConfig{provider}, "tester"); err != nil {
		t.Fatal(err)
	}
	display, err := configs.AIProviderConfigForDisplay(ctx)
	if err != nil || len(display) != 1 || display[0].APIKey != maskedSecretMarker {
		t.Fatalf("display=%+v err=%v", display, err)
	}
	provider.APIKey = maskedSecretMarker
	provider.Model = "deepseek-reasoner"
	if err := configs.SaveAIProviders(ctx, []AIProviderConfig{provider}, "tester"); err != nil {
		t.Fatal(err)
	}
	stored, err := configs.AIProviders(ctx)
	if err != nil || stored[0].APIKey != "secret-value" || stored[0].Model != "deepseek-reasoner" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestAIAnalysisIsExplicitAndAudited(t *testing.T) {
	db, configs, analyses := newAIAnalysisTestServices(t)
	analyses.WithHTTPClient(&http.Client{Transport: aiRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-value" {
			t.Errorf("missing authorization")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["model"] != "deepseek-chat" {
			t.Errorf("model=%v", payload["model"])
		}
		if payload["max_tokens"] != float64(aiAnalysisMaxOutputTokens) || payload["stream"] != false {
			t.Errorf("response limits payload=%+v", payload)
		}
		messages, ok := payload["messages"].([]any)
		if !ok || len(messages) != 2 {
			t.Errorf("messages=%#v", payload["messages"])
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"基于快照的审慎结论"}}]}`))}, nil
	})})
	analyses.WithInAppNotifications(NewInAppNotificationService(db, configs))
	if err := configs.SaveAIProviders(context.Background(), []AIProviderConfig{{ID: "deepseek", Name: "DeepSeek", APIBaseURL: "https://example.test/v1", APIKey: "secret-value", Model: "deepseek-chat", Enabled: true}}, "tester"); err != nil {
		t.Fatal(err)
	}
	result, err := analyses.GenerateTickerAnalysis(context.Background(), AIAnalysisInput{ProviderID: "deepseek", Evaluation: discovery.TickerEvaluationResult{Ticker: "NVDA", CompanyName: "NVIDIA", TargetType: "stock", Status: "ready"}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || !strings.Contains(result.Content, "审慎结论") || result.InputSHA256 == "" || result.SystemPrompt != aiAnalysisSystemPrompt || !strings.Contains(result.UserPrompt, "本地事实研究包") {
		t.Fatalf("result=%+v", result)
	}
	if result.DurationMS < 0 {
		t.Fatalf("duration=%d", result.DurationMS)
	}
	page, err := analyses.List(context.Background(), AIAnalysisListFilter{Ticker: "NVDA", Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || page.Items[0].InputSnapshot != "" || page.Items[0].UserPrompt == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var auditCount int64
	if err := db.Model(&model.OperationLog{}).Where("object_type = ?", "ai_analysis").Count(&auditCount).Error; err != nil || auditCount != 2 {
		t.Fatalf("audit=%d err=%v", auditCount, err)
	}
	var notification model.InAppNotification
	if err := db.Where("event_key = ?", "ai_analysis:1:success").First(&notification).Error; err != nil || notification.Link != "/ai-analyses" {
		t.Fatalf("notification=%+v err=%v", notification, err)
	}
}

func TestQueueTickerAnalysisRejectsDuplicateActiveRequest(t *testing.T) {
	_, configs, analyses := newAIAnalysisTestServices(t)
	if err := configs.SaveAIProviders(context.Background(), []AIProviderConfig{{ID: "deepseek", Name: "DeepSeek", APIBaseURL: "https://example.test/v1", APIKey: "secret-value", Model: "deepseek-chat", Enabled: true}}, "tester"); err != nil {
		t.Fatal(err)
	}
	input := AIAnalysisInput{ProviderID: "deepseek", Evaluation: discovery.TickerEvaluationResult{Ticker: "NVDA", CompanyName: "NVIDIA", TargetType: "stock", Status: "ready"}}
	queued, err := analyses.QueueTickerAnalysis(context.Background(), input, "tester")
	if err != nil || queued.Status != "queued" {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	if _, err := analyses.QueueTickerAnalysis(context.Background(), input, "tester"); !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestQueueTickerAnalysisRendersConfiguredPromptTemplate(t *testing.T) {
	_, configs, analyses := newAIAnalysisTestServices(t)
	ctx := context.Background()
	if err := configs.SaveAIProviders(ctx, []AIProviderConfig{{ID: "deepseek", Name: "DeepSeek", APIBaseURL: "https://example.test/v1", APIKey: "secret-value", Model: "deepseek-chat", Enabled: true}}, "tester"); err != nil {
		t.Fatal(err)
	}
	template := AIPromptTemplate{ID: "custom-research", Name: "自定义研究", Content: "标的={{ticker}}；公司={{company_name}}；类型={{target_type}}；时间={{as_of}}\n事实：{{research_facts_json}}"}
	if err := configs.SaveAIPromptTemplates(ctx, []AIPromptTemplate{template}, "tester"); err != nil {
		t.Fatal(err)
	}
	record, err := analyses.QueueTickerAnalysis(ctx, AIAnalysisInput{ProviderID: "deepseek", TemplateID: template.ID, Evaluation: discovery.TickerEvaluationResult{Ticker: "NVDA", CompanyName: "NVIDIA", TargetType: "stock", Status: "ready"}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record.UserPrompt, "标的=NVDA；公司=NVIDIA；类型=stock；时间=") || !strings.Contains(record.UserPrompt, `"ticker":"NVDA"`) {
		t.Fatalf("rendered prompt=%q", record.UserPrompt)
	}
	if !strings.HasPrefix(record.PromptVersion, "custom-") || record.SystemPrompt != aiAnalysisSystemPrompt || record.TemplateID != template.ID || record.TemplateName != template.Name {
		t.Fatalf("record=%+v", record)
	}
}

func TestAIPromptTemplatesRejectDuplicateAndUnavailableSelection(t *testing.T) {
	_, configs, analyses := newAIAnalysisTestServices(t)
	ctx := context.Background()
	valid := AIPromptTemplate{ID: "research", Name: "研究", Content: "{{research_facts_json}}"}
	if err := configs.SaveAIPromptTemplates(ctx, []AIPromptTemplate{valid, valid}, "tester"); !errors.Is(err, ErrValidation) {
		t.Fatalf("duplicate error=%v", err)
	}
	if err := configs.SaveAIProviders(ctx, []AIProviderConfig{{ID: "deepseek", Name: "DeepSeek", APIBaseURL: "https://example.test/v1", APIKey: "secret-value", Model: "deepseek-chat", Enabled: true}}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := configs.SaveAIPromptTemplates(ctx, []AIPromptTemplate{valid, {ID: "brief", Name: "简报", Content: "{{research_facts_json}}"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	_, err := analyses.QueueTickerAnalysis(ctx, AIAnalysisInput{ProviderID: "deepseek", TemplateID: "missing", Evaluation: discovery.TickerEvaluationResult{Ticker: "NVDA"}}, "tester")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("selection error=%v", err)
	}
}

func TestAIPromptTemplatesGenerateIDsForNewTemplates(t *testing.T) {
	_, configs, _ := newAIAnalysisTestServices(t)
	templates := []AIPromptTemplate{{Name: "无 ID 模板", Content: "{{research_facts_json}}"}}
	if err := configs.SaveAIPromptTemplates(context.Background(), templates, "tester"); err != nil {
		t.Fatal(err)
	}
	stored, err := configs.AIPromptTemplates(context.Background())
	if err != nil || len(stored) != 1 || !strings.HasPrefix(stored[0].ID, "template-") || stored[0].Name != "无 ID 模板" {
		t.Fatalf("templates=%+v err=%v", stored, err)
	}
}

func TestAIAnalysisPromptTemplateValidation(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "default", value: aiAnalysisDefaultUserPromptTemplate, valid: true},
		{name: "missing research package", value: "只分析 {{ticker}}", valid: false},
		{name: "unknown variable", value: "{{research_facts_json}} {{unknown}}", valid: false},
		{name: "unclosed variable", value: "{{research_facts_json}} {{ticker", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := renderAIAnalysisPromptTemplate(test.value, aiAnalysisPromptTemplateValues{})
			if (err == nil) != test.valid {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestEnsureDefaultsUpgradesOnlyPreviousAIPromptTemplate(t *testing.T) {
	_, configs, _ := newAIAnalysisTestServices(t)
	ctx := context.Background()
	if err := configs.UpsertMany(ctx, []ConfigInput{{Key: aiAnalysisPromptTemplateConfigKey, Value: aiAnalysisPreviousDefaultUserPromptTemplate, ValueType: "string", Category: "ai_analysis"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := configs.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	value, found, err := configs.GetValue(ctx, aiAnalysisPromptTemplateConfigKey)
	if err != nil || !found || value != aiAnalysisDefaultUserPromptTemplate {
		t.Fatalf("value=%q found=%v err=%v", value, found, err)
	}

	custom := "仅输出事实与推断。{{research_facts_json}}"
	if err := configs.UpsertMany(ctx, []ConfigInput{{Key: aiAnalysisPromptTemplateConfigKey, Value: custom, ValueType: "string", Category: "ai_analysis"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := configs.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	value, _, err = configs.GetValue(ctx, aiAnalysisPromptTemplateConfigKey)
	if err != nil || value != custom {
		t.Fatalf("custom value=%q err=%v", value, err)
	}
}

func TestNormalizePromptTemplate(t *testing.T) {
	if got := normalizePromptTemplate("\r\n  a\r\nb  \n"); got != "a\nb" {
		t.Fatalf("normalized=%q", got)
	}
}

func TestBuildAIResearchSnapshotRemovesScoresAndMissingValues(t *testing.T) {
	snapshot, err := buildAIResearchSnapshot(map[string]any{
		"ticker": "ACME", "total_score": 88, "grade": "A", "eligible_a": true,
		"candidate_score": map[string]any{
			"price_close_usd": 12.5, "review_priority_score": 42,
			"technical": map[string]any{"close_usd": 12.5, "signals": []any{"突破"}, "trade_setup": map[string]any{"entry_trigger": "突破"}},
		},
		"market_research": map[string]any{"latest": nil, "message": "尚未同步"},
		"empty_list":      []any{}, "blank": "", "missing": "unavailable",
		"institutional_holders": []any{
			map[string]any{"holder_name": "Example Fund", "percent_of_shares": 1.2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(snapshot)
	for _, forbidden := range []string{"total_score", "review_priority_score", "eligible_a", "trade_setup", "signals", "尚未同步", "empty_list"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("snapshot should not contain %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"price_close_usd", "close_usd", "Example Fund"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("snapshot should retain %q: %s", expected, text)
		}
	}
}

func TestAIProviderTimeout(t *testing.T) {
	for _, test := range []struct {
		name  string
		value int
		want  time.Duration
		valid bool
	}{
		{name: "default", value: 0, want: aiAnalysisDefaultTimeout, valid: true},
		{name: "minimum", value: 30, want: 30 * time.Second, valid: true},
		{name: "maximum", value: 300, want: 300 * time.Second, valid: true},
		{name: "too short", value: 29, want: aiAnalysisDefaultTimeout, valid: false},
		{name: "too long", value: 301, want: aiAnalysisDefaultTimeout, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := AIProviderConfig{ID: "test", Name: "Test", APIBaseURL: "https://example.test", APIKey: "key", Model: "model", Enabled: true, TimeoutSeconds: test.value}
			if got := aiProviderTimeout(provider); got != test.want {
				t.Fatalf("timeout=%s want=%s", got, test.want)
			}
			if err := validateAIProvider(provider); (err == nil) != test.valid {
				t.Fatalf("validate error=%v valid=%v", err, test.valid)
			}
		})
	}
}

func TestAIAnalysisUsesReasoningFallbackWhenFinalContentIsEmpty(t *testing.T) {
	_, _, analyses := newAIAnalysisTestServices(t)
	analyses.WithHTTPClient(&http.Client{Transport: aiRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"","reasoning_content":"这是可复核的分析过程"}}]}`))}, nil
	})})
	provider := AIProviderConfig{ID: "deepseek", Name: "DeepSeek", APIBaseURL: "https://api.deepseek.com/v1", APIKey: "secret", Model: "deepseek-v4-pro", Enabled: true}
	content, err := analyses.callOpenAICompatible(context.Background(), provider, `{"ticker":"NVDA"}`)
	if err != nil || !strings.Contains(content, "可复核的分析过程") {
		t.Fatalf("content=%q err=%v", content, err)
	}
}
