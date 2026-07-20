package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zulutime-io/cli/internal/keyringstore"
)

const (
	// Origin only — client paths already include /api/v1/...
	DefaultAPIURL = "https://zulutime.io"
	dirName       = "ztime"
	fileName      = "config.json"
	credFileName  = "credentials.json"
)

type Config struct {
	APIURL          string            `json:"api_url"`
	WebURL          string            `json:"web_url,omitempty"` // browser origin for login; defaults to APIURL
	ProjectByRemote map[string]string `json:"project_by_remote,omitempty"` // remote URL -> project_id
	LastClientID    string            `json:"last_client_id,omitempty"`
}

type Credentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	TokenType        string `json:"token_type,omitempty"` // "pat" or empty/session
	DeviceID         string `json:"device_id,omitempty"`
	DevicePrivateKey string `json:"device_private_key,omitempty"` // legacy file storage; migrated to OS keychain
	DeviceKeyAlg     string `json:"device_key_alg,omitempty"`
}

func (c *Credentials) IsPAT() bool {
	if c == nil {
		return false
	}
	if c.TokenType == "pat" {
		return true
	}
	return len(c.AccessToken) >= 6 && c.AccessToken[:6] == "ztpat_"
}

func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func path(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func Load() (*Config, error) {
	p, err := path(fileName)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		APIURL:          DefaultAPIURL,
		ProjectByRemote: map[string]string{},
	}
	if env := os.Getenv("ZTIME_API_URL"); env != "" {
		cfg.APIURL = env
	}
	if env := os.Getenv("ZTIME_WEB_URL"); env != "" {
		cfg.WebURL = env
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.applyEnvOverrides()
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	cfg.applyEnvOverrides()
	if cfg.ProjectByRemote == nil {
		cfg.ProjectByRemote = map[string]string{}
	}
	return cfg, nil
}

func (c *Config) applyEnvOverrides() {
	if env := os.Getenv("ZTIME_API_URL"); env != "" {
		c.APIURL = env
	}
	if env := os.Getenv("ZTIME_WEB_URL"); env != "" {
		c.WebURL = env
	}
}

// WebOrigin returns the browser origin for CLI authorize (web_url, else api_url).
func (c *Config) WebOrigin() string {
	if c.WebURL != "" {
		return strings.TrimRight(c.WebURL, "/")
	}
	return strings.TrimRight(c.APIURL, "/")
}

func (c *Config) Save() error {
	p, err := path(fileName)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func LoadCredentials() (*Credentials, error) {
	if env := strings.TrimSpace(os.Getenv("ZTIME_TOKEN")); env != "" {
		return &Credentials{AccessToken: env, TokenType: "pat"}, nil
	}
	p, err := path(credFileName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}

	// Migrate legacy on-disk private key into the OS keychain.
	if c.DevicePrivateKey != "" && c.DeviceID != "" {
		if err := keyringstore.SetDevicePrivateKey(c.DeviceID, c.DevicePrivateKey); err == nil {
			c.DevicePrivateKey = ""
			_ = SaveCredentials(&c)
		}
	}

	if c.DevicePrivateKey == "" && c.DeviceID != "" {
		if secret, err := keyringstore.GetDevicePrivateKey(c.DeviceID); err == nil {
			c.DevicePrivateKey = secret
		} else if !keyringstore.IsNotFound(err) {
			return nil, err
		}
	}
	return &c, nil
}

func SaveCredentials(c *Credentials) error {
	if c == nil {
		return ClearCredentials()
	}
	toStore := *c
	if toStore.DevicePrivateKey != "" && toStore.DeviceID != "" {
		if err := keyringstore.SetDevicePrivateKey(toStore.DeviceID, toStore.DevicePrivateKey); err != nil {
			return err
		}
		// Never persist the private key on disk.
		toStore.DevicePrivateKey = ""
	}
	p, err := path(credFileName)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(toStore, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func ClearCredentials() error {
	p, err := path(credFileName)
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(p); err == nil {
		var c Credentials
		if json.Unmarshal(data, &c) == nil && c.DeviceID != "" {
			_ = keyringstore.DeleteDevicePrivateKey(c.DeviceID)
		}
	}
	_ = keyringstore.DeleteDevicePrivateKey("")
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c *Config) RememberProject(remote, projectID, clientID string) {
	if remote == "" || projectID == "" {
		return
	}
	if c.ProjectByRemote == nil {
		c.ProjectByRemote = map[string]string{}
	}
	c.ProjectByRemote[remote] = projectID
	if clientID != "" {
		c.LastClientID = clientID
	}
}
