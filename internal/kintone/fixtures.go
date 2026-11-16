package kintone

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// TextField describes a plain SINGLE_LINE_TEXT field, without formulas or lookups.
type TextField struct {
	Code     string
	Label    string
	Required bool
	Unique   bool
}

// FieldValue intentionally supports strings only, not arbitrary JSON payloads.
type FieldValue struct {
	Value string `json:"value"`
}

// RecordIdentity identifies one inserted record, in request order.
type RecordIdentity struct {
	ID       string
	Revision string
}

// AddTextFields adds preview fields using optimistic concurrency. It never
// deploys or deletes anything. The caller must reconcile existing pending edits
// before deployment. A failed response can leave an unknown outcome; inspect
// preview settings before retrying rather than blindly repeating the mutation.
// API: https://cybozu.dev/ja/kintone/docs/rest-api/apps/form/add-form-fields/
func (c *Client) AddTextFields(ctx context.Context, appID, expectedRevision string, fields []TextField) (string, error) {
	if err := validID(appID); err != nil {
		return "", err
	}
	if !revisionPattern.MatchString(expectedRevision) {
		return "", errors.New("expected revision must be a non-negative decimal integer; unconditional updates are not allowed")
	}
	if len(fields) == 0 {
		return "", errors.New("at least one text field is required")
	}
	type property struct {
		Type     string `json:"type"`
		Code     string `json:"code"`
		Label    string `json:"label"`
		Required bool   `json:"required"`
		Unique   bool   `json:"unique"`
	}
	properties := make(map[string]property, len(fields))
	for _, f := range fields {
		if !validFixtureText(f.Code) || !validFixtureText(f.Label) {
			return "", errors.New("field code and label must be nonblank valid UTF-8 strings")
		}
		if _, exists := properties[f.Code]; exists {
			return "", errors.New("duplicate field code")
		}
		properties[f.Code] = property{"SINGLE_LINE_TEXT", f.Code, f.Label, f.Required, f.Unique}
	}
	var response struct {
		Revision numberString `json:"revision"`
	}
	if err := c.do(ctx, "POST", "/k/v1/preview/app/form/fields.json", nil, map[string]any{"app": appID, "revision": expectedRevision, "properties": properties}, &response); err != nil {
		return "", fmt.Errorf("add text fields failed; inspect preview settings before retrying: %w", err)
	}
	if !revisionPattern.MatchString(string(response.Revision)) {
		return "", errors.New("field response omitted revision; outcome unknown, inspect preview settings before retrying")
	}
	return string(response.Revision), nil
}

func validFixtureText(s string) bool { return utf8.ValidString(s) && strings.TrimSpace(s) != "" }

// AddFixtureRecords inserts 1..100 synthetic fixture records into a deployed
// app. It never retries: a timeout or invalid response may mean records already
// exist, so inspect the tenant before retrying to avoid duplicates. The caller
// must verify field codes and types against the deployed schema beforehand:
// kintone silently ignores unknown field codes. Returned IDs do not prove that
// every supplied field was persisted. This method neither reads nor deletes data.
// API: https://cybozu.dev/ja/kintone/docs/rest-api/records/add-records/
func (c *Client) AddFixtureRecords(ctx context.Context, appID string, records []map[string]FieldValue) ([]RecordIdentity, error) {
	if err := validID(appID); err != nil {
		return nil, err
	}
	if len(records) < 1 || len(records) > 100 {
		return nil, errors.New("fixture batch must contain 1..100 records")
	}
	for _, record := range records {
		if len(record) == 0 {
			return nil, errors.New("each fixture record must contain at least one field")
		}
		for code, value := range record {
			if !validFixtureText(code) || !utf8.ValidString(value.Value) {
				return nil, errors.New("fixture field codes must be nonblank and all strings must be valid UTF-8")
			}
		}
	}
	var response struct {
		IDs       []numberString `json:"ids"`
		Revisions []numberString `json:"revisions"`
	}
	if err := c.do(ctx, "POST", "/k/v1/records.json", nil, map[string]any{"app": appID, "records": records}, &response); err != nil {
		return nil, fmt.Errorf("fixture insert failed; outcome may be unknown, inspect records before retrying: %w", err)
	}
	invalid := errors.New("invalid fixture insert response; outcome unknown, inspect records before retrying")
	if len(response.IDs) != len(records) || len(response.Revisions) != len(records) {
		return nil, invalid
	}
	result := make([]RecordIdentity, len(records))
	seen := make(map[string]bool, len(records))
	for i, id := range response.IDs {
		s := string(id)
		if validID(s) != nil || !revisionPattern.MatchString(string(response.Revisions[i])) || seen[s] {
			return nil, invalid
		}
		seen[s] = true
		result[i] = RecordIdentity{s, string(response.Revisions[i])}
	}
	return result, nil
}
