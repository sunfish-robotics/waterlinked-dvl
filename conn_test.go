package dvl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestConnDemultiplexesReportAndCommandResponse(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_version_info" {
			return fmt.Errorf("command = %q", command.Command)
		}
		if err := writeTestFrame(socket, json.RawMessage(velocityFixture)); err != nil {
			return err
		}
		if err := writeTestFrame(socket, infoResponse(true, "")); err != nil {
			return err
		}
		<-release
		return nil
	})

	conn := dialTestPeer(t, peer.address)
	info, err := conn.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.ProductName != "DVL A50" || info.ProtocolVersion != ProtocolJSONV3_3 {
		t.Fatalf("info = %#v", info)
	}

	select {
	case sample := <-conn.Reports():
		if _, ok := sample.Report.(*VelocityReport); !ok {
			t.Fatalf("report type = %T", sample.Report)
		}
		if sample.DroppedBefore != 0 {
			t.Fatalf("dropped = %d", sample.DroppedBefore)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for report")
	}

	close(release)
	peer.wait(t)
}

func TestSlowReportConsumerDoesNotBlockCommands(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		for index := range reportBufferCapacity + 6 {
			if err := writeTestFrame(socket, velocityFrame(float64(index))); err != nil {
				return err
			}
		}

		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_version_info" {
			return fmt.Errorf("command = %q", command.Command)
		}
		if err := writeTestFrame(socket, infoResponse(true, "")); err != nil {
			return err
		}
		<-release
		return nil
	})

	conn := dialTestPeer(t, peer.address)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := conn.Info(ctx); err != nil {
		t.Fatalf("command blocked behind reports: %v", err)
	}

	select {
	case sample := <-conn.Reports():
		if sample.DroppedBefore != 6 {
			t.Fatalf("dropped = %d, want 6", sample.DroppedBefore)
		}
		report := sample.Report.(*VelocityReport)
		if report.Interval != 6*time.Millisecond {
			t.Fatalf("first retained interval = %s, want 6ms", report.Interval)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for retained report")
	}

	close(release)
	peer.wait(t)
}

func TestCancelledCommandResponseIsRetiredBeforeNextCommand(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		first, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if first.Command != "get_version_info" {
			return fmt.Errorf("first command = %q", first.Command)
		}
		time.Sleep(50 * time.Millisecond)
		if err := writeTestFrame(socket, infoResponse(true, "")); err != nil {
			return err
		}

		second, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if second.Command != "get_config" {
			return fmt.Errorf("second command = %q", second.Command)
		}
		return writeTestFrame(socket, configResponse())
	})

	conn := dialTestPeer(t, peer.address)
	cancelled, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := conn.Info(cancelled); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Info error = %v", err)
	}

	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	config, err := conn.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if config.SpeedOfSound != 1480 || config.RangeMode != RangeModeAuto {
		t.Fatalf("config = %#v", config)
	}
	peer.wait(t)
}

func TestCommandRejectionDoesNotEndConnection(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_config" {
			return fmt.Errorf("command = %q", command.Command)
		}
		if err := writeTestFrame(socket, configRejection()); err != nil {
			return err
		}
		<-release
		return nil
	})

	conn := dialTestPeer(t, peer.address)
	_, err := conn.Config(t.Context())
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if commandErr.Command != "get_config" || commandErr.Message != "configuration unavailable" {
		t.Fatalf("command error = %#v", commandErr)
	}
	if err := conn.Err(); err != nil {
		t.Fatalf("connection ended after rejection: %v", err)
	}

	close(release)
	peer.wait(t)
}

func TestMalformedCommandResultEndsConnection(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_config" {
			return fmt.Errorf("command = %q", command.Command)
		}
		return writeTestFrame(socket, successResponse("get_config", map[string]any{}))
	})

	conn := dialTestPeer(t, peer.address)
	_, err := conn.Config(t.Context())
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Config error = %T %v", err, err)
	}
	<-conn.Done()
	if !errors.As(conn.Err(), &protocolErr) {
		t.Fatalf("terminal error = %T %v", conn.Err(), conn.Err())
	}
	peer.wait(t)
}

func TestUpdateConfigSendsOnlySelectedFields(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "set_config" {
			return fmt.Errorf("command = %q", command.Command)
		}
		if len(command.Parameters) != 2 || command.Parameters["speed_of_sound"] != 1480.0 || command.Parameters["acoustic_enabled"] != false {
			return fmt.Errorf("parameters = %#v", command.Parameters)
		}
		return writeTestFrame(socket, successResponse("set_config", nil))
	})

	conn := dialTestPeer(t, peer.address)
	speed := 1480.0
	acoustic := false
	if err := conn.UpdateConfig(t.Context(), ConfigUpdate{
		SpeedOfSound:    &speed,
		AcousticEnabled: &acoustic,
	}); err != nil {
		t.Fatal(err)
	}
	peer.wait(t)
}

func TestSimpleCommandsUseDocumentedNames(t *testing.T) {
	tests := []struct {
		name    string
		command string
		call    func(context.Context, *Conn) error
	}{
		{name: "reset dead reckoning", command: "reset_dead_reckoning", call: func(ctx context.Context, conn *Conn) error {
			return conn.ResetDeadReckoning(ctx)
		}},
		{name: "calibrate gyro", command: "calibrate_gyro", call: func(ctx context.Context, conn *Conn) error {
			return conn.CalibrateGyro(ctx)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := startTestPeer(t, func(socket net.Conn) error {
				command, err := readTestCommand(socket)
				if err != nil {
					return err
				}
				if command.Command != test.command {
					return fmt.Errorf("command = %q, want %q", command.Command, test.command)
				}
				return writeTestFrame(socket, successResponse(test.command, nil))
			})

			conn := dialTestPeer(t, peer.address)
			if err := test.call(t.Context(), conn); err != nil {
				t.Fatal(err)
			}
			peer.wait(t)
		})
	}
}

func TestPeerClosurePublishesEOF(t *testing.T) {
	peer := startTestPeer(t, func(net.Conn) error { return nil })
	conn := dialTestPeer(t, peer.address)
	peer.wait(t)

	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close")
	}
	if !errors.Is(conn.Err(), io.EOF) {
		t.Fatalf("Err = %v", conn.Err())
	}
	select {
	case _, ok := <-conn.Reports():
		if ok {
			t.Fatal("Reports remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("Reports did not close")
	}
}

func TestCloseIsIdempotentAndPublishesNetErrClosed(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		_, err := io.Copy(io.Discard, socket)
		return err
	})
	conn := dialTestPeer(t, peer.address)

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	<-conn.Done()
	if !errors.Is(conn.Err(), net.ErrClosed) {
		t.Fatalf("Err = %v", conn.Err())
	}
	if _, err := conn.Info(t.Context()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Info after Close = %v", err)
	}
	if _, ok := <-conn.Reports(); ok {
		t.Fatal("Reports remained open after Close returned")
	}
	peer.wait(t)
}

func TestMalformedKnownReportEndsConnection(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		return writeTestFrame(socket, map[string]any{
			"type":   "velocity",
			"format": ProtocolJSONV3_3,
		})
	})
	conn := dialTestPeer(t, peer.address)
	peer.wait(t)
	<-conn.Done()

	var protocolErr *ProtocolError
	if !errors.As(conn.Err(), &protocolErr) {
		t.Fatalf("Err = %T %v", conn.Err(), conn.Err())
	}
	if protocolErr.MessageType != "velocity" {
		t.Fatalf("protocol error = %#v", protocolErr)
	}
}

func TestUnexpectedResponseEndsConnectionWithoutBlockingLaterCommands(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		return writeTestFrame(socket, successResponse("get_config", nil))
	})
	conn := dialTestPeer(t, peer.address)
	peer.wait(t)

	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("unexpected response did not end connection")
	}
	var protocolErr *ProtocolError
	if !errors.As(conn.Err(), &protocolErr) {
		t.Fatalf("Err = %T %v", conn.Err(), conn.Err())
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := conn.Info(ctx); !errors.Is(err, conn.Err()) {
		t.Fatalf("Info after unexpected response = %v, want %v", err, conn.Err())
	}
}

type testPeer struct {
	address string
	done    <-chan error
}

func startTestPeer(t *testing.T, handler func(net.Conn) error) testPeer {
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

	return testPeer{address: listener.Addr().String(), done: done}
}

func (p testPeer) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test peer did not finish")
	}
}

func dialTestPeer(t *testing.T, address string) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conn, err := Dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

type testCommand struct {
	Command    string         `json:"command"`
	Parameters map[string]any `json:"parameters"`
}

func readTestCommand(socket net.Conn) (testCommand, error) {
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return testCommand{}, err
	}
	var command testCommand
	if err := json.NewDecoder(socket).Decode(&command); err != nil {
		return testCommand{}, err
	}
	return command, nil
}

func writeTestFrame(socket net.Conn, message any) error {
	var data []byte
	var err error
	if raw, ok := message.(json.RawMessage); ok {
		data = raw
	} else {
		data, err = json.Marshal(message)
		if err != nil {
			return err
		}
	}
	data = append(data, '\r', '\n')
	_, err = socket.Write(data)
	return err
}

func velocityFrame(milliseconds float64) map[string]any {
	return map[string]any{
		"time": milliseconds,
		"vx":   0.1, "vy": 0.2, "vz": 0.3,
		"fom":        0.01,
		"covariance": [][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}},
		"altitude":   2.4,
		"transducers": []map[string]any{
			{"id": 0, "velocity": 0.1, "distance": 2.4, "rssi": -40.0, "nsd": -90.0, "beam_valid": true},
			{"id": 1, "velocity": 0.1, "distance": 2.4, "rssi": -40.0, "nsd": -90.0, "beam_valid": true},
			{"id": 2, "velocity": 0.1, "distance": 2.4, "rssi": -40.0, "nsd": -90.0, "beam_valid": true},
			{"id": 3, "velocity": 0.1, "distance": 2.4, "rssi": -40.0, "nsd": -90.0, "beam_valid": true},
		},
		"velocity_valid":       true,
		"status":               0,
		"format":               ProtocolJSONV3_3,
		"type":                 "velocity",
		"time_of_validity":     1789228180928221,
		"time_of_transmission": 1789228181074924,
	}
}

func infoResponse(success bool, message string) map[string]any {
	return map[string]any{
		"response_to":   "get_version_info",
		"success":       success,
		"error_message": message,
		"result": map[string]any{
			"chipid": "0xf14002310c0215", "hardware_revision": 4,
			"product_id": 21035, "product_name": "DVL A50", "variant": "performance",
			"version": "2.7.2 (build)", "version_short": "2.7.2", "is_ready": true,
		},
		"format": ProtocolJSONV3_3,
		"type":   "response",
	}
}

func configResponse() map[string]any {
	return successResponse("get_config", map[string]any{
		"speed_of_sound": 1480, "mounting_rotation_offset": 0,
		"acoustic_enabled": true, "dark_mode_enabled": false,
		"range_mode": "auto", "periodic_cycling_enabled": true,
	})
}

func configRejection() map[string]any {
	return map[string]any{
		"response_to": "get_config", "success": false,
		"error_message": "configuration unavailable", "result": nil,
		"format": ProtocolJSONV3_3, "type": "response",
	}
}

func successResponse(command string, result any) map[string]any {
	return map[string]any{
		"response_to": command, "success": true, "error_message": "",
		"result": result, "format": ProtocolJSONV3_3, "type": "response",
	}
}
