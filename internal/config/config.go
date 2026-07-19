package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	DefaultAPIURL = "https://zulutime.io/api/v1"
	dirName       = "ztime"
	fileName      = "config.json"
	credFileName  = "credentials.json"
)

type Config struct {
	APIURL           string            `json:"api_url"`
	ProjectByRemote  map[string]string `json:"project_by_remote,omitempty"` // remote URL -> project_id
	LastClientID     string            `json:"last_client_id,omitempty"`
}

type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
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
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
	if env := os.Getenv("ZTIME_API_URL"); env != "" {
		cfg.APIURL = env
	}
	if cfg.ProjectByRemote == nil {
		cfg.ProjectByRemote = map[string]string{}
	}
	return cfg, nil
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
	return &c, nil
}

func SaveCredentials(c *Credentials) error {
	p, err := path(credFileName)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
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
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
