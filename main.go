package main

import (
    "context"
    "flag"
    "log"

    "github.com/hashicorp/terraform-plugin-framework/providerserver"
    pidginhost "github.com/pidginhost/terraform-provider-pidginhost/internal/provider"
)

var (
	// version is set by goreleaser or go build during compilation.
	version = "dev"
)

func main() {
	flag.Parse()

    opts := providerserver.ServeOpts{
        Address: "registry.terraform.io/pidginhost/pidginhost",
    }

    err := providerserver.Serve(context.Background(), pidginhost.New, opts)
	if err != nil {
		log.Fatalf("Error serving PidginHost provider: %s", err)
	}
}
