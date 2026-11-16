package kintone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAddTextFieldsPayload(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/k/v1/preview/app/form/fields.json" {
			t.Errorf("unexpected operation %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			App        string `json:"app"`
			Revision   string `json:"revision"`
			Properties map[string]struct {
				Type, Code, Label string
				Required, Unique  bool
			} `json:"properties"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		p := body.Properties["customer"]
		if body.App != "42" || body.Revision != "5" || len(body.Properties) != 1 || p.Type != "SINGLE_LINE_TEXT" || p.Code != "customer" || p.Label != "顧客" || !p.Required || !p.Unique {
			t.Errorf("wrong payload: %+v", body)
		}
		fmt.Fprint(w, `{"revision":"6"}`)
	})
	rev, err := c.AddTextFields(context.Background(), "42", "5", []TextField{{Code: "customer", Label: "顧客", Required: true, Unique: true}})
	if err != nil || rev != "6" || calls.Load() != 1 {
		t.Fatalf("revision=%q err=%v calls=%d", rev, err, calls.Load())
	}
}

func TestAddFixtureRecordsPayload(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/k/v1/records.json" {
			t.Errorf("unexpected operation %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			App     string                  `json:"app"`
			Records []map[string]FieldValue `json:"records"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.App != "42" || len(body.Records) != 2 || body.Records[0]["name"].Value != "架空顧客" || body.Records[1]["name"].Value != "" {
			t.Errorf("wrong payload: %+v", body)
		}
		fmt.Fprint(w, `{"ids":["10","11"],"revisions":["1","1"]}`)
	})
	result, err := c.AddFixtureRecords(context.Background(), "42", []map[string]FieldValue{{"name": {Value: "架空顧客"}}, {"name": {Value: ""}}})
	if err != nil || len(result) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	if result[0].ID != "10" || result[1].ID != "11" || result[0].Revision != "1" || calls.Load() != 1 {
		t.Fatalf("wrong identities: %+v", result)
	}
}

func TestFixtureValidationBeforeRequest(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input made request") })
	fields := []TextField{{Code: "name", Label: "Name"}}
	for _, revision := range []string{"", "-1", "1.5", " 1", "x"} {
		if _, err := c.AddTextFields(context.Background(), "1", revision, fields); err == nil {
			t.Errorf("accepted revision %q", revision)
		}
	}
	for _, f := range [][]TextField{nil, {{Code: "", Label: "Name"}}, {{Code: "name", Label: " "}}, {{Code: "name", Label: "Name"}, {Code: "name", Label: "Other"}}, {{Code: "\xff", Label: "Name"}}} {
		if _, err := c.AddTextFields(context.Background(), "1", "0", f); err == nil {
			t.Error("accepted invalid fields")
		}
	}
	if _, err := c.AddTextFields(context.Background(), "../1", "1", fields); err == nil {
		t.Error("accepted invalid app")
	}
	for _, records := range [][]map[string]FieldValue{nil, make([]map[string]FieldValue, 101), {nil}, {{}}, {{"": {Value: "x"}}}, {{"name": {Value: "\xff"}}}} {
		if _, err := c.AddFixtureRecords(context.Background(), "1", records); err == nil {
			t.Error("accepted invalid records")
		}
	}
	if _, err := c.AddFixtureRecords(context.Background(), "0", []map[string]FieldValue{{"name": {}}}); err == nil {
		t.Error("accepted invalid app")
	}
}

func TestFixtureResponseValidationNoRetry(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"ids":["1"],"revisions":[]}`, `{"ids":["0"],"revisions":["1"]}`, `{"ids":["1"],"revisions":["-1"]}`, `{"ids":["1","2"],"revisions":["1","1"]}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, body) })
			got, err := c.AddFixtureRecords(context.Background(), "1", []map[string]FieldValue{{"name": {Value: "fake"}}})
			if err == nil || got != nil || calls.Load() != 1 || !strings.Contains(err.Error(), "before retrying") {
				t.Fatalf("got=%v err=%v calls=%d", got, err, calls.Load())
			}
		})
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ids":["1","1"],"revisions":["1","1"]}`) })
	if _, err := c.AddFixtureRecords(context.Background(), "1", []map[string]FieldValue{{"n": {}}, {"n": {}}}); err == nil {
		t.Fatal("accepted duplicate IDs")
	}
}

func TestAddFieldsErrorsNoRetry(t *testing.T) {
	for _, status := range []int{200, 409, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"code":"GAIA_CO02","message":"secret fixture"}`)
			})
			_, err := c.AddTextFields(context.Background(), "1", "0", []TextField{{Code: "n", Label: "Name"}})
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "secret fixture") {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			if status != 200 {
				var api *APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("lost API error: %v", err)
				}
			}
		})
	}
}

func TestFixtureBatchUpperBoundary(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		ids := make([]string, 100)
		revisions := make([]string, 100)
		for i := range ids {
			ids[i] = fmt.Sprint(i + 1)
			revisions[i] = "1"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ids": ids, "revisions": revisions})
	})
	records := make([]map[string]FieldValue, 100)
	for i := range records {
		records[i] = map[string]FieldValue{"name": {Value: "synthetic"}}
	}
	if got, err := c.AddFixtureRecords(context.Background(), "1", records); err != nil || len(got) != 100 {
		t.Fatalf("count=%d err=%v", len(got), err)
	}
}

func TestFixtureInsertErrorNoRetry(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"code":"GAIA_ERROR","message":"private fixture value"}`)
	})
	got, err := c.AddFixtureRecords(context.Background(), "1", []map[string]FieldValue{{"name": {Value: "private fixture value"}}})
	var api *APIError
	if got != nil || !errors.As(err, &api) || api.StatusCode != 500 || calls.Load() != 1 || strings.Contains(err.Error(), "private fixture value") {
		t.Fatalf("got=%v err=%v calls=%d", got, err, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.AddFixtureRecords(ctx, "1", []map[string]FieldValue{{"name": {}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("canceled request sent")
	}
}
