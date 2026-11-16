// Package kintone implements the small API surface used by the provider.
package kintone

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

var idPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
var revisionPattern = regexp.MustCompile(`^[0-9]+$`)
var errorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// APIError intentionally excludes response messages, which can contain input data.
type APIError struct {
	StatusCode int
	Code       string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("kintone API returned HTTP %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("kintone API returned HTTP %d", e.StatusCode)
}

// Client supports standard (non-guest-space) HTTPS domains and password auth.
// Credentials are kept in memory, never put in URLs or returned errors.
type Client struct {
	baseURL       string
	auth          string
	httpClient    *http.Client
	pollInterval  time.Duration
	deployTimeout time.Duration
}

func NewClient(baseURL, username, password string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("base URL must be an HTTPS origin without credentials, path, query, or fragment")
	}
	if username == "" || password == "" || strings.Contains(username, ":") {
		return nil, errors.New("username and password are required; username must not contain a colon")
	}
	return &Client{baseURL: strings.TrimSuffix(u.String(), "/"), auth: base64.StdEncoding.EncodeToString([]byte(username + ":" + password)), httpClient: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, pollInterval: time.Second, deployTimeout: 5 * time.Minute}, nil
}

// numberString accepts the string and number forms allowed by the API.
type numberString string

func (n *numberString) UnmarshalJSON(b []byte) error {
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		s = string(b)
	}
	if !revisionPattern.MatchString(s) {
		return errors.New("invalid numeric API value")
	}
	*n = numberString(s)
	return nil
}
func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	// All mutations must target the preview API. A live ACL PUT can also
	// publish unrelated pending settings; never introduce it as a shortcut.
	if method != http.MethodGet && !strings.HasPrefix(path, "/k/v1/preview/") {
		return errors.New("mutating live app endpoints is prohibited; use revision-guarded preview deployment")
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return errors.New("invalid request payload")
		}
		body = bytes.NewReader(b)
	}
	endpoint := c.baseURL + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("could not construct API request")
	}
	req.Header.Set("X-Cybozu-Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("kintone API transport failed (check connectivity, TLS, and timeout)")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("could not read API response")
	}
	if len(data) > maxResponseBytes {
		return errors.New("API response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		if !errorCodePattern.MatchString(e.Code) {
			e.Code = ""
		}
		return &APIError{resp.StatusCode, e.Code}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("invalid JSON API response")
		}
	}
	return nil
}
func validID(id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("app ID must be a positive decimal integer")
	}
	return nil
}
func (c *Client) CreateApp(ctx context.Context, name string) (string, error) {
	var r struct {
		App numberString `json:"app"`
	}
	err := c.do(ctx, "POST", "/k/v1/preview/app.json", nil, map[string]any{"name": name}, &r)
	if err != nil {
		return "", err
	}
	if err = validID(string(r.App)); err != nil {
		return "", errors.New("create response omitted a valid app ID; inspect the tenant before retrying")
	}
	return string(r.App), nil
}

type appSettings struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Revision    numberString `json:"revision"`
}

func (c *Client) settings(ctx context.Context, id string, preview bool) (appSettings, error) {
	var r appSettings
	if err := validID(id); err != nil {
		return r, err
	}
	path := "/k/v1/app/settings.json"
	if preview {
		path = "/k/v1/preview/app/settings.json"
	}
	err := c.do(ctx, "GET", path, url.Values{"app": {id}}, nil, &r)
	if err == nil && !revisionPattern.MatchString(string(r.Revision)) {
		err = errors.New("settings response omitted revision")
	}
	return r, err
}

// ReadApp reads deployed settings, not unpublished preview changes.
func (c *Client) ReadApp(ctx context.Context, id string) (string, string, error) {
	r, err := c.settings(ctx, id, false)
	return r.Name, r.Description, err
}
func (c *Client) ReadRights(ctx context.Context, id string) (json.RawMessage, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	var r struct {
		Rights json.RawMessage `json:"rights"`
	}
	if err := c.do(ctx, "GET", "/k/v1/app/acl.json", url.Values{"app": {id}}, nil, &r); err != nil {
		return nil, err
	}
	if err := validateRights(r.Rights); err != nil {
		return nil, errors.New("ACL response omitted a valid rights array")
	}
	return r.Rights, nil
}
func validateRights(rights json.RawMessage) error {
	var rows []json.RawMessage
	if err := json.Unmarshal(rights, &rows); err != nil || rows == nil {
		return errors.New("rights must be a JSON array")
	}
	for _, row := range rows {
		var obj map[string]json.RawMessage
		if json.Unmarshal(row, &obj) != nil || obj == nil {
			return errors.New("each rights entry must be an object")
		}
	}
	return nil
}

// ConfigureNewApp must only be used for an app just created by this operation.
// It skips the live/preview revision comparison because the app is unpublished.
func (c *Client) ConfigureNewApp(ctx context.Context, id, name, description string, rights json.RawMessage) error {
	return c.configure(ctx, id, name, description, rights, true)
}

// UpdateApp refuses to deploy pre-existing unpublished changes. Deployment
// publishes every preview setting, not just the fields owned by this provider.
func (c *Client) UpdateApp(ctx context.Context, id, name, description string, rights json.RawMessage) error {
	return c.configure(ctx, id, name, description, rights, false)
}
func (c *Client) configure(ctx context.Context, id, name, description string, rights json.RawMessage, newApp bool) error {
	if err := validateRights(rights); err != nil {
		return err
	}
	preview, err := c.settings(ctx, id, true)
	if err != nil {
		return err
	}
	if !newApp {
		live, err := c.settings(ctx, id, false)
		if err != nil {
			return err
		}
		if preview.Revision != live.Revision {
			return errors.New("unpublished app settings exist; reconcile them in kintone before applying (deployment would publish all pending changes)")
		}
	}
	var revision struct {
		Revision numberString `json:"revision"`
	}
	if err = c.do(ctx, "PUT", "/k/v1/preview/app/settings.json", nil, map[string]any{"app": id, "name": name, "description": description, "revision": string(preview.Revision)}, &revision); err != nil {
		return err
	}
	if !revisionPattern.MatchString(string(revision.Revision)) {
		return errors.New("settings update response omitted revision; inspect pending settings before retrying")
	}
	next := revision.Revision
	revision.Revision = ""
	if err = c.do(ctx, "PUT", "/k/v1/preview/app/acl.json", nil, map[string]any{"app": id, "rights": rights, "revision": string(next)}, &revision); err != nil {
		return err
	}
	if !revisionPattern.MatchString(string(revision.Revision)) {
		return errors.New("ACL update response omitted revision; inspect pending settings before retrying")
	}
	ctx, cancel := context.WithTimeout(ctx, c.deployTimeout)
	defer cancel()
	if err = c.do(ctx, "POST", "/k/v1/preview/app/deploy.json", nil, map[string]any{"apps": []map[string]string{{"app": id, "revision": string(revision.Revision)}}}, nil); err != nil {
		return err
	}
	for {
		var r struct {
			Apps []struct {
				App    numberString `json:"app"`
				Status string       `json:"status"`
			} `json:"apps"`
		}
		if err = c.do(ctx, "GET", "/k/v1/preview/app/deploy.json", url.Values{"apps[0]": {id}}, nil, &r); err != nil {
			return err
		}
		if len(r.Apps) != 1 || string(r.Apps[0].App) != id {
			return errors.New("deployment response did not identify the requested app")
		}
		switch r.Apps[0].Status {
		case "SUCCESS":
			return nil
		case "FAIL", "CANCEL":
			return fmt.Errorf("app deployment ended with %s; inspect pending settings before retrying", r.Apps[0].Status)
		case "PROCESSING":
		default:
			return errors.New("unrecognized app deployment status")
		}
		timer := time.NewTimer(c.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
