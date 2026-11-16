package kintone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ExerciseProgress is passed to the caller for persistence. The callback does
// not itself guarantee durability: Terraform response state survives a normal
// return, not necessarily a crash. Crash recovery requires an external journal.
// Stages are app_created, fields_added, deployed, records_submitting,
// records_created, complete. A records_submitting failure has unknown outcome.
type ExerciseProgress struct {
	AppID     string
	RecordIDs []string
	Stage     string
}

// InitExercise is a one-shot creation workflow, never a resume or retry API.
// Partial resources remain for manual reconciliation; nothing is rolled back.
// A checkpoint failure stops all subsequent API calls. Keep the credentials'
// record/field read permissions so the final synthetic-data verification works.
func (c *Client) InitExercise(ctx context.Context, name, description string, rights json.RawMessage, fields []TextField, records []map[string]FieldValue, checkpoint func(ExerciseProgress) error) error {
	if checkpoint == nil {
		return errors.New("checkpoint callback is required")
	}
	if !validFixtureText(name) || utf8.RuneCountInString(name) > 64 || !utf8.ValidString(description) || utf8.RuneCountInString(description) > 10000 {
		return errors.New("invalid app name or description")
	}
	if err := validateRights(rights); err != nil {
		return err
	}
	if err := validateExerciseFixtures(fields, records); err != nil {
		return err
	}
	progress := ExerciseProgress{RecordIDs: []string{}}
	save := func(stage string) error {
		progress.Stage = stage
		copy := progress
		copy.RecordIDs = append([]string{}, progress.RecordIDs...)
		if err := checkpoint(copy); err != nil {
			return fmt.Errorf("checkpoint failed at %s; stop and reconcile existing resources: %w", stage, err)
		}
		return nil
	}
	id, revision, err := c.createAppWithRevision(ctx, name)
	if err != nil {
		return fmt.Errorf("app creation failed; outcome may be unknown, inspect tenant before retrying: %w", err)
	}
	progress.AppID = id
	if err = save("app_created"); err != nil {
		return err
	}
	if !revisionPattern.MatchString(revision) {
		return errors.New("created app revision missing or invalid; app ID checkpointed, reconcile manually without retrying")
	}
	if len(fields) > 0 {
		revision, err = c.AddTextFields(ctx, id, revision, fields)
		if err != nil {
			return err
		}
		if err = save("fields_added"); err != nil {
			return err
		}
	}
	if err = c.configureAtRevision(ctx, id, name, description, rights, revision); err != nil {
		return fmt.Errorf("exercise configuration failed; app retained for manual reconciliation: %w", err)
	}
	if err = save("deployed"); err != nil {
		return err
	}
	if len(records) > 0 {
		if err = save("records_submitting"); err != nil {
			return err
		}
		identities, err := c.AddFixtureRecords(ctx, id, records)
		if err != nil {
			return err
		}
		for _, identity := range identities {
			progress.RecordIDs = append(progress.RecordIDs, identity.ID)
		}
		if err = save("records_created"); err != nil {
			return err
		}
		if err = c.verifyFixtureRecords(ctx, id, progress.RecordIDs, records); err != nil {
			return fmt.Errorf("records were created and IDs checkpointed but verification failed; do not reinsert: %w", err)
		}
	}
	return save("complete")
}

func validateExerciseFixtures(fields []TextField, records []map[string]FieldValue) error {
	known := map[string]TextField{}
	for _, f := range fields {
		if !validFixtureText(f.Code) || !validFixtureText(f.Label) || strings.HasPrefix(f.Code, "$") {
			return errors.New("invalid fixture field code or label")
		}
		if _, ok := known[f.Code]; ok {
			return errors.New("duplicate fixture field code")
		}
		known[f.Code] = f
	}
	if len(records) > 100 {
		return errors.New("at most 100 fixture records are allowed")
	}
	unique := map[string]map[string]bool{}
	for _, record := range records {
		if len(record) == 0 {
			return errors.New("fixture records must not be empty")
		}
		for code, value := range record {
			f, ok := known[code]
			if !ok {
				return errors.New("fixture record references a field not declared in initial fields")
			}
			if !utf8.ValidString(value.Value) {
				return errors.New("fixture values must be valid UTF-8")
			}
			if f.Unique {
				if unique[code] == nil {
					unique[code] = map[string]bool{}
				}
				if unique[code][value.Value] {
					return errors.New("duplicate fixture value for a unique field")
				}
				unique[code][value.Value] = true
			}
		}
		for code, f := range known {
			if f.Required && record[code].Value == "" {
				return errors.New("fixture record omits a required value")
			}
		}
	}
	return nil
}

// Only reads records whose IDs were returned by this operation, never all data.
// https://cybozu.dev/ja/kintone/docs/rest-api/records/get-records/
func (c *Client) verifyFixtureRecords(ctx context.Context, appID string, ids []string, expected []map[string]FieldValue) error {
	if len(ids) == 0 || len(ids) != len(expected) || len(ids) > 100 {
		return errors.New("invalid verification identity set")
	}
	wanted := map[string]int{}
	for i, id := range ids {
		if validID(id) != nil {
			return errors.New("invalid verification record ID")
		}
		wanted[id] = i
	}
	var response struct {
		Records []map[string]struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"records"`
	}
	q := url.Values{"app": {appID}, "query": {"$id in (" + strings.Join(ids, ",") + ") limit 100"}}
	if err := c.do(ctx, "GET", "/k/v1/records.json", q, nil, &response); err != nil {
		return err
	}
	if len(response.Records) != len(ids) {
		return errors.New("verification record count mismatch")
	}
	seen := map[string]bool{}
	for _, record := range response.Records {
		var id string
		if json.Unmarshal(record["$id"].Value, &id) != nil {
			return errors.New("verification response omitted record ID")
		}
		index, ok := wanted[id]
		if !ok || seen[id] {
			return errors.New("verification returned unexpected or duplicate record ID")
		}
		seen[id] = true
		for code, value := range expected[index] {
			actual, ok := record[code]
			var got string
			if !ok || actual.Type != "SINGLE_LINE_TEXT" || json.Unmarshal(actual.Value, &got) != nil || got != value.Value {
				return errors.New("fixture field missing, wrong type, or value mismatch")
			}
		}
	}
	return nil
}
