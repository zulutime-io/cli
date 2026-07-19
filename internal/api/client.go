package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/version"
)

var ErrUnauthorized = errors.New("not logged in")

type Client struct {
	BaseURL string
	HTTP    *http.Client

	mu       sync.Mutex
	creds    *config.Credentials
	onUpdate func(*config.Credentials) error
}

func New(cfg *config.Config, creds *config.Credentials, onUpdate func(*config.Credentials) error) *Client {
	return &Client{
		BaseURL: strings.TrimRight(cfg.APIURL, "/"),
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
		creds:    creds,
		onUpdate: onUpdate,
	}
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MeResponse struct {
	User         User         `json:"user"`
	Organization Organization `json:"organization"`
	Role         string       `json:"role"`
}

type ClientRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type Project struct {
	ID              string  `json:"id"`
	ClientID        string  `json:"client_id"`
	ClientName      string  `json:"client_name"`
	Name            string  `json:"name"`
	Code            string  `json:"code"`
	BillableDefault bool    `json:"billable_default"`
	HourlyRate      float64 `json:"hourly_rate"`
	Active          bool    `json:"active"`
}

type TimeEntry struct {
	ID              string      `json:"id"`
	ProjectID       string      `json:"project_id"`
	ProjectName     string      `json:"project_name"`
	ClientName      string      `json:"client_name"`
	EntryDate       string      `json:"entry_date"`
	DurationMinutes int         `json:"duration_minutes"`
	Description     string      `json:"description"`
	Billable        bool        `json:"billable"`
	Status          string      `json:"status"`
	SourceMeta      *SourceMeta `json:"source_meta,omitempty"`
}

type SourceMeta struct {
	Source    string     `json:"source,omitempty"`
	GitRemote string     `json:"git_remote,omitempty"`
	GitRepo   string     `json:"git_repo,omitempty"`
	GitBranch string     `json:"git_branch,omitempty"`
	GitRoot   string     `json:"git_root,omitempty"`
	Commits   CommitList `json:"commits,omitempty"`
}

type CreateTimeEntryInput struct {
	ProjectID     string      `json:"project_id"`
	EntryDate     string      `json:"entry_date"`
	DurationHours float64     `json:"duration_hours"`
	Description   string      `json:"description"`
	Billable      *bool       `json:"billable,omitempty"`
	TripMode      string      `json:"trip_mode"`
	SourceMeta    *SourceMeta `json:"source_meta,omitempty"`
}

func (c *Client) SetCredentials(creds *config.Credentials) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creds = creds
}

func (c *Client) Login(email, password string) (*TokenResponse, error) {
	var out TokenResponse
	err := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email": email, "password": password, "remember_me": true,
	}, false, &out)
	if err != nil {
		return nil, err
	}
	creds := &config.Credentials{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}
	c.SetCredentials(creds)
	if c.onUpdate != nil {
		_ = c.onUpdate(creds)
	}
	return &out, nil
}

func (c *Client) Refresh() error {
	c.mu.Lock()
	rt := ""
	if c.creds != nil {
		rt = c.creds.RefreshToken
	}
	c.mu.Unlock()
	if rt == "" {
		return ErrUnauthorized
	}
	var out TokenResponse
	err := c.do(http.MethodPost, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": rt,
	}, false, &out)
	if err != nil {
		return err
	}
	creds := &config.Credentials{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}
	c.SetCredentials(creds)
	if c.onUpdate != nil {
		_ = c.onUpdate(creds)
	}
	return nil
}

func (c *Client) Logout() error {
	c.mu.Lock()
	rt := ""
	if c.creds != nil {
		rt = c.creds.RefreshToken
	}
	c.mu.Unlock()
	if rt != "" {
		_ = c.do(http.MethodPost, "/api/v1/auth/logout", map[string]any{
			"refresh_token": rt,
		}, false, nil)
	}
	c.SetCredentials(nil)
	return nil
}

func (c *Client) Me() (*MeResponse, error) {
	var out MeResponse
	if err := c.doAuthed(http.MethodGet, "/api/v1/auth/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListClients() ([]ClientRow, error) {
	var out []ClientRow
	if err := c.doAuthed(http.MethodGet, "/api/v1/clients", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ClientRow{}
	}
	return out, nil
}

func (c *Client) ListProjects() ([]Project, error) {
	var out []Project
	if err := c.doAuthed(http.MethodGet, "/api/v1/projects", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Project{}
	}
	return out, nil
}

func (c *Client) ListTimeEntries(from, to, status string) ([]TimeEntry, error) {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	if status != "" {
		q.Set("status", status)
	}
	var out []TimeEntry
	if err := c.doAuthed(http.MethodGet, "/api/v1/time-entries?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []TimeEntry{}
	}
	return out, nil
}

func (c *Client) CreateTimeEntry(in CreateTimeEntryInput) (*TimeEntry, error) {
	if in.TripMode == "" {
		in.TripMode = "none"
	}
	var out TimeEntry
	if err := c.doAuthed(http.MethodPost, "/api/v1/time-entries", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type UpdateTimeEntryInput struct {
	ProjectID     string      `json:"project_id"`
	EntryDate     string      `json:"entry_date"`
	DurationHours float64     `json:"duration_hours"`
	Description   string      `json:"description"`
	Billable      *bool       `json:"billable,omitempty"`
	TripMode      string      `json:"trip_mode"`
	SourceMeta    *SourceMeta `json:"source_meta,omitempty"`
}

func (c *Client) UpdateTimeEntry(id string, in UpdateTimeEntryInput) (*TimeEntry, error) {
	if in.TripMode == "" {
		in.TripMode = "none"
	}
	var out TimeEntry
	if err := c.doAuthed(http.MethodPatch, "/api/v1/time-entries/"+id, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) PatchSourceMeta(id string, meta *SourceMeta, description *string) (*TimeEntry, error) {
	body := map[string]any{"source_meta": meta}
	if description != nil {
		body["description"] = *description
	}
	var out TimeEntry
	if err := c.doAuthed(http.MethodPatch, "/api/v1/time-entries/"+id+"/source-meta", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SubmitTimeEntry(id string) (*TimeEntry, error) {
	var out TimeEntry
	if err := c.doAuthed(http.MethodPost, "/api/v1/time-entries/"+id+"/submit", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteTimeEntry(id string) error {
	return c.doAuthed(http.MethodDelete, "/api/v1/time-entries/"+id, nil, nil)
}

func (c *Client) SubmitBatch(from, to string) (int, error) {
	var out struct {
		Submitted int `json:"submitted"`
	}
	if err := c.doAuthed(http.MethodPost, "/api/v1/time-entries/submit-batch", map[string]any{
		"from": from, "to": to,
	}, &out); err != nil {
		return 0, err
	}
	return out.Submitted, nil
}

func (c *Client) doAuthed(method, path string, body any, out any) error {
	err := c.do(method, path, body, true, out)
	if err == nil {
		return nil
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
		return err
	}
	if refreshErr := c.Refresh(); refreshErr != nil {
		return ErrUnauthorized
	}
	return c.do(method, path, body, true, out)
}

func (c *Client) do(method, path string, body any, authed bool, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if authed {
		c.mu.Lock()
		tok := ""
		if c.creds != nil {
			tok = c.creds.AccessToken
		}
		c.mu.Unlock()
		if tok == "" {
			return ErrUnauthorized
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		var wrap struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &wrap) == nil && wrap.Error != "" {
			msg = wrap.Error
		}
		return &APIError{Status: res.StatusCode, Message: msg}
	}
	if out == nil || res.StatusCode == http.StatusNoContent || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
