package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/susumutomita/terraform-provider-kintone/internal/kintone"
	"io"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type appAPI interface {
	CreateApp(context.Context, string) (string, error)
	ReadApp(context.Context, string) (string, string, error)
	ReadRights(context.Context, string) (json.RawMessage, error)
	UpdateApp(context.Context, string, string, string, json.RawMessage) error
	ConfigureNewApp(context.Context, string, string, string, json.RawMessage) error
}
type AppResource struct{ client appAPI }

var _ resource.ResourceWithConfigure = &AppResource{}
var _ resource.ResourceWithImportState = &AppResource{}

func NewAppResource() resource.Resource { return &AppResource{} }

type appModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Rights           types.List   `tfsdk:"rights"`
	Retain           types.Bool   `tfsdk:"retain_on_destroy"`
	InitialFields    types.String `tfsdk:"initial_fields_json"`
	InitialRecords   types.String `tfsdk:"initial_records_json"`
	InitialRecordIDs types.String `tfsdk:"initial_record_ids_json"`
	InitialStage     types.String `tfsdk:"initial_stage"`
}
type rightModel struct {
	Type        types.String `tfsdk:"entity_type"`
	Code        types.String `tfsdk:"entity_code"`
	IncludeSubs types.Bool   `tfsdk:"include_subs"`
	AppEditable types.Bool   `tfsdk:"app_editable"`
	View        types.Bool   `tfsdk:"record_viewable"`
	Add         types.Bool   `tfsdk:"record_addable"`
	Edit        types.Bool   `tfsdk:"record_editable"`
	Delete      types.Bool   `tfsdk:"record_deletable"`
	Import      types.Bool   `tfsdk:"record_importable"`
	Export      types.Bool   `tfsdk:"record_exportable"`
}
type right struct {
	Entity struct {
		Type string `json:"type"`
		Code string `json:"code,omitempty"`
	} `json:"entity"`
	IncludeSubs bool `json:"includeSubs"`
	AppEditable bool `json:"appEditable"`
	View        bool `json:"recordViewable"`
	Add         bool `json:"recordAddable"`
	Edit        bool `json:"recordEditable"`
	Delete      bool `json:"recordDeletable"`
	Import      bool `json:"recordImportable"`
	Export      bool `json:"recordExportable"`
}

func rightType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"entity_type": types.StringType, "entity_code": types.StringType, "include_subs": types.BoolType, "app_editable": types.BoolType,
		"record_viewable": types.BoolType, "record_addable": types.BoolType, "record_editable": types.BoolType, "record_deletable": types.BoolType, "record_importable": types.BoolType, "record_exportable": types.BoolType,
	}}
}
func (r *AppResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}
func (r *AppResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	b := func(desc string) schema.BoolAttribute {
		return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: desc}
	}
	resp.Schema = schema.Schema{Description: "Manages app name, description and the complete ordered application ACL. Deploy publishes ALL pending app settings. Exclusive ownership of the preview is required. Optional creation-only text fields and synthetic records. No tenant lifecycle or remote deletion.", Attributes: map[string]schema.Attribute{
		"id":                      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"initial_fields_json":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("[]"), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Creation-only JSON array of text field definitions (code, label, required, unique). Not reconciled on refresh. Changing this requires replacement, which default destroy protection blocks."},
		"initial_records_json":    schema.StringAttribute{Optional: true, Computed: true, Sensitive: true, Default: stringdefault.StaticString("[]"), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Creation-only JSON array of synthetic record objects. Stored in state despite sensitive display. Never use customer data. Not reseeded on update."},
		"initial_record_ids_json": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "JSON IDs recorded during initial fixture insertion. Not a current data inventory."},
		"initial_stage":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Last checkpoint of initialization; not a health check. Failed initialization requires manual reconciliation, not automatic retry."},
		"name":                    schema.StringAttribute{Required: true},
		"description":             schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
		"retain_on_destroy":       b("Explicitly allow Terraform to forget this app while retaining it remotely. Default false blocks destroy, since no documented delete API is implemented. Does not clean up permissions or data."),
		"rights": schema.ListNestedAttribute{Required: true, Description: "Complete ordered app ACL, not an additive grant. Preserve the provisioning user's management access. Array order controls precedence. Omitted flags are false.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"entity_type":  schema.StringAttribute{Required: true, Description: "USER, GROUP, ORGANIZATION or CREATOR."},
			"entity_code":  schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
			"include_subs": b("Include sub-organizations."), "app_editable": b("May administer app."),
			"record_viewable": b("May view records."), "record_addable": b("May add records."), "record_editable": b("May edit records."), "record_deletable": b("May delete records."), "record_importable": b("May import records."), "record_exportable": b("May export records."),
		}}},
	}}
}
func (r *AppResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(appAPI)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", "Expected kintone client.")
		return
	}
	r.client = c
}
func payload(ctx context.Context, m appModel) (json.RawMessage, diag.Diagnostics) {
	var ds diag.Diagnostics
	if m.Name.IsUnknown() || m.Name.IsNull() || m.Name.ValueString() == "" {
		ds.AddError("Invalid app name", "A known, nonempty app name is required.")
		return nil, ds
	}
	if m.Rights.IsUnknown() || m.Rights.IsNull() {
		ds.AddError("Invalid ACL", "A known nonempty ACL is required.")
		return nil, ds
	}
	var ms []rightModel
	ds.Append(m.Rights.ElementsAs(ctx, &ms, false)...)
	if ds.HasError() {
		return nil, ds
	}
	if len(ms) == 0 {
		ds.AddError("Invalid ACL", "At least one ACL entry is required.")
		return nil, ds
	}
	rs := make([]right, 0, len(ms))
	seen := map[string]bool{}
	for _, v := range ms {
		if v.Type.IsUnknown() || v.Code.IsUnknown() {
			ds.AddError("Unknown ACL principal", "Principal type/code must be known.")
			return nil, ds
		}
		t, c := v.Type.ValueString(), v.Code.ValueString()
		if t != "USER" && t != "GROUP" && t != "ORGANIZATION" && t != "CREATOR" {
			ds.AddError("Invalid ACL principal", "Unsupported entity_type.")
			return nil, ds
		}
		if (t == "CREATOR" && c != "") || (t != "CREATOR" && c == "") {
			ds.AddError("Invalid ACL principal", "entity_code must be empty for CREATOR and nonempty otherwise.")
			return nil, ds
		}
		key := t + "\x00" + c
		if seen[key] {
			ds.AddError("Duplicate ACL principal", "Each entity type/code may appear only once.")
			return nil, ds
		}
		seen[key] = true
		flags := []types.Bool{v.IncludeSubs, v.AppEditable, v.View, v.Add, v.Edit, v.Delete, v.Import, v.Export}
		for _, f := range flags {
			if f.IsUnknown() || f.IsNull() {
				ds.AddError("Unknown ACL flag", "All permission flags must be resolved before apply.")
				return nil, ds
			}
		}
		if v.IncludeSubs.ValueBool() && t != "ORGANIZATION" {
			ds.AddError("Invalid include_subs", "include_subs=true is supported only for ORGANIZATION principals.")
			return nil, ds
		}
		rr := right{IncludeSubs: v.IncludeSubs.ValueBool(), AppEditable: v.AppEditable.ValueBool(), View: v.View.ValueBool(), Add: v.Add.ValueBool(), Edit: v.Edit.ValueBool(), Delete: v.Delete.ValueBool(), Import: v.Import.ValueBool(), Export: v.Export.ValueBool()}
		rr.Entity.Type = t
		rr.Entity.Code = c
		rs = append(rs, rr)
	}
	b, e := json.Marshal(rs)
	if e != nil {
		ds.AddError("ACL encoding failed", e.Error())
	}
	return b, ds
}
func (r *AppResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, ds := payload(ctx, m)
	resp.Diagnostics.Append(ds...)
	if resp.Diagnostics.HasError() {
		return
	}
	fields, records, err := initialFixtures(m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid initialization data", err.Error())
		return
	}
	m.InitialRecordIDs = types.StringValue("[]")
	m.InitialStage = types.StringValue("creating")
	if len(fields) > 0 || len(records) > 0 {
		client, ok := r.client.(interface {
			InitExercise(context.Context, string, string, json.RawMessage, []kintone.TextField, []map[string]kintone.FieldValue, func(kintone.ExerciseProgress) error) error
		})
		if !ok {
			resp.Diagnostics.AddError("Initialization unsupported", "Provider client does not support exercise initialization.")
			return
		}
		err = client.InitExercise(ctx, m.Name.ValueString(), m.Description.ValueString(), p, fields, records, func(progress kintone.ExerciseProgress) error {
			m.ID = types.StringValue(progress.AppID)
			ids := progress.RecordIDs
			if ids == nil {
				ids = []string{}
			}
			b, _ := json.Marshal(ids)
			m.InitialRecordIDs = types.StringValue(string(b))
			m.InitialStage = types.StringValue(progress.Stage)
			ds := resp.State.Set(ctx, &m)
			resp.Diagnostics.Append(ds...)
			if ds.HasError() {
				return fmt.Errorf("cannot checkpoint initialization state")
			}
			return nil
		})
		if err != nil {
			resp.Diagnostics.AddError("Exercise initialization incomplete", "Inspect the recorded app ID, stage and record IDs before recovery. Do not automatically retry: remote changes may already exist. "+err.Error())
		}
		return
	}
	id, e := r.client.CreateApp(ctx, m.Name.ValueString())
	if e != nil {
		resp.Diagnostics.AddError("App creation failed", e.Error())
		return
	}
	m.ID = types.StringValue(id)
	m.InitialStage = types.StringValue("created")
	// Preserve the ID before further remote operations; failures must not silently orphan an app.
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if e = r.client.ConfigureNewApp(ctx, id, m.Name.ValueString(), m.Description.ValueString(), p); e != nil {
		resp.Diagnostics.AddError("App created but configuration incomplete", fmt.Sprintf("App %s is retained in state. Inspect its preview/deployment and recover manually before retrying. %s", id, e))
		return
	}
	m.InitialStage = types.StringValue("ready")
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *AppResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name, description, e := r.client.ReadApp(ctx, m.ID.ValueString())
	if e != nil {
		resp.Diagnostics.AddError("Cannot read app", e.Error())
		return
	}
	raw, e := r.client.ReadRights(ctx, m.ID.ValueString())
	if e != nil {
		resp.Diagnostics.AddError("Cannot read ACL", e.Error())
		return
	}
	var rs []right
	if e = json.Unmarshal(raw, &rs); e != nil {
		resp.Diagnostics.AddError("Invalid ACL response", "The API returned an invalid ACL.")
		return
	}
	ms := make([]rightModel, 0, len(rs))
	for _, v := range rs {
		ms = append(ms, rightModel{Type: types.StringValue(v.Entity.Type), Code: types.StringValue(v.Entity.Code), IncludeSubs: types.BoolValue(v.IncludeSubs), AppEditable: types.BoolValue(v.AppEditable), View: types.BoolValue(v.View), Add: types.BoolValue(v.Add), Edit: types.BoolValue(v.Edit), Delete: types.BoolValue(v.Delete), Import: types.BoolValue(v.Import), Export: types.BoolValue(v.Export)})
	}
	var ds diag.Diagnostics
	m.Rights, ds = types.ListValueFrom(ctx, rightType(), ms)
	resp.Diagnostics.Append(ds...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.Name = types.StringValue(name)
	m.Description = types.StringValue(description)
	if m.InitialFields.IsNull() {
		m.InitialFields = types.StringValue("[]")
	}
	if m.InitialRecords.IsNull() {
		m.InitialRecords = types.StringValue("[]")
	}
	if m.InitialRecordIDs.IsNull() {
		m.InitialRecordIDs = types.StringValue("[]")
	}
	if m.InitialStage.IsNull() {
		m.InitialStage = types.StringValue("untracked")
	}
	if m.Retain.IsNull() {
		m.Retain = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *AppResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	var prior appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, ds := payload(ctx, m)
	resp.Diagnostics.Append(ds...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.Name.Equal(prior.Name) && m.Description.Equal(prior.Description) && m.Rights.Equal(prior.Rights) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	if e := r.client.UpdateApp(ctx, m.ID.ValueString(), m.Name.ValueString(), m.Description.ValueString(), p); e != nil {
		resp.Diagnostics.AddError("App update failed", e.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *AppResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Retain.ValueBool() {
		resp.Diagnostics.AddError("Remote deletion unsupported", "No app deletion API is implemented. To forget the app without deleting it, apply retain_on_destroy=true first, then destroy. Otherwise keep the resource in configuration.")
		return
	}
	resp.Diagnostics.AddWarning("Remote app retained", "Terraform forgets this app only. Its settings, access permissions and data are unchanged; remove it manually when appropriate.")
}
func (r *AppResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, e := strconv.ParseUint(req.ID, 10, 64)
	if e != nil || id == 0 {
		resp.Diagnostics.AddError("Invalid import ID", "Use a positive decimal app ID.")
		return
	}
	req.ID = strconv.FormatUint(id, 10)
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// Parse creation-only fixture input strictly before any remote operation.
func initialFixtures(m appModel) ([]kintone.TextField, []map[string]kintone.FieldValue, error) {
	var fields []kintone.TextField
	var records []map[string]kintone.FieldValue
	decode := func(v types.String, out any) error {
		if v.IsUnknown() {
			return fmt.Errorf("initialization JSON must be known before creation")
		}
		if v.IsNull() || v.ValueString() == "" {
			return nil
		}
		decoder := json.NewDecoder(bytes.NewBufferString(v.ValueString()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(out); err != nil {
			return fmt.Errorf("initialization JSON must match the documented field/record shape")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("initialization JSON must contain one value")
		}
		return nil
	}
	if err := decode(m.InitialFields, &fields); err != nil {
		return nil, nil, err
	}
	if err := decode(m.InitialRecords, &records); err != nil {
		return nil, nil, err
	}
	return fields, records, nil
}
