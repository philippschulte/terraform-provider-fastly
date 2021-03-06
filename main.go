package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/plugin"
	"github.com/terraform-providers/terraform-provider-fastly/fastly"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: fastly.Provider})
}
