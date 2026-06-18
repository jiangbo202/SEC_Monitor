package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Message struct {
	Text string
}

type Notifier interface {
	Send(ctx context.Context, message Message) error
}

type HTTPNotifier struct {
	BotToken string
	ChatID   string
	BaseURL  string
	Client   *http.Client
}

func NewHTTPNotifier(botToken string, chatID string, timeout time.Duration) *HTTPNotifier {
	return &HTTPNotifier{
		BotToken: botToken,
		ChatID:   chatID,
		BaseURL:  "https://api.telegram.org",
		Client:   &http.Client{Timeout: timeout},
	}
}

func (n *HTTPNotifier) Send(ctx context.Context, message Message) error {
	if n.BotToken == "" || n.ChatID == "" {
		return fmt.Errorf("telegram bot token and chat id are required")
	}
	body, err := json.Marshal(map[string]string{
		"chat_id": n.ChatID,
		"text":    message.Text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL()+"/bot"+n.BotToken+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if len(body) > 0 {
			return fmt.Errorf("telegram status: %d: %s", resp.StatusCode, string(body))
		}
		return fmt.Errorf("telegram status: %d", resp.StatusCode)
	}
	return nil
}

func (n *HTTPNotifier) baseURL() string {
	if n.BaseURL != "" {
		return n.BaseURL
	}
	return "https://api.telegram.org"
}
