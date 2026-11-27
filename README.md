# noderings-go

[![ci](https://github.com/noderings/noderings-go/actions/workflows/ci.yml/badge.svg)](https://github.com/noderings/noderings-go/actions/workflows/ci.yml)
[![live](https://github.com/noderings/noderings-go/actions/workflows/live.yml/badge.svg)](https://github.com/noderings/noderings-go/actions/workflows/live.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/noderings/noderings-go.svg)](https://pkg.go.dev/github.com/noderings/noderings-go)
[![Go version](https://img.shields.io/github/go-mod/go-version/noderings/noderings-go)](go.mod)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/noderings/noderings-go/badge)](https://scorecard.dev/viewer/?uri=github.com/noderings/noderings-go)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

The official Go SDK for the [NodeRings](https://noderings.com) API. It covers the whole public
API: virtual machines, DNS, firewalls, snapshots and backups, game servers, IP address
management, the marketplace, billing, access control, support tickets, and the provider side
for hosts who sell on NodeRings. That is 485 operations in 69 services, all typed.

```sh
go get github.com/noderings/noderings-go
```

Requires Go 1.24 or later.

## Quick start

```go
client, err := noderings.NewClient(os.Getenv("NODERINGS_TOKEN"))
if err != nil {
	log.Fatal(err)
}
ctx := context.Background()

// Every virtual machine, page by page.
for vm, err := range client.VirtualMachine.ListVirtualMachinesAll(ctx, nil) {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(vm.Hostname, vm.OperationStatus, vm.IPv4)
}

// Order a server and wait until it runs.
created, err := client.VirtualMachine.CreateVirtualMachine(ctx, &noderings.CreateVirtualMachineRequest{
	Hostname:  "web-1",
	PlanID:    planID,   // from client.Marketplace.ListProviders
	RegionID:  regionID,
	Distro:    noderings.DistroUbuntu2604,
	SSHKeyIDs: []string{sshKeyID},
})
if err != nil {
	log.Fatal(err)
}
vm, err := client.WaitForVirtualMachineStatus(ctx, created.VirtualMachine.ID, nil, noderings.OperationStatusStarted)

// Give it a DNS name.
_, err = client.DNSRecord.UpsertDNSRecord(ctx, zoneID, &noderings.UpsertDNSRecordBody{
	Record: &noderings.DNSRecord{Name: "web-1", Type: "A", TTL: 300, Rdata: []string{vm.IPv4}},
})
```

[`examples/quickstart`](examples/quickstart) is a complete program, and the
[package documentation](https://pkg.go.dev/github.com/noderings/noderings-go) has more examples.

## Authentication

Create a service account in the NodeRings console under **Organization → Access Control →
Service accounts**, give it the permissions your program needs, and pass its token to
`NewClient`. Keep the token out of source code; read it from the environment or a secret store.

| Option | Use |
|---|---|
| `WithOrganization(id)` | Act in another organization the token can access (the `X-Organization-ID` header). By default the token's own organization is used. |
| `WithBaseURL(url)` | Another API endpoint. The default is `https://api.noderings.com`. |
| `WithHTTPClient(c)` | Your own `*http.Client` (proxy, timeouts, transport). |
| `WithUserAgent(s)` | Add your product to the User-Agent. |
| `WithRetries(n, min, max)` | Retry count and backoff (default 3, 500 ms, 10 s). |
| `WithLogger(l)` | A `*slog.Logger` for one debug line per request. Tokens and bodies are never logged. |

A few user-account operations (sign-in, MFA, linked identities, the OAuth authorize step) only
work with a signed-in user's session, not with a service account token.

## Services

Every area of the API is a field of `Client`, and every method is one API operation, named as in
the [API description](https://api.noderings.com/v1/openapi.json).

| Area | Services |
|---|---|
| Compute | `VirtualMachine` (create, start, stop, restart, reinstall, delete, tags, firewall, backups, extra IPs, console), `Snapshot`, `Backup`, `VMTemplate`, `CloudInitUserData`, `CloudInitTemplate`, `InstantApp`, `Metadata` (distro catalog) |
| Game servers | `GameServer` (46 operations: lifecycle, files, databases, backups, schedules, allocations), `GameServerTemplate` |
| DNS | `DNSZone`, `DNSRecord`, `DNSSEC`, `Domain` |
| Network | `Firewall` (rulesets, rules, address groups), `IPAM` |
| Marketplace | `Marketplace` (regions, plan codes, offers, plans), `Region`, `Plan`, `ClientProviderReview` |
| Billing | `Subscription`, `Addon`, `Invoice`, `Payment`, `PaymentMethod`, `Refund`, `Credit`, `Coupon`, `UsageReport`, `ExchangeRate` |
| Organization and access | `Organization`, `User`, `ServiceAccount`, `Group`, `Role`, `Permission`, `Invite`, `Secret`, `EnvironmentVariable`, `OAuth2` |
| Monitoring | `Monitoring`, `BusinessMonitoring`, `Metric`, `MetricsCredential`, `ClientDashboard` |
| Support | `Ticket`, `Chat`, `Roadmap` |
| Providers (hosts selling on NodeRings) | `ProviderPlan`, `ProviderStorefront`, `ProviderCustomer`, `ProviderDashboard`, `ProviderDashboardPlansTab`, `ProviderReview`, `ProviderNetwork`, `ProviderConnect`, `ProviderOnboarding`, `ProviderTicketNotification`, `PayoutConfiguration`, `PayoutInvoice`, `ManualPayout`, `LiveValidation`, `Preflight`, `Platform`, `Node`, `NodePlacement`, `Agent`, `HookScript` |

For an operation the SDK does not have yet, `client.Do(ctx, method, path, query, body, &out)`
sends a request with the same authentication, retries and error handling.

## How it works

**Types.** Every request and response is a Go struct with the API's JSON names. Enums are typed
string constants (`noderings.DistroUbuntu2604`, `noderings.OperationStatusStarted`), timestamps
are `*time.Time`, and 64-bit numbers, which the API sends as strings, are plain `int64`. Booleans
in request bodies are `*bool`, so `false` can be sent: `StopVM: noderings.Ptr(false)`.

**Paging.** List operations that take `page` and `pageSize` have an `...All` variant returning an
iterator over every result. It reads 100 rows per page (the API's maximum) and stops when you
break out of the loop. `noderings.Collect` turns an iterator into a slice.

**Errors.** API errors are `*noderings.Error` with the HTTP status, the API's status code, its
message and structured details. Helpers cover the common checks: `IsNotFound`, `IsAlreadyExists`,
`IsUnauthenticated`, `IsPermissionDenied`, `IsInvalidArgument`, `IsRateLimited`,
`IsQuotaExceeded` (a plan limit such as the number of servers, with `Reason()` like
`VM_LIMIT_REACHED`) and `IsBusy` (another operation on the resource is still running).

**Retries.** A request is retried when the API rate-limits it (HTTP 429, unless a plan quota is
used up, honoring `Retry-After`), and on connection errors and HTTP 502, 503 and 504 when it is
safe to repeat: reads, `PUT`, `DELETE`, and creates that carry an idempotency key.

**Idempotency.** Creates that take an idempotency key (virtual machines, game servers) get a new
UUID automatically, written back into your request struct. Send the same struct again to retry
a create that may have gone through, and no second server is ordered.

**Waiting.** `WaitForVirtualMachineStatus`, `WaitForVirtualMachineDeleted` and the general
`WaitForVirtualMachine` poll until a condition holds, with an interval and timeout.

## Testing

| Suite | What it checks |
|---|---|
| `go test ./...` | Calls every one of the 485 generated methods against a fake API and checks the HTTP method, the path and its escaping, every query parameter's name, the body and the auth headers against the API description. Also covers retries, `Retry-After`, errors, idempotency keys, iterators, waiting, 64-bit and enum encoding. |
| `go test -tags live` | Against the real API: every read that needs no ID, checked for fields the description lacks; SSH keys, a DNS zone with records, firewall rulesets and rules, and cloud-init data created and deleted. With `NODERINGS_TEST_BILLED=1`, a virtual machine is created, started, stopped, tagged, snapshotted, given a firewall and deleted. |

The live suite runs nightly in CI. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Versioning

The SDK is generated from [`api/openapi.json`](api/openapi.json) by [`internal/gen`](internal/gen).
A weekly job fetches the published description and opens a pull request when it changed.
Releases follow [semantic versioning](https://semver.org); while the version is 0.x, a minor
release can rename a field when the API itself renames it, and [CHANGELOG.md](CHANGELOG.md) says
so.

Not included: the payment-provider webhook endpoint (it receives calls, it does not make them) and
NodeRings staff-only enforcement endpoints.

## Related

[Terraform provider](https://github.com/noderings/terraform-provider-noderings) ·
[Pulumi](https://github.com/noderings/pulumi-noderings) ·
[Ansible](https://github.com/noderings/ansible-collection-noderings) ·
[external-dns webhook](https://github.com/noderings/external-dns-noderings-webhook) ·
[cert-manager webhook](https://github.com/noderings/cert-manager-webhook-noderings) ·
[Prometheus service discovery](https://github.com/noderings/prometheus-noderings-sd)

## License

[Apache 2.0](LICENSE)
