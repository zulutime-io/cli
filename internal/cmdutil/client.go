package cmdutil

import (
	"errors"
	"fmt"
	"os"

	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/config"
)

func LoadConfig() (*config.Config, error) {
	return config.Load()
}

func NewAPI(cfg *config.Config) (*api.Client, error) {
	creds, err := config.LoadCredentials()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return api.New(cfg, creds, func(c *config.Credentials) error {
		return config.SaveCredentials(c)
	}), nil
}

func RequireAuth(client *api.Client) error {
	_, err := client.Me()
	if err == nil {
		return nil
	}
	if errors.Is(err, api.ErrUnauthorized) {
		if client != nil && client.BaseURL != "" {
			return fmt.Errorf("not logged in (token rejected by %s) — run `ztime login` again (device key / API URL)", client.BaseURL)
		}
		return fmt.Errorf("not logged in — run `ztime login` first")
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.Status == 401 {
		return fmt.Errorf("not logged in (%s) — run `ztime login` again", ae.Message)
	}
	return err
}

func ExitErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err.Error())
	os.Exit(1)
}
