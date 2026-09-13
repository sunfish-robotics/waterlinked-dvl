package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNormaliseAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "10.9.4.12", want: "10.9.4.12:16171"},
		{input: "dvl.local:1234", want: "dvl.local:1234"},
		{input: "2001:db8::1", want: "[2001:db8::1]:16171"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := normaliseAddress(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("normaliseAddress(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestInfoUsesOnlyInfoFlagsAndWritesJSON(t *testing.T) {
	peer := startCLITestPeer(t, func(socket net.Conn) error {
		command, err := readCLICommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_version_info" {
			return fmt.Errorf("command = %q", command.Command)
		}
		if err := writeCLIFrame(socket, infoCLIResponse()); err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, socket)
		return ignoreClosedConnection(err)
	})

	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"info", "-address", peer.address, "-json"}, commandIO{
		stdout: &stdout,
		stderr: &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer.wait(t)

	var result deviceInfoOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if result.ProductName != "DVL A50" || !result.Ready {
		t.Fatalf("result = %#v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	err = run(t.Context(), []string{"info", "-speed-of-sound=1480"}, commandIO{
		stdout: io.Discard,
		stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("out-of-scope flag error = %v", err)
	}
}

func TestConfigSetSendsOnlyExplicitValuesAndVerifiesReadback(t *testing.T) {
	peer := startCLITestPeer(t, func(socket net.Conn) error {
		first, err := readCLICommand(socket)
		if err != nil {
			return err
		}
		if first.Command != "get_config" {
			return fmt.Errorf("first command = %q", first.Command)
		}
		if err := writeCLIFrame(socket, configCLIResponse(1475, true)); err != nil {
			return err
		}

		second, err := readCLICommand(socket)
		if err != nil {
			return err
		}
		if second.Command != "set_config" {
			return fmt.Errorf("second command = %q", second.Command)
		}
		if len(second.Parameters) != 2 || second.Parameters["speed_of_sound"] != 1480.0 || second.Parameters["acoustic_enabled"] != false {
			return fmt.Errorf("parameters = %#v", second.Parameters)
		}
		if err := writeCLIFrame(socket, successCLIResponse("set_config", nil)); err != nil {
			return err
		}

		third, err := readCLICommand(socket)
		if err != nil {
			return err
		}
		if third.Command != "get_config" {
			return fmt.Errorf("third command = %q", third.Command)
		}
		if err := writeCLIFrame(socket, configCLIResponse(1480, false)); err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, socket)
		return ignoreClosedConnection(err)
	})

	var stdout bytes.Buffer
	err := run(t.Context(), []string{
		"config", "set",
		"-address", peer.address,
		"-speed-of-sound=1480",
		"-acoustic-enabled=false",
		"-json",
	}, commandIO{stdout: &stdout, stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	peer.wait(t)

	var result configUpdateOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !result.Accepted || !result.Observed || result.After.SpeedOfSound != 1480 || result.After.AcousticEnabled {
		t.Fatalf("result = %#v", result)
	}

	err = run(t.Context(), []string{"config", "set", "-address", "unused"}, commandIO{
		stdout: io.Discard,
		stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no configuration values") {
		t.Fatalf("empty update error = %v", err)
	}
}

func TestSimpleCommandDispatch(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		command  string
		response func() map[string]any
	}{
		{
			name: "config get", args: []string{"config", "get"}, command: "get_config",
			response: func() map[string]any { return configCLIResponse(1480, true) },
		},
		{
			name: "reset dead reckoning", args: []string{"reset-dead-reckoning"}, command: "reset_dead_reckoning",
			response: func() map[string]any { return successCLIResponse("reset_dead_reckoning", nil) },
		},
		{
			name: "calibrate gyro", args: []string{"calibrate-gyro"}, command: "calibrate_gyro",
			response: func() map[string]any { return successCLIResponse("calibrate_gyro", nil) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := startCLITestPeer(t, func(socket net.Conn) error {
				command, err := readCLICommand(socket)
				if err != nil {
					return err
				}
				if command.Command != test.command {
					return fmt.Errorf("command = %q, want %q", command.Command, test.command)
				}
				if err := writeCLIFrame(socket, test.response()); err != nil {
					return err
				}
				_, err = io.Copy(io.Discard, socket)
				return ignoreClosedConnection(err)
			})

			args := append(append([]string{}, test.args...), "-address", peer.address, "-json")
			var stdout bytes.Buffer
			if err := run(t.Context(), args, commandIO{stdout: &stdout, stderr: io.Discard}); err != nil {
				t.Fatal(err)
			}
			peer.wait(t)
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("invalid JSON output: %s", stdout.String())
			}
		})
	}
}

func TestWatchWritesEachTypedReportAsJSONL(t *testing.T) {
	peer := startCLITestPeer(t, func(socket net.Conn) error {
		for _, frame := range []any{velocityCLIFrame(), deadReckoningCLIFrame(), map[string]any{
			"type": "temperature", "format": "json_v3.3", "value": 21.5,
		}} {
			if err := writeCLIFrame(socket, frame); err != nil {
				return err
			}
		}
		_, err := io.Copy(io.Discard, socket)
		return ignoreClosedConnection(err)
	})

	var stdout bytes.Buffer
	err := run(t.Context(), []string{
		"watch", "-address", peer.address, "-duration", "200ms", "-json",
	}, commandIO{stdout: &stdout, stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	peer.wait(t)

	seen := make(map[string]bool)
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode %q: %v", scanner.Text(), err)
		}
		seen[event.Type] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, reportType := range []string{"velocity", "dead_reckoning", "unknown"} {
		if !seen[reportType] {
			t.Fatalf("missing %q event in %s", reportType, stdout.String())
		}
	}
}

type cliTestCommand struct {
	Command    string         `json:"command"`
	Parameters map[string]any `json:"parameters"`
}

type cliTestPeer struct {
	address string
	done    <-chan error
}

func startCLITestPeer(t *testing.T, handler func(net.Conn) error) cliTestPeer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	done := make(chan error, 1)
	go func() {
		socket, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer socket.Close()
		done <- handler(socket)
	}()
	return cliTestPeer{address: listener.Addr().String(), done: done}
}

func (peer cliTestPeer) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-peer.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test peer did not finish")
	}
}

func readCLICommand(socket net.Conn) (cliTestCommand, error) {
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return cliTestCommand{}, err
	}
	var command cliTestCommand
	if err := json.NewDecoder(socket).Decode(&command); err != nil {
		return cliTestCommand{}, err
	}
	return command, nil
}

func writeCLIFrame(socket net.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\r', '\n')
	_, err = socket.Write(data)
	return err
}

func ignoreClosedConnection(err error) error {
	if err == nil || strings.Contains(err.Error(), "use of closed network connection") {
		return nil
	}
	return err
}

func infoCLIResponse() map[string]any {
	return map[string]any{
		"response_to": "get_version_info", "success": true, "error_message": "",
		"result": map[string]any{
			"chipid": "0xf14002310c0215", "hardware_revision": 4,
			"product_id": 21035, "product_name": "DVL A50", "variant": "performance",
			"version": "2.7.2 (build)", "version_short": "2.7.2", "is_ready": true,
		},
		"format": "json_v3.3", "type": "response",
	}
}

func configCLIResponse(speedOfSound float64, acousticEnabled bool) map[string]any {
	return successCLIResponse("get_config", map[string]any{
		"speed_of_sound": speedOfSound, "mounting_rotation_offset": 0,
		"acoustic_enabled": acousticEnabled, "dark_mode_enabled": false,
		"range_mode": "auto", "periodic_cycling_enabled": true,
	})
}

func successCLIResponse(command string, result any) map[string]any {
	return map[string]any{
		"response_to": command, "success": true, "error_message": "",
		"result": result, "format": "json_v3.3", "type": "response",
	}
}

func velocityCLIFrame() map[string]any {
	transducers := make([]map[string]any, 4)
	for id := range transducers {
		transducers[id] = map[string]any{
			"id": id, "velocity": 0.1, "distance": 2.4,
			"rssi": -40.0, "nsd": -90.0, "beam_valid": true,
		}
	}
	return map[string]any{
		"time": 190.5, "vx": 0.1, "vy": 0.2, "vz": 0.3, "fom": 0.01,
		"covariance": [][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}},
		"altitude":   2.4, "transducers": transducers, "velocity_valid": true,
		"status": 0, "format": "json_v3.3", "type": "velocity",
		"time_of_validity": 1789228180928221, "time_of_transmission": 1789228181074924,
	}
}

func deadReckoningCLIFrame() map[string]any {
	return map[string]any{
		"ts": 1789228180.928221, "x": 12.4, "y": 64.6, "z": 1.7, "std": 0.002,
		"roll": 0.6, "pitch": 0.7, "yaw": 90.1,
		"type": "position_local", "status": 0, "format": "json_v3.3",
	}
}
