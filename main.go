package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/pidginhost/terraform-provider-pidginhost/internal/provider"
)

//go:generate go tool tfplugindocs generate --provider-name pidginhost

// version is set by goreleaser at build time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/pidginhost/pidginhost",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
