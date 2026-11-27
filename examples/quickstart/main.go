// Command quickstart lists your NodeRings virtual machines, DNS zones and the
// operating systems you can install.
//
//	NODERINGS_TOKEN=... go run ./examples/quickstart
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/noderings/noderings-go"
)

func main() {
	client, err := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	org, err := client.Organization.GetOrganization(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Organization:", org.Organization.Name)

	vms, err := noderings.Collect(client.VirtualMachine.ListVirtualMachinesAll(ctx, nil))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d virtual machines\n", len(vms))
	for _, vm := range vms {
		fmt.Printf("  %-30s %-28s %-16s %s, %s\n", vm.Hostname, vm.OperationStatus, vm.IPv4, vm.RegionCity, vm.PlanCode)
	}

	zones, err := noderings.Collect(client.DNSZone.ListDNSZonesAll(ctx, nil))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d DNS zones\n", len(zones))
	for _, z := range zones {
		fmt.Printf("  %-30s %s\n", z.Name, z.State)
	}

	distros, err := client.Metadata.ListDistros(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d distros\n", len(distros.Distros))
	for _, d := range distros.Distros {
		fmt.Printf("  %-28s %s\n", d.Distro, d.Label)
	}
}
