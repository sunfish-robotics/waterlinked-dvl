# waterlinked-dvl

[![CI][ci-badge]][ci]
[![Go Reference][reference-badge]][go-reference]
[![License: Apache-2.0][license-badge]][license]

A Go client for the Water Linked DVL A50/A125 TCP JSON API. It provides typed
access to velocity, water-tracking, dead-reckoning, identity, and configuration
data while preserving the device protocol's timing, validity, covariance,
status, and transducer details.

## Installation

```console
go get github.com/sunfish-robotics/waterlinked-dvl
```

## Protocol coverage

The package connects to the configured DVL TCP endpoint on port `16171` and
supports:

- newline-delimited `velocity`, `velocity_water`, and `position_local` reports;
- separate receive-only streams for velocity, dead-reckoning, and unknown reports,
  with per-stream dropped-report counts when a consumer falls behind;
- complete timing, covariance, validity, status, and per-transducer fields;
- typed device identity and configuration commands;
- interleaved report and command-response handling;
- broadcast connection lifecycle with a durable terminal error; and
- explicit cancellation, command, and protocol errors.

Each connection represents one TCP epoch. When the connection ends, the caller
opens a new one and decides how and when to retry. Measurements remain in the
frame emitted by the DVL so callers can apply installation-specific
transformations deliberately.

## Requirements

Waterlinked-dvl requires Go 1.25 or later.

## Development

Use the standard Go tools directly:

```console
go test -race ./...
go vet ./...
gofmt -w .
```

CI runs race-enabled tests on Go 1.25 and the current stable Go release, then
checks formatting and `go vet`.

## Protocol documentation

- [A50/A125 TCP JSON API](https://docs.waterlinked.com/dvl/dvl-json-protocol/)
- [Axes and mounting conventions](https://docs.waterlinked.com/dvl/axes/)
- [Water Linked integration guide](https://docs.waterlinked.com/dvl/integration/)

## Stability

Waterlinked-dvl is pre-1.0, so its public API may change while it is validated
against production DVL installations.

## Licence

Apache-2.0. See [LICENSE](LICENSE).

[ci]: https://github.com/sunfish-robotics/waterlinked-dvl/actions/workflows/ci.yml
[ci-badge]: https://github.com/sunfish-robotics/waterlinked-dvl/actions/workflows/ci.yml/badge.svg
[go-reference]: https://pkg.go.dev/github.com/sunfish-robotics/waterlinked-dvl
[license]: LICENSE
[license-badge]: https://img.shields.io/badge/license-Apache--2.0-blue.svg
[reference-badge]: https://pkg.go.dev/badge/github.com/sunfish-robotics/waterlinked-dvl.svg
