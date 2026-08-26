package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

const (
	aiAnalysisMaxProviderAttempts = 3
	aiAnalysisMaxEvidenceItems    = 6
)

const aiAnalysisStructuredSystemPrompt = `你必须清楚区分事实、推断和数据缺口。只能输出一个 JSON 对象，不得输出 Markdown、代码围栏或 JSON 之外的文字。输出必须符合 ai-research-v1：
{
  "schema_version":"ai-research-v1",
  "stance":"focus|watch|avoid|insufficient_evidence",
  "conclusion":"简明结论",
  "evidence":[{"fact":"研究包中的事实","inference":"基于该事实的推断","impact":"为何重要","source_paths":["$.研究包字段路径"]}],
  "counter_evidence":[{"fact":"反面事实","inference":"反面推断","impact":"可能影响","source_paths":["$.研究包字段路径"]}],
  "invalidation_conditions":["会推翻当前判断的条件"],
  "catalysts":["有事实依据的观察点"],
  "data_gaps":["会实质改变判断的缺失数据"],
  "risk_notes":["主要风险"],
  "evidence_sufficiency":"high|medium|low"
}
不得把 evidence_sufficiency 解释为上涨概率；没有足够证据时使用 insufficient_evidence。`

type aiAnalysisCallResult struct {
	Structured        model.AIAnalysisStructuredResult
	ResultJSON        string
	Content           string
	Attempts          int
	ResponseMode      string
	ValidationWarning string
	ReusedFromID      *uint
}

type aiCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiProviderHTTPError struct {
	StatusCode int
	Body       string
}

type aiProviderProtocolError struct{ Message string }

func (e *aiProviderProtocolError) Error() string { return e.Message }

func (e *aiProviderHTTPError) Error() string {
	return fmt.Sprintf("AI provider returned HTTP %d: %s", e.StatusCode, SanitizeSensitiveError(strings.TrimSpace(e.Body)))
}

func structuredAIAnalysisSystemPrompt() string { return aiAnalysisStructuredSystemPrompt }

func aiAnalysisKey(record model.AIAnalysis) string {
	encoded, _ := json.Marshal(struct {
		Scope, SourceID, ProviderID, Model, PromptVersion, SystemPrompt, UserPrompt, InputSHA256, SchemaVersion string
	}{record.Scope, record.SourceID, record.ProviderID, record.Model, record.PromptVersion, record.SystemPrompt, record.UserPrompt, record.InputSHA256, model.AIAnalysisSchemaV1})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func parseAIAnalysisStructuredResult(content string) (model.AIAnalysisStructuredResult, string, error) {
	raw := extractAIJSONObject(content)
	if raw == "" {
		return model.AIAnalysisStructuredResult{}, "", errors.New("AI 结果不是 JSON 对象")
	}
	var wire struct {
		SchemaVersion       string          `json:"schema_version"`
		Stance              string          `json:"stance"`
		Conclusion          string          `json:"conclusion"`
		Evidence            json.RawMessage `json:"evidence"`
		CounterEvidence     json.RawMessage `json:"counter_evidence"`
		Invalidation        json.RawMessage `json:"invalidation_conditions"`
		Catalysts           json.RawMessage `json:"catalysts"`
		DataGaps            json.RawMessage `json:"data_gaps"`
		RiskNotes           json.RawMessage `json:"risk_notes"`
		EvidenceSufficiency string          `json:"evidence_sufficiency"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return model.AIAnalysisStructuredResult{}, "", fmt.Errorf("解析 AI 结构结果: %w", err)
	}
	evidence, err := decodeAIAnalysisEvidenceList(wire.Evidence, "evidence")
	if err != nil {
		return model.AIAnalysisStructuredResult{}, "", fmt.Errorf("解析 AI 结构结果: %w", err)
	}
	counterEvidence, err := decodeAIAnalysisEvidenceList(wire.CounterEvidence, "counter_evidence")
	if err != nil {
		return model.AIAnalysisStructuredResult{}, "", fmt.Errorf("解析 AI 结构结果: %w", err)
	}
	stringLists := make(map[string][]string, 4)
	for field, value := range map[string]json.RawMessage{
		"invalidation_conditions": wire.Invalidation,
		"catalysts":               wire.Catalysts,
		"data_gaps":               wire.DataGaps,
		"risk_notes":              wire.RiskNotes,
	} {
		items, err := decodeAIAnalysisStringList(value, field)
		if err != nil {
			return model.AIAnalysisStructuredResult{}, "", fmt.Errorf("解析 AI 结构结果: %w", err)
		}
		stringLists[field] = items
	}
	result := model.AIAnalysisStructuredResult{
		SchemaVersion:       wire.SchemaVersion,
		Stance:              wire.Stance,
		Conclusion:          wire.Conclusion,
		Evidence:            evidence,
		CounterEvidence:     counterEvidence,
		Invalidation:        stringLists["invalidation_conditions"],
		Catalysts:           stringLists["catalysts"],
		DataGaps:            stringLists["data_gaps"],
		RiskNotes:           stringLists["risk_notes"],
		EvidenceSufficiency: wire.EvidenceSufficiency,
	}
	if err := validateAIAnalysisStructuredResult(result); err != nil {
		return result, "", err
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return result, "", err
	}
	return result, string(canonical), nil
}

// decodeAIAnalysisEvidenceList tolerates only harmless source-path shape
// differences emitted by OpenAI-compatible providers. It never invents a
// missing fact, inference or impact, so semantic validation remains strict.
func decodeAIAnalysisEvidenceList(raw json.RawMessage, field string) ([]model.AIAnalysisEvidence, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, fmt.Errorf("字段 %s 不是有效数组: %w", field, err)
	}
	result := make([]model.AIAnalysisEvidence, 0, len(rows))
	for index, row := range rows {
		var wire struct {
			Fact             string          `json:"fact"`
			Inference        string          `json:"inference"`
			Impact           string          `json:"impact"`
			SourcePaths      json.RawMessage `json:"source_paths"`
			SourcePath       json.RawMessage `json:"source_path"`
			SourcePathsCamel json.RawMessage `json:"sourcePaths"`
		}
		decoder := json.NewDecoder(bytes.NewReader(row))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&wire); err != nil {
			return nil, fmt.Errorf("字段 %s 第 %d 项无效: %w", field, index+1, err)
		}
		pathRaw := wire.SourcePaths
		if len(bytes.TrimSpace(pathRaw)) == 0 {
			pathRaw = wire.SourcePath
		}
		if len(bytes.TrimSpace(pathRaw)) == 0 {
			pathRaw = wire.SourcePathsCamel
		}
		paths, err := decodeAIAnalysisSourcePaths(pathRaw)
		if err != nil {
			return nil, fmt.Errorf("字段 %s 第 %d 项的事实包路径无效: %w", field, index+1, err)
		}
		result = append(result, model.AIAnalysisEvidence{
			Fact: strings.TrimSpace(wire.Fact), Inference: strings.TrimSpace(wire.Inference), Impact: strings.TrimSpace(wire.Impact), SourcePaths: paths,
		})
	}
	return result, nil
}

func decodeAIAnalysisSourcePaths(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err == nil {
		single = strings.TrimSpace(single)
		if single == "" {
			return nil, errors.New("路径不能为空")
		}
		return []string{single}, nil
	}
	var paths []string
	if err := json.Unmarshal(trimmed, &paths); err != nil {
		return nil, errors.New("路径必须是字符串或字符串数组")
	}
	for index := range paths {
		paths[index] = strings.TrimSpace(paths[index])
		if paths[index] == "" {
			return nil, errors.New("路径不能为空")
		}
	}
	return paths, nil
}

// decodeAIAnalysisStringList accepts the documented string-array shape and a
// narrow compatibility shape used by some OpenAI-compatible models: a single
// string/object or an array containing strings and objects. Objects are only
// accepted when they contain a recognised textual field; arbitrary JSON is
// never stringified into a seemingly valid research result.
func decodeAIAnalysisStringList(raw json.RawMessage, field string) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	values := []json.RawMessage{trimmed}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return nil, fmt.Errorf("字段 %s 不是有效数组: %w", field, err)
		}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			text = strings.TrimSpace(text)
			if text == "" {
				return nil, fmt.Errorf("字段 %s 包含空白条目", field)
			}
			result = append(result, text)
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil || object == nil {
			return nil, fmt.Errorf("字段 %s 只能包含字符串或可识别的文本对象", field)
		}
		text, err := normalizeAIAnalysisStringObject(field, object)
		if err != nil {
			return nil, err
		}
		result = append(result, text)
	}
	return result, nil
}

func normalizeAIAnalysisStringObject(field string, object map[string]json.RawMessage) (string, error) {
	primaryKeys := map[string][]string{
		"invalidation_conditions": {"condition", "invalidation_condition", "trigger", "title", "description", "content", "text"},
		"catalysts":               {"catalyst", "event", "observation", "title", "description", "content", "text"},
		"data_gaps":               {"gap", "data_gap", "missing_data", "title", "description", "content", "text"},
		"risk_notes":              {"risk", "risk_note", "title", "description", "content", "text"},
	}[field]
	parts := make([]string, 0, 4)
	seen := map[string]struct{}{}
	appendText := func(key, prefix string) {
		raw, ok := object[key]
		if !ok {
			return
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		parts = append(parts, prefix+value)
	}
	for _, key := range primaryKeys {
		appendText(key, "")
	}
	primaryCount := len(parts)
	for _, item := range []struct{ key, prefix string }{
		{"timing", "时间："}, {"timeframe", "时间："}, {"date", "日期："}, {"impact", "影响："},
	} {
		appendText(item.key, item.prefix)
	}
	if primaryCount == 0 {
		return "", fmt.Errorf("字段 %s 的对象缺少可识别文本字段", field)
	}
	return strings.Join(parts, "；"), nil
}

func extractAIJSONObject(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```JSON")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return ""
	}
	return strings.TrimSpace(content[start : end+1])
}

func validateAIAnalysisStructuredResult(result model.AIAnalysisStructuredResult) error {
	if result.SchemaVersion != model.AIAnalysisSchemaV1 {
		return fmt.Errorf("AI 结果 schema_version=%q 不受支持", result.SchemaVersion)
	}
	if !oneOf(result.Stance, "focus", "watch", "avoid", "insufficient_evidence") || !oneOf(result.EvidenceSufficiency, "high", "medium", "low") {
		return errors.New("AI 结果包含无效的研究倾向或证据充分程度")
	}
	if strings.TrimSpace(result.Conclusion) == "" || len(result.Conclusion) > 4_000 {
		return errors.New("AI 结果缺少有效结论")
	}
	if len(result.Evidence) > aiAnalysisMaxEvidenceItems || len(result.CounterEvidence) > aiAnalysisMaxEvidenceItems {
		return errors.New("AI 结果的证据条目过多")
	}
	if result.Stance != "insufficient_evidence" && len(result.Evidence) == 0 {
		return errors.New("AI 结果缺少支持当前判断的证据")
	}
	for _, group := range [][]model.AIAnalysisEvidence{result.Evidence, result.CounterEvidence} {
		for _, item := range group {
			if strings.TrimSpace(item.Fact) == "" || strings.TrimSpace(item.Inference) == "" || strings.TrimSpace(item.Impact) == "" || len(item.SourcePaths) == 0 || len(item.SourcePaths) > 6 {
				return errors.New("AI 证据必须包含事实、推断、影响和事实包路径")
			}
			for _, path := range item.SourcePaths {
				if !strings.HasPrefix(strings.TrimSpace(path), "$") {
					return errors.New("AI 证据路径必须是以 $ 开头的事实包路径")
				}
			}
		}
	}
	for _, values := range [][]string{result.Invalidation, result.Catalysts, result.DataGaps, result.RiskNotes} {
		if len(values) > aiAnalysisMaxEvidenceItems {
			return errors.New("AI 结果的观察条目过多")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > 2_000 {
				return errors.New("AI 结果包含空白或过长的观察条目")
			}
		}
	}
	return nil
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func renderAIAnalysisStructuredMarkdown(result model.AIAnalysisStructuredResult) string {
	stance := map[string]string{"focus": "关注", "watch": "观望", "avoid": "回避", "insufficient_evidence": "证据不足"}[result.Stance]
	sufficiency := map[string]string{"high": "高", "medium": "中", "low": "低"}[result.EvidenceSufficiency]
	var out strings.Builder
	fmt.Fprintf(&out, "## 研究结论\n\n**%s** · 证据充分程度：%s\n\n%s\n", stance, sufficiency, result.Conclusion)
	renderEvidenceMarkdown(&out, "关键证据与推断", result.Evidence)
	renderEvidenceMarkdown(&out, "反面证据", result.CounterEvidence)
	renderStringListMarkdown(&out, "判断失效条件", result.Invalidation)
	renderStringListMarkdown(&out, "近期催化剂与观察点", result.Catalysts)
	renderStringListMarkdown(&out, "主要风险", result.RiskNotes)
	renderStringListMarkdown(&out, "数据缺口与下一步验证", result.DataGaps)
	return strings.TrimSpace(out.String())
}

func renderEvidenceMarkdown(out *strings.Builder, title string, rows []model.AIAnalysisEvidence) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(out, "\n\n## %s\n", title)
	for _, row := range rows {
		fmt.Fprintf(out, "\n- **事实：** %s\n  - **推断：** %s\n  - **影响：** %s\n  - **依据：** `%s`\n", row.Fact, row.Inference, row.Impact, strings.Join(row.SourcePaths, "`, `"))
	}
}

func renderStringListMarkdown(out *strings.Builder, title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "\n\n## %s\n", title)
	for _, value := range values {
		fmt.Fprintf(out, "\n- %s", value)
	}
}

func (s *AIAnalysisService) callStructuredAIAnalysis(ctx context.Context, provider AIProviderConfig, systemPrompt, userPrompt string) (aiAnalysisCallResult, error) {
	messages := []aiCompletionMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}
	raw, attempts, mode, err := s.requestAICompletionWithRetry(ctx, provider, messages, true)
	if err != nil {
		return aiAnalysisCallResult{Attempts: attempts, ResponseMode: mode}, err
	}
	result, canonical, parseErr := parseAIAnalysisStructuredResult(raw)
	if parseErr != nil {
		repairPrompt := buildAIAnalysisRepairPrompt(parseErr)
		repairMessages := append(append([]aiCompletionMessage(nil), messages...),
			aiCompletionMessage{Role: "assistant", Content: raw},
			aiCompletionMessage{Role: "user", Content: repairPrompt})
		// Structural repair is deliberately a single provider call. The original
		// request already consumed its retry budget; an invalid payload must not
		// open a second, unbounded retry cycle.
		repairStructured := mode != "prompt_json"
		repaired, repairAttempts, repairMode, repairErr := s.requestAICompletionWithRetryLimit(ctx, provider, repairMessages, repairStructured, 1)
		attempts += repairAttempts
		if repairMode != "" {
			mode = repairMode
		}
		if repairErr != nil {
			return aiAnalysisCallResult{Attempts: attempts, ResponseMode: mode}, errors.Join(parseErr, fmt.Errorf("修复 AI 结构结果: %w", repairErr))
		}
		result, canonical, parseErr = parseAIAnalysisStructuredResult(repaired)
		if parseErr != nil {
			warning := SanitizeSensitiveError(fmt.Sprintf("AI 结构结果在一次修复后仍无效: %v", parseErr))
			result = safeAIAnalysisFallback()
			canonicalBytes, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return aiAnalysisCallResult{Attempts: attempts, ResponseMode: mode}, marshalErr
			}
			return aiAnalysisCallResult{
				Structured: result, ResultJSON: string(canonicalBytes), Content: renderAIAnalysisStructuredMarkdown(result),
				Attempts: attempts, ResponseMode: "validation_fallback", ValidationWarning: warning,
			}, nil
		}
	}
	return aiAnalysisCallResult{Structured: result, ResultJSON: canonical, Content: renderAIAnalysisStructuredMarkdown(result), Attempts: attempts, ResponseMode: mode}, nil
}

func buildAIAnalysisRepairPrompt(validationErr error) string {
	return fmt.Sprintf(`上一个回答未通过结构校验：%s
只修复格式和字段完整性，不得新增研究包中不存在的事实。请重新输出符合 ai-research-v1 的单个 JSON 对象。
每个 evidence 和 counter_evidence 条目必须同时包含非空 fact、inference、impact，以及 source_paths 字符串数组；每条路径必须以 $ 开头。
若无法提供可回溯的完整证据，请使用 stance="insufficient_evidence"、evidence=[]、counter_evidence=[]、evidence_sufficiency="low"，并在 data_gaps 中说明缺口。`, SanitizeSensitiveError(validationErr.Error()))
}

func safeAIAnalysisFallback() model.AIAnalysisStructuredResult {
	return model.AIAnalysisStructuredResult{
		SchemaVersion: model.AIAnalysisSchemaV1,
		Stance:        "insufficient_evidence",
		Conclusion:    "模型未返回可验证的完整结构化证据，本次不形成研究判断。",
		Evidence:      []model.AIAnalysisEvidence{}, CounterEvidence: []model.AIAnalysisEvidence{},
		Invalidation: []string{}, Catalysts: []string{},
		DataGaps:            []string{"模型输出未通过结构校验，需重新生成包含事实包路径的完整证据后再判断。"},
		RiskNotes:           []string{"本次结果为系统安全降级，不应作为研究结论或交易依据。"},
		EvidenceSufficiency: "low",
	}
}

func (s *AIAnalysisService) requestAICompletionWithRetry(ctx context.Context, provider AIProviderConfig, messages []aiCompletionMessage, structured bool) (string, int, string, error) {
	return s.requestAICompletionWithRetryLimit(ctx, provider, messages, structured, aiAnalysisMaxProviderAttempts)
}

func (s *AIAnalysisService) requestAICompletionWithRetryLimit(ctx context.Context, provider AIProviderConfig, messages []aiCompletionMessage, structured bool, maxAttempts int) (string, int, string, error) {
	mode := "json_object"
	if !structured {
		mode = "prompt_json"
	}
	attempts := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts++
		content, response, err := s.requestAICompletion(ctx, provider, messages, structured)
		if err == nil {
			return content, attempts, mode, nil
		}
		var providerErr *aiProviderHTTPError
		if structured && errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(providerErr.Body), "response_format") {
			structured = false
			mode = "prompt_json"
			if attempt == maxAttempts {
				return "", attempts, mode, err
			}
			continue
		}
		if attempt == maxAttempts || !retryableAIProviderError(err) {
			return "", attempts, mode, err
		}
		delay := time.Duration(attempt) * 500 * time.Millisecond
		if response != nil {
			if retryAfter := parseAIRetryAfter(response.Header.Get("Retry-After")); retryAfter > delay {
				delay = retryAfter
			}
		}
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		if err := s.waitForAIRetry(ctx, delay); err != nil {
			return "", attempts, mode, err
		}
	}
	return "", attempts, mode, errors.New("AI provider retry budget exhausted")
}

func (s *AIAnalysisService) requestAICompletion(ctx context.Context, provider AIProviderConfig, messages []aiCompletionMessage, structured bool) (string, *http.Response, error) {
	requestPayload := map[string]any{"model": provider.Model, "messages": messages, "temperature": 0.2, "max_tokens": aiAnalysisMaxOutputTokens, "stream": false}
	if structured {
		requestPayload["response_format"] = map[string]string{"type": "json_object"}
	}
	if isDeepSeekProvider(provider) {
		requestPayload["thinking"] = map[string]string{"type": "disabled"}
	}
	body, _ := json.Marshal(requestPayload)
	endpoint := strings.TrimRight(provider.APIBaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	client := *s.httpClient
	timeout := aiProviderTimeout(provider)
	client.Timeout = timeout
	response, err := client.Do(req)
	if err != nil {
		return "", response, explainAIProviderError(err, timeout)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", response, explainAIProviderError(err, timeout)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", response, &aiProviderHTTPError{StatusCode: response.StatusCode, Body: string(payload)}
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", response, &aiProviderProtocolError{Message: fmt.Sprintf("parse AI response: %v", err)}
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", response, &aiProviderProtocolError{Message: "AI 提供商未返回可用的结构化分析结果"}
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), response, nil
}

func retryableAIProviderError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var providerErr *aiProviderHTTPError
	if errors.As(err, &providerErr) {
		return providerErr.StatusCode == http.StatusRequestTimeout || providerErr.StatusCode == http.StatusTooManyRequests || providerErr.StatusCode >= 500
	}
	var protocolErr *aiProviderProtocolError
	if errors.As(err, &protocolErr) {
		return false
	}
	return true
}

func parseAIRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		if delay := time.Until(date); delay > 0 {
			return delay
		}
	}
	return 0
}

func (s *AIAnalysisService) waitForAIRetry(ctx context.Context, delay time.Duration) error {
	if s.aiRetryWait != nil {
		return s.aiRetryWait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func hydrateAIAnalysisStructuredResult(record *model.AIAnalysis) {
	if record == nil || strings.TrimSpace(record.ResultJSON) == "" {
		return
	}
	result, _, err := parseAIAnalysisStructuredResult(record.ResultJSON)
	if err == nil {
		record.StructuredResult = &result
	}
}

func (s *AIAnalysisService) reusableAIAnalysis(ctx context.Context, record model.AIAnalysis) (model.AIAnalysis, bool, error) {
	if strings.TrimSpace(record.AnalysisKeySHA256) == "" {
		return model.AIAnalysis{}, false, nil
	}
	var reusable model.AIAnalysis
	err := s.db.WithContext(ctx).
		Where("id <> ? AND analysis_key_sha256 = ? AND schema_version = ? AND status = ? AND result_json <> '' AND response_mode <> ?", record.ID, record.AnalysisKeySHA256, model.AIAnalysisSchemaV1, "success", "validation_fallback").
		Order("completed_at DESC, id DESC").First(&reusable).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.AIAnalysis{}, false, nil
	}
	if err != nil {
		return model.AIAnalysis{}, false, err
	}
	result, canonical, err := parseAIAnalysisStructuredResult(reusable.ResultJSON)
	if err != nil {
		return model.AIAnalysis{}, false, nil
	}
	reusable.ResultJSON = canonical
	reusable.StructuredResult = &result
	return reusable, true, nil
}
