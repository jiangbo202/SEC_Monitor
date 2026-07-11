package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const encryptedSecretPrefix = "enc:v1:"

var (
	telegramBotURLPattern    = regexp.MustCompile(`(?i)(https?://[^/\s"']+/bot)([^/\s"']+)(/[^\s"']*)?`)
	bearerTokenPattern       = regexp.MustCompile(`(?i)(\bBearer\s+)([^\s"']+)`)
	queryTokenPattern        = regexp.MustCompile(`(?i)([?&](?:token|bot_token|access_token|api_key)=)([^&\s"']+)`)
	namedTokenPattern        = regexp.MustCompile(`(?i)(\b(?:token|bot_token|access_token|api_key)\s*[:=]\s*)([^\s,"']+)`)
	errEncryptionUnavailable = errors.New("configuration encryption key is unavailable")
)

// EncryptionHealth reports whether encrypted configuration writes are safe.
type EncryptionHealth struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	Configured bool   `json:"configured"`
}

func (s *ConfigService) encryptSecret(plain string) (string, error) {
	if len(s.encryptionKey) != 32 {
		return "", errEncryptionUnavailable
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plain), nil)
	payload := append(nonce, ciphertext...)
	return encryptedSecretPrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func (s *ConfigService) decryptSecret(stored string) (string, error) {
	if !strings.HasPrefix(stored, encryptedSecretPrefix) {
		return stored, nil
	}
	if len(s.encryptionKey) != 32 {
		return "", errEncryptionUnavailable
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encryptedSecretPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted configuration: %w", err)
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted configuration payload")
	}
	plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt configuration: %w", err)
	}
	return string(plain), nil
}

// SanitizeSensitiveError removes credential fragments that may be embedded in
// transport errors before they are persisted or returned from an API.
func SanitizeSensitiveError(value string) string {
	value = telegramBotURLPattern.ReplaceAllString(value, "${1}******${3}")
	value = bearerTokenPattern.ReplaceAllString(value, "${1}******")
	value = queryTokenPattern.ReplaceAllString(value, "${1}******")
	return namedTokenPattern.ReplaceAllString(value, "${1}******")
}
