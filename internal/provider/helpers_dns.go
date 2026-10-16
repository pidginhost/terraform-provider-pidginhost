package provider

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"golang.org/x/net/idna"
)

// dnsIDNA converts names the way the API's IDNA codec does, keeping the
// underscores of service labels such as _dmarc.
var dnsIDNA = idna.New(idna.MapForLookup(), idna.Transitional(true), idna.StrictDomainName(false))

// fqdn is the lower case ASCII form of a domain name, without the trailing dot.
func fqdn(name string) string {
	name = strings.ToLower(strings.TrimRight(strings.TrimSpace(name), "."))
	if ascii, err := dnsIDNA.ToASCII(name); err == nil {
		return ascii
	}
	return name
}

// dnsSourceAttribute is the FreeDNS source of a zone: where its domain lives.
func dnsSourceAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Optional: true,
		Computed: true,
		Default:  stringdefault.StaticString("internal"),
		Description: "internal for a domain registered with PidginHost, external for a domain added to the " +
			"account in the PidginHost panel. Defaults to internal. Changing it replaces the resource.",
		Validators:    []validator.String{stringvalidator.OneOf("internal", "external")},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}
