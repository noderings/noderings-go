package noderings_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/noderings/noderings-go"
)

func Example() {
	client, err := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// Every virtual machine in the organization, page by page.
	for vm, err := range client.VirtualMachine.ListVirtualMachinesAll(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(vm.Hostname, vm.OperationStatus, vm.IPv4)
	}
}

// Order a virtual machine and wait until it runs. Find planID and regionID
// with client.Marketplace.ListProviders.
func ExampleVirtualMachineService_CreateVirtualMachine() {
	client, _ := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
	ctx := context.Background()

	req := &noderings.CreateVirtualMachineRequest{
		Hostname:  "web-1",
		PlanID:    "your-plan-id",
		RegionID:  "your-region-id",
		Distro:    noderings.DistroUbuntu2604,
		SSHKeyIDs: []string{"your-ssh-key-id"},
		Tags:      []string{"web"},
	}
	created, err := client.VirtualMachine.CreateVirtualMachine(ctx, req)
	if err != nil {
		// req.IdempotencyKey was filled in; resend the same req to retry safely.
		log.Fatal(err)
	}
	vm, err := client.WaitForVirtualMachineStatus(ctx, created.VirtualMachine.ID,
		&noderings.WaitOptions{Timeout: 10 * time.Minute}, noderings.OperationStatusStarted)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(vm.Hostname, "is running at", vm.IPv4)
}

// Point a name at an address. Upsert replaces every value of the name and type.
func ExampleDNSRecordService_UpsertDNSRecord() {
	client, _ := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
	_, err := client.DNSRecord.UpsertDNSRecord(context.Background(), "your-zone-id", &noderings.UpsertDNSRecordBody{
		Record: &noderings.DNSRecord{Name: "www", Type: "A", TTL: 300, Rdata: []string{"203.0.113.10"}},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Errors carry the API's status code; helpers check the common cases.
func ExampleIsNotFound() {
	client, _ := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
	_, err := client.VirtualMachine.GetVirtualMachine(context.Background(), "00000000-0000-0000-0000-000000000000")
	switch {
	case noderings.IsNotFound(err):
		fmt.Println("no such virtual machine")
	case noderings.IsQuotaExceeded(err):
		fmt.Println("plan limit reached")
	case err != nil:
		log.Fatal(err)
	}
}
