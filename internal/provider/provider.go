package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/susumutomita/terraform-provider-kintone/internal/kintone"
	"os"
)

type KintoneProvider struct{ version string }
type configModel struct {
	BaseURL  types.String `tfsdk:"base_url"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &KintoneProvider{version: version} }
}
func (p *KintoneProvider) Metadata(_ context.Context, _ provider.MetadataRequest, r *provider.MetadataResponse) {
	r.TypeName = "kintone"
	r.Version = p.version
}
func (p *KintoneProvider) Schema(_ context.Context, _ provider.SchemaRequest, r *provider.SchemaResponse) {
	r.Schema = schema.Schema{Description: "Experimental kintone app configuration provider. No tenant provisioning or remote deletion.", Attributes: map[string]schema.Attribute{
		"base_url": schema.StringAttribute{Optional: true, Description: "HTTPS tenant origin; alternatively KINTONE_BASE_URL. Guest spaces unsupported."},
		"username": schema.StringAttribute{Optional: true, Sensitive: true, Description: "Dedicated provisioning user; alternatively KINTONE_USERNAME."},
		"password": schema.StringAttribute{Optional: true, Sensitive: true, Description: "Prefer KINTONE_PASSWORD environment variable. Never put credentials in committed HCL."},
	}}
}
func (p *KintoneProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var c configModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &c)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if c.BaseURL.IsUnknown() || c.Username.IsUnknown() || c.Password.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration", "Provider configuration must be known before applying resources.")
		return
	}
	val := func(v types.String, key string) string {
		if !v.IsNull() {
			return v.ValueString()
		}
		return os.Getenv(key)
	}
	client, err := kintone.NewClient(val(c.BaseURL, "KINTONE_BASE_URL"), val(c.Username, "KINTONE_USERNAME"), val(c.Password, "KINTONE_PASSWORD"))
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}
	resp.ResourceData = client
}
func (p *KintoneProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewAppResource}
}
func (p *KintoneProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }
