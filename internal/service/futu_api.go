package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sec_monitor/internal/discovery"
)

const futuAPIHost = "https://webapi.futunn.com"
const futuCallback = "http://127.0.0.1:9090/api/providers/futu/oauth/callback"
const futuInstitutionalDocs = "https://open.futunn.com/api/quote/shareholders/institutional"

// Multiple sync services share credentials. Serialise refresh-token rotation
// and credential replacement across service instances in this process.
var futuCredentialMu sync.Mutex

type futuOAuthSession struct {
	Verifier, ClientID string
	Expires            time.Time
}
type FutuAPIService struct {
	db         *gorm.DB
	configs    *ConfigService
	monitor    *discovery.APIMonitor
	authClient *http.Client
	dataClient *http.Client
	mu         sync.Mutex
	tokenMu    *sync.Mutex
	sessions   map[string]futuOAuthSession
	running    map[string]bool
	host       string
}

func NewFutuAPIService(db *gorm.DB, configs *ConfigService, monitor *discovery.APIMonitor) *FutuAPIService {
	return &FutuAPIService{db: db, configs: configs, monitor: monitor, host: futuAPIHost, tokenMu: &futuCredentialMu,
		authClient: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect not allowed") }},
		dataClient: monitor.HTTPClient("futu"), sessions: map[string]futuOAuthSession{}, running: map[string]bool{}}
}
func (s *FutuAPIService) Configured(ctx context.Context) (bool, error) {
	view, err := s.Credentials(ctx)
	if err != nil {
		return false, err
	}
	if view.Mode == "api_key" {
		return view.AppKeyConfigured && view.PrivateKeyConfigured, nil
	}
	token, _, err := s.configs.GetValue(ctx, "futu.access_token")
	if err != nil {
		return false, err
	}
	scope, _, err := s.configs.GetValue(ctx, "futu.scope")
	return token != "" && validateFutuScope(scope) && err == nil, err
}

// Connectivity is tested with one read-only calendar request; this does not
// prove that institutional data exists for any particular stock.
func (s *FutuAPIService) Probe(ctx context.Context) (map[string]any, error) {
	var policy discovery.APIProviderPolicy
	if err := s.db.WithContext(ctx).First(&policy, "provider = ?", "futu").Error; err != nil {
		return nil, err
	}
	if policy.Paused {
		return nil, errors.New("Futu 已暂停，请先显式启用")
	}
	day := time.Now().UTC().Format(time.DateOnly)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.host+"/api/v1.0/quote/trading-days?market=US&start="+day+"&end="+day, nil)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeRequest(ctx, req, nil); err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := s.dataClient.Do(req)
	if err != nil {
		return nil, errors.New("Futu 连接测试失败，请检查预算、授权和网络")
	}
	defer resp.Body.Close()
	var envelope struct {
		RetCode *int `json:"ret_code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, errors.New("Futu 连接测试响应格式无法识别")
	}
	if resp.StatusCode != 200 || envelope.RetCode == nil || *envelope.RetCode != 0 {
		return nil, fmt.Errorf("Futu 连接测试未通过：HTTP %d（未验证机构数据覆盖）", resp.StatusCode)
	}
	return map[string]any{"status": "ok", "provider": "futu", "elapsed_millis": time.Since(started).Milliseconds(), "message": "行情日历只读请求通过；机构持仓权限与股票覆盖需另行试查"}, nil
}
func randomOAuthString() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}

// No account/trading scopes are requested or accepted.
func (s *FutuAPIService) BeginAuthorization(ctx context.Context) (string, error) {
	if s.configs.EncryptionHealth().Status != "ok" {
		return "", errors.New("请先配置配置加密密钥，再授权富途")
	}
	clientID, _, err := s.configs.GetValue(ctx, "futu.client_id")
	if err != nil {
		return "", err
	}
	if clientID == "" {
		body, _ := json.Marshal(map[string]any{"redirect_uris": []string{futuCallback}, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "client_name": "SEC Monitor read-only", "scope": "quote:read"})
		var registered struct {
			ClientID string `json:"client_id"`
		}
		if err := s.authJSON(ctx, "/oauth2/register", "application/json", body, &registered); err != nil {
			return "", err
		}
		if registered.ClientID == "" {
			return "", errors.New("Futu OAuth registration returned no client ID")
		}
		clientID = registered.ClientID
		if err := s.configs.UpsertMany(ctx, []ConfigInput{{Key: "futu.client_id", Value: clientID, Category: "futu", ValueType: "string"}}, "futu_oauth"); err != nil {
			return "", err
		}
	}
	state, err := randomOAuthString()
	if err != nil {
		return "", err
	}
	verifier, err := randomOAuthString()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, session := range s.sessions {
		if time.Now().After(session.Expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 10 {
		return "", errors.New("已有过多授权请求，请稍后重试")
	}
	s.sessions[state] = futuOAuthSession{Verifier: verifier, ClientID: clientID, Expires: time.Now().Add(10 * time.Minute)}
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {clientID}, "response_type": {"code"}, "redirect_uri": {futuCallback}, "state": {state}, "scope": {"quote:read"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}}
	return s.host + "/oauth2/authorize/confirm?" + q.Encode(), nil
}

type futuTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func validateFutuScope(scope string) bool {
	fields := strings.Fields(scope)
	return len(fields) == 1 && fields[0] == "quote:read"
}
func (s *FutuAPIService) CompleteAuthorization(ctx context.Context, state, code string) error {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.mu.Lock()
	session, ok := s.sessions[state]
	delete(s.sessions, state)
	s.mu.Unlock()
	if !ok || time.Now().After(session.Expires) || code == "" {
		return errors.New("授权状态无效或过期，请重新开始授权")
	}
	q := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {session.ClientID}, "redirect_uri": {futuCallback}, "code_verifier": {session.Verifier}}
	var token futuTokenResponse
	if err := s.authJSON(ctx, "/oauth2/token", "application/x-www-form-urlencoded", []byte(q.Encode()), &token); err != nil {
		return err
	}
	return s.saveToken(ctx, token, true)
}
func (s *FutuAPIService) saveToken(ctx context.Context, t futuTokenResponse, requireRefresh bool) error {
	if t.AccessToken == "" || t.ExpiresIn <= 0 || t.ExpiresIn > 365*86400 || !validateFutuScope(t.Scope) || (requireRefresh && t.RefreshToken == "") {
		return errors.New("未收到有效的纯行情只读授权，凭据未保存；请仅授权 quote:read")
	}
	values := []ConfigInput{{Key: "futu.access_token", Value: t.AccessToken, Category: "futu", ValueType: "string", Encrypted: true}, {Key: "futu.token_expires_at", Value: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).UTC().Format(time.RFC3339), Category: "futu", ValueType: "string"}, {Key: "futu.scope", Value: t.Scope, Category: "futu", ValueType: "string"}}
	if t.RefreshToken != "" {
		values = append(values, ConfigInput{Key: "futu.refresh_token", Value: t.RefreshToken, Category: "futu", ValueType: "string", Encrypted: true})
	}
	if requireRefresh {
		values = append(values, ConfigInput{Key: "futu.auth_mode", Value: "oauth", Category: "futu", ValueType: "string"})
	}
	return s.configs.UpsertMany(ctx, values, "futu_oauth")
}
func (s *FutuAPIService) authJSON(ctx context.Context, path, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := s.authClient.Do(req)
	if err != nil {
		return errors.New("Futu authorization network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("Futu authorization HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("Futu authorization response could not be decoded")
	}
	return nil
}
func (s *FutuAPIService) token(ctx context.Context) (string, error) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	scope, _, err := s.configs.GetValue(ctx, "futu.scope")
	if err != nil || !validateFutuScope(scope) {
		return "", errors.New("Futu 缺少已验证的纯行情只读权限，请重新授权")
	}
	token, _, err := s.configs.GetValue(ctx, "futu.access_token")
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", errors.New("请先完成 Futu 只读授权")
	}
	expiry, _, err := s.configs.GetValue(ctx, "futu.token_expires_at")
	if err != nil {
		return "", err
	}
	expires, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		return "", errors.New("Futu 授权有效期未知，请重新授权")
	}
	if time.Until(expires) > time.Minute {
		return token, nil
	}
	refresh, _, err := s.configs.GetValue(ctx, "futu.refresh_token")
	if err != nil {
		return "", err
	}
	id, _, err := s.configs.GetValue(ctx, "futu.client_id")
	if err != nil {
		return "", err
	}
	if refresh == "" || id == "" {
		return "", errors.New("Futu 授权已过期，请重新授权")
	}
	q := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {id}}
	var renewed futuTokenResponse
	if err := s.authJSON(ctx, "/oauth2/token", "application/x-www-form-urlencoded", []byte(q.Encode()), &renewed); err != nil {
		return "", err
	}
	// Official refresh responses may omit scope; retain the previously validated scope.
	if renewed.Scope == "" {
		renewed.Scope, _, err = s.configs.GetValue(ctx, "futu.scope")
		if err != nil {
			return "", err
		}
	}
	if err := s.saveToken(ctx, renewed, false); err != nil {
		return "", err
	}
	return renewed.AccessToken, nil
}

type FutuOwnershipRefreshResult struct {
	Ticker string `json:"ticker"`
	Cached bool   `json:"cached"`
	Pages  int    `json:"pages"`
	Points int    `json:"points"`
	Status string `json:"status"`
}

var futuTickerPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]{0,19}$`)
var futuPeriodPattern = regexp.MustCompile(`^[0-9]{4}/Q[1-4]$`)

type futuInstitutionalResponse struct {
	RetCode *int `json:"ret_code"`
	Data    struct {
		Holders []struct {
			Period                    string   `json:"period_text"`
			InstitutionQuantity       *int64   `json:"institution_quantity"`
			InstitutionQuantityChange *int64   `json:"institution_quantity_change"`
			HolderQuantity            *int64   `json:"holder_quantity"`
			HolderQuantityChange      *int64   `json:"holder_quantity_change"`
			HolderPct                 *float64 `json:"holder_pct"`
			HolderPctChange           *float64 `json:"holder_pct_change"`
			UpdateTime                int64    `json:"update_time"`
		} `json:"holders"`
	} `json:"data"`
	Pagination struct {
		HasMore bool   `json:"has_more"`
		NextKey string `json:"next_key"`
	} `json:"pagination"`
}

func (s *FutuAPIService) RefreshOwnership(ctx context.Context, ticker string) (FutuOwnershipRefreshResult, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	result := FutuOwnershipRefreshResult{Ticker: ticker}
	if !futuTickerPattern.MatchString(ticker) {
		return result, ErrValidation
	}
	s.mu.Lock()
	if s.running[ticker] {
		s.mu.Unlock()
		return result, TaskAlreadyRunning("futu_ownership:" + ticker)
	}
	s.running[ticker] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, ticker); s.mu.Unlock() }()
	var receipt discovery.FutuInstitutionalReceipt
	err := s.db.WithContext(ctx).First(&receipt, "ticker = ?", ticker).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err == nil && receipt.NextKey == "" && time.Since(receipt.FetchedAt) >= 0 && time.Since(receipt.FetchedAt) < 24*time.Hour {
		result.Cached = true
		result.Status = receipt.Status
		return result, nil
	}
	var policy discovery.APIProviderPolicy
	if err := s.db.WithContext(ctx).First(&policy, "provider = ?", "futu").Error; err != nil {
		return result, err
	}
	if policy.Paused {
		return result, errors.New("Futu 已暂停，请先在数据源管理中启用")
	}
	if err := discovery.CheckAPIModuleEndpoint(ctx, s.db, "futu", "/api/v1.0/quote/{symbol}/shareholders/institutional"); err != nil {
		return result, err
	}
	cursor := ""
	if receipt.NextKey != "" {
		cursor = receipt.NextKey
	}
	seen := map[string]bool{}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for page := 0; page < 2; page++ {
		q := url.Values{"limit": {"50"}}
		if cursor != "" {
			q.Set("next_key", cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.host+"/api/v1.0/quote/US."+url.PathEscape(ticker)+"/shareholders/institutional?"+q.Encode(), nil)
		if err != nil {
			return result, err
		}
		if err := s.authorizeRequest(ctx, req, nil); err != nil {
			return result, err
		}
		resp, err := s.dataClient.Do(req)
		if err != nil {
			return result, errors.New("Futu 数据请求失败（检查暂停、预算、授权及网络）")
		}
		var payload futuInstitutionalResponse
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload)
		resp.Body.Close()
		result.Pages++
		if resp.StatusCode != http.StatusOK {
			return result, fmt.Errorf("Futu data HTTP %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return result, errors.New("Futu 数据格式无法识别")
		}
		if payload.RetCode == nil {
			return result, errors.New("Futu 响应缺少状态码，未保存记录")
		}
		if *payload.RetCode != 0 && *payload.RetCode != -10 {
			return result, fmt.Errorf("Futu API ret_code %d", *payload.RetCode)
		}
		if *payload.RetCode == -10 {
			payload.Data.Holders = nil
		}
		rows := []discovery.FutuInstitutionalPoint{}
		if len(payload.Data.Holders) > 50 {
			return result, errors.New("Futu 返回超出单页上限的记录，未保存")
		}
		for _, item := range payload.Data.Holders {
			if !futuPeriodPattern.MatchString(item.Period) {
				return result, errors.New("Futu 报告期无法识别，未混入其他日期口径")
			}
			for _, value := range []*float64{item.HolderPct, item.HolderPctChange} {
				if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
					return result, errors.New("Futu 返回无效比例")
				}
			}
			if (item.HolderPct != nil && *item.HolderPct < 0) || (item.HolderQuantity != nil && *item.HolderQuantity < 0) || (item.InstitutionQuantity != nil && *item.InstitutionQuantity < 0) {
				return result, errors.New("Futu 返回无效持仓数量或比例")
			}
			row := discovery.FutuInstitutionalPoint{Ticker: ticker, Period: item.Period, InstitutionQuantity: item.InstitutionQuantity, InstitutionQuantityChange: item.InstitutionQuantityChange, HolderQuantity: item.HolderQuantity, HolderQuantityChange: item.HolderQuantityChange, HolderPct: item.HolderPct, HolderPctChange: item.HolderPctChange, FetchedAt: time.Now().UTC(), SourceURL: futuInstitutionalDocs}
			if item.UpdateTime > 0 {
				updated := time.UnixMilli(item.UpdateTime).UTC()
				row.ProviderUpdatedAt = &updated
			}
			rows = append(rows, row)
		}
		next := ""
		if payload.Pagination.HasMore && *payload.RetCode == 0 {
			next = payload.Pagination.NextKey
			if next == "" || next == "-1" || len(next) > 1024 || seen[next] || next == cursor {
				return result, errors.New("Futu 分页游标无效，已停止续查")
			}
			seen[next] = true
		}
		status := "available"
		if len(rows) == 0 {
			status = "no_coverage"
			if cursor != "" {
				var previous int64
				if err := s.db.WithContext(ctx).Model(&discovery.FutuInstitutionalPoint{}).Where("ticker = ?", ticker).Count(&previous).Error; err != nil {
					return result, err
				}
				if previous > 0 {
					status = "available"
				}
			}
		}
		if next != "" {
			status = "partial"
		}
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if len(rows) > 0 {
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticker"}, {Name: "period"}}, DoUpdates: clause.AssignmentColumns([]string{"institution_quantity", "institution_quantity_change", "holder_quantity", "holder_quantity_change", "holder_pct", "holder_pct_change", "provider_updated_at", "fetched_at", "source_url"})}).Create(&rows).Error; err != nil {
					return err
				}
			}
			return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&discovery.FutuInstitutionalReceipt{Ticker: ticker, Status: status, NextKey: next, FetchedAt: time.Now().UTC()}).Error
		}); err != nil {
			return result, err
		}
		result.Points += len(rows)
		result.Status = status
		cursor = next
		if next == "" {
			break
		}
	}
	return result, nil
}

// Disconnect keeps cached research, clears both authentication methods and pauses requests.
func (s *FutuAPIService) Disconnect(ctx context.Context) error {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.mu.Lock()
	s.sessions = map[string]futuOAuthSession{}
	s.mu.Unlock()
	if err := s.db.WithContext(ctx).Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Update("paused", true).Error; err != nil {
		return err
	}
	values := []ConfigInput{}
	for _, key := range []string{"futu.access_token", "futu.refresh_token", "futu.scope", "futu.token_expires_at", "futu.app_key", "futu.private_key"} {
		values = append(values, ConfigInput{Key: key, Value: "", Category: "futu", ValueType: "string", Encrypted: strings.Contains(key, "token") && !strings.Contains(key, "expires")})
	}
	return s.configs.UpsertMany(ctx, values, "futu_disconnect")
}
