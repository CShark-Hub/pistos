// Package ai runs bring-your-own-key discovery passes and triage. Its results are advisory: nothing
// here can change the deterministic check plan or create a feedback decision.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/pistos/internal/config"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Messages  []Message
	MaxTokens int
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Response struct {
	Content  string
	Usage    Usage
	Attempts int
}

// Provider is the boundary to a model backend; tests substitute fake HTTP servers, not this interface.
type Provider interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

// ErrOffline is returned when a pass needs the network but --offline forbids it.
var ErrOffline = errors.New("provider calls are disabled by --offline")

// openAICompatible speaks the Chat Completions API (POST {endpoint}/chat/completions), which hosted
// providers and local servers (vLLM, llama.cpp, Ollama's OpenAI endpoint) share.
type openAICompatible struct {
	alias   string
	m       config.ModelAlias
	client  *http.Client
	limiter chan struct{}
	key     string
}

var (
	limitersMu sync.Mutex
	limiters   = map[string]chan struct{}{}
)

// limiterFor bounds concurrent requests per endpoint, shared by every alias that points at it.
func limiterFor(endpoint string, n int) chan struct{} {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	if l, ok := limiters[endpoint]; ok {
		return l
	}
	if n <= 0 {
		n = 2
	}
	l := make(chan struct{}, n)
	limiters[endpoint] = l
	return l
}

func NewProvider(alias string, m config.ModelAlias) (Provider, error) {
	p := &openAICompatible{alias: alias, m: m, limiter: limiterFor(m.Endpoint, m.Concurrency)}
	timeout := m.Timeout.Duration
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	p.client = &http.Client{Timeout: timeout}
	if m.APIKeyEnv != "" {
		p.key = os.Getenv(m.APIKeyEnv)
		if p.key == "" {
			return nil, fmt.Errorf("model %s: environment variable %s is not set", alias, m.APIKeyEnv)
		}
	}
	return p, nil
}

const maxAttempts = 3

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

// transient errors are retried a bounded number of times; everything else fails the pass at once.
func transient(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.status == http.StatusTooManyRequests || he.status >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func (p *openAICompatible) Complete(ctx context.Context, r Request) (Response, error) {
	select {
	case p.limiter <- struct{}{}:
		defer func() { <-p.limiter }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	body := map[string]any{
		"model":           p.m.Model,
		"messages":        r.Messages,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	payload, _ := json.Marshal(body)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := p.once(ctx, payload)
		if err == nil {
			resp.Attempts = attempt
			return resp, nil
		}
		lastErr = err
		if !transient(err) || attempt == maxAttempts {
			break
		}
		select {
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		case <-ctx.Done():
			return Response{Attempts: attempt}, p.redact(ctx.Err())
		}
	}
	return Response{Attempts: maxAttempts}, p.redact(lastErr)
}

func (p *openAICompatible) once(ctx context.Context, payload []byte) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.m.Endpoint, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return Response{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Response{}, &httpError{status: res.StatusCode, body: truncate(string(raw), 300)}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("unexpected response body: %s", truncate(string(raw), 300))
	}
	return Response{Content: out.Choices[0].Message.Content, Usage: out.Usage}, nil
}

// redact removes the API key from anything that could reach a report or log.
func (p *openAICompatible) redact(err error) error {
	if err == nil || p.key == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), p.key, "***"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
