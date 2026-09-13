package dvl

import "testing"

// The fuzz targets double as seed-corpus unit tests under plain `go test`.
// Run a fuzzing session with, for example:
//
//	go test -run '^$' -fuzz FuzzDecodeMessage -fuzztime 30s .

func FuzzDecodeMessage(f *testing.F) {
	f.Add([]byte(velocityFixture))
	f.Add([]byte(`{"ts":1789228180.928221,"x":12.4,"y":64.6,"z":1.7,"std":0.002,"roll":0.6,"pitch":0.7,"yaw":90.1,"type":"position_local","status":0,"format":"json_v3.3"}`))
	f.Add([]byte(`{"response_to":"get_config","success":true,"error_message":"","result":{"speed_of_sound":1475.0,"acoustic_enabled":true,"dark_mode_enabled":false,"mounting_rotation_offset":20.0,"range_mode":"auto","periodic_cycling_enabled":true},"format":"json_v3.3","type":"response"}`))
	f.Add([]byte(`{"response_to":"set_config","success":false,"error_message":"bad","result":null,"format":"json_v3.3","type":"response"}`))
	f.Add([]byte(`{"type":"temperature","format":"json_v3.4","celsius":20}`))
	f.Add([]byte(`{"time":1e308,"vx":0,"vy":0,"vz":0,"fom":0,"covariance":[[0,0,0],[0,0,0],[0,0,0]],"transducers":[],"velocity_valid":true,"status":0,"format":"json_v3","type":"velocity","time_of_validity":0,"time_of_transmission":0}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		message, err := decodeMessage(data)
		if err != nil {
			return
		}
		payloads := 0
		for _, present := range []bool{
			message.response != nil,
			message.velocity != nil,
			message.deadReckoning != nil,
			message.unknown != nil,
		} {
			if present {
				payloads++
			}
		}
		if payloads != 1 {
			t.Fatalf("decoded message carries %d payloads, want exactly 1: %q", payloads, data)
		}
	})
}

func FuzzDecodeCommandResults(f *testing.F) {
	f.Add([]byte(`{"speed_of_sound":1475.0,"acoustic_enabled":true,"dark_mode_enabled":false,"mounting_rotation_offset":20.0,"range_mode":"auto","periodic_cycling_enabled":true}`))
	f.Add([]byte(`{"chipid":"0x1","hardware_revision":4,"product_id":21035,"product_name":"DVL A50","variant":"standard","version_short":"2.7.2","version":"2.7.2","is_ready":true}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = decodeConfig(data)
		_, _ = decodeDeviceInfo(data, ProtocolJSONV3_3)
	})
}

func FuzzRangeModeAndProtocolVersion(f *testing.F) {
	for _, seed := range []string{"auto", "wt", "=0", "=4", "0<=4", "1<=3", "=5", "5<=1", "", "<=", "=", "json_v3", "json_v3.", "json_v3.10", "json_v4.0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		_ = validRangeMode(RangeMode(text))
		_ = supportedProtocolVersion(ProtocolVersion(text))
	})
}
