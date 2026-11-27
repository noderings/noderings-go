package main

import "testing"

func TestGoName(t *testing.T) {
	for in, want := range map[string]string{
		"planId":                      "PlanID",
		"sshKeyIds":                   "SSHKeyIDs",
		"ipv4":                        "IPv4",
		"additionalIpv4":              "AdditionalIPv4",
		"dnsZoneId":                   "DNSZoneID",
		"VirtualMachineService":       "VirtualMachineService",
		"OAuth2Service":               "OAuth2Service",
		"IPAMService":                 "IPAMService",
		"UBUNTU_26_04":                "Ubuntu2604",
		"ListDnsZones":                "ListDNSZones",
		"GetVNCConsole":               "GetVNCConsole",
		"vmId":                        "VMID",
		"k3sVersion":                  "K3sVersion",
		"ttl":                         "TTL",
		"idempotencyKey":              "IdempotencyKey",
		"FIREWALL_PROTOCOL_ICMP":      "FirewallProtocolICMP",
		"filterDdosProviders":         "FilterDDoSProviders",
		"x-request-id":                "XRequestID",
		"2fa":                         "N2Fa",
		"providerOrganizationId":      "ProviderOrganizationID",
		"CreateConnectTaxSessionBody": "CreateConnectTaxSessionBody",
	} {
		if got := goName(in); got != want {
			t.Errorf("goName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArgName(t *testing.T) {
	for in, want := range map[string]string{"vmId": "vmID", "id": "id", "type": "typeArg", "zoneId": "zoneID", "slug": "slug"} {
		if got := argName(in); got != want {
			t.Errorf("argName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommonPrefix(t *testing.T) {
	if p := commonPrefix([]string{"DISTRO_UNSPECIFIED", "DISTRO_UBUNTU_24_04"}); p != "DISTRO_" {
		t.Errorf("got %q", p)
	}
	// A prefix that would leave a value starting with a digit is not used.
	if p := commonPrefix([]string{"V_1", "V_2"}); p != "" {
		t.Errorf("got %q", p)
	}
}
