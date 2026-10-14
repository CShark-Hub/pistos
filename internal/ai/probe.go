package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Setup-time probes. They only read: listing models and checking a key never spends tokens.

const OpenRouterEndpoint = "https://openrouter.ai/api/v1"

func IsOpenRouter(endpoint string) bool {
	return strings.HasPrefix(strings.TrimRight(endpoint, "/"), "https://openrouter.ai/api")
}

// ErrUnauthorized means the endpoint answered and refused the key.
var ErrUnauthorized = errors.New("the endpoint rejected the key")

// ErrNoModelList means the endpoint answered but does not offer GET /models.
var ErrNoModelList = errors.New("the endpoint does not list its models")

type ModelInfo struct {
	ID            string
	Name          string
	ContextLength int
	// Prices are USD per million tokens; negative when the endpoint does not publish them.
	PromptPrice     float64
	CompletionPrice float64
	// JSONMode is false only when the endpoint says the model ignores response_format, which every
	// pistos request sets.
	JSONMode bool
}

var probeClient = &http.Client{Timeout: 20 * time.Second}

func get(ctx context.Context, url, key string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := probeClient.Do(req)
	if err != nil {
		return 0, nil, redactKey(err, key)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	return res.StatusCode, b, redactKey(err, key)
}

func redactKey(err error, key string) error {
	if err == nil || key == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "***"))
}

// Reachable reports whether anything answers HTTP at the endpoint; any status code counts.
func Reachable(ctx context.Context, endpoint string) error {
	_, _, err := get(ctx, strings.TrimRight(endpoint, "/")+"/models", "")
	return err
}

// CheckKey verifies a key without spending tokens: OpenRouter's /key describes the key itself (its
// model list is public, so listing proves nothing), other endpoints are asked to list their models. ErrNoModelList means the key could not be checked.
func CheckKey(ctx context.Context, endpoint, key string, openRouter bool) error {
	base := strings.TrimRight(endpoint, "/")
	url := base + "/models"
	if openRouter {
		url = base + "/key"
	}
	code, body, err := get(ctx, url, key)
	switch {
	case err != nil:
		return err
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d)", ErrUnauthorized, code)
	case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
		return ErrNoModelList
	case code != http.StatusOK:
		return fmt.Errorf("HTTP %d: %s", code, truncate(redactKey(errors.New(string(body)), key).Error(), 200))
	}
	return nil
}

// ListModels returns the endpoint's models sorted by id.
func ListModels(ctx context.Context, endpoint, key string) ([]ModelInfo, error) {
	code, body, err := get(ctx, strings.TrimRight(endpoint, "/")+"/models", key)
	switch {
	case err != nil:
		return nil, err
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrUnauthorized, code)
	case code != http.StatusOK:
		return nil, ErrNoModelList
	}
	var out struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			ContextLength       int      `json:"context_length"`
			SupportedParameters []string `json:"supported_parameters"`
			Pricing             *struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: unexpected /models response", ErrNoModelList)
	}
	var models []ModelInfo
	for _, d := range out.Data {
		if d.ID == "" {
			continue
		}
		m := ModelInfo{ID: d.ID, Name: d.Name, ContextLength: d.ContextLength, PromptPrice: -1, CompletionPrice: -1, JSONMode: true}
		if d.Pricing != nil {
			m.PromptPrice, m.CompletionPrice = perMillion(d.Pricing.Prompt), perMillion(d.Pricing.Completion)
		}
		if d.SupportedParameters != nil {
			m.JSONMode = false
			for _, p := range d.SupportedParameters {
				if p == "response_format" || p == "structured_outputs" {
					m.JSONMode = true
				}
			}
		}
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func perMillion(perToken string) float64 {
	v, err := strconv.ParseFloat(perToken, 64)
	if err != nil || v < 0 {
		return -1
	}
	return v * 1e6
}
