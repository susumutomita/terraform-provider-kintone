package main

import (
	"context"
	"flag"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/susumutomita/terraform-provider-kintone/internal/provider"
	"log"
)

var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "Run with debugger support")
	flag.Parse()
	if err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{Address: "registry.terraform.io/susumutomita/kintone", Debug: *debug}); err != nil {
		log.Fatal(err)
	}
}
