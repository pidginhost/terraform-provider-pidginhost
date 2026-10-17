package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var _ ephemeral.EphemeralResourceWithConfigure = &bucketCredentialsEphemeral{}

func NewBucketCredentialsEphemeral() ephemeral.EphemeralResource {
	return &bucketCredentialsEphemeral{}
}

type bucketCredentialsEphemeral struct{ client *pidginhost.APIClient }

type bucketCredentialsEphemeralModel struct {
	BucketID  types.String `tfsdk:"bucket_id"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

func (e *bucketCredentialsEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_credentials"
}

func (e *bucketCredentialsEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The current S3 access key and secret key of a bucket, read on every plan and apply and never " +
			"saved in the Terraform state. Reading them does not rotate them, but the API records every read in an " +
			"audit trail. Rotate keys with the pidginhost_bucket_credentials resource. Needs Terraform 1.10 or later.",
		Attributes: map[string]schema.Attribute{
			"bucket_id": schema.StringAttribute{
				Required:    true,
				Description: "Id of the bucket.",
			},
			"access_key": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "S3 access key.",
			},
			"secret_key": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "S3 secret key.",
			},
		},
	}
}

func (e *bucketCredentialsEphemeral) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	e.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (e *bucketCredentialsEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var config bucketCredentialsEphemeralModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(config.BucketID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket credentials", err.Error())
		return
	}
	creds, httpResp, err := e.client.CloudAPI.CloudBucketsCredentialsRevealCreate(ctx, id).Execute()
	if err != nil {
		resp.Diagnostics.Append(secretRouteError("Reading bucket credentials", httpResp, err))
		return
	}
	config.AccessKey = types.StringValue(creds.AccessKey)
	config.SecretKey = types.StringValue(creds.SecretKey)
	resp.Diagnostics.Append(resp.Result.Set(ctx, config)...)
}
