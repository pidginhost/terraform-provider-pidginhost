package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure  = &bucketCredentialsResource{}
	_ resource.ResourceWithModifyPlan = &bucketCredentialsResource{}
)

func NewBucketCredentialsResource() resource.Resource { return &bucketCredentialsResource{} }

type bucketCredentialsResource struct{ client *pidginhost.APIClient }

type bucketCredentialsModel struct {
	ID               types.String `tfsdk:"id"`
	BucketID         types.String `tfsdk:"bucket_id"`
	RotationTriggers types.Map    `tfsdk:"rotation_triggers"`
	AccessKey        types.String `tfsdk:"access_key"`
	SecretKey        types.String `tfsdk:"secret_key"`
}

func (r *bucketCredentialsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_credentials"
}

func (r *bucketCredentialsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	unchanged := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "The S3 access key and secret key of a bucket. Creating this resource reads the keys the " +
			"bucket already has, so clients that use them keep working. Changing rotation_triggers rotates the keys. " +
			"Replacing the resource, for example for a new bucket_id, reads the keys again without rotating them. " +
			"Terraform reads the keys only when it creates or rotates them, never on refresh, since the API records " +
			"every read and rotation in an audit trail. Destroying the resource only removes the keys from the " +
			"Terraform state: they stay valid.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"bucket_id": schema.StringAttribute{
				Required:      true,
				Description:   "Id of the bucket. Changing it replaces the resource and reads the keys of the new bucket.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"rotation_triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Arbitrary values, for example a date. Any change to this map, including setting it for " +
					"the first time, rotates the keys in place: the API issues a new pair and revokes the old one, so " +
					"clients still using the old keys stop working.",
			},
			"access_key": schema.StringAttribute{
				Computed:      true,
				Sensitive:     true,
				Description:   "S3 access key.",
				PlanModifiers: unchanged,
			},
			"secret_key": schema.StringAttribute{
				Computed:      true,
				Sensitive:     true,
				Description:   "S3 secret key.",
				PlanModifiers: unchanged,
			},
		},
	}
}

func (r *bucketCredentialsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan announces new keys when rotation_triggers changes, since the
// update then rotates them.
func (r *bucketCredentialsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var planned, current types.Map
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("rotation_triggers"), &planned)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("rotation_triggers"), &current)...)
	if resp.Diagnostics.HasError() || planned.Equal(current) {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("access_key"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("secret_key"), types.StringUnknown())...)
}

func (r *bucketCredentialsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketCredentialsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(plan.BucketID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket credentials", err.Error())
		return
	}
	creds, httpResp, err := r.client.CloudAPI.CloudBucketsCredentialsRevealCreate(ctx, id).Execute()
	if err != nil {
		resp.Diagnostics.Append(secretRouteError("Reading bucket credentials", httpResp, err))
		return
	}
	plan.ID = plan.BucketID
	plan.setKeys(creds)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read only checks that the bucket still exists: revealing the keys again
// would add an audit entry on every refresh.
func (r *bucketCredentialsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketCredentialsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := parseID(state.BucketID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket credentials", err.Error())
		return
	}
	bucket, httpResp, err := r.client.CloudAPI.CloudBucketsRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) || err == nil && bucketDeleted(bucket.Status) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading bucket", err))
	}
}

// Update runs only when rotation_triggers changed (bucket_id replaces). A
// failed rotation leaves the prior state, so the next apply tries again.
func (r *bucketCredentialsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state bucketCredentialsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Triggers unknown at plan time can turn out unchanged.
	if plan.RotationTriggers.Equal(state.RotationTriggers) {
		plan.AccessKey, plan.SecretKey = state.AccessKey, state.SecretKey
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}
	id, err := parseID(state.BucketID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Rotating bucket credentials", err.Error())
		return
	}
	creds, httpResp, err := r.client.CloudAPI.CloudBucketsCredentialsRotateCreate(ctx, id).Execute()
	if err != nil {
		resp.Diagnostics.Append(secretRouteError("Rotating bucket credentials", httpResp, err))
		return
	}
	plan.setKeys(creds)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete only forgets the keys; they stay valid.
func (r *bucketCredentialsResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (m *bucketCredentialsModel) setKeys(creds *pidginhost.BucketCredentials) {
	m.AccessKey = types.StringValue(creds.AccessKey)
	m.SecretKey = types.StringValue(creds.SecretKey)
}
