// Package fakeai serves canned OpenAI-compatible chat completion responses and records every request,
// so AI tests never need a real key or network access.
package fakeai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

type Request struct {
	Path          string
	Authorization string
	Body          map[string]any
}

type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []Request
	// Respond returns the assistant content and HTTP status for a request; defaults to an empty JSON object.
	Respond func(r Request) (content string, status int)
}

func New() *Server {
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	req := Request{Path: r.URL.Path, Authorization: r.Header.Get("Authorization")}
	_ = json.Unmarshal(raw, &req.Body)
	s.mu.Lock()
	s.requests = append(s.requests, req)
	respond := s.Respond
	s.mu.Unlock()

	content, status := "{}", http.StatusOK
	if respond != nil {
		content, status = respond(req)
	}
	if status != http.StatusOK {
		http.Error(w, `{"error":{"message":"fake failure"}}`, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "fake",
		"object":  "chat.completion",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}
