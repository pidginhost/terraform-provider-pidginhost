package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestServerUserDataMigrationPlans(t *testing.T) {
	for _, tc := range []struct {
		name   string
		extra  string
		action plancheck.ResourceActionType
	}{
		{"same script", `user_data_wo = "#!/bin/sh\ntrue\n"`, plancheck.ResourceActionUpdate},
		{"changed script", `user_data_wo = "#!/bin/sh\nfalse\n"`, plancheck.ResourceActionDestroyBeforeCreate},
		{"removed script", "", plancheck.ResourceActionDestroyBeforeCreate},
		{"unknown script", `user_data_wo = terraform_data.script.output`, plancheck.ResourceActionDestroyBeforeCreate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeServerAPI()
			srv := httptest.NewServer(fake)
			defer srv.Close()
			fastKubernetesPoll(t)
			config := serverSecretsConfig(srv.URL, tc.extra)
			if tc.name == "unknown script" {
				config += `
resource "terraform_data" "script" {
  input = "#!/bin/sh\nfalse\n"
}`
			}
			resource.UnitTest(t, resource.TestCase{
				TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
				ProtoV6ProviderFactories: testAccProviders,
				Steps: []resource.TestStep{
					{Config: serverSecretsConfig(srv.URL, `user_data = "#!/bin/sh\ntrue\n"`)},
					{
						Config: config,
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("pidginhost_server.test", tc.action),
						}},
					},
				},
			})
		})
	}
}

func TestServerPasswordModeAndUnknownVersionTransitions(t *testing.T) {
	fake := newFakeServerAPI()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	fastKubernetesPoll(t)
	config := func(extra string) string { return serverSecretsConfig(srv.URL, extra) }
	unknownVersion := func(name string, value int) string {
		return config(fmt.Sprintf(`password_wo = "test-next"
  password_wo_version = terraform_data.%s.output`, name)) + fmt.Sprintf(`
resource "terraform_data" %q {
  input = %d
}`, name, value)
	}
	var steps []resource.TestStep
	for i, tc := range []struct {
		config string
		sent   string
	}{
		{config(`password = "test-initial"`), "POST password=test-initial user_data=<nil>"},
		{config(`password_wo = "test-initial"`), ""},
		{config(`password_wo = "test-rotated"
  password_wo_version = 1`), "PATCH password=test-rotated user_data=<nil>"},
		{config(`password = "test-stored"`), "PATCH password=test-stored user_data=<nil>"},
		{unknownVersion("first", 1), "PATCH password=test-next user_data=<nil>"},
		{unknownVersion("same", 1), ""},
		{unknownVersion("changed", 2), "PATCH password=test-next user_data=<nil>"},
		{config(`password = "test-final"`), "PATCH password=test-final user_data=<nil>"},
	} {
		step := resource.TestStep{Config: tc.config}
		if i > 0 {
			step.ConfigPlanChecks.PreApply = []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("pidginhost_server.test", plancheck.ResourceActionUpdate),
			}
		}
		step.Check = func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			got := strings.Join(fake.secrets, "\n")
			fake.secrets = nil
			if got != tc.sent {
				return fmt.Errorf("unexpected password submission in step %d", i+1)
			}
			return nil
		}
		steps = append(steps, step)
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		Steps:                    steps,
	})
}

// The API's reason for refusing a request stays in the error, but an echo of
// the submitted password or script does not, even reformatted.
func TestServerSecretErrorsKeepTheReasonNotTheSecret(t *testing.T) {
	const script = "#!/bin/sh\r\ntoken=test-script-secret\r\n"
	const password = `test-pass"word\secret`
	for _, tc := range []struct {
		operation string
		echo      func(body map[string]any) (contentType, text string)
	}{
		{"create password", func(body map[string]any) (string, string) {
			escaped, _ := json.Marshal(body["password"])
			return "text/plain", "Unknown package. refused " + string(escaped)
		}},
		{"create script", func(body map[string]any) (string, string) {
			// The API normalizes line endings and trims the script.
			normalized := strings.TrimSpace(strings.ReplaceAll(body["user_data"].(string), "\r\n", "\n"))
			out, _ := json.Marshal(map[string]any{"package": []string{"Unknown package."}, "detail": "refused " + normalized})
			return "application/json", string(out)
		}},
		{"update password", func(body map[string]any) (string, string) {
			out, _ := json.Marshal(map[string]any{"package": []string{"Unknown package."}, "detail": fmt.Sprint("refused ", body["password"])})
			return "application/json", string(out)
		}},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				contentType, text := tc.echo(body)
				w.Header().Set("Content-Type", contentType)
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, text)
			}))
			defer srv.Close()
			r := &serverResource{client: newAPIClient("test", srv.URL, "test")}
			model := serverTestModel()
			model.ID = types.StringValue("101")
			state := serverTestPlan(t, model)
			configModel := model
			if tc.operation == "create script" {
				configModel.UserDataWO = types.StringValue(script)
			} else {
				configModel.PasswordWO = types.StringValue(password)
				model.PasswordWOVersion = types.Int64Value(1)
			}
			plan := serverTestPlan(t, model)
			config := tfsdk.Config(serverTestPlan(t, configModel))
			var diags diag.Diagnostics
			if tc.operation == "update password" {
				resp := fwresource.UpdateResponse{State: tfsdk.State(state)}
				r.Update(t.Context(), fwresource.UpdateRequest{Plan: plan, State: tfsdk.State(state), Config: config}, &resp)
				diags = resp.Diagnostics
			} else {
				resp := fwresource.CreateResponse{State: tfsdk.State{Schema: plan.Schema, Raw: tftypes.NewValue(plan.Raw.Type(), nil)}}
				r.Create(t.Context(), fwresource.CreateRequest{Plan: plan, Config: config}, &resp)
				diags = resp.Diagnostics
			}
			if !diags.HasError() {
				t.Fatal("expected a request error")
			}
			text := ""
			for _, d := range diags {
				text += d.Summary() + " " + d.Detail()
			}
			if strings.Contains(text, "secret") {
				t.Fatalf("the error repeats the submitted secret: %q", text)
			}
			if !strings.Contains(text, "Unknown package.") {
				t.Fatalf("the error lost the API's reason: %q", text)
			}
		})
	}
}

func TestServerWriteOnlyRejectsOlderClients(t *testing.T) {
	server := providerserver.NewProtocol6(New("test")())()
	schemas, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	checkProtocolDiagnostics(t, schemas.Diagnostics, err)
	typ := schemas.ResourceSchemas["pidginhost_server"].ValueType()
	for _, name := range []string{"password_wo", "user_data_wo"} {
		for _, value := range []any{"test-input", tftypes.UnknownValue} {
			resp, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{
				TypeName: "pidginhost_server",
				Config: protocolValue(t, typ, map[string]any{
					"image": "ubuntu-24", "package": "cloudv-1", name: value,
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range resp.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError && strings.Contains(d.Detail, "Terraform 1.11") {
					found = true
				}
			}
			if !found {
				t.Fatalf("older client accepted %s without a version error", name)
			}
		}
	}
}
