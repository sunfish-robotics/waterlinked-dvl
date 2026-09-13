package dvl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
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
	case sample := <-conn.VelocityReports():
		if sample.Report == nil {
			t.Fatal("velocity report is nil")
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

func TestSlowVelocityConsumerDoesNotBlockOtherStreamsOrCommands(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		for index := range reportBufferCapacity + 6 {
			if err := writeTestFrame(socket, velocityFrame(float64(index))); err != nil {
				return err
			}
		}
		if err := writeTestFrame(socket, deadReckoningFrame()); err != nil {
			return err
		}
		if err := writeTestFrame(socket, map[string]any{
			"type":   "temperature",
			"format": ProtocolJSONV3_3,
			"value":  20,
		}); err != nil {
			return err
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
	case sample := <-conn.DeadReckoningReports():
		if sample.Report == nil || sample.DroppedBefore != 0 {
			t.Fatalf("dead-reckoning sample = %#v", sample)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for dead-reckoning report")
	}
	select {
	case sample := <-conn.UnhandledFrames():
		if sample.Report.Type != "temperature" || sample.Report.Err != nil || sample.DroppedBefore != 0 {
			t.Fatalf("unhandled sample = %#v", sample)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for unhandled frame")
	}

	select {
	case sample := <-conn.VelocityReports():
		if sample.DroppedBefore != 6 {
			t.Fatalf("dropped = %d, want 6", sample.DroppedBefore)
		}
		report := sample.Report
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

func TestMalformedCommandResultFailsOnlyThatCommand(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_config" {
			return fmt.Errorf("first command = %q", command.Command)
		}
		if err := writeTestFrame(socket, successResponse("get_config", map[string]any{})); err != nil {
			return err
		}

		command, err = readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_version_info" {
			return fmt.Errorf("second command = %q", command.Command)
		}
		return writeTestFrame(socket, infoResponse(true, ""))
	})

	conn := dialTestPeer(t, peer.address)
	_, err := conn.Config(t.Context())
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Config error = %T %v", err, err)
	}
	if protocolErr.Operation != "decode response" || protocolErr.MessageType != "get_config" {
		t.Fatalf("protocol error = %#v", protocolErr)
	}
	assertStillConnected(t, conn)

	info, err := conn.Info(t.Context())
	if err != nil {
		t.Fatalf("command after undecodable result: %v", err)
	}
	if info.ProductName != "DVL A50" {
		t.Fatalf("info = %#v", info)
	}
	peer.wait(t)
}

func TestUndecodableResponseFailsOnlyThePendingCommand(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_version_info" {
			return fmt.Errorf("first command = %q", command.Command)
		}
		// A response frame the client cannot decode: success is missing.
		if err := writeTestFrame(socket, json.RawMessage(
			`{"type":"response","response_to":"get_version_info","error_message":"","result":null,"format":"json_v3.3"}`,
		)); err != nil {
			return err
		}

		command, err = readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_config" {
			return fmt.Errorf("second command = %q", command.Command)
		}
		return writeTestFrame(socket, configResponse())
	})

	conn := dialTestPeer(t, peer.address)
	_, err := conn.Info(t.Context())
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Info error = %T %v", err, err)
	}
	if protocolErr.Operation != "decode message" || protocolErr.MessageType != "response" {
		t.Fatalf("protocol error = %#v", protocolErr)
	}
	assertStillConnected(t, conn)

	assertNoUnhandledFrame(t, conn, "failed response was also published")

	config, err := conn.Config(t.Context())
	if err != nil {
		t.Fatalf("command after undecodable response: %v", err)
	}
	if config.SpeedOfSound != 1480 {
		t.Fatalf("config = %#v", config)
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
	waitForClosed(t, "velocity reports", conn.VelocityReports())
	waitForClosed(t, "dead-reckoning reports", conn.DeadReckoningReports())
	waitForClosed(t, "unhandled frames", conn.UnhandledFrames())
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
	assertClosed(t, "velocity reports", conn.VelocityReports())
	assertClosed(t, "dead-reckoning reports", conn.DeadReckoningReports())
	assertClosed(t, "unhandled frames", conn.UnhandledFrames())
	peer.wait(t)
}

// TestUndecodableFrameIsReportedAndConnectionContinues walks the non-fatal rows
// of the reader policy: the frame appears on the unhandled stream, the
// connection stays up, and both reports and commands keep working on it.
func TestUndecodableFrameIsReportedAndConnectionContinues(t *testing.T) {
	tests := []struct {
		name            string
		frame           string
		messageType     string
		protocolVersion ProtocolVersion
		wantErr         bool

		// errContains is checked when the row names text the contract promises.
		errContains string
	}{
		{
			name:    "not a JSON object",
			frame:   `[1,2,3]`,
			wantErr: true,
		},
		{
			name:            "JSON object without type",
			frame:           `{"format":"json_v3.3","vx":0.1}`,
			protocolVersion: ProtocolJSONV3_3,
			wantErr:         true,
			errContains:     "missing type",
		},
		{
			name:        "report without format",
			frame:       `{"type":"velocity"}`,
			messageType: "velocity",
			wantErr:     true,
		},
		{
			name:            "report outside the json_v3 family",
			frame:           `{"type":"temperature","format":"json_v4.0","celsius":20}`,
			messageType:     "temperature",
			protocolVersion: "json_v4.0",
			wantErr:         true,
			errContains:     `unsupported format "json_v4.0"`,
		},
		{
			name:            "well-formed report of unmodelled type",
			frame:           `{"type":"temperature","format":"json_v3.4","celsius":20}`,
			messageType:     "temperature",
			protocolVersion: "json_v3.4",
		},
		{
			name:            "malformed velocity report",
			frame:           `{"type":"velocity","format":"json_v3.3"}`,
			messageType:     "velocity",
			protocolVersion: ProtocolJSONV3_3,
			wantErr:         true,
		},
		{
			name:            "malformed water velocity report",
			frame:           `{"type":"velocity_water","format":"json_v3.3","vx":"fast"}`,
			messageType:     "velocity_water",
			protocolVersion: ProtocolJSONV3_3,
			wantErr:         true,
		},
		{
			name:            "malformed dead-reckoning report",
			frame:           `{"type":"position_local","format":"json_v3.3"}`,
			messageType:     "position_local",
			protocolVersion: ProtocolJSONV3_3,
			wantErr:         true,
		},
		{
			name:            "undecodable response with no command pending",
			frame:           `{"type":"response","format":"json_v3.3","success":true,"error_message":"","result":null}`,
			messageType:     "response",
			protocolVersion: ProtocolJSONV3_3,
			wantErr:         true,
		},
		{
			name:            "unsolicited response",
			frame:           `{"type":"response","response_to":"get_config","success":true,"error_message":"","result":null,"format":"json_v3.3"}`,
			messageType:     "response",
			protocolVersion: ProtocolJSONV3_3,
			wantErr:         true,
			errContains:     `unsolicited response to "get_config"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := startTestPeer(t, func(socket net.Conn) error {
				if err := writeTestFrame(socket, json.RawMessage(test.frame)); err != nil {
					return err
				}
				if err := writeTestFrame(socket, velocityFrame(7)); err != nil {
					return err
				}
				command, err := readTestCommand(socket)
				if err != nil {
					return err
				}
				if command.Command != "get_version_info" {
					return fmt.Errorf("command = %q", command.Command)
				}
				return writeTestFrame(socket, infoResponse(true, ""))
			})

			conn := dialTestPeer(t, peer.address)
			select {
			case sample := <-conn.UnhandledFrames():
				frame := sample.Report
				if frame.Type != test.messageType {
					t.Fatalf("type = %q, want %q", frame.Type, test.messageType)
				}
				if frame.ProtocolVersion != test.protocolVersion {
					t.Fatalf("protocol version = %q, want %q", frame.ProtocolVersion, test.protocolVersion)
				}
				if string(frame.Raw) != test.frame {
					t.Fatalf("raw = %q, want %q", frame.Raw, test.frame)
				}
				var protocolErr *ProtocolError
				switch {
				case !test.wantErr && frame.Err != nil:
					t.Fatalf("err = %v, want nil", frame.Err)
				case test.wantErr && !errors.As(frame.Err, &protocolErr):
					t.Fatalf("err = %T %v", frame.Err, frame.Err)
				case test.wantErr && protocolErr.Operation != "decode message":
					t.Fatalf("protocol error = %#v", protocolErr)
				}
				if test.errContains != "" && !strings.Contains(frame.Err.Error(), test.errContains) {
					t.Fatalf("err = %q, want it to mention %q", frame.Err, test.errContains)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for unhandled frame")
			}

			assertStillConnected(t, conn)
			assertVelocityAndCommandStillWork(t, conn)
			peer.wait(t)
		})
	}
}

func TestBlankFramesAreSkippedSilently(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		for _, blank := range []string{``, `   `, "	"} {
			if err := writeTestFrame(socket, json.RawMessage(blank)); err != nil {
				return err
			}
		}
		if err := writeTestFrame(socket, velocityFrame(7)); err != nil {
			return err
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
	assertVelocityAndCommandStillWork(t, conn)

	assertNoUnhandledFrame(t, conn, "blank frame was reported")
	assertStillConnected(t, conn)

	close(release)
	peer.wait(t)
}

func TestMiscorrelatedResponseEndsConnection(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		command, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		if command.Command != "get_config" {
			return fmt.Errorf("command = %q", command.Command)
		}
		return writeTestFrame(socket, successResponse("set_config", nil))
	})

	conn := dialTestPeer(t, peer.address)
	if _, err := conn.Config(t.Context()); err == nil {
		t.Fatal("miscorrelated response accepted")
	}
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("miscorrelated response did not end connection")
	}
	var protocolErr *ProtocolError
	if !errors.As(conn.Err(), &protocolErr) {
		t.Fatalf("Err = %T %v", conn.Err(), conn.Err())
	}
	if protocolErr.Operation != "correlate response" || protocolErr.MessageType != "set_config" {
		t.Fatalf("protocol error = %#v", protocolErr)
	}
	peer.wait(t)
}

func TestOversizedFrameEndsConnection(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		// The client closes the socket once framing is lost, so a short write
		// here is the expected outcome rather than a failure.
		_, _ = socket.Write(bytes.Repeat([]byte("a"), maximumFrameSize+1))
		return nil
	})

	conn := dialTestPeer(t, peer.address)
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("oversized frame did not end connection")
	}
	if !errors.Is(conn.Err(), bufio.ErrTooLong) {
		t.Fatalf("Err = %v, want bufio.ErrTooLong", conn.Err())
	}
	peer.wait(t)
}

// assertNoUnhandledFrame waits long enough for the reader to have handed a
// frame to the unhandled broker and for the broker to offer it, so the absence
// it asserts is not just the absence of a queued value.
func assertNoUnhandledFrame(t *testing.T, conn *Conn, reason string) {
	t.Helper()
	select {
	case sample := <-conn.UnhandledFrames():
		t.Fatalf("%s: %#v", reason, sample)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertStillConnected(t *testing.T, conn *Conn) {
	t.Helper()
	select {
	case <-conn.Done():
		t.Fatalf("connection ended: %v", conn.Err())
	default:
	}
}

// assertVelocityAndCommandStillWork consumes the velocity report the test peer
// sends after the frame under test and runs one command on the same connection.
func assertVelocityAndCommandStillWork(t *testing.T, conn *Conn) {
	t.Helper()
	select {
	case sample, ok := <-conn.VelocityReports():
		if !ok {
			t.Fatalf("velocity stream closed: %v", conn.Err())
		}
		if sample.Report.Interval != 7*time.Millisecond {
			t.Fatalf("interval = %s, want 7ms", sample.Report.Interval)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the following velocity report")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	info, err := conn.Info(ctx)
	if err != nil {
		t.Fatalf("command on the same connection: %v", err)
	}
	if info.ProductName != "DVL A50" {
		t.Fatalf("info = %#v", info)
	}
}

func waitForClosed[T any](t *testing.T, name string, stream <-chan T) {
	t.Helper()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatalf("%s remained open", name)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s did not close", name)
	}
}

func assertClosed[T any](t *testing.T, name string, stream <-chan T) {
	t.Helper()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatalf("%s remained open", name)
		}
	default:
		t.Fatalf("%s was not closed when Close returned", name)
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

func deadReckoningFrame() map[string]any {
	return map[string]any{
		"ts":     1789228180.928221,
		"x":      12.4,
		"y":      64.6,
		"z":      1.7,
		"std":    0.002,
		"roll":   0.6,
		"pitch":  0.7,
		"yaw":    90.1,
		"type":   "position_local",
		"status": 0,
		"format": ProtocolJSONV3_3,
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
