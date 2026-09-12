package dvl

import (
	"encoding/json"
	"time"
)

// Report is a typed message from the DVL report stream.
//
// The package owns the set of report implementations. Callers should use a
// type switch and handle UnknownReport so newer protocol messages do not need
// to terminate an otherwise compatible connection.
type Report interface {
	report()
}

// Sample is one report delivered to a consumer.
type Sample struct {
	Report Report

	// DroppedBefore is the number of older reports discarded since the previous
	// delivered sample because the consumer did not keep up.
	DroppedBefore uint64
}

// ProtocolVersion is the format identifier carried by a device message.
type ProtocolVersion string

const (
	// ProtocolJSONV3_3 is the current documented A50/A125 TCP JSON format.
	ProtocolJSONV3_3 ProtocolVersion = "json_v3.3"
)

// VelocityReference identifies what a velocity measurement is relative to.
type VelocityReference string

const (
	// VelocityReferenceBottom is velocity relative to the reflecting bottom.
	VelocityReferenceBottom VelocityReference = "bottom"

	// VelocityReferenceWater is velocity relative to the water column.
	VelocityReferenceWater VelocityReference = "water"
)

// Vector3 is a three-dimensional value in the frame emitted by the DVL: X
// forward, Y starboard, and Z down.
type Vector3 struct {
	X float64
	Y float64
	Z float64
}

// EulerAngles is roll, pitch, and yaw in degrees.
type EulerAngles struct {
	Roll  float64
	Pitch float64
	Yaw   float64
}

// Matrix3 is a 3×3 matrix in row-major order.
type Matrix3 [3][3]float64

// Status is the raw eight-bit status mask on a velocity report. Unknown bits
// are preserved for newer device firmware.
type Status uint8

const (
	// StatusHighTemperature indicates that the DVL is approaching thermal
	// shutdown.
	StatusHighTemperature Status = 1 << iota
)

// Has reports whether every bit in flag is present.
func (s Status) Has(flag Status) bool {
	return s&flag == flag
}

// TransducerReading contains one beam's measurement and diagnostics.
type TransducerReading struct {
	// ID is the zero-based protocol identifier, normally in the range 0–3.
	ID uint8

	// Velocity and Distance are measured in metres per second and metres.
	Velocity float64
	Distance float64

	// RSSI and NSD are measured in dBm.
	RSSI float64
	NSD  float64

	BeamValid bool
}

// VelocityReport is a bottom- or water-relative velocity calculation.
type VelocityReport struct {
	Reference VelocityReference

	// Interval is the elapsed time since the preceding velocity report.
	Interval time.Duration
	Velocity Vector3

	// FigureOfMerit is the estimated velocity standard deviation in metres per
	// second. Lower values indicate a more precise estimate.
	FigureOfMerit float64

	// Covariance is the velocity covariance matrix in (m/s)².
	Covariance Matrix3

	// Altitude is the distance to the reflecting surface along the emitted Z
	// axis, in metres. It is nil when the device omits altitude.
	Altitude *float64

	Valid         bool
	Status        Status
	ValidAt       time.Time
	TransmittedAt time.Time
	Transducers   []TransducerReading

	ProtocolVersion ProtocolVersion
}

func (*VelocityReport) report() {}

// DeadReckoningStatus is the raw status value on a position_local report.
type DeadReckoningStatus uint8

const (
	DeadReckoningStatusOK    DeadReckoningStatus = 0
	DeadReckoningStatusFault DeadReckoningStatus = 1
)

// DeadReckoningReport is the device's local position and orientation estimate.
// Its frame is established at startup or by ResetDeadReckoning.
type DeadReckoningReport struct {
	At       time.Time
	Position Vector3
	Attitude EulerAngles

	// HorizontalStandardDeviation is the estimated horizontal position error in
	// metres.
	HorizontalStandardDeviation float64

	Status          DeadReckoningStatus
	ProtocolVersion ProtocolVersion
}

func (*DeadReckoningReport) report() {}

// UnknownReport preserves a well-formed report type unknown to this version of
// the package. Raw is an owned copy of the complete JSON object.
type UnknownReport struct {
	Type            string
	ProtocolVersion ProtocolVersion
	Raw             json.RawMessage
}

func (*UnknownReport) report() {}
