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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"strings"
	"testing"
)

func TestFutuManualCredentialsOptionalEncryptedAndSigning(t *testing.T) {
	for _, algorithm := range []string{"Ed25519", "RSA-SHA256"} {
		t.Run(algorithm, func(t *testing.T) {
			s := apiManagementTestService(t)
			ctx := context.Background()
			if ok, err := s.Futu.Configured(ctx); err != nil || ok {
				t.Fatalf("optional credentials: %v %v", ok, err)
			}
			var signer crypto.Signer
			if algorithm == "Ed25519" {
				_, key, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				signer = key
			} else {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatal(err)
				}
				signer = key
			}
			raw, err := x509.MarshalPKCS8PrivateKey(signer)
			if err != nil {
				t.Fatal(err)
			}
			secret := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}))
			calls := 0
			verify := func(r *http.Request, body []byte) {
				t.Helper()
				if r.Header.Get("X-Api-Key") != "test-generated-appkey" || r.Header.Get("X-Nonce") == "" {
					t.Error("missing signing headers")
				}
				bodyHash := ""
				if len(body) > 0 {
					h := sha256.Sum256(body)
					bodyHash = hex.EncodeToString(h[:])
				}
				payload := []byte(strings.Join([]string{r.Header.Get("X-Timestamp"), r.Method, r.URL.EscapedPath(), r.URL.RawQuery, bodyHash}, "\n"))
				signature, err := base64.StdEncoding.DecodeString(r.Header.Get("Authorization"))
				if err != nil {
					t.Fatal(err)
				}
				switch key := signer.Public().(type) {
				case ed25519.PublicKey:
					if !ed25519.Verify(key, payload, signature) {
						t.Error("Ed25519 payload/signature mismatch")
					}
				case *rsa.PublicKey:
					hash := sha256.Sum256(payload)
					if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature); err != nil {
						t.Error(err)
					}
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				verify(r, nil)
				io.WriteString(w, `{"ret_code":0,"data":{"items":[]}}`)
			}))
			defer server.Close()
			s.Futu.host = server.URL
			input := FutuCredentialsInput{Mode: "api_key", Algorithm: algorithm, AppKey: "test-generated-appkey", PrivateKey: secret}
			if err := s.Futu.SaveCredentials(ctx, input); err != nil {
				t.Fatal(err)
			}
			var policy discovery.APIProviderPolicy
			s.db.First(&policy, "provider = ?", "futu")
			if calls != 0 || !policy.Paused {
				t.Fatal("save dispatched or enabled provider")
			}
			if ok, err := s.Futu.Configured(ctx); err != nil || !ok {
				t.Fatalf("configured %v %v", ok, err)
			}
			for _, key := range []string{"futu.app_key", "futu.private_key"} {
				var row model.SystemConfig
				s.main.First(&row, "config_key = ?", key)
				if !row.Encrypted || !strings.HasPrefix(row.ConfigValue, "enc:v1:") {
					t.Fatal("not encrypted", key)
				}
			}
			list, _ := s.configs.List(ctx, "futu", true)
			view, _ := s.Futu.Credentials(ctx)
			var audit []model.OperationLog
			s.main.Find(&audit)
			visible, _ := json.Marshal([]any{list, view, audit})
			if strings.Contains(string(visible), "test-generated-appkey") || strings.Contains(string(visible), "PRIVATE KEY") {
				t.Fatal("credential leakage")
			}
			s.db.Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Updates(map[string]any{"paused": false, "min_interval_ms": 0})
			s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "company").Update("provider", "futu")
			var out any
			if err := s.Futu.ReadJSON(ctx, http.MethodGet, "/api/v1.0/quote/US.TEST/company/profile", url.Values{"lang": {"en us"}}, nil, &out); err != nil {
				t.Fatal(err)
			}
			body := []byte(`{"symbols":["US.TEST"]}`)
			req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1.0/quote/snapshot?b=2&a=1", nil)
			if err := s.Futu.authorizeRequest(ctx, req, body); err != nil {
				t.Fatal(err)
			}
			verify(req, body)
			if err := s.Futu.SaveCredentials(ctx, FutuCredentialsInput{Mode: "api_key", Algorithm: algorithm}); err != nil {
				t.Fatal("blank did not retain", err)
			}
			if err := s.Futu.SaveCredentials(ctx, FutuCredentialsInput{Mode: "api_key", Algorithm: algorithm, Clear: true}); err != nil {
				t.Fatal(err)
			}
			if ok, _ := s.Futu.Configured(ctx); ok {
				t.Fatal("clear did not remove manual credentials")
			}
			s.db.First(&policy, "provider = ?", "futu")
			if !policy.Paused {
				t.Fatal("clear did not pause")
			}
		})
	}
}
func TestFutuInvalidCredentialsAndDisabledNeverDispatch(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	for _, in := range []FutuCredentialsInput{{Mode: "bad"}, {Mode: "api_key", AppKey: "partial"}, {Mode: "api_key", AppKey: "key", PrivateKey: "invalid PEM"}, {Mode: "api_key", Algorithm: "wrong"}} {
		if err := s.Futu.SaveCredentials(ctx, in); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid accepted: %+v %v", in, err)
		}
	}
	seedFutuTestToken(t, s)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected dispatch", r.URL.Path) }))
	defer server.Close()
	s.Futu.host = server.URL
	for _, path := range []string{"/api/v1.0/trade/order", "/api/v1.0/quote/../../trade/order", "https://other.test", "/api/v1.0/quote/US.TEST/private-data"} {
		var out any
		if s.Futu.ReadJSON(ctx, http.MethodGet, path, nil, nil, &out) == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "analyst").Updates(map[string]any{"provider": "futu", "enabled": false})
	var out any
	if err := s.Futu.ReadJSON(ctx, http.MethodGet, "/api/v1.0/quote/US.TEST/research/analyst-consensus", nil, nil, &out); !errors.Is(err, discovery.ErrAPIDisabled) {
		t.Fatalf("no module preflight: %v", err)
	}
	if calls != 0 {
		t.Fatal("invalid or disabled request dispatched")
	}
}
