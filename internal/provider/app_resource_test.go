package provider

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/susumutomita/terraform-provider-kintone/internal/kintone"
	"testing"
)

func model(t *testing.T) appModel {
	t.Helper()
	rr := rightModel{Type: types.StringValue("USER"), Code: types.StringValue("operator"), IncludeSubs: types.BoolValue(false), AppEditable: types.BoolValue(true), View: types.BoolValue(true), Add: types.BoolValue(false), Edit: types.BoolValue(false), Delete: types.BoolValue(false), Import: types.BoolValue(false), Export: types.BoolValue(false)}
	list, ds := types.ListValueFrom(context.Background(), rightType(), []rightModel{rr})
	if ds.HasError() {
		t.Fatal(ds)
	}
	return appModel{ID: types.StringUnknown(), Name: types.StringValue("Exercise"), Description: types.StringValue(""), Rights: list, Retain: types.BoolValue(false)}
}
func TestPayload(t *testing.T) {
	m := model(t)
	b, ds := payload(context.Background(), m)
	if ds.HasError() {
		t.Fatal(ds)
	}
	var rs []right
	if e := json.Unmarshal(b, &rs); e != nil {
		t.Fatal(e)
	}
	if len(rs) != 1 || rs[0].Entity.Code != "operator" || !rs[0].AppEditable || rs[0].Delete {
		t.Fatalf("unexpected payload %s", b)
	}
}
func TestInvalidACL(t *testing.T) {
	for _, typ := range []string{"INVALID", "CREATOR"} {
		t.Run(typ, func(t *testing.T) {
			m := model(t)
			var rs []rightModel
			m.Rights.ElementsAs(context.Background(), &rs, false)
			rs[0].Type = types.StringValue(typ)
			m.Rights, _ = types.ListValueFrom(context.Background(), rightType(), rs)
			_, ds := payload(context.Background(), m)
			if !ds.HasError() {
				t.Fatal("expected validation error")
			}
		})
	}
}
func TestDuplicateACL(t *testing.T) {
	m := model(t)
	var rs []rightModel
	m.Rights.ElementsAs(context.Background(), &rs, false)
	rs = append(rs, rs[0])
	m.Rights, _ = types.ListValueFrom(context.Background(), rightType(), rs)
	_, ds := payload(context.Background(), m)
	if !ds.HasError() {
		t.Fatal("expected duplicate error")
	}
}

type fakeAPI struct {
	configureErr error
	updates      int
	name         string
	raw          json.RawMessage
}

func (f *fakeAPI) CreateApp(context.Context, string) (string, error)           { return "42", nil }
func (f *fakeAPI) ReadApp(context.Context, string) (string, string, error)     { return f.name, "", nil }
func (f *fakeAPI) ReadRights(context.Context, string) (json.RawMessage, error) { return f.raw, nil }
func (f *fakeAPI) ConfigureNewApp(context.Context, string, string, string, json.RawMessage) error {
	return f.configureErr
}
func (f *fakeAPI) UpdateApp(context.Context, string, string, string, json.RawMessage) error {
	f.updates++
	return nil
}
func appSchema(t *testing.T) schema.Schema {
	t.Helper()
	var sr resource.SchemaResponse
	(&AppResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sr)
	return sr.Schema
}
func TestCreateRetainsIDAfterConfigureFailure(t *testing.T) {
	ctx := context.Background()
	s := appSchema(t)
	p := tfsdk.Plan{Schema: s}
	if ds := p.Set(ctx, model(t)); ds.HasError() {
		t.Fatal(ds)
	}
	r := AppResource{client: &fakeAPI{configureErr: errors.New("deploy failed")}}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected failure")
	}
	var got appModel
	if ds := resp.State.Get(ctx, &got); ds.HasError() {
		t.Fatal(ds)
	}
	if got.ID.ValueString() != "42" {
		t.Fatal("lost created app ID")
	}
}
func TestDestroyRequiresExplicitRetain(t *testing.T) {
	ctx := context.Background()
	for _, retain := range []bool{false, true} {
		m := model(t)
		m.ID = types.StringValue("42")
		m.Retain = types.BoolValue(retain)
		s := tfsdk.State{Schema: appSchema(t)}
		s.Set(ctx, m)
		var resp resource.DeleteResponse
		(&AppResource{}).Delete(ctx, resource.DeleteRequest{State: s}, &resp)
		if resp.Diagnostics.HasError() == retain {
			t.Fatalf("retain %v diagnostics %v", retain, resp.Diagnostics)
		}
	}
}
func TestReadReflectsRemoteDrift(t *testing.T) {
	ctx := context.Background()
	m := model(t)
	m.ID = types.StringValue("42")
	raw, _ := payload(ctx, m)
	var acl []right
	json.Unmarshal(raw, &acl)
	acl[0].View = false
	raw, _ = json.Marshal(acl)
	r := AppResource{client: &fakeAPI{name: "Changed by participant", raw: raw}}
	s := tfsdk.State{Schema: appSchema(t)}
	s.Set(ctx, m)
	resp := resource.ReadResponse{State: s}
	r.Read(ctx, resource.ReadRequest{State: s}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got appModel
	resp.State.Get(ctx, &got)
	if got.Name.ValueString() != "Changed by participant" {
		t.Fatal("name drift not detected")
	}
	var rights []rightModel
	got.Rights.ElementsAs(ctx, &rights, false)
	if rights[0].View.ValueBool() {
		t.Fatal("ACL drift not detected")
	}
}

func TestIncludeSubsOnlyForOrganization(t *testing.T) {
	for _, typ := range []string{"USER", "GROUP", "CREATOR", "ORGANIZATION"} {
		t.Run(typ, func(t *testing.T) {
			ctx := context.Background()
			m := model(t)
			var rs []rightModel
			m.Rights.ElementsAs(ctx, &rs, false)
			rs[0].Type = types.StringValue(typ)
			rs[0].IncludeSubs = types.BoolValue(true)
			if typ == "CREATOR" {
				rs[0].Code = types.StringValue("")
			}
			m.Rights, _ = types.ListValueFrom(ctx, rightType(), rs)
			_, ds := payload(ctx, m)
			if ds.HasError() == (typ == "ORGANIZATION") {
				t.Fatalf("unexpected diagnostics: %v", ds)
			}
		})
	}
}
func TestRetainOnlyUpdateDoesNotCallAPI(t *testing.T) {
	ctx := context.Background()
	m := model(t)
	m.ID = types.StringValue("42")
	s := tfsdk.State{Schema: appSchema(t)}
	s.Set(ctx, m)
	m.Retain = types.BoolValue(true)
	p := tfsdk.Plan{Schema: appSchema(t)}
	p.Set(ctx, m)
	f := &fakeAPI{}
	r := AppResource{client: f}
	resp := resource.UpdateResponse{State: s}
	r.Update(ctx, resource.UpdateRequest{Plan: p, State: s}, &resp)
	if resp.Diagnostics.HasError() || f.updates != 0 {
		t.Fatalf("unexpected update: %v %d", resp.Diagnostics, f.updates)
	}
	var got appModel
	resp.State.Get(ctx, &got)
	if !got.Retain.ValueBool() {
		t.Fatal("retain flag not saved")
	}
}

type fakeExerciseAPI struct {
	fakeAPI
	failAfterRecords bool
	calls            int
}

func (f *fakeExerciseAPI) InitExercise(ctx context.Context, name, description string, rights json.RawMessage, fields []kintone.TextField, records []map[string]kintone.FieldValue, checkpoint func(kintone.ExerciseProgress) error) error {
	f.calls++
	if err := checkpoint(kintone.ExerciseProgress{AppID: "42", Stage: "app_created"}); err != nil {
		return err
	}
	if err := checkpoint(kintone.ExerciseProgress{AppID: "42", RecordIDs: []string{"7"}, Stage: "records_created"}); err != nil {
		return err
	}
	if f.failAfterRecords {
		return errors.New("fixture verification failed")
	}
	return checkpoint(kintone.ExerciseProgress{AppID: "42", RecordIDs: []string{"7"}, Stage: "complete"})
}
func TestExerciseCheckpointsSurviveVerificationFailure(t *testing.T) {
	ctx := context.Background()
	s := appSchema(t)
	m := model(t)
	m.InitialFields = types.StringValue(`[{"code":"customer","label":"Customer"}]`)
	m.InitialRecords = types.StringValue(`[{"customer":{"value":"Synthetic A"}}]`)
	p := tfsdk.Plan{Schema: s}
	if ds := p.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	f := &fakeExerciseAPI{failAfterRecords: true}
	r := AppResource{client: f}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected verification failure")
	}
	var got appModel
	if ds := resp.State.Get(ctx, &got); ds.HasError() {
		t.Fatal(ds)
	}
	if got.ID.ValueString() != "42" || got.InitialStage.ValueString() != "records_created" || got.InitialRecordIDs.ValueString() != `["7"]` {
		t.Fatalf("lost recovery state: %#v", got)
	}
}
func TestExerciseInitialJSONRejectedBeforeRemote(t *testing.T) {
	for _, input := range []string{`broken`, `[{"code":"x","unexpected":true}]`, `[] []`} {
		ctx := context.Background()
		s := appSchema(t)
		m := model(t)
		m.InitialFields = types.StringValue(input)
		p := tfsdk.Plan{Schema: s}
		p.Set(ctx, m)
		f := &fakeExerciseAPI{}
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		(&AppResource{client: f}).Create(ctx, resource.CreateRequest{Plan: p}, &resp)
		if !resp.Diagnostics.HasError() || f.calls != 0 {
			t.Fatalf("input=%s calls=%d diagnostics=%v", input, f.calls, resp.Diagnostics)
		}
	}
}

func TestReadBackfillsCreationDefaultsForExistingState(t *testing.T) {
	ctx := context.Background()
	m := model(t)
	m.ID = types.StringValue("42")
	raw, _ := payload(ctx, m)
	f := &fakeAPI{name: "Exercise", raw: raw}
	s := tfsdk.State{Schema: appSchema(t)}
	if ds := s.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	resp := resource.ReadResponse{State: s}
	(&AppResource{client: f}).Read(ctx, resource.ReadRequest{State: s}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got appModel
	resp.State.Get(ctx, &got)
	if got.InitialFields.ValueString() != "[]" || got.InitialRecords.ValueString() != "[]" || got.InitialRecordIDs.ValueString() != "[]" || got.InitialStage.ValueString() != "untracked" {
		t.Fatalf("migration defaults not populated: %#v", got)
	}
}
