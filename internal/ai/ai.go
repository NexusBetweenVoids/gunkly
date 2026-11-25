// Package ai talks to an OpenAI-compatible chat completions endpoint that the
// instance admin configures (a paid API or a model on their own network, such
// as Ollama, LM Studio, llama.cpp or vLLM). It knows nothing about chores; it
// sends messages and returns the model's text.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Timeout bounds one request: local models on modest hardware can take a while.
const Timeout = 120 * time.Second

// Failure kinds, safe to show to an admin (never the key, never the raw body).
var (
	ErrUnreachable = errors.New("unreachable")  // DNS, connection refused, TLS, blocked address
	ErrAuth        = errors.New("auth")         // 401/403: wrong or missing API key
	ErrNotFound    = errors.New("not_found")    // 404: wrong URL (missing /v1?) or unknown model
	ErrRateLimited = errors.New("rate_limited") // 429: the provider's limit or quota
	ErrTimeout     = errors.New("timeout")      // no answer within Timeout
	ErrBadResponse = errors.New("bad_response") // not a chat completion we can read
	ErrServer      = errors.New("server_error") // any other non-2xx status
)

// Client is one configured endpoint.
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. https://api.openai.com/v1 or http://ollama:11434/v1
	Model   string
	Key     string // optional (local servers often need none)
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // system | user
	Content string `json:"content"`
}

type request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Chat sends the messages and returns the first choice's text.
func (c *Client) Chat(ctx context.Context, msgs []Message, maxTokens int) (string, error) {
	body, err := json.Marshal(request{Model: c.Model, Messages: msgs, Temperature: 0, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", ErrUnreachable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gunkly")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ErrTimeout
		}
		return "", fmt.Errorf("%w: %v", ErrUnreachable, redact(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ErrTimeout
		}
		return "", ErrBadResponse
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", ErrAuth
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", ErrRateLimited
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("%w: HTTP %d", ErrServer, resp.StatusCode)
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", ErrBadResponse
	}
	return out.Choices[0].Message.Content, nil
}

// Test checks that the endpoint answers a tiny request.
func (c *Client) Test(ctx context.Context) error {
	_, err := c.Chat(ctx, []Message{{Role: "user", Content: `Reply with exactly: {"ok":true}`}}, 20)
	return err
}

// Kind is the failure kind's name for the API ("" when err is nil).
func Kind(err error) string {
	for _, k := range []error{ErrUnreachable, ErrAuth, ErrNotFound, ErrRateLimited, ErrTimeout, ErrBadResponse, ErrServer} {
		if errors.Is(err, k) {
			return k.Error()
		}
	}
	if err != nil {
		return ErrServer.Error()
	}
	return ""
}

// redact keeps transport errors short for logs; they carry no credentials (the
// key travels in a header, never in the URL).
func redact(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// JSONObject extracts the first top-level JSON object from a model's reply,
// which may wrap it in prose or a ```json fence. It returns "" when none.
func JSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case esc:
			esc = false
		case inStr && ch == '\\':
			esc = true
		case ch == '"':
			inStr = !inStr
		case inStr:
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
