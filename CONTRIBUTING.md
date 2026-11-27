# Contributing

Thanks for helping improve noderings-go.

1. Open an issue before large changes.
2. Do not edit `zz_generated_*.go` by hand. Change the generator in `internal/gen` (or update
   `api/openapi.json`) and run `go generate ./...`. CI fails when the generated code is stale.
3. `go test -race ./...` and `golangci-lint run` must pass. The unit tests need no token.
4. For changes that affect requests, also run the live tests against your own organization:

   ```sh
   NODERINGS_TOKEN=... go test -tags live -run Live -v ./...
   ```

   They create and delete throwaway resources. `NODERINGS_TEST_BILLED=1` (with
   `NODERINGS_TEST_PLAN_ID` and `NODERINGS_TEST_REGION_ID`) also creates a virtual machine,
   which is billed for the minutes it exists.
5. Use [Conventional Commits](https://www.conventionalcommits.org/) and sign off your commits
   (`git commit -s`) for the [Developer Certificate of Origin](https://developercertificate.org/).

Report security problems privately as described in [SECURITY.md](SECURITY.md).
