//go:build live

// Live tests against a real NodeRings organization. They need NODERINGS_TOKEN
// and are run with:
//
//	NODERINGS_TOKEN=... go test -tags live -run Live -v ./...
//
// NODERINGS_TEST_ZONE_ID (a DNS zone to write test records in) enables the DNS
// record test. NODERINGS_TEST_BILLED=1 also creates and deletes a virtual
// machine, which is billed for the minutes it exists. Everything the tests
// create is deleted again.
package noderings

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	token := os.Getenv("NODERINGS_TOKEN")
	if token == "" {
		t.Skip("NODERINGS_TOKEN not set")
	}
	opts := []Option{WithUserAgent("noderings-go-live-test")}
	if u := os.Getenv("NODERINGS_API_URL"); u != "" {
		opts = append(opts, WithBaseURL(u))
	}
	c, err := NewClient(token, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func suffix() string { return strings.ToLower(NewIdempotencyKey()[:8]) }

// TestLiveReadSweep calls every GET operation that needs no ID, checks the
// response decodes into the SDK's type, and lists response fields the API
// sends but the OpenAPI description does not have (spec drift).
func TestLiveReadSweep(t *testing.T) {
	c := liveClient(t)
	spec := loadSpecOperations(t)
	ctx := context.Background()
	counts := map[string]int{}
	var drift []string
	for _, op := range generatedOperations {
		so := spec[op.operationID]
		if so.method != http.MethodGet || strings.Contains(so.path, "{") {
			continue
		}
		m := reflect.ValueOf(c).Elem().FieldByName(op.service).MethodByName(op.method)
		args := []reflect.Value{reflect.ValueOf(ctx)}
		for i := 1; i < m.Type().NumIn(); i++ {
			args = append(args, reflect.Zero(m.Type().In(i)))
		}
		outs := m.Call(args)
		err, _ := outs[1].Interface().(error)
		var apiErr *Error
		switch {
		case err == nil:
			counts["ok"]++
		case errors.As(err, &apiErr):
			// Permission and validation answers are expected: the sweep calls
			// provider and admin reads with a customer token and no filters.
			counts[fmt.Sprintf("HTTP %d", apiErr.StatusCode)]++
			if apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusUnauthorized {
				t.Logf("%s.%s: %v", op.service, op.method, err)
			}
			continue
		default:
			t.Errorf("%s.%s: %v", op.service, op.method, err)
			continue
		}
		// Strict decode of the same response to find fields missing from the spec.
		var raw json.RawMessage
		if err := c.Do(ctx, http.MethodGet, so.path, nil, nil, &raw); err != nil {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(reflect.New(outs[0].Type().Elem()).Interface()); err != nil {
			drift = append(drift, fmt.Sprintf("%s.%s: %v", op.service, op.method, err))
		}
	}
	t.Logf("GET operations without an ID: %v", counts)
	if len(drift) == 0 {
		t.Log("no spec drift: every successful response decoded strictly into its SDK type")
	}
	sort.Strings(drift)
	for _, d := range drift {
		t.Logf("spec drift: %s", d)
	}
	if counts["ok"] < 20 {
		t.Errorf("only %d reads succeeded", counts["ok"])
	}
}

func TestLiveOrganizationAndCatalog(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	org, err := c.Organization.GetOrganization(ctx)
	if err != nil || org.Organization == nil || org.Organization.ID == "" {
		t.Fatalf("organization: %+v %v", org, err)
	}
	distros, err := c.Metadata.ListDistros(ctx)
	if err != nil || len(distros.Distros) == 0 {
		t.Fatalf("distros: %v", err)
	}
	found := false
	for _, d := range distros.Distros {
		found = found || d.Distro == DistroUbuntu2604
	}
	if !found {
		t.Error("Ubuntu 26.04 missing from the distro catalog (or the enum constant is wrong)")
	}
	regions, err := c.Marketplace.ListAvailableRegions(ctx, &ListAvailableRegionsParams{FilterAssetType: AssetTypeVM})
	if err != nil || len(regions.Regions) == 0 {
		t.Fatalf("regions: %v", err)
	}
	r := regions.Regions[0]
	plans, err := c.Marketplace.ListAvailablePlans(ctx, &ListAvailablePlansParams{FilterAssetType: AssetTypeVM, FilterCountry: r.Country, FilterCity: r.City})
	if err != nil || len(plans.Plans) == 0 {
		t.Fatalf("plans in %s, %s: %v", r.City, r.Country, err)
	}
	offers, err := c.Marketplace.ListProviders(ctx, &ListProvidersParams{FilterPlanCode: plans.Plans[0].PlanCode, FilterCountry: r.Country, FilterCity: r.City, PageSize: 10})
	if err != nil || len(offers.Providers) == 0 {
		t.Fatalf("offers: %v", err)
	}
	t.Logf("org %s; %d distros; %d regions; plan %s in %s has %d offers",
		org.Organization.Name, len(distros.Distros), len(regions.Regions), plans.Plans[0].PlanCode, r.City, len(offers.Providers))
}

// testSSHKey returns a fresh ed25519 public key in OpenSSH format.
func testSSHKey(t *testing.T) string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(part)))
		b.Write(part)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(b.Bytes()) + " noderings-go-test"
}

func TestLiveSSHKey(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	name := "noderings-go-test-" + suffix()
	created, err := c.SSHKey.CreateSSHKey(ctx, &CreateSSHKeyRequest{Name: name, PublicKey: testSSHKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	id := created.SSHKey.ID
	t.Cleanup(func() { _, _ = c.SSHKey.DeleteSSHKey(context.Background(), id) })
	got, err := c.SSHKey.GetSSHKey(ctx, id)
	if err != nil || got.SSHKey.Name != name {
		t.Fatalf("get: %+v %v", got, err)
	}
	all, err := Collect(c.SSHKey.ListSSHKeysAll(ctx, &ListSSHKeysParams{PageSize: 1}))
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, k := range all {
		listed = listed || k.ID == id
	}
	if !listed {
		t.Errorf("new key not found in %d keys read one per page", len(all))
	}
	if _, err := c.SSHKey.DeleteSSHKey(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SSHKey.GetSSHKey(ctx, id); !IsNotFound(err) {
		t.Errorf("after delete: %v, want NotFound", err)
	}
}

func TestLiveDNSZone(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	name := "noderings-go-" + suffix() + ".com"
	created, err := c.DNSZone.CreateDNSZone(ctx, &CreateDNSZoneRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	zoneID := created.Zone.ID
	t.Cleanup(func() {
		_, _ = c.DNSZone.DeleteDNSZone(context.Background(), zoneID, &DeleteDNSZoneParams{Force: Ptr(true)})
	})
	if len(created.Zone.AssignedNameservers) == 0 {
		t.Error("zone has no nameservers")
	}
	testRecords(t, c, zoneID)
	if _, err := c.DNSZone.DeleteDNSZone(ctx, zoneID, &DeleteDNSZoneParams{Force: Ptr(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestLiveDNSRecordsInExistingZone(t *testing.T) {
	zoneID := os.Getenv("NODERINGS_TEST_ZONE_ID")
	if zoneID == "" {
		t.Skip("NODERINGS_TEST_ZONE_ID not set")
	}
	testRecords(t, liveClient(t), zoneID)
}

func testRecords(t *testing.T, c *Client, zoneID string) {
	t.Helper()
	ctx := context.Background()
	name := "sdk-" + suffix()
	t.Cleanup(func() {
		for _, typ := range []string{"A", "TXT"} {
			_, _ = c.DNSRecord.DeleteDNSRecord(context.Background(), zoneID, &DeleteDNSRecordParams{Name: name, Type: typ})
		}
	})
	rec, err := c.DNSRecord.UpsertDNSRecord(ctx, zoneID, &UpsertDNSRecordBody{
		Record: &DNSRecord{Name: name, Type: "A", TTL: 300, Rdata: []string{"203.0.113.10", "203.0.113.11"}},
	})
	if err != nil || len(rec.Rdata) != 2 {
		t.Fatalf("upsert A: %+v %v", rec, err)
	}
	if _, err := c.DNSRecord.UpsertDNSRecord(ctx, zoneID, &UpsertDNSRecordBody{
		Record: &DNSRecord{Name: name, Type: "TXT", TTL: 300, Rdata: []string{`"made by noderings-go"`}},
	}); err != nil {
		t.Fatalf("upsert TXT: %v", err)
	}
	list, err := c.DNSRecord.ListDNSRecords(ctx, zoneID, &ListDNSRecordsParams{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, r := range list.Records {
		if r.Name == name {
			types[r.Type] = true
		}
	}
	if !types["A"] || !types["TXT"] {
		t.Fatalf("records %+v", list.Records)
	}
	for _, typ := range []string{"A", "TXT"} {
		if _, err := c.DNSRecord.DeleteDNSRecord(ctx, zoneID, &DeleteDNSRecordParams{Name: name, Type: typ}); err != nil {
			t.Fatalf("delete %s: %v", typ, err)
		}
	}
	list, _ = c.DNSRecord.ListDNSRecords(ctx, zoneID, &ListDNSRecordsParams{Name: name})
	for _, r := range list.Records {
		if r.Name == name {
			t.Errorf("record left after delete: %+v", r)
		}
	}
}

func TestLiveFirewall(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	rs, err := c.Firewall.CreateFirewallRuleSet(ctx, &CreateFirewallRuleSetRequest{Ruleset: &FirewallRuleSetInput{Name: "noderings-go-test-" + suffix()}})
	if err != nil {
		t.Fatal(err)
	}
	id := rs.Ruleset.ID
	t.Cleanup(func() {
		_, _ = c.Firewall.DeleteFirewallRuleSet(context.Background(), id, &DeleteFirewallRuleSetParams{Force: Ptr(true)})
	})
	rule, err := c.Firewall.CreateFirewallRule(ctx, id, &CreateFirewallRuleBody{Rule: &FirewallRuleInput{
		Action: FirewallActionAccept, Direction: FirewallDirectionIngress, Protocol: FirewallProtocolTCP,
		DestinationPorts: "22", Source: &FirewallRuleSource{IPs: &FirewallRuleIPs{IPs: []string{"203.0.113.0/24"}}},
	}})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	rules, err := Collect(c.Firewall.ListFirewallRulesAll(ctx, id, nil))
	if err != nil || len(rules) != 1 || rules[0].Rule.DestinationPorts != "22" || rules[0].Rule.Protocol != FirewallProtocolTCP {
		t.Fatalf("rules %+v %v", rules, err)
	}
	renamed, err := c.Firewall.UpdateFirewallRuleSet(ctx, id, &UpdateFirewallRuleSetBody{Ruleset: &FirewallRuleSetInput{Name: "noderings-go-renamed-" + suffix()}})
	if err != nil || !strings.HasPrefix(renamed.Name, "noderings-go-renamed-") {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if _, err := c.Firewall.DeleteFirewallRule(ctx, rule.Rule.ID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if _, err := c.Firewall.DeleteFirewallRuleSet(ctx, id, &DeleteFirewallRuleSetParams{Force: Ptr(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestLiveCloudInitUserData(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	p, err := c.CloudInitUserData.CreateCloudInitUserData(ctx, &CreateCloudInitUserDataRequest{
		Name: "noderings-go-test-" + suffix(), UserData: "#cloud-config\npackages: [htop]\n", Description: "noderings-go live test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.CloudInitUserData.DeleteCloudInitUserData(context.Background(), p.ID) })
	u, err := c.CloudInitUserData.UpdateCloudInitUserData(ctx, p.ID, &UpdateCloudInitUserDataBody{Name: p.Name, UserData: "#cloud-config\npackages: [htop, jq]\n"})
	if err != nil || !strings.Contains(u.UserData, "jq") {
		t.Fatalf("update: %+v %v", u, err)
	}
	if _, err := c.CloudInitUserData.DeleteCloudInitUserData(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
}

// TestLiveVirtualMachineLifecycle orders the cheapest hourly VM it can find,
// waits for it, stops and starts it, snapshots it, tags it, attaches a
// firewall and deletes everything. It is billed for the minutes it runs.
func TestLiveVirtualMachineLifecycle(t *testing.T) {
	if os.Getenv("NODERINGS_TEST_BILLED") != "1" {
		t.Skip("NODERINGS_TEST_BILLED=1 not set")
	}
	c := liveClient(t)
	ctx := context.Background()
	planID, regionID := os.Getenv("NODERINGS_TEST_PLAN_ID"), os.Getenv("NODERINGS_TEST_REGION_ID")
	if planID == "" || regionID == "" {
		t.Skip("NODERINGS_TEST_PLAN_ID and NODERINGS_TEST_REGION_ID not set")
	}
	key, err := c.SSHKey.CreateSSHKey(ctx, &CreateSSHKeyRequest{Name: "noderings-go-vm-" + suffix(), PublicKey: testSSHKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.SSHKey.DeleteSSHKey(context.Background(), key.SSHKey.ID) })

	req := &CreateVirtualMachineRequest{
		Hostname: "noderings-go-" + suffix(), PlanID: planID, RegionID: regionID,
		Distro: DistroUbuntu2604, SSHKeyIDs: []string{key.SSHKey.ID}, Tags: []string{"noderings-go-test"},
	}
	start := time.Now()
	created, err := c.VirtualMachine.CreateVirtualMachine(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	vmID := created.VirtualMachine.ID
	t.Logf("created %s (%s) with idempotency key %s", req.Hostname, vmID, req.IdempotencyKey)
	t.Cleanup(func() {
		_, _ = c.VirtualMachine.DeleteVirtualMachine(context.Background(), vmID)
		_ = c.WaitForVirtualMachineDeleted(context.Background(), vmID, &WaitOptions{Timeout: 5 * time.Minute})
	})
	wait := &WaitOptions{Interval: 5 * time.Second, Timeout: 10 * time.Minute}
	vm, err := c.WaitForVirtualMachineStatus(ctx, vmID, wait, OperationStatusStarted)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("started after %v, has IPv4: %v", time.Since(start).Round(time.Second), vm.IPv4 != "")

	if _, err := c.VirtualMachine.StopVirtualMachine(ctx, vmID, &StopVirtualMachineBody{}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := c.WaitForVirtualMachineStatus(ctx, vmID, wait, OperationStatusStopped); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VirtualMachine.StartVirtualMachine(ctx, vmID, &StartVirtualMachineBody{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := c.WaitForVirtualMachineStatus(ctx, vmID, wait, OperationStatusStarted); err != nil {
		t.Fatal(err)
	}

	tagged, err := c.VirtualMachine.UpdateVirtualMachine(ctx, vmID, &UpdateVirtualMachineBody{AddTags: []string{"sdk"}, RemoveTags: []string{"noderings-go-test"}})
	if err != nil || len(tagged.Tags) != 1 || tagged.Tags[0] != "sdk" {
		t.Fatalf("tags: %v %v", tagged.Tags, err)
	}

	snap, err := c.Snapshot.CreateSnapshot(ctx, vmID, &CreateSnapshotBody{Description: "noderings-go test"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snaps, err := Collect(c.Snapshot.ListSnapshotsAll(ctx, vmID, nil))
	if err != nil || len(snaps) == 0 {
		t.Fatalf("snapshots %v %v", snaps, err)
	}
	if _, err := c.Snapshot.DeleteSnapshot(ctx, vmID, snap.Snapshot.ID); err != nil && !IsBusy(err) {
		t.Errorf("delete snapshot: %v", err)
	}

	rs, err := c.Firewall.CreateFirewallRuleSet(ctx, &CreateFirewallRuleSetRequest{Ruleset: &FirewallRuleSetInput{Name: "noderings-go-vm-" + suffix()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = c.Firewall.DeleteFirewallRuleSet(context.Background(), rs.Ruleset.ID, &DeleteFirewallRuleSetParams{Force: Ptr(true)})
	})
	if _, err := c.VirtualMachine.AttachVMFirewall(ctx, vmID, &AttachVMFirewallBody{RulesetID: rs.Ruleset.ID}); err != nil {
		t.Errorf("attach firewall: %v", err)
	} else if got, _ := c.VirtualMachine.GetVirtualMachine(ctx, vmID); got.FirewallRulesetID != rs.Ruleset.ID {
		t.Errorf("firewall ruleset %q, want %q", got.FirewallRulesetID, rs.Ruleset.ID)
	}

	if _, err := c.VirtualMachine.DeleteVirtualMachine(ctx, vmID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.WaitForVirtualMachineDeleted(ctx, vmID, wait); err != nil {
		t.Fatal(err)
	}
	t.Logf("whole lifecycle took %v", time.Since(start).Round(time.Second))
}
