package service

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
)

type FutuCredentialsInput struct {
	Mode       string `json:"mode"`
	AppKey     string `json:"app_key"`
	PrivateKey string `json:"private_key"`
	Algorithm  string `json:"algorithm"`
	Clear      bool   `json:"clear"`
}
type FutuCredentialsView struct {
	Mode                 string `json:"mode"`
	Algorithm            string `json:"algorithm"`
	AppKeyConfigured     bool   `json:"app_key_configured"`
	PrivateKeyConfigured bool   `json:"private_key_configured"`
	OAuthConfigured      bool   `json:"oauth_configured"`
}

func parseFutuSigningKey(raw, algorithm string) (crypto.Signer, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("私钥必须是单个未加密 PEM（PKCS8 或 RSA PKCS1）")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, errors.New("无法解析私钥，请使用未加密的 PEM 私钥")
	}
	switch k := key.(type) {
	case ed25519.PrivateKey:
		if algorithm == "Ed25519" {
			return k, nil
		}
	case *rsa.PrivateKey:
		if algorithm == "RSA-SHA256" && k.N.BitLen() >= 2048 && k.Validate() == nil {
			return k, nil
		}
	}
	return nil, errors.New("签名算法与私钥不匹配；RSA 至少需要 2048 位")
}
func (s *FutuAPIService) Credentials(ctx context.Context) (FutuCredentialsView, error) {
	v := FutuCredentialsView{Algorithm: "Ed25519"}
	for key, target := range map[string]*string{"futu.auth_mode": &v.Mode, "futu.sign_algorithm": &v.Algorithm} {
		value, _, err := s.configs.GetValue(ctx, key)
		if err != nil {
			return v, err
		}
		if value != "" {
			*target = value
		}
	}
	key, _, err := s.configs.GetValue(ctx, "futu.app_key")
	if err != nil {
		return v, err
	}
	v.AppKeyConfigured = key != ""
	secret, _, err := s.configs.GetValue(ctx, "futu.private_key")
	if err != nil {
		return v, err
	}
	v.PrivateKeyConfigured = secret != ""
	token, _, err := s.configs.GetValue(ctx, "futu.access_token")
	if err != nil {
		return v, err
	}
	scope, _, err := s.configs.GetValue(ctx, "futu.scope")
	v.OAuthConfigured = token != "" && validateFutuScope(scope)
	if v.Mode == "" {
		v.Mode = "api_key"
		if v.OAuthConfigured {
			v.Mode = "oauth"
		}
	}
	return v, err
}

// Saving credentials is local-only and never enables a provider or task.
func (s *FutuAPIService) SaveCredentials(ctx context.Context, in FutuCredentialsInput) error {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if in.Mode != "oauth" && in.Mode != "api_key" {
		return fmt.Errorf("%w: 认证方式无效", ErrValidation)
	}
	if in.Clear {
		in.Mode = "api_key"
		in.AppKey = ""
		in.PrivateKey = ""
	} else {
		if strings.TrimSpace(in.AppKey) == "" {
			var err error
			in.AppKey, _, err = s.configs.GetValue(ctx, "futu.app_key")
			if err != nil {
				return err
			}
		}
		if strings.TrimSpace(in.PrivateKey) == "" {
			var err error
			in.PrivateKey, _, err = s.configs.GetValue(ctx, "futu.private_key")
			if err != nil {
				return err
			}
		}
	}
	in.AppKey = strings.TrimSpace(in.AppKey)
	in.PrivateKey = strings.TrimSpace(in.PrivateKey)
	if in.Algorithm == "" {
		in.Algorithm = "Ed25519"
	}
	if in.Algorithm != "Ed25519" && in.Algorithm != "RSA-SHA256" {
		return fmt.Errorf("%w: 签名算法无效", ErrValidation)
	}
	if in.AppKey != "" || in.PrivateKey != "" {
		if len(in.AppKey) > 256 || strings.ContainsAny(in.AppKey, "\r\n\t ") || in.AppKey == "" || in.PrivateKey == "" || len(in.PrivateKey) > 64<<10 {
			return fmt.Errorf("%w: 请同时配置 AppKey 和签名私钥", ErrValidation)
		}
		if _, err := parseFutuSigningKey(in.PrivateKey, in.Algorithm); err != nil {
			return fmt.Errorf("%w: %s", ErrValidation, err)
		}
		if s.configs.EncryptionHealth().Status != "ok" {
			return fmt.Errorf("%w: 请先配置配置加密密钥", ErrValidation)
		}
	}
	inputs := []ConfigInput{}
	if in.Clear {
		if err := s.db.WithContext(ctx).Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Update("paused", true).Error; err != nil {
			return err
		}
	}
	for key, value := range map[string]string{"futu.auth_mode": in.Mode, "futu.sign_algorithm": in.Algorithm, "futu.app_key": in.AppKey, "futu.private_key": in.PrivateKey} {
		inputs = append(inputs, ConfigInput{Key: key, Value: value, Category: "futu", ValueType: "string"})
	}
	return s.configs.UpsertMany(ctx, inputs, "futu_credentials")
}
func (s *FutuAPIService) authorizeRequest(ctx context.Context, req *http.Request, body []byte) error {
	mode, _, err := s.configs.GetValue(ctx, "futu.auth_mode")
	if err != nil {
		return err
	}
	if mode != "api_key" {
		token, err := s.token(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	appKey, _, err := s.configs.GetValue(ctx, "futu.app_key")
	if err != nil {
		return err
	}
	raw, _, err := s.configs.GetValue(ctx, "futu.private_key")
	if err != nil {
		return err
	}
	algorithm, _, err := s.configs.GetValue(ctx, "futu.sign_algorithm")
	if err != nil {
		return err
	}
	if appKey == "" || raw == "" {
		return errors.New("富途未配置 AppKey / 签名私钥；可保持不配置")
	}
	key, err := parseFutuSigningKey(raw, algorithm)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce, err := randomOAuthString()
	if err != nil {
		return err
	}
	hashBody := ""
	if len(body) > 0 {
		h := sha256.Sum256(body)
		hashBody = hex.EncodeToString(h[:])
	}
	payload := []byte(strings.Join([]string{timestamp, req.Method, req.URL.EscapedPath(), req.URL.RawQuery, hashBody}, "\n"))
	var signature []byte
	if algorithm == "Ed25519" {
		signature, err = key.Sign(rand.Reader, payload, crypto.Hash(0))
	} else {
		hash := sha256.Sum256(payload)
		signature, err = key.Sign(rand.Reader, hash[:], crypto.SHA256)
	}
	if err != nil {
		return errors.New("富途请求签名失败")
	}
	req.Header.Set("Authorization", base64.StdEncoding.EncodeToString(signature))
	req.Header.Set("X-Api-Key", appKey)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	return nil
}

// A code-owned quote-only allowlist protects against accidental trading calls.
func futuReadPathAllowed(path string) bool {
	if !strings.HasPrefix(path, "/api/v1.0/quote/") {
		return false
	}
	if path == "/api/v1.0/quote/trading-days" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1.0/quote/"), "/")
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "US.") || (!futuTickerPattern.MatchString(strings.TrimPrefix(parts[0], "US.")) && !isUSFuturesSymbol(parts[0])) {
		return false
	}
	switch strings.Join(parts[1:], "/") {
	case "reference-future":
		return isUSFuturesSymbol(parts[0])
	case "history-kline", "company/profile", "research/analyst-consensus", "shareholders/institutional":
		return true
	}
	return false
}

var ErrFutuRateLimited = errors.New("Futu HTTP rate limited")

func isUSFuturesSymbol(symbol string) bool {
	for _, def := range usFuturesDefinitions {
		if def.Symbol == symbol {
			return true
		}
	}
	return false
}
func (s *FutuAPIService) ReadJSON(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
	if method != http.MethodGet || len(body) > 0 || !futuReadPathAllowed(path) {
		return errors.New("仅允许已接入的富途只读行情接口")
	}
	if parts := strings.Split(path, "/"); len(parts) > 4 && isUSFuturesSymbol(parts[4]) {
		ctx = discovery.WithFutuFutures(ctx)
	}
	endpoint := path
	parts := strings.Split(path, "/")
	if len(parts) > 4 && strings.HasPrefix(parts[4], "US.") {
		parts[4] = "{symbol}"
		endpoint = strings.Join(parts, "/")
	}
	if err := discovery.CheckAPIModuleEndpoint(ctx, s.db, "futu", endpoint); err != nil {
		return err
	}
	var policy discovery.APIProviderPolicy
	if err := s.db.WithContext(ctx).First(&policy, "provider = ?", "futu").Error; err != nil {
		return err
	}
	if policy.Paused {
		return errors.New("Futu 已暂停，请先显式启用")
	}
	req, err := http.NewRequestWithContext(ctx, method, s.host+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if err = s.authorizeRequest(ctx, req, body); err != nil {
		return err
	}
	resp, err := s.dataClient.Do(req)
	if err != nil {
		return fmt.Errorf("Futu 数据请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrFutuRateLimited
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Futu data HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(payload) > 4<<20 {
		return errors.New("Futu 响应超出限制或读取失败")
	}
	var envelope struct {
		RetCode *int            `json:"ret_code"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.RetCode == nil {
		return errors.New("Futu 响应缺少有效状态码")
	}
	if *envelope.RetCode != 0 {
		return fmt.Errorf("Futu API ret_code %d", *envelope.RetCode)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("Futu 响应缺少数据对象，未保存记录")
	}
	if json.Unmarshal(payload, out) != nil {
		return errors.New("Futu 数据格式无法识别")
	}
	return nil
}
