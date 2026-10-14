package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"example.com/pistos/internal/fsutil"
)

// Saved keys live in the user's configuration directory, never in the repository: config.toml only
// names a variable, and a key is looked up under that name in the environment first, then here.
// The file is created readable by its owner only.

// CredentialsPath honours PISTOS_CREDENTIALS_FILE so tests and unusual setups can relocate the store.
func CredentialsPath() (string, error) {
	if p := os.Getenv("PISTOS_CREDENTIALS_FILE"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no user configuration directory to save the key in: %w", err)
	}
	return filepath.Join(dir, "pistos", "credentials.json"), nil
}

type credentials struct {
	Keys map[string]string `json:"keys"`
}

func loadCredentials() (credentials, string, error) {
	c := credentials{Keys: map[string]string{}}
	p, err := CredentialsPath()
	if err != nil {
		return c, "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, p, nil
	}
	if err != nil {
		return c, p, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, p, fmt.Errorf("%s is not valid JSON: %w", p, err)
	}
	if c.Keys == nil {
		c.Keys = map[string]string{}
	}
	return c, p, nil
}

// LookupKey returns the key for an api_key_env name and where it came from ("environment" or the
// credentials file path); an empty key means neither has it.
func LookupKey(name string) (key, source string) {
	if v := os.Getenv(name); v != "" {
		return v, "environment"
	}
	c, p, err := loadCredentials()
	if err != nil {
		return "", ""
	}
	if v := c.Keys[name]; v != "" {
		return v, p
	}
	return "", ""
}

// SaveKey stores key under name in the credentials file and returns the file's path.
func SaveKey(name, key string) (string, error) {
	c, p, err := loadCredentials()
	if err != nil {
		return "", err
	}
	c.Keys[name] = key
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	return p, fsutil.WriteFileAtomic(p, append(b, '\n'), 0o600)
}

// SavedKeyNames lists the variable names that have a saved key, for diagnostics.
func SavedKeyNames() []string {
	c, _, _ := loadCredentials()
	var out []string
	for k := range c.Keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
