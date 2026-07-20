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
	"github.com/zulutime-io/cli/internal/devicekey"
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
		BaseURL: normalizeBaseURL(cfg.APIURL),
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
		creds:    creds,
		onUpdate: onUpdate,
	}
}

// normalizeBaseURL keeps the API origin. Paths already start with /api/v1/.
func normalizeBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	base = strings.TrimSuffix(base, "/api/v1")
	return strings.TrimRight(base, "/")
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

type Entitlements struct {
	Plan           string `json:"plan"`
	Status         string `json:"status"`
	Approvals      bool   `json:"approvals"`
	ClientInvoices bool   `json:"client_invoices"`
	ClientRequests bool   `json:"client_requests"`
	Integrations   bool   `json:"integrations"`
}

// MsgRequestsLocked is shown when client_requests is off (free / locked Team).
const MsgRequestsLocked = "Verzoeken zit in Team — upgrade op zulutime.io"

type MeResponse struct {
	User         User          `json:"user"`
	Organization Organization  `json:"organization"`
	Role         string        `json:"role"`
	Timezone     string        `json:"timezone"`
	Entitlements *Entitlements `json:"entitlements"`
}

func (m *MeResponse) HasClientRequests() bool {
	return m != nil && m.Entitlements != nil && m.Entitlements.ClientRequests
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
	UserName        string      `json:"user_name,omitempty"`
	EntryDate       string      `json:"entry_date"`
	DurationMinutes int         `json:"duration_minutes"`
	Description     string      `json:"description"`
	Billable        bool        `json:"billable"`
	Status          string      `json:"status"`
	RequestID       *string     `json:"request_id,omitempty"`
	SourceMeta      *SourceMeta `json:"source_meta,omitempty"`
	CreatedAt       string      `json:"created_at,omitempty"`
	SubmittedAt     *string     `json:"submitted_at,omitempty"`
}

type ActivityEvent struct {
	ID        string          `json:"id"`
	EventType string          `json:"event_type"`
	ActorName string          `json:"actor_name,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
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
	RequestID     string      `json:"request_id,omitempty"`
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

type CLILoginStartResponse struct {
	LoginID   string `json:"login_id"`
	ExpiresIn int    `json:"expires_in"`
}

type CLITokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	DeviceID    string `json:"device_id"`
	ExpiresIn   int    `json:"expires_in"`
}

func (c *Client) StartCLILogin(body map[string]any) (*CLILoginStartResponse, error) {
	var out CLILoginStartResponse
	if err := c.do(http.MethodPost, "/api/v1/cli/login/start", body, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ExchangeCLICode(code, verifier, redirectURI, devicePrivateKey, deviceKeyAlg string) (*CLITokenResponse, error) {
	var out CLITokenResponse
	err := c.do(http.MethodPost, "/api/v1/cli/token", map[string]any{
		"code": code, "code_verifier": verifier, "redirect_uri": redirectURI,
	}, false, &out)
	if err != nil {
		return nil, err
	}
	creds := &config.Credentials{
		AccessToken:      out.AccessToken,
		TokenType:        "pat",
		DeviceID:         out.DeviceID,
		DevicePrivateKey: devicePrivateKey,
		DeviceKeyAlg:     deviceKeyAlg,
	}
	c.SetCredentials(creds)
	if c.onUpdate != nil {
		_ = c.onUpdate(creds)
	}
	return &out, nil
}

func (c *Client) Refresh() error {
	c.mu.Lock()
	if c.creds != nil && c.creds.IsPAT() {
		c.mu.Unlock()
		return ErrUnauthorized
	}
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
	deviceID := ""
	rt := ""
	isPAT := false
	if c.creds != nil {
		deviceID = c.creds.DeviceID
		rt = c.creds.RefreshToken
		isPAT = c.creds.IsPAT()
	}
	c.mu.Unlock()
	if isPAT && deviceID != "" {
		_ = c.doAuthed(http.MethodDelete, "/api/v1/account/devices/"+deviceID, nil, nil)
	} else if rt != "" {
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

type ClientRequest struct {
	ID            string            `json:"id"`
	Ref           string            `json:"ref"`
	ClientID      string            `json:"client_id"`
	ClientName    string            `json:"client_name,omitempty"`
	ProjectID     *string           `json:"project_id,omitempty"`
	ProjectName   string            `json:"project_name,omitempty"`
	TypeID        string            `json:"type_id"`
	TypeName      string            `json:"type_name,omitempty"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Status        string            `json:"status"`
	CreatedByName string            `json:"created_by_name,omitempty"`
	Assignees     []RequestAssignee `json:"assignees,omitempty"`
	MinutesLogged int               `json:"minutes_logged,omitempty"`
	CreatedAt     string            `json:"created_at"`
}

type RequestAssignee struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
}

func (c *Client) ListRequests(clientID, status string) ([]ClientRequest, error) {
	q := url.Values{}
	if clientID != "" {
		q.Set("client_id", clientID)
	}
	if status != "" {
		q.Set("status", status)
	}
	path := "/api/v1/requests"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out []ClientRequest
	if err := c.doAuthed(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ClientRequest{}
	}
	return out, nil
}

func (c *Client) GetRequest(id string) (*ClientRequest, error) {
	var out ClientRequest
	if err := c.doAuthed(http.MethodGet, "/api/v1/requests/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListRequestHours(id string) ([]TimeEntry, error) {
	var out []TimeEntry
	if err := c.doAuthed(http.MethodGet, "/api/v1/requests/"+id+"/hours", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []TimeEntry{}
	}
	return out, nil
}

func (c *Client) UpdateRequestStatus(id, status string) (*ClientRequest, error) {
	var out ClientRequest
	if err := c.doAuthed(http.MethodPatch, "/api/v1/requests/"+id+"/status", map[string]string{"status": status}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AssignRequest(id, userID string) (*ClientRequest, error) {
	body := map[string]string{}
	if userID != "" {
		body["user_id"] = userID
	}
	var out ClientRequest
	if err := c.doAuthed(http.MethodPost, "/api/v1/requests/"+id+"/assignees", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UnassignRequest(id, userID string) (*ClientRequest, error) {
	if userID == "" {
		userID = "me"
	}
	var out ClientRequest
	if err := c.doAuthed(http.MethodDelete, "/api/v1/requests/"+id+"/assignees/"+userID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListRequestActivity(id string) ([]ActivityEvent, error) {
	q := url.Values{}
	q.Set("entity_type", "client_request")
	q.Set("entity_id", id)
	var out []ActivityEvent
	if err := c.doAuthed(http.MethodGet, "/api/v1/activity?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ActivityEvent{}
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
	c.mu.Lock()
	isPAT := c.creds != nil && c.creds.IsPAT()
	c.mu.Unlock()
	if isPAT {
		return ErrUnauthorized
	}
	if refreshErr := c.Refresh(); refreshErr != nil {
		return ErrUnauthorized
	}
	return c.do(method, path, body, true, out)
}

func (c *Client) do(method, path string, body any, authed bool, out any) error {
	var bodyBytes []byte
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyBytes = b
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
		priv := ""
		if c.creds != nil {
			tok = c.creds.AccessToken
			priv = c.creds.DevicePrivateKey
		}
		c.mu.Unlock()
		if tok == "" {
			return ErrUnauthorized
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if priv != "" {
			// Sign the path the server verifies (URL.Path), never the query string.
			ts, sig, err := devicekey.Sign(priv, method, req.URL.Path, bodyBytes)
			if err != nil {
				return err
			}
			req.Header.Set("X-ZT-Timestamp", ts)
			req.Header.Set("X-ZT-Signature", sig)
		}
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
