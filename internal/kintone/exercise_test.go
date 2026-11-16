package kintone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func exerciseMock(t *testing.T, failAt string, steps *[]string) *Client {
	t.Helper()
	return testClient(t, func(w http.ResponseWriter, r *http.Request) {
		step := r.Method + " " + r.URL.Path
		*steps = append(*steps, step)
		if step == failAt {
			w.WriteHeader(409)
			fmt.Fprint(w, `{"code":"GAIA_CO02"}`)
			return
		}
		var body map[string]json.RawMessage
		if r.Method != "GET" {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		revision := func(want string) {
			var got string
			_ = json.Unmarshal(body["revision"], &got)
			if got != want {
				t.Errorf("%s revision=%q want=%q", step, got, want)
			}
		}
		switch step {
		case "POST /k/v1/preview/app.json":
			fmt.Fprint(w, `{"app":"42","revision":"0"}`)
		case "POST /k/v1/preview/app/form/fields.json":
			revision("0")
			fmt.Fprint(w, `{"revision":"1"}`)
		case "PUT /k/v1/preview/app/settings.json":
			revision("1")
			fmt.Fprint(w, `{"revision":"2"}`)
		case "PUT /k/v1/preview/app/acl.json":
			revision("2")
			fmt.Fprint(w, `{"revision":"3"}`)
		case "POST /k/v1/preview/app/deploy.json":
			if !strings.Contains(string(body["apps"]), `"revision":"3"`) {
				t.Error("wrong deployment revision")
			}
			fmt.Fprint(w, `{}`)
		case "GET /k/v1/preview/app/deploy.json":
			fmt.Fprint(w, `{"apps":[{"app":"42","status":"SUCCESS"}]}`)
		case "POST /k/v1/records.json":
			fmt.Fprint(w, `{"ids":["10"],"revisions":["1"]}`)
		case "GET /k/v1/records.json":
			if r.URL.Query().Get("app") != "42" || r.URL.Query().Get("query") != "$id in (10) limit 100" {
				t.Error("unbounded verification")
			}
			fmt.Fprint(w, `{"records":[{"$id":{"type":"__ID__","value":"10"},"name":{"type":"SINGLE_LINE_TEXT","value":"fake"}}]}`)
		default:
			t.Errorf("unexpected call: %s", step)
			w.WriteHeader(500)
		}
	})
}
func runExercise(c *Client, checkpoint func(ExerciseProgress) error) error {
	return c.InitExercise(context.Background(), "exercise", "", json.RawMessage(`[]`), []TextField{{Code: "name", Label: "Name"}}, []map[string]FieldValue{{"name": {Value: "fake"}}}, checkpoint)
}
func TestInitExerciseOrder(t *testing.T) {
	var steps []string
	c := exerciseMock(t, "", &steps)
	err := runExercise(c, func(p ExerciseProgress) error {
		steps = append(steps, p.Stage)
		if p.AppID != "42" {
			t.Error("missing saved ID")
		}
		if p.Stage == "records_created" && !reflect.DeepEqual(p.RecordIDs, []string{"10"}) {
			t.Error("missing record checkpoint")
		}
		return nil
	})
	want := []string{"POST /k/v1/preview/app.json", "app_created", "POST /k/v1/preview/app/form/fields.json", "fields_added", "PUT /k/v1/preview/app/settings.json", "PUT /k/v1/preview/app/acl.json", "POST /k/v1/preview/app/deploy.json", "GET /k/v1/preview/app/deploy.json", "deployed", "records_submitting", "POST /k/v1/records.json", "records_created", "GET /k/v1/records.json", "complete"}
	if err != nil || !reflect.DeepEqual(steps, want) {
		t.Fatalf("err=%v steps=%v", err, steps)
	}
}
func TestExerciseCheckpointStopsNextCall(t *testing.T) {
	for _, stage := range []string{"app_created", "fields_added", "deployed", "records_submitting", "records_created", "complete"} {
		t.Run(stage, func(t *testing.T) {
			var steps []string
			c := exerciseMock(t, "", &steps)
			sentinel := errors.New("checkpoint unavailable")
			err := runExercise(c, func(p ExerciseProgress) error {
				steps = append(steps, p.Stage)
				if p.Stage == stage {
					return sentinel
				}
				return nil
			})
			if !errors.Is(err, sentinel) || steps[len(steps)-1] != stage {
				t.Fatalf("err=%v steps=%v", err, steps)
			}
		})
	}
}
func TestExerciseRevisionConflictStops(t *testing.T) {
	for _, step := range []string{"POST /k/v1/preview/app/form/fields.json", "PUT /k/v1/preview/app/settings.json", "PUT /k/v1/preview/app/acl.json", "POST /k/v1/preview/app/deploy.json"} {
		t.Run(step, func(t *testing.T) {
			var steps []string
			c := exerciseMock(t, step, &steps)
			err := runExercise(c, func(ExerciseProgress) error { return nil })
			if err == nil || steps[len(steps)-1] != step {
				t.Fatalf("err=%v steps=%v", err, steps)
			}
		})
	}
}
func TestExerciseMissingCreationRevisionPreservesID(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"app":"42","revision":"bad"}`) })
	var saved ExerciseProgress
	err := runExercise(c, func(p ExerciseProgress) error { saved = p; return nil })
	if err == nil || saved.AppID != "42" || saved.Stage != "app_created" || calls != 1 {
		t.Fatalf("%v %+v calls=%d", err, saved, calls)
	}
}
func TestExerciseValidationBeforeCreation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("invalid fixtures created app") })
	cb := func(ExerciseProgress) error { return nil }
	if err := runExercise(c, nil); err == nil {
		t.Fatal("nil checkpoint accepted")
	}
	if err := c.InitExercise(context.Background(), "name", "", json.RawMessage(`[]`), nil, []map[string]FieldValue{{"unknown": {Value: "fake"}}}, cb); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := c.InitExercise(context.Background(), "name", "", json.RawMessage(`[]`), []TextField{{Code: "name", Label: "Name", Required: true}}, []map[string]FieldValue{{"name": {}}}, cb); err == nil {
		t.Fatal("missing required value accepted")
	}
}
func TestVerifyFixtureFailures(t *testing.T) {
	for _, body := range []string{`{}`, `{"records":[]}`, `{"records":[{"$id":{"value":"99"}}]}`, `{"records":[{"$id":{"value":"10"},"name":{"type":"SINGLE_LINE_TEXT","value":"wrong"}}]}`, `{"records":[{"$id":{"value":"10"},"name":{"type":"NUMBER","value":"fake"}}]}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			if err := c.verifyFixtureRecords(context.Background(), "42", []string{"10"}, []map[string]FieldValue{{"name": {Value: "fake"}}}); err == nil {
				t.Fatal("verification accepted malformed result")
			}
		})
	}
}

func TestExerciseEmptyFixtures(t *testing.T) {
	var steps []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		step := r.Method + " " + r.URL.Path
		steps = append(steps, step)
		switch step {
		case "POST /k/v1/preview/app.json":
			fmt.Fprint(w, `{"app":"42","revision":"0"}`)
		case "PUT /k/v1/preview/app/settings.json":
			var body struct{ Revision string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Revision != "0" {
				t.Error("creation revision not used")
			}
			fmt.Fprint(w, `{"revision":"1"}`)
		case "PUT /k/v1/preview/app/acl.json":
			fmt.Fprint(w, `{"revision":"2"}`)
		case "POST /k/v1/preview/app/deploy.json":
			fmt.Fprint(w, `{}`)
		case "GET /k/v1/preview/app/deploy.json":
			fmt.Fprint(w, `{"apps":[{"app":"42","status":"SUCCESS"}]}`)
		default:
			t.Errorf("unexpected call %s", step)
			w.WriteHeader(500)
		}
	})
	var stages []string
	err := c.InitExercise(context.Background(), "empty", "", json.RawMessage(`[]`), nil, nil, func(p ExerciseProgress) error { stages = append(stages, p.Stage); return nil })
	if err != nil || !reflect.DeepEqual(stages, []string{"app_created", "deployed", "complete"}) || len(steps) != 5 {
		t.Fatalf("err=%v stages=%v calls=%v", err, stages, steps)
	}
}

func TestExerciseMalformedRecordResultStopsBeforeVerification(t *testing.T) {
	var steps []string
	c := exerciseMock(t, "", &steps)
	transport := c.httpClient.Transport
	c.httpClient.Transport = exerciseRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && r.URL.Path == "/k/v1/records.json" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ids":[],"revisions":[]}`)), Header: make(http.Header), Request: r}, nil
		}
		return transport.RoundTrip(r)
	})
	var last ExerciseProgress
	err := runExercise(c, func(p ExerciseProgress) error { last = p; return nil })
	if err == nil || last.Stage != "records_submitting" || len(last.RecordIDs) != 0 {
		t.Fatalf("err=%v checkpoint=%+v", err, last)
	}
	for _, step := range steps {
		if step == "GET /k/v1/records.json" {
			t.Fatal("verified unknown record IDs")
		}
	}
}

type exerciseRoundTripper func(*http.Request) (*http.Response, error)

func (f exerciseRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
