# waterlinked-dvl

[![CI][ci-badge]][ci]
[![Go Reference][reference-badge]][go-reference]
[![License: Apache-2.0][license-badge]][license]

A Go client for the Water Linked DVL A50/A125 TCP JSON API. It provides typed
access to velocity, water-tracking, dead-reckoning, identity, and configuration
data while preserving the device protocol's timing, lock state, covariance,
status, and transducer details.

## Supported hardware

The package targets DVL A50/A125 devices running firmware 2.4.0 or later,
which speak TCP JSON protocol json_v3.1 and later. `Info` additionally
requires firmware 2.7.2, because the device did not add `get_version_info`
until that release.

## Installation

```console
go get github.com/sunfish-robotics/waterlinked-dvl
```

## Reports

The package connects to the configured DVL TCP endpoint on port `16171` and
demultiplexes every frame it reads onto one of three independent, receive-only
streams: `VelocityReports` for `velocity` and `velocity_water` reports,
`DeadReckoningReports` for `position_local` reports, and `UnhandledFrames` for
everything else the package cannot turn into one of those — a malformed
report, a report of an unknown type, or an unsolicited or unreadable command
response. Each stream is a bounded, drop-oldest queue with its own loss
counter, so a slow or absent consumer on one stream can never block another
stream or a pending command.

A velocity report's status, transducer readings, and timing are always
populated. `VelocityReport.Measurement` is populated only while the DVL has a
lock on the reflecting surface and is `nil` otherwise, so a caller cannot
accidentally read the stale velocity, figure of merit, covariance, or altitude
the device keeps sending after losing lock.

Identity and configuration are typed commands rather than streams: `Info`,
`Config`, `UpdateConfig`, `ResetDeadReckoning`, and `CalibrateGyro` each send
one command and wait for its response, interleaved with report delivery on the
same connection.

## Frame policy

A connection ends only on a transport error or EOF, a frame over the size cap,
a failed command write, a command whose response has not arrived within the
command timeout, no data within the idle timeout when one is set, or a
response naming a command other than the one in flight. Every other frame the
package cannot decode is published on `UnhandledFrames` instead, and a
response the caller cannot use fails only that one command; the connection and
every other stream carry on.

## Connecting

`Dial` opens a connection with default settings. A `Dialer` configures the TCP
dialer, an optional idle timeout that ends the connection when the device goes
quiet, the command-response timeout, and the per-stream report buffer:

```go
dialer := dvl.Dialer{IdleTimeout: 5 * time.Second}
conn, err := dialer.Dial(ctx, "192.168.194.95:16171")
```

A device with acoustics disabled may legitimately send nothing, so pick an
idle timeout with that in mind, or leave it zero to disable the check.

Each connection represents one TCP epoch. When it ends, the caller opens a new
one and decides how and when to retry; `Conn.Done` and `Conn.Err` report when
and why. Measurements remain in the frame emitted by the DVL so callers can
apply installation-specific transformations deliberately.

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
