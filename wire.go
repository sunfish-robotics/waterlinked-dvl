package dvl

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type commandMessage struct {
	Command    string         `json:"command"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type commandResponse struct {
	ResponseTo   string
	Success      bool
	ErrorMessage string
	Result       json.RawMessage
	Format       ProtocolVersion
}

type decodedMessage struct {
	messageType   string
	response      *commandResponse
	velocity      *VelocityReport
	deadReckoning *DeadReckoningReport
	unknown       *UnknownReport
}

type messageHeader struct {
	Type   string          `json:"type"`
	Format ProtocolVersion `json:"format"`
}

type responseWire struct {
	ResponseTo   string          `json:"response_to"`
	Success      *bool           `json:"success"`
	ErrorMessage *string         `json:"error_message"`
	Result       json.RawMessage `json:"result"`
	Format       ProtocolVersion `json:"format"`
}

type velocityWire struct {
	Time               *float64         `json:"time"`
	VX                 *float64         `json:"vx"`
	VY                 *float64         `json:"vy"`
	VZ                 *float64         `json:"vz"`
	FigureOfMerit      *float64         `json:"fom"`
	Covariance         [][]float64      `json:"covariance"`
	Altitude           *float64         `json:"altitude"`
	Transducers        []transducerWire `json:"transducers"`
	VelocityValid      *bool            `json:"velocity_valid"`
	Status             *uint8           `json:"status"`
	Format             ProtocolVersion  `json:"format"`
	Type               string           `json:"type"`
	TimeOfValidity     *int64           `json:"time_of_validity"`
	TimeOfTransmission *int64           `json:"time_of_transmission"`
}

type transducerWire struct {
	ID        *uint8   `json:"id"`
	Velocity  *float64 `json:"velocity"`
	Distance  *float64 `json:"distance"`
	RSSI      *float64 `json:"rssi"`
	NSD       *float64 `json:"nsd"`
	BeamValid *bool    `json:"beam_valid"`
}

type deadReckoningWire struct {
	Timestamp *float64        `json:"ts"`
	X         *float64        `json:"x"`
	Y         *float64        `json:"y"`
	Z         *float64        `json:"z"`
	Standard  *float64        `json:"std"`
	Roll      *float64        `json:"roll"`
	Pitch     *float64        `json:"pitch"`
	Yaw       *float64        `json:"yaw"`
	Status    *uint8          `json:"status"`
	Format    ProtocolVersion `json:"format"`
}

type deviceInfoWire struct {
	ChipID               *string `json:"chipid"`
	HardwareRevision     *int    `json:"hardware_revision"`
	ProductID            *int    `json:"product_id"`
	ProductName          *string `json:"product_name"`
	Variant              *string `json:"variant"`
	FirmwareVersion      *string `json:"version"`
	FirmwareVersionShort *string `json:"version_short"`
	Ready                *bool   `json:"is_ready"`
}

type configWire struct {
	SpeedOfSound           *float64 `json:"speed_of_sound"`
	MountingYawOffset      *float64 `json:"mounting_rotation_offset"`
	AcousticEnabled        *bool    `json:"acoustic_enabled"`
	DarkModeEnabled        *bool    `json:"dark_mode_enabled"`
	RangeMode              *string  `json:"range_mode"`
	PeriodicCyclingEnabled *bool    `json:"periodic_cycling_enabled"`
}

func decodeMessage(data []byte) (decodedMessage, error) {
	var header messageHeader
	if err := json.Unmarshal(data, &header); err != nil {
		return decodedMessage{}, err
	}
	message := decodedMessage{messageType: header.Type}
	if header.Type == "" {
		return message, errors.New("missing type")
	}
	if !supportedProtocolVersion(header.Format) {
		return message, fmt.Errorf("unsupported format %q", header.Format)
	}

	switch header.Type {
	case "velocity", "velocity_water":
		var err error
		message.velocity, err = decodeVelocityReport(data)
		return message, err
	case "position_local":
		var err error
		message.deadReckoning, err = decodeDeadReckoningReport(data)
		return message, err
	case "response":
		response, err := decodeResponse(data)
		message.response = &response
		return message, err
	default:
		raw := append(json.RawMessage(nil), data...)
		message.unknown = &UnknownReport{
			Type:            header.Type,
			ProtocolVersion: header.Format,
			Raw:             raw,
		}
		return message, nil
	}
}

func decodeResponse(data []byte) (commandResponse, error) {
	var wire responseWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return commandResponse{}, err
	}
	if wire.ResponseTo == "" {
		return commandResponse{}, errors.New("missing response_to")
	}
	if wire.Success == nil {
		return commandResponse{}, errors.New("missing success")
	}
	if wire.ErrorMessage == nil {
		return commandResponse{}, errors.New("missing error_message")
	}
	if wire.Result == nil {
		return commandResponse{}, errors.New("missing result")
	}
	if wire.Format == "" {
		return commandResponse{}, errors.New("missing format")
	}

	return commandResponse{
		ResponseTo:   wire.ResponseTo,
		Success:      *wire.Success,
		ErrorMessage: *wire.ErrorMessage,
		Result:       append(json.RawMessage(nil), wire.Result...),
		Format:       wire.Format,
	}, nil
}

func decodeVelocityReport(data []byte) (*VelocityReport, error) {
	var wire velocityWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}

	missing := missingFields(map[string]bool{
		"time":                 wire.Time != nil,
		"vx":                   wire.VX != nil,
		"vy":                   wire.VY != nil,
		"vz":                   wire.VZ != nil,
		"fom":                  wire.FigureOfMerit != nil,
		"covariance":           wire.Covariance != nil,
		"transducers":          wire.Transducers != nil,
		"velocity_valid":       wire.VelocityValid != nil,
		"status":               wire.Status != nil,
		"format":               wire.Format != "",
		"time_of_validity":     wire.TimeOfValidity != nil,
		"time_of_transmission": wire.TimeOfTransmission != nil,
	})
	if missing != "" {
		return nil, fmt.Errorf("missing %s", missing)
	}

	interval, err := milliseconds(*wire.Time)
	if err != nil {
		return nil, fmt.Errorf("time: %w", err)
	}
	covariance, err := matrix3(wire.Covariance)
	if err != nil {
		return nil, fmt.Errorf("covariance: %w", err)
	}
	transducers, err := decodeTransducers(wire.Transducers)
	if err != nil {
		return nil, err
	}

	reference := VelocityReferenceBottom
	if wire.Type == "velocity_water" {
		reference = VelocityReferenceWater
	}

	return &VelocityReport{
		Reference:       reference,
		Interval:        interval,
		Velocity:        Vector3{X: *wire.VX, Y: *wire.VY, Z: *wire.VZ},
		FigureOfMerit:   *wire.FigureOfMerit,
		Covariance:      covariance,
		Altitude:        wire.Altitude,
		Valid:           *wire.VelocityValid,
		Status:          Status(*wire.Status),
		ValidAt:         time.UnixMicro(*wire.TimeOfValidity).UTC(),
		TransmittedAt:   time.UnixMicro(*wire.TimeOfTransmission).UTC(),
		Transducers:     transducers,
		ProtocolVersion: wire.Format,
	}, nil
}

func decodeTransducers(wire []transducerWire) ([]TransducerReading, error) {
	if len(wire) != 4 {
		return nil, fmt.Errorf("transducers: got %d, want 4", len(wire))
	}

	readings := make([]TransducerReading, len(wire))
	var seen [4]bool
	for index, transducer := range wire {
		missing := missingFields(map[string]bool{
			"id":         transducer.ID != nil,
			"velocity":   transducer.Velocity != nil,
			"distance":   transducer.Distance != nil,
			"rssi":       transducer.RSSI != nil,
			"nsd":        transducer.NSD != nil,
			"beam_valid": transducer.BeamValid != nil,
		})
		if missing != "" {
			return nil, fmt.Errorf("transducer %d: missing %s", index, missing)
		}
		if *transducer.ID >= uint8(len(seen)) {
			return nil, fmt.Errorf("transducer %d: id %d outside 0-3", index, *transducer.ID)
		}
		if seen[*transducer.ID] {
			return nil, fmt.Errorf("transducer %d: duplicate id %d", index, *transducer.ID)
		}
		seen[*transducer.ID] = true

		readings[index] = TransducerReading{
			ID:        *transducer.ID,
			Velocity:  *transducer.Velocity,
			Distance:  *transducer.Distance,
			RSSI:      *transducer.RSSI,
			NSD:       *transducer.NSD,
			BeamValid: *transducer.BeamValid,
		}
	}
	return readings, nil
}

func decodeDeadReckoningReport(data []byte) (*DeadReckoningReport, error) {
	var wire deadReckoningWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}

	missing := missingFields(map[string]bool{
		"ts":     wire.Timestamp != nil,
		"x":      wire.X != nil,
		"y":      wire.Y != nil,
		"z":      wire.Z != nil,
		"std":    wire.Standard != nil,
		"roll":   wire.Roll != nil,
		"pitch":  wire.Pitch != nil,
		"yaw":    wire.Yaw != nil,
		"status": wire.Status != nil,
		"format": wire.Format != "",
	})
	if missing != "" {
		return nil, fmt.Errorf("missing %s", missing)
	}

	at, err := unixSeconds(*wire.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("ts: %w", err)
	}

	return &DeadReckoningReport{
		At:                          at,
		Position:                    Vector3{X: *wire.X, Y: *wire.Y, Z: *wire.Z},
		Attitude:                    EulerAngles{Roll: *wire.Roll, Pitch: *wire.Pitch, Yaw: *wire.Yaw},
		HorizontalStandardDeviation: *wire.Standard,
		Status:                      DeadReckoningStatus(*wire.Status),
		ProtocolVersion:             wire.Format,
	}, nil
}

func decodeDeviceInfo(data []byte, format ProtocolVersion) (DeviceInfo, error) {
	var wire deviceInfoWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return DeviceInfo{}, err
	}

	missing := missingFields(map[string]bool{
		"chipid":            wire.ChipID != nil,
		"hardware_revision": wire.HardwareRevision != nil,
		"product_id":        wire.ProductID != nil,
		"product_name":      wire.ProductName != nil,
		"variant":           wire.Variant != nil,
		"version":           wire.FirmwareVersion != nil,
		"version_short":     wire.FirmwareVersionShort != nil,
		"is_ready":          wire.Ready != nil,
	})
	if missing != "" {
		return DeviceInfo{}, fmt.Errorf("missing %s", missing)
	}

	return DeviceInfo{
		ChipID:               *wire.ChipID,
		HardwareRevision:     *wire.HardwareRevision,
		ProductID:            *wire.ProductID,
		ProductName:          *wire.ProductName,
		Variant:              *wire.Variant,
		FirmwareVersion:      *wire.FirmwareVersion,
		FirmwareVersionShort: *wire.FirmwareVersionShort,
		Ready:                *wire.Ready,
		ProtocolVersion:      format,
	}, nil
}

func decodeConfig(data []byte) (Config, error) {
	var wire configWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return Config{}, err
	}

	missing := missingFields(map[string]bool{
		"speed_of_sound":           wire.SpeedOfSound != nil,
		"mounting_rotation_offset": wire.MountingYawOffset != nil,
		"acoustic_enabled":         wire.AcousticEnabled != nil,
		"dark_mode_enabled":        wire.DarkModeEnabled != nil,
		"range_mode":               wire.RangeMode != nil,
		"periodic_cycling_enabled": wire.PeriodicCyclingEnabled != nil,
	})
	if missing != "" {
		return Config{}, fmt.Errorf("missing %s", missing)
	}
	if err := validateSpeedOfSound(*wire.SpeedOfSound); err != nil {
		return Config{}, err
	}
	if err := validateMountingYawOffset(*wire.MountingYawOffset); err != nil {
		return Config{}, err
	}
	if !validRangeMode(RangeMode(*wire.RangeMode)) {
		return Config{}, fmt.Errorf("range_mode: invalid value %q", *wire.RangeMode)
	}

	return Config{
		SpeedOfSound:           *wire.SpeedOfSound,
		MountingYawOffset:      *wire.MountingYawOffset,
		AcousticEnabled:        *wire.AcousticEnabled,
		DarkModeEnabled:        *wire.DarkModeEnabled,
		RangeMode:              RangeMode(*wire.RangeMode),
		PeriodicCyclingEnabled: *wire.PeriodicCyclingEnabled,
	}, nil
}

func encodeConfigUpdate(update ConfigUpdate) (map[string]any, error) {
	parameters := make(map[string]any, 6)
	if update.SpeedOfSound != nil {
		if err := validateSpeedOfSound(*update.SpeedOfSound); err != nil {
			return nil, err
		}
		parameters["speed_of_sound"] = *update.SpeedOfSound
	}
	if update.MountingYawOffset != nil {
		if err := validateMountingYawOffset(*update.MountingYawOffset); err != nil {
			return nil, err
		}
		parameters["mounting_rotation_offset"] = *update.MountingYawOffset
	}
	if update.AcousticEnabled != nil {
		parameters["acoustic_enabled"] = *update.AcousticEnabled
	}
	if update.DarkModeEnabled != nil {
		parameters["dark_mode_enabled"] = *update.DarkModeEnabled
	}
	if update.RangeMode != nil {
		if !validRangeMode(*update.RangeMode) {
			return nil, fmt.Errorf("dvl: range mode %q is invalid", *update.RangeMode)
		}
		parameters["range_mode"] = *update.RangeMode
	}
	if update.PeriodicCyclingEnabled != nil {
		parameters["periodic_cycling_enabled"] = *update.PeriodicCyclingEnabled
	}
	if len(parameters) == 0 {
		return nil, errors.New("dvl: configuration update is empty")
	}
	return parameters, nil
}

func validateSpeedOfSound(value float64) error {
	if !finite(value) || value < 1000 || value > 2000 {
		return fmt.Errorf("dvl: speed of sound must be from 1000 to 2000 m/s, got %v", value)
	}
	return nil
}

func validateMountingYawOffset(value float64) error {
	if !finite(value) || value < 0 || value > 360 {
		return fmt.Errorf("dvl: mounting yaw offset must be from 0 to 360 degrees, got %v", value)
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validRangeMode(mode RangeMode) bool {
	if mode == RangeModeAuto || mode == RangeModeWaterTracking {
		return true
	}
	text := string(mode)
	if len(text) == 2 && text[0] == '=' {
		return text[1] >= '0' && text[1] <= '4'
	}
	if len(text) == 4 && text[1] == '<' && text[2] == '=' {
		return text[0] >= '0' && text[0] <= text[3] && text[3] <= '4'
	}
	return false
}

func supportedProtocolVersion(version ProtocolVersion) bool {
	text := string(version)
	if text == "json_v3" {
		return true
	}
	const prefix = "json_v3."
	if !strings.HasPrefix(text, prefix) || len(text) == len(prefix) {
		return false
	}
	for _, digit := range text[len(prefix):] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func missingFields(fields map[string]bool) string {
	missing := make([]string, 0, len(fields))
	for name, present := range fields {
		if !present {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return strings.Join(missing, ", ")
}

func milliseconds(value float64) (time.Duration, error) {
	if !finite(value) || value < 0 {
		return 0, fmt.Errorf("invalid millisecond duration %v", value)
	}
	nanoseconds := math.Round(value * float64(time.Millisecond))
	if !finite(nanoseconds) || nanoseconds >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("millisecond duration %v is out of range", value)
	}
	return time.Duration(int64(nanoseconds)), nil
}

func matrix3(values [][]float64) (Matrix3, error) {
	if len(values) != 3 {
		return Matrix3{}, fmt.Errorf("got %d rows, want 3", len(values))
	}
	var matrix Matrix3
	for row := range values {
		if len(values[row]) != 3 {
			return Matrix3{}, fmt.Errorf("row %d has %d columns, want 3", row, len(values[row]))
		}
		copy(matrix[row][:], values[row])
	}
	return matrix, nil
}

func unixSeconds(value float64) (time.Time, error) {
	if !finite(value) {
		return time.Time{}, fmt.Errorf("invalid Unix timestamp %v", value)
	}
	microseconds := math.Round(value * 1e6)
	if !finite(microseconds) ||
		microseconds <= float64(math.MinInt64) || microseconds >= float64(math.MaxInt64) {
		return time.Time{}, fmt.Errorf("Unix timestamp %v is out of range", value)
	}
	return time.UnixMicro(int64(microseconds)).UTC(), nil
}
