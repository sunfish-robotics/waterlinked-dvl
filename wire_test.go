package dvl

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

const velocityFixture = `{"time":189.83340454101562,"vx":0.00046143701183609664,"vy":-0.00018614993314258754,"vz":-0.000024985427444335073,"fom":0.0000225062103709206,"covariance":[[2.8961616438394344e-10,-1.3226965356327725e-10,4.608463610722424e-11],[-1.3226965356327725e-10,4.0107298038272177e-10,-7.872978657896823e-11],[4.608463610722424e-11,-7.872978657896823e-11,3.6557531307712665e-11]],"altitude":2.244393825531006,"transducers":[{"id":0,"velocity":-0.00019833934493362904,"distance":2.3010001182556152,"rssi":-39.13928985595703,"nsd":-97.46102142333984,"beam_valid":true},{"id":1,"velocity":-0.00009753491031005979,"distance":2.6550002098083496,"rssi":-56.54206085205078,"nsd":-96.86553192138672,"beam_valid":true},{"id":2,"velocity":-0.006173509638756514,"distance":2.312800168991089,"rssi":-63.628997802734375,"nsd":-96.52327728271484,"beam_valid":true},{"id":3,"velocity":0.00005143247835803777,"distance":2.513400077819824,"rssi":-50.673370361328125,"nsd":-96.73756408691406,"beam_valid":true}],"velocity_valid":true,"status":0,"format":"json_v3.3","type":"velocity","time_of_validity":1789228180928221,"time_of_transmission":1789228181074924}`

func TestDecodeVelocityReportFromZoda(t *testing.T) {
	t.Parallel()

	message := decodeMessage([]byte(velocityFixture))
	if message.response != nil {
		t.Fatal("velocity decoded as response")
	}

	velocity := message.velocity
	if velocity == nil {
		t.Fatal("velocity report is nil")
	}
	if velocity.Reference != VelocityReferenceBottom {
		t.Fatalf("reference = %q", velocity.Reference)
	}
	if velocity.Interval != 189833405*time.Nanosecond {
		t.Fatalf("interval = %s", velocity.Interval)
	}
	if velocity.Velocity.X != 0.00046143701183609664 {
		t.Fatalf("vx = %v", velocity.Velocity.X)
	}
	if velocity.Altitude == nil || *velocity.Altitude != 2.244393825531006 {
		t.Fatalf("altitude = %v", velocity.Altitude)
	}
	if velocity.Covariance[2][2] != 3.6557531307712665e-11 {
		t.Fatalf("covariance[2][2] = %v", velocity.Covariance[2][2])
	}
	if !velocity.Valid || velocity.Status != 0 {
		t.Fatalf("valid/status = %v/%d", velocity.Valid, velocity.Status)
	}
	if got := velocity.ValidAt; !got.Equal(time.UnixMicro(1789228180928221)) {
		t.Fatalf("valid at = %s", got)
	}
	if got := velocity.TransmittedAt; !got.Equal(time.UnixMicro(1789228181074924)) {
		t.Fatalf("transmitted at = %s", got)
	}
	if len(velocity.Transducers) != 4 || velocity.Transducers[3].ID != 3 || !velocity.Transducers[3].BeamValid {
		t.Fatalf("transducers = %#v", velocity.Transducers)
	}
	if velocity.ProtocolVersion != ProtocolJSONV3_3 {
		t.Fatalf("protocol version = %q", velocity.ProtocolVersion)
	}
}

func TestDecodeWaterVelocityWithoutAltitude(t *testing.T) {
	t.Parallel()

	var message map[string]any
	if err := json.Unmarshal([]byte(velocityFixture), &message); err != nil {
		t.Fatal(err)
	}
	message["type"] = "velocity_water"
	delete(message, "altitude")
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}

	velocity := decodeMessage(data).velocity
	if velocity == nil {
		t.Fatal("velocity report is nil")
	}
	if velocity.Reference != VelocityReferenceWater {
		t.Fatalf("reference = %q", velocity.Reference)
	}
	if velocity.Altitude != nil {
		t.Fatalf("altitude = %v, want nil", *velocity.Altitude)
	}
}

func TestDecodeDeadReckoningReport(t *testing.T) {
	t.Parallel()

	data := []byte(`{"ts":1789228180.928221,"x":12.4,"y":64.6,"z":1.7,"std":0.002,"roll":0.6,"pitch":0.7,"yaw":90.1,"type":"position_local","status":0,"format":"json_v3.3"}`)
	position := decodeMessage(data).deadReckoning
	if position == nil {
		t.Fatal("dead-reckoning report is nil")
	}
	if !position.At.Equal(time.Unix(1789228180, 928221000)) {
		t.Fatalf("timestamp = %s", position.At)
	}
	if position.Position != (Vector3{X: 12.4, Y: 64.6, Z: 1.7}) {
		t.Fatalf("position = %#v", position.Position)
	}
	if position.Attitude.Yaw != 90.1 || position.HorizontalStandardDeviation != 0.002 {
		t.Fatalf("attitude/std = %#v/%v", position.Attitude, position.HorizontalStandardDeviation)
	}
}

func TestDecodeUnknownTypeIsUnhandledWithoutErrorAndOwnsItsFrame(t *testing.T) {
	t.Parallel()

	data := []byte(`{"type":"temperature","format":"json_v3.4","celsius":20}`)
	unhandled := decodeMessage(data).unhandled
	if unhandled == nil {
		t.Fatal("unhandled frame is nil")
	}
	data[0] = 'x'
	if !json.Valid(unhandled.Raw) {
		t.Fatalf("raw frame aliases input: %q", unhandled.Raw)
	}
	if unhandled.Type != "temperature" || unhandled.ProtocolVersion != "json_v3.4" {
		t.Fatalf("unhandled frame = %#v", unhandled)
	}
	if unhandled.Err != nil {
		t.Fatalf("well-formed unknown type carries an error: %v", unhandled.Err)
	}
}

func TestDecodeReportsUndecodableFramesAsProtocolErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frame       string
		messageType string
	}{
		{name: "not a JSON object", frame: `[1,2,3]`},
		{name: "object without type", frame: `{"format":"json_v3.3"}`},
		{name: "unsupported protocol major", frame: `{"type":"temperature","format":"json_v4.0","celsius":20}`, messageType: "temperature"},
		{name: "report without format", frame: `{"type":"velocity"}`, messageType: "velocity"},
		{name: "incomplete velocity report", frame: `{"type":"velocity","format":"json_v3.3"}`, messageType: "velocity"},
		{name: "incomplete dead-reckoning report", frame: `{"type":"position_local","format":"json_v3.3"}`, messageType: "position_local"},
		{name: "response without response_to", frame: `{"type":"response","format":"json_v3.3","success":true,"error_message":"","result":null}`, messageType: "response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			unhandled := decodeMessage([]byte(test.frame)).unhandled
			if unhandled == nil {
				t.Fatalf("frame %s was not reported as unhandled", test.frame)
			}
			if unhandled.Type != test.messageType {
				t.Fatalf("type = %q, want %q", unhandled.Type, test.messageType)
			}
			if string(unhandled.Raw) != test.frame {
				t.Fatalf("raw = %q, want %q", unhandled.Raw, test.frame)
			}
			var protocolErr *ProtocolError
			if !errors.As(unhandled.Err, &protocolErr) {
				t.Fatalf("err = %T %v", unhandled.Err, unhandled.Err)
			}
			if protocolErr.Operation != "decode message" || protocolErr.MessageType != test.messageType {
				t.Fatalf("protocol error = %#v", protocolErr)
			}
		})
	}
}

func TestDecodeAcceptsAnyTransducerSet(t *testing.T) {
	t.Parallel()

	var frame map[string]any
	if err := json.Unmarshal([]byte(velocityFixture), &frame); err != nil {
		t.Fatal(err)
	}
	beam := func(id int) map[string]any {
		return map[string]any{
			"id": id, "velocity": 0.1, "distance": 2.4,
			"rssi": -40.0, "nsd": -90.0, "beam_valid": true,
		}
	}
	frame["transducers"] = []map[string]any{beam(9), beam(9), beam(0), beam(1), beam(2), beam(3)}
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}

	velocity := decodeMessage(data).velocity
	if velocity == nil {
		t.Fatalf("velocity report rejected: %#v", decodeMessage(data).unhandled)
	}
	if len(velocity.Transducers) != 6 || velocity.Transducers[0].ID != 9 {
		t.Fatalf("transducers = %#v", velocity.Transducers)
	}
}

func TestDecodeConfigAcceptsValuesTheClientWouldNotSend(t *testing.T) {
	t.Parallel()

	config, err := decodeConfig([]byte(`{
		"speed_of_sound": 900,
		"mounting_rotation_offset": 400,
		"acoustic_enabled": true,
		"dark_mode_enabled": false,
		"range_mode": "chirp",
		"periodic_cycling_enabled": true
	}`))
	if err != nil {
		t.Fatalf("device-reported configuration rejected: %v", err)
	}
	if config.SpeedOfSound != 900 || config.MountingYawOffset != 400 || config.RangeMode != "chirp" {
		t.Fatalf("config = %#v", config)
	}
}

func TestEncodeConfigUpdate(t *testing.T) {
	t.Parallel()

	speed := 1480.5
	yaw := 0.25
	disabled := false
	mode := RangeMode("1<=3")
	parameters, err := encodeConfigUpdate(ConfigUpdate{
		SpeedOfSound:      &speed,
		MountingYawOffset: &yaw,
		AcousticEnabled:   &disabled,
		RangeMode:         &mode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if parameters["speed_of_sound"] != 1480.5 || parameters["mounting_rotation_offset"] != 0.25 {
		t.Fatalf("numeric parameters = %#v", parameters)
	}
	if enabled, ok := parameters["acoustic_enabled"].(bool); !ok || enabled {
		t.Fatalf("acoustic_enabled = %#v", parameters["acoustic_enabled"])
	}
	if parameters["range_mode"] != mode {
		t.Fatalf("range_mode = %#v", parameters["range_mode"])
	}
}

func TestDecodeConfigAcceptsFractionalValues(t *testing.T) {
	t.Parallel()

	config, err := decodeConfig([]byte(`{
		"speed_of_sound": 1480.5,
		"mounting_rotation_offset": 0.25,
		"acoustic_enabled": true,
		"dark_mode_enabled": false,
		"range_mode": "auto",
		"periodic_cycling_enabled": true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.SpeedOfSound != 1480.5 || config.MountingYawOffset != 0.25 {
		t.Fatalf("config = %#v", config)
	}
}

func TestEncodeConfigUpdateRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		update ConfigUpdate
	}{
		{name: "empty", update: ConfigUpdate{}},
		{name: "non-finite speed", update: ConfigUpdate{SpeedOfSound: floatPointer(math.NaN())}},
		{name: "speed below range", update: ConfigUpdate{SpeedOfSound: floatPointer(999)}},
		{name: "yaw above range", update: ConfigUpdate{MountingYawOffset: floatPointer(361)}},
		{name: "reversed range", update: ConfigUpdate{RangeMode: rangeModePointer("3<=1")}},
		{name: "unknown range", update: ConfigUpdate{RangeMode: rangeModePointer("manual")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := encodeConfigUpdate(test.update); err == nil {
				t.Fatal("invalid update accepted")
			}
		})
	}
}

func TestDecodeResponseRequiresCorrelationFields(t *testing.T) {
	t.Parallel()

	_, err := decodeResponse([]byte(`{"type":"response","success":true,"error_message":"","result":null,"format":"json_v3.3"}`))
	if err == nil {
		t.Fatalf("response without response_to accepted: %v", err)
	}
}

func TestMillisecondsKeepsNegativeIntervals(t *testing.T) {
	t.Parallel()

	interval, err := milliseconds(-1.5)
	if err != nil {
		t.Fatal(err)
	}
	if interval != -1500*time.Microsecond {
		t.Fatalf("interval = %s, want -1.5ms", interval)
	}
}

func TestTimeConversionsRejectValuesThatRoundOutsideInt64(t *testing.T) {
	t.Parallel()

	if _, err := milliseconds(float64(math.MaxInt64) / float64(time.Millisecond)); err == nil {
		t.Fatal("duration rounding beyond MaxInt64 accepted")
	}
	if _, err := milliseconds(-float64(math.MaxInt64) / float64(time.Millisecond)); err == nil {
		t.Fatal("duration rounding beyond MinInt64 accepted")
	}
	if _, err := unixSeconds(float64(math.MaxInt64) / 1e6); err == nil {
		t.Fatal("timestamp rounding beyond MaxInt64 accepted")
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

func rangeModePointer(value RangeMode) *RangeMode {
	return &value
}
