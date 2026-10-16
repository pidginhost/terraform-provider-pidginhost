package provider

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	pidginhost "github.com/pidginhost/sdk-go"
)

var (
	_ resource.ResourceWithConfigure   = &bucketResource{}
	_ resource.ResourceWithImportState = &bucketResource{}
)

const bucketTimeout = 45 * time.Minute

func NewBucketResource() resource.Resource { return &bucketResource{} }

type bucketResource struct{ client *pidginhost.APIClient }

type bucketModel struct {
	ID         types.String   `tfsdk:"id"`
	Name       types.String   `tfsdk:"name"`
	QuotaGB    types.Int64    `tfsdk:"quota_gb"`
	PublicRead types.Bool     `tfsdk:"public_read"`
	FullName   types.String   `tfsdk:"full_name"`
	Endpoint   types.String   `tfsdk:"endpoint"`
	Region     types.String   `tfsdk:"region"`
	Status     types.String   `tfsdk:"status"`
	Timeouts   timeouts.Value `tfsdk:"timeouts"`
}

func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (r *bucketResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	unchanged := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "An S3 compatible object storage bucket. Terraform waits until the bucket is active after " +
			"creating or changing it, and until it is gone after deleting it: the name stays taken until then.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute(),
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Bucket name, without the account prefix. Changing it replaces the bucket.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 63)},
			},
			"quota_gb": schema.Int64Attribute{
				Required:    true,
				Description: "Storage quota in GB. Changed in place.",
				Validators:  []validator.Int64{int64validator.AtLeast(1)},
			},
			"public_read": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether anyone can read the objects without credentials. Defaults to false. Changed in place.",
			},
			"full_name": schema.StringAttribute{
				Computed:      true,
				Description:   "Bucket name with the account prefix, the name S3 clients use.",
				PlanModifiers: unchanged,
			},
			"endpoint": schema.StringAttribute{
				Computed:      true,
				Description:   "S3 endpoint URL.",
				PlanModifiers: unchanged,
			},
			"region": schema.StringAttribute{
				Computed:      true,
				Description:   "S3 region.",
				PlanModifiers: unchanged,
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status, such as active.",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *bucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	timeout, diags := plan.Timeouts.Create(ctx, bucketTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := pidginhost.NewBucketCreateRequest(plan.Name.ValueString(), int32(plan.QuotaGB.ValueInt64()))
	body.PublicRead = plan.PublicRead.ValueBoolPointer()
	bucket, _, err := r.client.CloudAPI.CloudBucketsCreate(ctx).BucketCreateRequest(*body).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Creating bucket", err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.Itoa(int(bucket.Id)))...)
	bucket, err = waitBucketActive(ctx, r.client, bucket)
	if err != nil {
		resp.Diagnostics.AddError("Waiting for bucket", describeError(err))
		return
	}
	plan.fill(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := apiID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket", err.Error())
		return
	}
	bucket, httpResp, err := r.client.CloudAPI.CloudBucketsRetrieve(ctx, id).Execute()
	if isNotFound(httpResp, err) || err == nil && bucketDeleted(bucket.Status) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading bucket", err))
		return
	}
	state.fill(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	timeout, diags := plan.Timeouts.Update(ctx, bucketTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id, err := apiID(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Updating bucket", err.Error())
		return
	}
	bucket, _, err := r.client.CloudAPI.CloudBucketsRetrieve(ctx, id).Execute()
	if err != nil {
		resp.Diagnostics.Append(apiError("Reading bucket", err))
		return
	}
	switch bucket.Status {
	case pidginhost.RESOURCESTATUSENUM_CANCELLING, pidginhost.RESOURCESTATUSENUM_TERMINATING:
		resp.Diagnostics.AddError("Updating bucket", fmt.Sprintf("Bucket %s is being deleted.", bucket.Name))
		return
	case pidginhost.RESOURCESTATUSENUM_PENDING, pidginhost.RESOURCESTATUSENUM_PROVISIONING,
		pidginhost.RESOURCESTATUSENUM_UPGRADING, pidginhost.RESOURCESTATUSENUM_FAILED:
		if bucket, err = waitBucketActive(ctx, r.client, bucket); err != nil {
			resp.Diagnostics.AddError("Waiting for bucket", describeError(err))
			return
		}
	}

	if quota := int32(plan.QuotaGB.ValueInt64()); quota != bucket.QuotaGb {
		bucket, _, err = r.client.CloudAPI.CloudBucketsResizeCreate(ctx, id).
			BucketResizeRequest(*pidginhost.NewBucketResizeRequest(quota)).Execute()
		if err != nil {
			resp.Diagnostics.Append(apiError("Resizing bucket", err))
			return
		}
	}
	if public := plan.PublicRead.ValueBool(); public != bucket.PublicRead {
		bucket, _, err = r.client.CloudAPI.CloudBucketsVisibilityCreate(ctx, id).
			BucketVisibilityRequest(*pidginhost.NewBucketVisibilityRequest(public)).Execute()
		if err != nil {
			resp.Diagnostics.Append(apiError("Changing bucket visibility", err))
			return
		}
	}
	if bucket, err = waitBucketActive(ctx, r.client, bucket); err != nil {
		resp.Diagnostics.AddError("Waiting for bucket", describeError(err))
		return
	}
	plan.fill(bucket)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	timeout, diags := state.Timeouts.Delete(ctx, bucketTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id, err := apiID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Deleting bucket", err.Error())
		return
	}
	// A deletion already under way (cancelling, terminating) only needs the wait.
	if !bucketDeleting(pidginhost.ResourceStatusEnum(state.Status.ValueString())) {
		_, httpResp, err := r.client.CloudAPI.CloudBucketsDestroy(ctx, id).Execute()
		if isNotFound(httpResp, err) {
			return
		}
		if err != nil {
			resp.Diagnostics.Append(apiError("Deleting bucket", err))
			return
		}
	}
	if err := waitBucketGone(ctx, r.client, id); err != nil {
		resp.Diagnostics.AddError("Waiting for bucket deletion", describeError(err))
	}
}

func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *bucketModel) fill(b *pidginhost.Bucket) {
	m.ID = types.StringValue(strconv.Itoa(int(b.Id)))
	m.Name = types.StringValue(b.Name)
	m.QuotaGB = types.Int64Value(int64(b.QuotaGb))
	m.PublicRead = types.BoolValue(b.PublicRead)
	m.FullName = types.StringValue(b.FullName)
	m.Endpoint = types.StringValue(b.Endpoint)
	m.Region = types.StringValue(b.Region)
	m.Status = types.StringValue(string(b.Status))
}

func bucketDeleting(status pidginhost.ResourceStatusEnum) bool {
	return status == pidginhost.RESOURCESTATUSENUM_CANCELLING || status == pidginhost.RESOURCESTATUSENUM_TERMINATING
}

func bucketDeleted(status pidginhost.ResourceStatusEnum) bool {
	return status == pidginhost.RESOURCESTATUSENUM_CANCELLED || status == pidginhost.RESOURCESTATUSENUM_TERMINATED
}

// waitBucketActive polls a bucket that is not active yet until it is active
// or failed, and fails unless it ends up active.
func waitBucketActive(ctx context.Context, client *pidginhost.APIClient, bucket *pidginhost.Bucket) (*pidginhost.Bucket, error) {
	if bucket.Status == pidginhost.RESOURCESTATUSENUM_ACTIVE {
		return bucket, nil
	}
	err := waitFor(ctx, storagePoll, func() (bool, error) {
		current, _, err := client.CloudAPI.CloudBucketsRetrieve(ctx, bucket.Id).Execute()
		if err != nil {
			return false, err
		}
		bucket = current
		if bucket.Status == pidginhost.RESOURCESTATUSENUM_FAILED {
			return false, fmt.Errorf("bucket %s failed", bucket.Name)
		}
		return bucket.Status == pidginhost.RESOURCESTATUSENUM_ACTIVE, nil
	})
	return bucket, err
}

// waitBucketGone waits until a deleted bucket leaves the list or reaches a
// deleted status. The API keeps listing it while the deletion runs, and the
// name stays taken until then.
func waitBucketGone(ctx context.Context, client *pidginhost.APIClient, id int32) error {
	return waitFor(ctx, storagePoll, func() (bool, error) {
		buckets, _, err := client.CloudAPI.CloudBucketsList(ctx).Execute()
		if err != nil {
			return false, err
		}
		for _, b := range buckets {
			switch {
			case b.Id != id:
				continue
			case bucketDeleting(b.Status):
				return false, nil
			case !bucketDeleted(b.Status):
				return false, fmt.Errorf("bucket %s is %s instead of deleted", b.Name, b.Status)
			}
		}
		return true, nil
	})
}
