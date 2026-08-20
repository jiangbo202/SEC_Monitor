package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"

	"gorm.io/gorm"
)

const (
	aiAnalysisPromptVersion           = "ticker-v2-facts-only"
	aiAnalysisPromptTemplateConfigKey = "ai_analysis.user_prompt_template"
	aiAnalysisMaxInputBytes           = 48_000
	aiAnalysisMaxOutputTokens         = 2_400
	aiAnalysisDefaultTimeout          = 120 * time.Second
	aiAnalysisMaxArrayItems           = 12
	aiAnalysisMaxSECDocumentBytes     = 36_000
)

var (
	secFilingNonContentElementPattern = regexp.MustCompile(`(?is)<(?:head|script|style|noscript|template|svg)[^>]*>.*?</(?:head|script|style|noscript|template|svg)\s*>`)
	secFilingCommentPattern           = regexp.MustCompile(`(?is)<!--.*?-->`)
	secFilingTagPattern               = regexp.MustCompile(`(?is)<[^>]+>`)
)

var aiAnalysisSystemPrompt = aiAnalysisStructuredSystemPrompt

// aiAnalysisDefaultUserPromptTemplate is deliberately a facts-only template.
// The project fills the research package at request time, rather than sending
// its own scores, grades or trading-rule conclusions to a third-party model.
// Users can adjust the wording in System Config → AI Analysis; the placeholder
// remains mandatory so every manual analysis still has its local evidence.
const aiAnalysisPreviousDefaultUserPromptTemplate = "你是审慎的美股研究助理。仅基于下方本地事实研究包，用中文输出：\n1. 核心结论（不构成投资建议）；\n2. 基本面、趋势/动量、量价的支持与风险；\n3. 数据缺口与需要人工验证的事项；\n4. 不要编造任何未在研究包中出现的事实、价格、日期或持仓。\n5. 研究包已主动移除本系统的评分、候选等级、入场/离场规则结论；请独立分析，勿将缺失字段视为负面事实。\n\n本地事实研究包：\n{{research_facts_json}}"

const aiAnalysisMarkdownDefaultUserPromptTemplate = "你是一位审慎的美股研究助理。仅基于下方本地事实研究包完成一次真正的研究判断，不要逐字段复述研究包，也不构成投资建议。\n\n输出请严格采用以下结构：\n\n## 1. 研究结论\n用 2–4 句话给出“关注 / 观望 / 回避”的研究倾向及最重要的原因；若证据不足，明确写“证据不足”，不要勉强下结论。\n\n## 2. 最关键的证据与推断\n挑选最多 4 项最具解释力的事实。每项都要先写事实（带可用的数值、日期或变化），再说明它为何会影响成长、盈利质量、估值预期或市场行为；不要把所有字段重新罗列一遍。\n\n## 3. 反证、风险与失效条件\n说明与结论相矛盾的事实、主要风险，以及哪些后续事实会推翻当前判断。\n\n## 4. 近期催化剂与观察重点\n仅在研究包有依据时，列出未来财报、公告、价格/成交量变化、分析师或持仓变化等值得跟踪的催化剂；没有依据则说明未识别到。\n\n## 5. 数据缺口与下一步验证\n只列出会实质改变判断的缺失数据或待核验事项，并说明应验证什么。\n\n写作要求：\n- 清楚标注“事实”“推断”“待验证”，推断必须能回溯到研究包中的事实。\n- 优先分析变化、趋势、矛盾和相对重要性，而不是同义改写数字。\n- 研究包已主动移除本系统的评分、候选等级、入场/离场规则结论；不得猜测或恢复这些内容。\n- 不得编造研究包中不存在的事实、价格、日期、持仓或市场共识；缺失数据不等同于负面事实。\n- 避免泛泛而谈的免责声明和重复表述，正文应尽量具体、紧凑。\n\n本地事实研究包：\n{{research_facts_json}}"

const aiAnalysisDefaultUserPromptTemplate = "你是一位审慎的美股研究助理。仅基于下方本地事实研究包做判断，不要逐字段复述，也不构成投资建议。请按系统消息定义的 ai-research-v1 JSON 返回：证据必须写出事实、推断、影响以及可回溯的 source_paths；缺少依据时使用 insufficient_evidence，不得猜测系统评分、候选等级或交易结论。\n\n本地事实研究包：\n{{research_facts_json}}"

type AIAnalysisInput struct {
	ProviderID  string                           `json:"provider_id"`
	TemplateID  string                           `json:"template_id,omitempty"`
	Scope       string                           `json:"scope,omitempty"`
	Ticker      string                           `json:"ticker,omitempty"`
	CompanyName string                           `json:"company_name,omitempty"`
	TargetType  string                           `json:"target_type,omitempty"`
	SourceID    string                           `json:"source_id,omitempty"`
	SourceURL   string                           `json:"source_url,omitempty"`
	Context     any                              `json:"context,omitempty"`
	Evaluation  discovery.TickerEvaluationResult `json:"evaluation"`
}

type AIAnalysisListFilter struct {
	Ticker   string
	Scope    string
	Page     int
	PageSize int
}

type AIAnalysisService struct {
	db                 *gorm.DB
	configs            *ConfigService
	audit              *AuditService
	inApp              *InAppNotificationService
	notificationCenter *NotificationBatchService
	httpClient         *http.Client
	secFilingFetcher   sec.FilingDocumentFetcher
	queueMu            sync.Mutex
	aiRetryWait        func(context.Context, time.Duration) error
}

func NewAIAnalysisService(db *gorm.DB, configs *ConfigService, audit *AuditService) *AIAnalysisService {
	return &AIAnalysisService{db: db, configs: configs, audit: audit, httpClient: &http.Client{Timeout: aiAnalysisDefaultTimeout}}
}

func (s *AIAnalysisService) WithHTTPClient(client *http.Client) *AIAnalysisService {
	if client != nil {
		s.httpClient = client
	}
	return s
}

func (s *AIAnalysisService) WithSECFilingFetcher(fetcher sec.FilingDocumentFetcher) *AIAnalysisService {
	if fetcher != nil {
		s.secFilingFetcher = fetcher
	}
	return s
}

func (s *AIAnalysisService) WithInAppNotifications(notifications *InAppNotificationService) *AIAnalysisService {
	s.inApp = notifications
	return s
}

func (s *AIAnalysisService) WithNotificationCenter(notifications *NotificationBatchService) *AIAnalysisService {
	s.notificationCenter = notifications
	return s
}

// QueueTickerAnalysis persists a user-approved AI request without holding an
// HTTP connection open. It is never used by schedules, page views, or events.
func (s *AIAnalysisService) QueueTickerAnalysis(ctx context.Context, input AIAnalysisInput, operator string) (model.AIAnalysis, error) {
	if s == nil || s.db == nil || s.configs == nil {
		return model.AIAnalysis{}, errors.New("AI analysis service is not configured")
	}
	evaluation := input.Evaluation
	ticker := strings.ToUpper(strings.TrimSpace(input.Ticker))
	companyName, targetType := strings.TrimSpace(input.CompanyName), strings.TrimSpace(input.TargetType)
	snapshotValue := input.Context
	if ticker == "" {
		evaluation.Ticker = strings.ToUpper(strings.TrimSpace(evaluation.Ticker))
		ticker, companyName, targetType, snapshotValue = evaluation.Ticker, evaluation.CompanyName, evaluation.TargetType, evaluation
	}
	if ticker == "" || strings.TrimSpace(input.ProviderID) == "" || snapshotValue == nil {
		return model.AIAnalysis{}, ErrValidation
	}
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		scope = "ticker_evaluation"
	}
	input.Scope = scope
	providers, err := s.configs.AIProviders(ctx)
	if err != nil {
		return model.AIAnalysis{}, err
	}
	var provider *AIProviderConfig
	for index := range providers {
		if providers[index].ID == strings.ToLower(strings.TrimSpace(input.ProviderID)) && providers[index].Enabled {
			provider = &providers[index]
			break
		}
	}
	if provider == nil {
		return model.AIAnalysis{}, errors.New("selected AI provider is unavailable or disabled")
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		return model.AIAnalysis{}, errors.New("selected AI provider has no API key")
	}

	snapshot, err := buildAIResearchSnapshot(snapshotValue)
	if err != nil {
		return model.AIAnalysis{}, err
	}
	if len(snapshot) > aiAnalysisMaxInputBytes {
		snapshot = snapshot[:aiAnalysisMaxInputBytes]
	}
	now := time.Now().UTC()
	systemPrompt, userPrompt, promptVersion, template, err := s.configuredAIAnalysisPrompts(ctx, string(snapshot), input, ticker, companyName, targetType, now)
	if err != nil {
		return model.AIAnalysis{}, err
	}
	hash := sha256.Sum256(snapshot)
	record := model.AIAnalysis{Scope: scope, SourceID: strings.TrimSpace(input.SourceID), SourceURL: strings.TrimSpace(input.SourceURL), Ticker: ticker, CompanyName: companyName, TargetType: targetType, ProviderID: provider.ID, ProviderName: provider.Name, Model: provider.Model, TemplateID: template.ID, TemplateName: template.Name, PromptVersion: promptVersion, SystemPrompt: systemPrompt, UserPrompt: userPrompt, InputSHA256: hex.EncodeToString(hash[:]), InputSnapshot: string(snapshot), SchemaVersion: model.AIAnalysisSchemaV1, ResponseMode: "json_object", Status: "queued", RequestedAt: now}
	record.AnalysisKeySHA256 = aiAnalysisKey(record)
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	var existing model.AIAnalysis
	activeQuery := s.db.WithContext(ctx).Where("scope = ? AND ticker = ? AND provider_id = ? AND status IN ?", scope, ticker, provider.ID, []string{"queued", "running"})
	// SEC analyses are tied to one persisted filing. Different filings for the
	// same ticker may be analysed at the same time; only the same document is
	// deduplicated while it is queued or running.
	if scope == "sec_filing" {
		activeQuery = activeQuery.Where("source_id = ?", strings.TrimSpace(input.SourceID))
	}
	err = activeQuery.Order("id DESC").First(&existing).Error
	if err == nil {
		return model.AIAnalysis{}, TaskAlreadyRunning("ai_analysis:" + scope + ":" + ticker + ":" + provider.ID)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.AIAnalysis{}, err
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return model.AIAnalysis{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, operator, "queue", "ai_analysis", fmt.Sprintf("%d", record.ID), nil, map[string]any{"ticker": record.Ticker, "provider_id": record.ProviderID, "model": record.Model, "status": record.Status, "prompt_version": record.PromptVersion})
	}
	return record, nil
}

// QueueSECFilingAnalysis creates an auditable, explicitly requested SEC
// analysis. The source URL comes only from a persisted Filing record; callers
// cannot use this path as an arbitrary server-side URL fetcher.
func (s *AIAnalysisService) QueueSECFilingAnalysis(ctx context.Context, providerID, templateID string, filing model.Filing, operator string) (model.AIAnalysis, error) {
	if filing.ID == 0 || strings.TrimSpace(filing.Ticker) == "" || !isAllowedSECFilingURL(filing.FilingURL) {
		return model.AIAnalysis{}, ErrValidation
	}
	contextValue := map[string]any{
		"filing_id": filing.FilingID, "accession_number": filing.AccessionNumber,
		"filing_type": filing.FilingType, "filing_date": filing.FilingDate.Format("2006-01-02"),
		"published_at": filing.PublishedAt, "title": filing.Title, "filing_url": filing.FilingURL,
	}
	return s.QueueTickerAnalysis(ctx, AIAnalysisInput{
		ProviderID: providerID, TemplateID: templateID, Scope: "sec_filing", SourceID: fmt.Sprintf("%d", filing.ID), SourceURL: filing.FilingURL,
		Ticker: filing.Ticker, CompanyName: filing.CompanyName, TargetType: "sec_filing", Context: contextValue,
	}, operator)
}

// ProcessTickerAnalysis claims a queued request and performs the provider call
// independently from the original browser request. The durable status change
// makes refreshes and restarts observable instead of silently losing work.
func (s *AIAnalysisService) ProcessTickerAnalysis(ctx context.Context, id uint, operator string) (model.AIAnalysis, error) {
	if s == nil || s.db == nil || s.configs == nil {
		return model.AIAnalysis{}, errors.New("AI analysis service is not configured")
	}
	claim := s.db.WithContext(ctx).Model(&model.AIAnalysis{}).Where("id = ? AND status = ?", id, "queued").Update("status", "running")
	if claim.Error != nil {
		return model.AIAnalysis{}, claim.Error
	}
	if claim.RowsAffected == 0 {
		return model.AIAnalysis{}, TaskAlreadyRunning(fmt.Sprintf("ai_analysis:%d", id))
	}
	var record model.AIAnalysis
	if err := s.db.WithContext(ctx).First(&record, id).Error; err != nil {
		return model.AIAnalysis{}, err
	}
	providers, err := s.configs.AIProviders(ctx)
	if err != nil {
		return s.finishAIAnalysis(ctx, record, aiAnalysisCallResult{}, err, operator)
	}
	var provider *AIProviderConfig
	for index := range providers {
		if providers[index].ID == record.ProviderID && providers[index].Enabled && strings.TrimSpace(providers[index].APIKey) != "" {
			provider = &providers[index]
			break
		}
	}
	if provider == nil {
		return s.finishAIAnalysis(ctx, record, aiAnalysisCallResult{}, errors.New("selected AI provider is unavailable or disabled"), operator)
	}
	if record.Scope == "sec_filing" {
		if record, err = s.enrichSECFilingAnalysis(ctx, record); err != nil {
			return s.finishAIAnalysis(ctx, record, aiAnalysisCallResult{}, err, operator)
		}
	}
	systemPrompt, userPrompt := record.SystemPrompt, record.UserPrompt
	if strings.TrimSpace(systemPrompt) == "" || strings.TrimSpace(userPrompt) == "" {
		// Records made before request prompts were persisted remain executable.
		systemPrompt, userPrompt = aiAnalysisPrompts(record.InputSnapshot)
	}
	record.SystemPrompt, record.UserPrompt, record.SchemaVersion = systemPrompt, userPrompt, model.AIAnalysisSchemaV1
	record.AnalysisKeySHA256 = aiAnalysisKey(record)
	if err := s.db.WithContext(ctx).Model(&model.AIAnalysis{}).Where("id = ?", record.ID).Updates(map[string]any{
		"system_prompt": record.SystemPrompt, "user_prompt": record.UserPrompt, "schema_version": record.SchemaVersion,
		"analysis_key_sha256": record.AnalysisKeySHA256,
	}).Error; err != nil {
		return s.finishAIAnalysis(ctx, record, aiAnalysisCallResult{}, err, operator)
	}
	if reusable, ok, reuseErr := s.reusableAIAnalysis(ctx, record); reuseErr != nil {
		return s.finishAIAnalysis(ctx, record, aiAnalysisCallResult{}, reuseErr, operator)
	} else if ok {
		reusedID := reusable.ID
		result := aiAnalysisCallResult{Structured: *reusable.StructuredResult, ResultJSON: reusable.ResultJSON, Content: reusable.Content, ResponseMode: "cache", ReusedFromID: &reusedID}
		return s.finishAIAnalysis(ctx, record, result, nil, operator)
	}
	callResult, callErr := s.callStructuredAIAnalysis(ctx, *provider, systemPrompt, userPrompt)
	return s.finishAIAnalysis(ctx, record, callResult, callErr, operator)
}

func (s *AIAnalysisService) enrichSECFilingAnalysis(ctx context.Context, record model.AIAnalysis) (model.AIAnalysis, error) {
	if s.secFilingFetcher == nil {
		return record, errors.New("SEC filing document fetcher is not configured")
	}
	if !isAllowedSECFilingURL(record.SourceURL) {
		return record, ErrValidation
	}
	document, err := s.secFilingFetcher.FetchFilingDocument(ctx, record.SourceURL)
	if err != nil {
		return record, fmt.Errorf("fetch persisted SEC filing document: %w", err)
	}
	text := compactSECFilingDocument(document)
	if text == "" {
		return record, errors.New("SEC filing document has no readable text")
	}
	var snapshotValue map[string]any
	if err := json.Unmarshal([]byte(record.InputSnapshot), &snapshotValue); err != nil {
		return record, fmt.Errorf("parse SEC filing analysis snapshot: %w", err)
	}
	snapshotValue["document_text"] = text
	snapshot, err := buildAIResearchSnapshot(snapshotValue)
	if err != nil {
		return record, err
	}
	if len(snapshot) > aiAnalysisMaxInputBytes {
		snapshot = snapshot[:aiAnalysisMaxInputBytes]
	}
	input := AIAnalysisInput{TemplateID: record.TemplateID, Scope: record.Scope, SourceURL: record.SourceURL}
	systemPrompt, userPrompt, promptVersion, template, err := s.configuredAIAnalysisPrompts(ctx, string(snapshot), input, record.Ticker, record.CompanyName, record.TargetType, record.RequestedAt)
	if err != nil {
		return record, err
	}
	hash := sha256.Sum256(snapshot)
	updates := map[string]any{
		"input_snapshot": string(snapshot), "input_sha256": hex.EncodeToString(hash[:]), "system_prompt": systemPrompt,
		"user_prompt": userPrompt, "prompt_version": promptVersion, "template_name": template.Name,
	}
	if err := s.db.WithContext(ctx).Model(&model.AIAnalysis{}).Where("id = ?", record.ID).Updates(updates).Error; err != nil {
		return record, err
	}
	record.InputSnapshot, record.InputSHA256, record.SystemPrompt, record.UserPrompt = string(snapshot), hex.EncodeToString(hash[:]), systemPrompt, userPrompt
	record.PromptVersion, record.TemplateName = promptVersion, template.Name
	record.AnalysisKeySHA256 = aiAnalysisKey(record)
	if err := s.db.WithContext(ctx).Model(&model.AIAnalysis{}).Where("id = ?", record.ID).Update("analysis_key_sha256", record.AnalysisKeySHA256).Error; err != nil {
		return record, err
	}
	return record, nil
}

func isAllowedSECFilingURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "sec.gov" || host == "www.sec.gov" || host == "archives.sec.gov"
}

func compactSECFilingDocument(value string) string {
	// EDGAR primary documents are usually HTML. A deliberately conservative
	// text normalisation keeps the factual content readable without executing
	// or forwarding presentation markup (especially large SEC CSS blocks) to
	// the third-party model.
	value = secFilingCommentPattern.ReplaceAllString(value, " ")
	value = secFilingNonContentElementPattern.ReplaceAllString(value, " ")
	value = strings.NewReplacer("</p>", " ", "</div>", " ", "</tr>", " ", "<br>", " ", "<br/>", " ", "<br />", " ").Replace(value)
	value = secFilingTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	value = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ", "&nbsp;", " ").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > aiAnalysisMaxSECDocumentBytes {
		value = value[:aiAnalysisMaxSECDocumentBytes] + " [正文已截断]"
	}
	return strings.TrimSpace(value)
}

func (s *AIAnalysisService) finishAIAnalysis(ctx context.Context, record model.AIAnalysis, result aiAnalysisCallResult, callErr error, operator string) (model.AIAnalysis, error) {
	completed := time.Now().UTC()
	record.DurationMS = completed.Sub(record.RequestedAt).Milliseconds()
	if record.DurationMS < 0 {
		record.DurationMS = 0
	}
	updates := map[string]any{"completed_at": &completed, "duration_ms": record.DurationMS, "request_attempts": result.Attempts, "schema_version": model.AIAnalysisSchemaV1}
	record.RequestAttempts = result.Attempts
	record.SchemaVersion = model.AIAnalysisSchemaV1
	if result.ResponseMode != "" {
		record.ResponseMode = result.ResponseMode
		updates["response_mode"] = result.ResponseMode
	}
	if callErr != nil {
		record.Status = "failed"
		record.ErrorMessage = SanitizeSensitiveError(callErr.Error())
		updates["status"], updates["error_message"] = record.Status, record.ErrorMessage
	} else {
		record.Status, record.Content = "success", result.Content
		record.SchemaVersion, record.ResultJSON, record.ResponseMode, record.ReusedFromID = model.AIAnalysisSchemaV1, result.ResultJSON, result.ResponseMode, result.ReusedFromID
		structured := result.Structured
		record.StructuredResult = &structured
		updates["status"], updates["content"] = record.Status, record.Content
		updates["schema_version"], updates["result_json"], updates["response_mode"], updates["reused_from_id"] = record.SchemaVersion, record.ResultJSON, record.ResponseMode, record.ReusedFromID
	}
	record.CompletedAt = &completed
	if err := s.db.WithContext(ctx).Model(&model.AIAnalysis{}).Where("id = ?", record.ID).Updates(updates).Error; err != nil {
		return model.AIAnalysis{}, err
	}
	if s.audit != nil {
		action := "complete"
		if record.ReusedFromID != nil {
			action = "reuse"
		}
		_ = s.audit.Record(ctx, operator, action, "ai_analysis", fmt.Sprintf("%d", record.ID), nil, map[string]any{"ticker": record.Ticker, "provider_id": record.ProviderID, "model": record.Model, "status": record.Status, "prompt_version": record.PromptVersion, "schema_version": record.SchemaVersion, "request_attempts": record.RequestAttempts, "reused_from_id": record.ReusedFromID, "duration_ms": record.DurationMS})
	}
	if s.inApp != nil {
		severity, title := "success", fmt.Sprintf("%s | AI 研判已完成", record.Ticker)
		body := fmt.Sprintf("%s · %s · 耗时 %s", record.ProviderName, record.Model, formatAIAnalysisDuration(record.DurationMS))
		if record.Status != "success" {
			severity, title = "warning", fmt.Sprintf("%s | AI 研判失败", record.Ticker)
			body = fmt.Sprintf("%s · %s · 耗时 %s。%s", record.ProviderName, record.Model, formatAIAnalysisDuration(record.DurationMS), record.ErrorMessage)
		}
		link := "/ai-analyses"
		if record.Scope == "sec_filing" {
			link = fmt.Sprintf("/ai-analyses?scope=sec_filing&ticker=%s", url.QueryEscape(record.Ticker))
		}
		_, _, _ = s.inApp.Create(ctx, InAppNotificationInput{EventKey: fmt.Sprintf("ai_analysis:%d:%s", record.ID, record.Status), Source: "ai_analysis", Scope: record.Scope, EntityKind: "ai_analysis", Ticker: record.Ticker, CompanyName: record.CompanyName, Severity: severity, Title: title, Body: body, Link: link, OccurredAt: completed})
	}
	if s.notificationCenter != nil {
		statusText := "已完成"
		if record.Status != "success" {
			statusText = "失败"
		}
		_, _ = s.notificationCenter.DeliverMessage(ctx, NotificationMessageInput{Source: "ai_analysis", Trigger: "completion", EventKey: fmt.Sprintf("ai_analysis:%d:%s", record.ID, record.Status), EntityKind: "ai_analysis", Title: fmt.Sprintf("%s | AI 研判%s", record.Ticker, statusText), SummaryText: fmt.Sprintf("%s | AI 研判%s\n模型：%s · %s\n耗时：%s", record.Ticker, statusText, record.ProviderName, record.Model, formatAIAnalysisDuration(record.DurationMS)), EventAt: completed})
	}
	if callErr != nil {
		return record, callErr
	}
	return record, nil
}

func formatAIAnalysisDuration(durationMS int64) string {
	if durationMS <= 0 {
		return "-"
	}
	if durationMS < 1000 {
		return fmt.Sprintf("%d ms", durationMS)
	}
	return fmt.Sprintf("%.2f 秒", float64(durationMS)/1000)
}

// GenerateTickerAnalysis keeps the synchronous service API available for
// focused tests and internal callers. HTTP handlers use QueueTickerAnalysis
// plus ProcessTickerAnalysis in a background goroutine instead.
func (s *AIAnalysisService) GenerateTickerAnalysis(ctx context.Context, input AIAnalysisInput, operator string) (model.AIAnalysis, error) {
	record, err := s.QueueTickerAnalysis(ctx, input, operator)
	if err != nil {
		return model.AIAnalysis{}, err
	}
	return s.ProcessTickerAnalysis(ctx, record.ID, operator)
}

func (s *AIAnalysisService) List(ctx context.Context, filter AIAnalysisListFilter) (PageResult[model.AIAnalysis], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.AIAnalysis{})
	if ticker := strings.ToUpper(strings.TrimSpace(filter.Ticker)); ticker != "" {
		query = query.Where("ticker = ?", ticker)
	}
	if scope := strings.TrimSpace(filter.Scope); scope != "" {
		query = query.Where("scope = ?", scope)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.AIAnalysis]{}, err
	}
	var rows []model.AIAnalysis
	err := query.Order("requested_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	for index := range rows {
		// Input snapshots can be large and are not needed for normal list views;
		// hashes + prompt version retain the audit reference without network I/O.
		rows[index].InputSnapshot = ""
		rows[index].ErrorMessage = SanitizeSensitiveError(rows[index].ErrorMessage)
		hydrateAIAnalysisStructuredResult(&rows[index])
	}
	return newPageResult(rows, total, page, pageSize), err
}

func aiAnalysisPrompts(snapshot string) (string, string) {
	userPrompt, _ := renderAIAnalysisPromptTemplate(aiAnalysisDefaultUserPromptTemplate, aiAnalysisPromptTemplateValues{ResearchFactsJSON: snapshot})
	return aiAnalysisSystemPrompt, userPrompt
}

type aiAnalysisPromptTemplateValues struct {
	Ticker            string
	CompanyName       string
	TargetType        string
	AsOf              string
	ResearchFactsJSON string
	SECFilingContent  string
	FilingURL         string
	FilingType        string
}

func (s *AIAnalysisService) configuredAIAnalysisPrompts(ctx context.Context, snapshot string, input AIAnalysisInput, ticker, companyName, targetType string, requestedAt time.Time) (string, string, string, AIPromptTemplate, error) {
	template, err := s.configs.AIPromptTemplateForScope(ctx, input.TemplateID, input.Scope)
	if err != nil {
		return "", "", "", AIPromptTemplate{}, err
	}
	if companyName == "" {
		companyName = strings.TrimSpace(input.Evaluation.CompanyName)
	}
	if targetType == "" {
		targetType = strings.TrimSpace(input.Evaluation.TargetType)
	}
	userPrompt, err := renderAIAnalysisPromptTemplate(template.Content, aiAnalysisPromptTemplateValues{
		Ticker:            ticker,
		CompanyName:       companyName,
		TargetType:        targetType,
		AsOf:              requestedAt.Format(time.RFC3339),
		ResearchFactsJSON: snapshot,
		SECFilingContent:  snapshot,
		FilingURL:         input.SourceURL,
		FilingType:        filingTypeFromSnapshot(snapshot),
	})
	if err != nil {
		return "", "", "", AIPromptTemplate{}, err
	}
	return aiAnalysisSystemPrompt, userPrompt, aiAnalysisPromptTemplateVersion(template.Content), template, nil
}

func filingTypeFromSnapshot(snapshot string) string {
	var value struct {
		FilingType string `json:"filing_type"`
	}
	_ = json.Unmarshal([]byte(snapshot), &value)
	return value.FilingType
}

func aiAnalysisPromptTemplateVersion(template string) string {
	if template == aiAnalysisDefaultUserPromptTemplate {
		return aiAnalysisPromptVersion
	}
	sum := sha256.Sum256([]byte(template))
	return "custom-" + hex.EncodeToString(sum[:])[:12]
}

func renderAIAnalysisPromptTemplate(template string, values aiAnalysisPromptTemplateValues) (string, error) {
	template = strings.TrimSpace(template)
	if template == "" || (!strings.Contains(template, "{{research_facts_json}}") && !strings.Contains(template, "{{sec_filing_content}}")) {
		return "", fmt.Errorf("%w: AI 提示词模板必须包含事实包变量", ErrValidation)
	}
	allowed := map[string]string{
		"ticker":              values.Ticker,
		"company_name":        values.CompanyName,
		"target_type":         values.TargetType,
		"as_of":               values.AsOf,
		"research_facts_json": values.ResearchFactsJSON,
		"sec_filing_content":  values.SECFilingContent,
		"filing_url":          values.FilingURL,
		"filing_type":         values.FilingType,
	}
	for remaining := template; ; {
		start := strings.Index(remaining, "{{")
		if start < 0 {
			break
		}
		remaining = remaining[start+2:]
		end := strings.Index(remaining, "}}")
		if end < 0 {
			return "", fmt.Errorf("%w: AI 提示词模板存在未闭合变量", ErrValidation)
		}
		name := strings.TrimSpace(remaining[:end])
		if _, ok := allowed[name]; !ok {
			return "", fmt.Errorf("%w: AI 提示词模板包含不支持的变量 {{%s}}", ErrValidation, name)
		}
		remaining = remaining[end+2:]
	}
	replacerValues := make([]string, 0, len(allowed)*2)
	for name, value := range allowed {
		replacerValues = append(replacerValues, "{{"+name+"}}", value)
	}
	return strings.NewReplacer(replacerValues...).Replace(template), nil
}

func validateAIPromptTemplate(template AIPromptTemplate) (string, error) {
	content, err := renderAIAnalysisPromptTemplate(template.Content, aiAnalysisPromptTemplateValues{})
	if err != nil {
		return "", err
	}
	hasResearchFacts := strings.Contains(template.Content, "{{research_facts_json}}")
	hasSECFilingContent := strings.Contains(template.Content, "{{sec_filing_content}}")
	needsResearchFacts, needsSECFilingContent := len(template.Scopes) == 0, false
	for _, scope := range template.Scopes {
		if scope == "sec_filing" {
			needsSECFilingContent = true
		} else {
			needsResearchFacts = true
		}
	}
	if (needsResearchFacts && !hasResearchFacts) || (needsSECFilingContent && !hasSECFilingContent) {
		return "", fmt.Errorf("%w: 所选功能区缺少对应事实包变量", ErrValidation)
	}
	return content, nil
}

// buildAIResearchSnapshot makes the third-party request independent from this
// product's own scoring and trading-rule outcomes. It keeps factual research
// evidence only, removes empty/missing values, and bounds repeated history so
// a detailed page cannot unexpectedly consume an excessive model context.
func buildAIResearchSnapshot(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	cleaned, keep := compactAIResearchValue(decoded, "")
	if !keep {
		return json.Marshal(map[string]any{})
	}
	return json.Marshal(cleaned)
}

var aiPromptExcludedFields = map[string]bool{
	"id": true, "batch_id": true, "security_id": true, "candidate_score": false,
	"grade": true, "eligible_a": true, "eligible_b": true, "total_score": true,
	"revenue_growth_score": true, "cash_runway_score": true, "insider_score": true,
	"gross_margin_score": true, "dilution_risk_score": true, "sector_score": true,
	"review_priority_score": true, "review_priority_reasons": true,
	"quality_tier": true, "quality_tags": true, "quality_adjusted_score": true,
	"quality_adjustments": true, "active_blocks_a": true, "active_blocks_b": true,
	"reason_code": true, "scoring_version": true, "business_model_at_score": true,
	"revenue_score_cap_reason": true, "score": true, "score_history": true,
	"sector_rating_score": true, "revenue_score_cap": true, "followed": true,
	"signal_events": true, "signals": true, "trade_setup": true, "trade_setup_history": true,
	"investability": true, "research_readiness": true, "research_next_step": true,
	"performance": true, "data_quality": true, "data_lineage": true,
	"warnings": true, "refresh_notes": true, "sources": true,
	"reasons": true, "message": true, "holdings_scope_note": true,
	"status": true, "data_source": true,
	"change_status": true, "change_reasons": true, "previous_total_score": true,
	"previous_grade": true, "entry_trigger": true, "exit_reason": true,
	"technical_signals": true, "created_at": true, "updated_at": true,
	"parser_version": true, "score_effective_date": true,
}

func compactAIResearchValue(value any, key string) (any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" || isAIMissingValue(trimmed) {
			return nil, false
		}
		return trimmed, true
	case bool:
		return typed, typed
	case float64:
		return typed, typed != 0
	case map[string]any:
		result := make(map[string]any, len(typed))
		for rawKey, child := range typed {
			childKey := strings.ToLower(strings.TrimSpace(rawKey))
			if aiPromptExcludedFields[childKey] {
				continue
			}
			cleaned, keep := compactAIResearchValue(child, childKey)
			if keep {
				result[rawKey] = cleaned
			}
		}
		return result, len(result) > 0
	case []any:
		result := make([]any, 0, min(len(typed), aiAnalysisMaxArrayItems))
		for _, child := range typed {
			cleaned, keep := compactAIResearchValue(child, key)
			if keep {
				result = append(result, cleaned)
			}
			if len(result) == aiAnalysisMaxArrayItems {
				break
			}
		}
		return result, len(result) > 0
	default:
		return typed, true
	}
}

func isAIMissingValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "missing" || value == "unavailable" || value == "not_applicable" || value == "insufficient" || value == "unknown" || value == "n/a" || value == "-" || strings.HasPrefix(value, "0001-01-01t00:00:00")
}

func (s *AIAnalysisService) callOpenAICompatible(ctx context.Context, provider AIProviderConfig, snapshot string) (string, error) {
	systemPrompt, userPrompt := aiAnalysisPrompts(snapshot)
	result, err := s.callStructuredAIAnalysis(ctx, provider, systemPrompt, userPrompt)
	return result.Content, err
}

func (s *AIAnalysisService) callOpenAICompatibleWithPrompts(ctx context.Context, provider AIProviderConfig, systemPrompt, userPrompt string) (string, error) {
	result, err := s.callStructuredAIAnalysis(ctx, provider, systemPrompt, userPrompt)
	return result.Content, err
}

func isDeepSeekProvider(provider AIProviderConfig) bool {
	return strings.Contains(strings.ToLower(provider.APIBaseURL), "deepseek.com") || strings.HasPrefix(strings.ToLower(provider.Model), "deepseek-v4")
}

func aiProviderTimeout(provider AIProviderConfig) time.Duration {
	if provider.TimeoutSeconds >= 30 && provider.TimeoutSeconds <= 300 {
		return time.Duration(provider.TimeoutSeconds) * time.Second
	}
	return aiAnalysisDefaultTimeout
}

func explainAIProviderError(err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("AI 提供商在 %d 秒内未完成响应；可在系统配置 → AI 分析中提高该模型的请求超时后手动重试: %w", int(timeout.Seconds()), err)
	}
	return err
}
