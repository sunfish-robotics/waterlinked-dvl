# Changelog

## [1.0.0](https://github.com/sunfish-robotics/waterlinked-dvl/compare/v0.1.0...v1.0.0) (2026-09-13)


### ⚠ BREAKING CHANGES

* Conn.VelocityReports now returns <-chan Sample[VelocityReport] instead of <-chan Sample[*VelocityReport]. Conn.DeadReckoningReports now returns <-chan Sample[DeadReckoningReport] instead of <-chan Sample[*DeadReckoningReport].
* VelocityReport.Valid, Velocity, FigureOfMerit, Covariance, and Altitude are replaced by VelocityReport.Measurement, a *VelocityMeasurement that is nil when the device has no lock.
* UnknownReport and Conn.UnknownReports are replaced by UnhandledFrame and Conn.UnhandledFrames. Malformed reports, unsolicited responses, and undecodable command results no longer end the connection.

### Features

* add diagnostic CLI ([cbee23e](https://github.com/sunfish-robotics/waterlinked-dvl/commit/cbee23e5beadab3e6a861724b78f40ce4040b81f))
* add Dialer with idle and command timeouts ([5de60f2](https://github.com/sunfish-robotics/waterlinked-dvl/commit/5de60f201a9105b48682bec34e6db7384ce03f0b))
* define public DVL API ([80d5c53](https://github.com/sunfish-robotics/waterlinked-dvl/commit/80d5c53247129af1e2413b32494c1ff927fb9fd3))
* deliver report samples by value ([b4f96a6](https://github.com/sunfish-robotics/waterlinked-dvl/commit/b4f96a619f951cac140183111d8bebe67cb52103))
* expose typed report streams ([7d9d9ff](https://github.com/sunfish-robotics/waterlinked-dvl/commit/7d9d9fff10b64f3cc5f32bb53f46704d876ec506))
* implement DVL TCP client ([9105b59](https://github.com/sunfish-robotics/waterlinked-dvl/commit/9105b590a8eea7a88c931d9ac10b89793f08a265))
* model velocity measurements as absent without lock ([d02f0a1](https://github.com/sunfish-robotics/waterlinked-dvl/commit/d02f0a13a642fef3d1be35023f9ce7d4ad96e0fd))
* report undecodable frames on a stream instead of ending the connection ([8523859](https://github.com/sunfish-robotics/waterlinked-dvl/commit/8523859505efd7cba5c59436136bba2b477b4bf0))


### Bug Fixes

* align CLI with report API ([fa93963](https://github.com/sunfish-robotics/waterlinked-dvl/commit/fa93963bec0728d98595d39a340d1913c1b3bec4))

## 0.1.0 (2026-09-13)

Initial public release of the Go client and diagnostic CLI for the Water Linked DVL A50 and A125 TCP JSON API.

### Go client

- Decodes bottom- and water-relative velocity and local dead-reckoning reports into typed Go values while preserving device timestamps, covariance, status, and per-transducer diagnostics.
- Represents a velocity report without lock as a nil `Measurement`, preventing stale velocity, covariance, and altitude values from being consumed as current measurements.
- Multiplexes reports and commands over one TCP connection. Independent bounded streams drop their oldest items under backpressure and report the loss through `Sample.DroppedBefore`.
- Exposes explicit connection lifecycle and supervision through `Dialer`, `Done`, `Err`, and `Close`, including configurable idle and command timeouts.
- Supports device identity, complete configuration reads, validated partial configuration updates, dead-reckoning reset, and gyroscope calibration.
- Preserves unknown and malformed frames on `UnhandledFrames` when doing so does not make command correlation ambiguous.

### Diagnostic CLI

- Adds `waterlinked-dvl` commands for identity, configuration, report capture, dead-reckoning reset, and gyroscope calibration.
- Supports human-readable output and machine-readable JSON, with configuration readback and per-stream loss counts.

### Compatibility

The client targets A50 and A125 devices running firmware 2.4.0 or later and the backwards-compatible `json_v3` protocol family from `json_v3.1` onwards. `Info` requires firmware 2.7.2. Live validation covered an A50 running firmware 2.7.2; the A125, water tracking, and dead-reckoning report capture were not exercised on hardware before this release.
