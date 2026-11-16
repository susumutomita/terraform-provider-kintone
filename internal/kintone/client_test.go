package kintone

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c, err := NewClient(s.URL, "operator", "fake-test-password")
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient.Transport = s.Client().Transport
	c.pollInterval = time.Millisecond
	c.deployTimeout = time.Second
	return c
}
func TestNewClientRejectsUnsafeOrigins(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://u:p@example.com", "https://example.com/path", "https://example.com?secret=x", "https://example.com#f", "file:///tmp/kintone", "https:///"} {
		t.Run(u, func(t *testing.T) {
			if _, err := NewClient(u, "u", "p"); err == nil {
				t.Fatal("accepted unsafe origin")
			}
		})
	}
	if _, err := NewClient("https://example.com", "a:b", "p"); err == nil {
		t.Fatal("accepted ambiguous auth")
	}
}
func TestCreateAndRead(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Cybozu-Authorization") != base64.StdEncoding.EncodeToString([]byte("operator:fake-test-password")) {
			t.Error("wrong auth")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /k/v1/preview/app.json":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "Demo" {
				t.Error(body)
			}
			fmt.Fprint(w, `{"app":"42","revision":"1"}`)
		case "GET /k/v1/app/settings.json":
			if r.URL.Query().Get("app") != "42" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, `{"name":"Demo","description":"test","revision":"7"}`)
		case "GET /k/v1/app/acl.json":
			fmt.Fprint(w, `{"rights":[{"entity":{"type":"CREATOR"},"appEditable":true}],"revision":"7"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	})
	id, err := c.CreateApp(context.Background(), "Demo")
	if err != nil || id != "42" {
		t.Fatalf("%q %v", id, err)
	}
	n, d, err := c.ReadApp(context.Background(), id)
	if err != nil || n != "Demo" || d != "test" {
		t.Fatalf("%s %s %v", n, d, err)
	}
	rights, err := c.ReadRights(context.Background(), id)
	if err != nil || !strings.Contains(string(rights), "CREATOR") {
		t.Fatalf("%s %v", rights, err)
	}
}
func TestUpdateChainsRevisionsAndPolls(t *testing.T) {
	var sequence []string
	var polls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		sequence = append(sequence, key)
		switch key {
		case "GET /k/v1/preview/app/settings.json", "GET /k/v1/app/settings.json":
			fmt.Fprint(w, `{"name":"old","description":"old","revision":"5"}`)
		case "PUT /k/v1/preview/app/settings.json":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["revision"] != "5" || b["name"] != "new" || b["description"] != "desc" {
				t.Error(b)
			}
			fmt.Fprint(w, `{"revision":"6"}`)
		case "PUT /k/v1/preview/app/acl.json":
			var b map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&b)
			if string(b["revision"]) != `"6"` || string(b["rights"]) != `[]` {
				t.Error(b)
			}
			fmt.Fprint(w, `{"revision":"7"}`)
		case "POST /k/v1/preview/app/deploy.json":
			var b struct {
				Apps []map[string]string `json:"apps"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if len(b.Apps) != 1 || b.Apps[0]["revision"] != "7" || b.Apps[0]["app"] != "42" {
				t.Error(b)
			}
			fmt.Fprint(w, `{}`)
		case "GET /k/v1/preview/app/deploy.json":
			if r.URL.Query().Get("apps[0]") != "42" {
				t.Error(r.URL)
			}
			polls++
			status := "PROCESSING"
			if polls == 2 {
				status = "SUCCESS"
			}
			fmt.Fprintf(w, `{"apps":[{"app":"42","status":%q}]}`, status)
		default:
			t.Error(key)
			w.WriteHeader(500)
		}
	})
	if err := c.UpdateApp(context.Background(), "42", "new", "desc", json.RawMessage(`[]`)); err != nil {
		t.Fatal(err)
	}
	if len(sequence) != 7 || polls != 2 {
		t.Fatalf("sequence %#v", sequence)
	}
}
func TestUnpublishedChangesBlockAllWrites(t *testing.T) {
	var writes int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
			t.Error("unexpected write")
		}
		rev := 5
		if strings.Contains(r.URL.Path, "preview") {
			rev = 6
		}
		fmt.Fprintf(w, `{"name":"a","description":"b","revision":"%d"}`, rev)
	})
	err := c.UpdateApp(context.Background(), "42", "a", "b", json.RawMessage(`[]`))
	if err == nil || !strings.Contains(err.Error(), "unpublished") || writes != 0 {
		t.Fatalf("%v writes=%d", err, writes)
	}
}
func TestConfigureNewDoesNotReadLive(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/k/v1/app/settings.json" {
			t.Error("read unpublished live settings")
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "settings.json"):
			fmt.Fprint(w, `{"name":"a","revision":"1"}`)
		case r.Method == "PUT":
			fmt.Fprint(w, `{"revision":"2"}`)
		case r.Method == "POST":
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprint(w, `{"apps":[{"app":"42","status":"SUCCESS"}]}`)
		}
	})
	if err := c.ConfigureNewApp(context.Background(), "42", "a", "", json.RawMessage(`[]`)); err != nil {
		t.Fatal(err)
	}
}
func TestAPIErrorRedactsResponse(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"code":"GAIA_NO01","message":"fake-test-password","errors":{"x":"secret"}}`)
	})
	_, _, err := c.ReadApp(context.Background(), "42")
	var e *APIError
	if !errors.As(err, &e) || e.StatusCode != 403 || e.Code != "GAIA_NO01" {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatal("leaked response")
	}
}
func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	var received atomic.Bool
	dest := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Store(true) }))
	defer dest.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	})
	_, _, err := c.ReadApp(context.Background(), "42")
	var e *APIError
	if !errors.As(err, &e) || e.StatusCode != 307 || received.Load() {
		t.Fatalf("err=%v forwarded=%v", err, received.Load())
	}
}
func TestMalformedResponsesFail(t *testing.T) {
	for _, body := range []string{`{}`, `not-json`, `{"app":"0"}`, `{"app":"not-an-id"}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			if _, err := c.CreateApp(context.Background(), "a"); err == nil {
				t.Fatal("accepted malformed creation response")
			}
		})
	}
}
func TestDeployFailureAndTimeout(t *testing.T) {
	for _, status := range []string{"FAIL", "CANCEL", "UNKNOWN", "PROCESSING", "MISSING"} {
		t.Run(status, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "settings.json"):
					fmt.Fprint(w, `{"revision":"1"}`)
				case strings.HasSuffix(r.URL.Path, "acl.json"):
					fmt.Fprint(w, `{"revision":"2"}`)
				case r.Method == "POST":
					fmt.Fprint(w, `{}`)
				case status == "MISSING":
					fmt.Fprint(w, `{"apps":[]}`)
				default:
					fmt.Fprintf(w, `{"apps":[{"app":"42","status":%q}]}`, status)
				}
			})
			c.deployTimeout = 20 * time.Millisecond
			err := c.ConfigureNewApp(context.Background(), "42", "a", "", json.RawMessage(`[]`))
			if err == nil {
				t.Fatal("accepted invalid deploy status")
			}
			if status == "PROCESSING" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}
func TestInvalidInputMakesNoRequests(t *testing.T) {
	var count int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { count++ })
	if _, _, err := c.ReadApp(context.Background(), "42&other=1"); err == nil {
		t.Fatal("invalid id accepted")
	}
	for _, v := range []string{`null`, `{}`, `[null]`, `[1]`} {
		if err := c.ConfigureNewApp(context.Background(), "42", "a", "", json.RawMessage(v)); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	if count != 0 {
		t.Fatal(count)
	}
}
func TestRevisionConflictStopsBeforeDeploy(t *testing.T) {
	var writes int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"revision":"5"}`)
			return
		}
		writes++
		w.WriteHeader(409)
		fmt.Fprint(w, `{"code":"GAIA_CO02"}`)
	})
	err := c.UpdateApp(context.Background(), "42", "a", "", json.RawMessage(`[]`))
	if err == nil || writes != 1 {
		t.Fatalf("%v writes=%d", err, writes)
	}
}
func TestMissingRevisionStopsBeforeNextWrite(t *testing.T) {
	var writes int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"revision":"5"}`)
			return
		}
		writes++
		fmt.Fprint(w, `{}`)
	})
	err := c.UpdateApp(context.Background(), "42", "a", "", json.RawMessage(`[]`))
	if err == nil || writes != 1 {
		t.Fatalf("%v writes=%d", err, writes)
	}
}
func TestCancelledContext(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("request after cancellation") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.ReadApp(ctx, "42")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestOversizedResponseRejected(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1)) })
	_, _, err := c.ReadApp(context.Background(), "42")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatal(err)
	}
}

func TestLiveMutationRejectedWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, method := range []string{"PUT", "POST", "DELETE"} {
		err := c.do(context.Background(), method, "/k/v1/app/acl.json", nil, map[string]any{"app": "42"}, nil)
		if err == nil {
			t.Fatal("live mutation accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("sent a live mutation")
	}
}

// Model an external preview edit just before each mutation. Each API must
// compare the exact previous revision; no retries, forced deploy or revert.
func TestConcurrentEditAtEveryMutationStopsWorkflow(t *testing.T) {
	for _, conflictAt := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("write_%d", conflictAt), func(t *testing.T) {
			var writes int
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if !strings.HasSuffix(r.URL.Path, "settings.json") {
						t.Error("polled after rejected deploy")
					}
					fmt.Fprint(w, `{"revision":"5"}`)
					return
				}
				writes++
				paths := []string{"/k/v1/preview/app/settings.json", "/k/v1/preview/app/acl.json", "/k/v1/preview/app/deploy.json"}
				if writes > 3 || r.URL.Path != paths[writes-1] {
					t.Errorf("unexpected mutation %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				var revision any = body["revision"]
				if writes == 3 {
					apps, ok := body["apps"].([]any)
					if !ok || len(apps) != 1 {
						t.Error("invalid deployment body")
						w.WriteHeader(500)
						return
					}
					revision = apps[0].(map[string]any)["revision"]
					if _, ok := body["revert"]; ok {
						t.Error("must not revert external changes")
					}
				}
				if revision != fmt.Sprint(4+writes) {
					t.Errorf("unguarded or stale revision: %#v", revision)
				}
				if writes == conflictAt {
					w.WriteHeader(409)
					fmt.Fprint(w, `{"code":"GAIA_CO02"}`)
					return
				}
				fmt.Fprintf(w, `{"revision":"%d"}`, 5+writes)
			})
			err := c.UpdateApp(context.Background(), "42", "new", "", json.RawMessage(`[]`))
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 409 || writes != conflictAt {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
		})
	}
}

func TestFixtureExceptionDoesNotAllowOtherLiveMutations(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, request := range []struct{ method, path string }{
		{"PUT", "/k/v1/records.json"}, {"DELETE", "/k/v1/records.json"},
		{"POST", "/k/v1/record.json"}, {"POST", "/k/v1/app/acl.json"},
		{"POST", "/k/v1/records.json/"},
	} {
		if err := c.do(context.Background(), request.method, request.path, nil, nil, nil); err == nil {
			t.Fatalf("allowed %s %s", request.method, request.path)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unexpected live request")
	}
}
