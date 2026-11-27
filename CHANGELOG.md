# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Typed client for the whole public NodeRings API: 485 operations in 69 services, generated from
  the API description, with typed enums, timestamps and 64-bit numbers.
- Retries for rate limits and, where safe, for gateway and availability errors; typed errors
  with helpers; automatic idempotency keys on creates; iterators for every paged list; wait
  helpers for virtual machines; `Client.Do` for raw requests.
