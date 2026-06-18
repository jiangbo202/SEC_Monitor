package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPNotifierSendTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		chatID     string
		statusCode int
		wantErr    bool
	}{
		{name: "sends message", token: "token", chatID: "10001", statusCode: http.StatusOK},
		{name: "missing token", chatID: "10001", statusCode: http.StatusOK, wantErr: true},
		{name: "missing chat id", token: "token", statusCode: http.StatusOK, wantErr: true},
		{name: "telegram error status", token: "token", chatID: "10001", statusCode: http.StatusBadGateway, wantErr: true},
		{name: "uses default base url", token: "token", chatID: "10001", statusCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier := NewHTTPNotifier(tt.token, tt.chatID, time.Second)
			if tt.name != "uses default base url" {
				notifier.BaseURL = "https://telegram.test"
			}
			notifier.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(r.URL.Path, "/bottoken/sendMessage") {
					t.Fatalf("path = %q", r.URL.Path)
				}
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					Header:     make(http.Header),
				}, nil
			})}

			err := notifier.Send(context.Background(), Message{Text: "hello"})
			if tt.wantErr && err == nil {
				t.Fatalf("Send expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Send: %v", err)
			}
		})
	}
}

func TestHTTPNotifierBaseURLTableDriven(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default", want: "https://api.telegram.org"},
		{name: "custom", in: "https://telegram.test", want: "https://telegram.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (&HTTPNotifier{BaseURL: tt.in}).baseURL()
			if got != tt.want {
				t.Fatalf("baseURL = %q, want %q", got, tt.want)
			}
		})
	}
}
