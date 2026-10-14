package review

import (
	"testing"

	"example.com/pistos/internal/config"
)

// The secrets pack reads the same files as language packs, possibly first; that must not leave the
// language pack without symbols.
func TestSourceCacheIsPerEcosystem(t *testing.T) {
	c := newSourceCache()
	src := func() ([]byte, error) { return []byte("package main\n\nfunc f() {\n\t_ = 1\n}\n"), nil }
	if got := c.get("/snap/main.go", config.Secrets, src).enclosing(4); got != "" {
		t.Fatalf("the secrets pack has no symbols: %q", got)
	}
	if got := c.get("/snap/main.go", config.Go, src).enclosing(4); got != "f" {
		t.Fatalf("go symbols after a secrets read: %q", got)
	}
}
