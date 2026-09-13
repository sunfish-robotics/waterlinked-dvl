package dvl

// DeviceInfo identifies the connected DVL and its firmware.
type DeviceInfo struct {
	// ChipID is the device's unique hardware identifier, such as
	// "0xf1440c09140b04".
	ChipID string

	// HardwareRevision is the board revision.
	HardwareRevision int

	// ProductID is the numeric product code. A50 devices report 21035.
	ProductID int

	// ProductName is the model name, such as "DVL A50".
	ProductName string

	// Variant distinguishes builds of one model, such as "standard" or
	// "performance".
	Variant string

	// FirmwareVersion is the complete version string including build metadata.
	// FirmwareVersionShort is the release number alone, such as "2.7.2".
	FirmwareVersion      string
	FirmwareVersionShort string

	// Ready reports whether the device has finished starting up.
	Ready bool

	// ProtocolVersion is the format the device used for the response.
	ProtocolVersion ProtocolVersion
}

// Config is the complete configuration reported by the device.
type Config struct {
	// SpeedOfSound is measured in metres per second. The device accepts
	// values from 1000 to 2000.
	SpeedOfSound float64

	// MountingYawOffset is the clockwise rotation in degrees, from 0 to 360,
	// from the vehicle's forward axis to the DVL's forward axis. When it is
	// non-zero the device emits velocity and dead reckoning in the vehicle
	// frame. The device does not compensate for mounting pitch or roll.
	MountingYawOffset float64

	// AcousticEnabled controls whether the DVL pings on its own. When false
	// the device pings only when externally triggered and otherwise sends no
	// velocity reports. Triggering is not exposed by this package.
	AcousticEnabled bool

	// DarkModeEnabled turns off the status LED, which avoids interference
	// with cameras.
	DarkModeEnabled bool

	// RangeMode limits where the device searches for bottom lock, or selects
	// water tracking.
	RangeMode RangeMode

	// PeriodicCyclingEnabled makes the device confirm every ten seconds that
	// the surface it has locked onto is the real bottom rather than a closer
	// reflection. It is on by default. Some measurements are lost during each
	// check.
	PeriodicCyclingEnabled bool
}

// ConfigUpdate is a partial device configuration. A nil field is left
// unchanged; a non-nil field is applied even when its value is false or zero.
// Each field has the meaning and range documented on Config.
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
// Besides the named modes below, A50/A125 devices accept a fixed range mode
// written "=a" and an inclusive span written "a<=b", where a and b are digits
// from 0 to 4. Water Linked's range mode page lists the altitude each digit
// covers and the report rate it yields.
type RangeMode string

const (
	// RangeModeAuto searches the device's full operating range for bottom lock.
	// It is the device default.
	RangeModeAuto RangeMode = "auto"

	// RangeModeWaterTracking measures velocity relative to the water column,
	// about 1.5 to 4.5 metres below the DVL, instead of the bottom. Reports
	// arrive at 2 Hz and carry no altitude.
	RangeModeWaterTracking RangeMode = "wt"
)
