package dvl

// DeviceInfo identifies the connected DVL and its firmware.
type DeviceInfo struct {
	ChipID               string
	HardwareRevision     int
	ProductID            int
	ProductName          string
	Variant              string
	FirmwareVersion      string
	FirmwareVersionShort string
	Ready                bool
	ProtocolVersion      ProtocolVersion
}

// Config is the complete configuration reported by the device.
type Config struct {
	// SpeedOfSound is measured in metres per second.
	SpeedOfSound float64

	// MountingYawOffset is the clockwise rotation in degrees from the vehicle's
	// forward axis to the DVL's forward axis. The device does not compensate for
	// mounting pitch or roll.
	MountingYawOffset float64

	AcousticEnabled        bool
	DarkModeEnabled        bool
	RangeMode              RangeMode
	PeriodicCyclingEnabled bool
}

// ConfigUpdate is a partial device configuration. A nil field is left
// unchanged; a non-nil field is applied even when its value is false or zero.
type ConfigUpdate struct {
	SpeedOfSound           *float64
	MountingYawOffset      *float64
	AcousticEnabled        *bool
	DarkModeEnabled        *bool
	RangeMode              *RangeMode
	PeriodicCyclingEnabled *bool
}

// RangeMode controls where the DVL searches for a tracking reference.
//
// Besides the named modes below, A50/A125 devices accept fixed modes such as
// "=1" and inclusive ranges such as "1<=3".
type RangeMode string

const (
	// RangeModeAuto searches the device's full operating range for bottom lock.
	RangeModeAuto RangeMode = "auto"

	// RangeModeWaterTracking measures velocity relative to the water column.
	RangeModeWaterTracking RangeMode = "wt"
)
