package fakeai

import (
	"net/http"
	"strings"
	"testing"
)

func TestRecordsRequestsAndFailures(t *testing.T) {
	s := New()
	defer s.Close()
	s.Respond = func(r Request) (string, int) {
		if r.Body["model"] == "broken" {
			return "", http.StatusServiceUnavailable
		}
		return `{"candidates":[]}`, http.StatusOK
	}
	for _, model := range []string{"ok", "broken"} {
		resp, err := http.Post(s.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"`+model+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if (model == "ok") != (resp.StatusCode == http.StatusOK) {
			t.Fatalf("%s: status %d", model, resp.StatusCode)
		}
	}
	if s.Count() != 2 || s.Requests()[0].Authorization != "" {
		t.Fatalf("requests: %+v", s.Requests())
	}
}
