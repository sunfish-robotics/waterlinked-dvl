package dvl

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIdleTimeoutEndsSilentConnection(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		// Accept the connection and send nothing: the client must notice.
		_, err := io.Copy(io.Discard, socket)
		return err
	})

	conn := dialTestPeerWith(t, &Dialer{IdleTimeout: 50 * time.Millisecond}, peer.address)
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("silent peer did not end the connection")
	}
	if !errors.Is(conn.Err(), os.ErrDeadlineExceeded) {
		t.Fatalf("Err = %v, want os.ErrDeadlineExceeded", conn.Err())
	}
	peer.wait(t)
}

func TestSteadyReportsKeepIdleTimeoutFromFiring(t *testing.T) {
	stop := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return nil
			case <-ticker.C:
				if err := writeTestFrame(socket, velocityFrame(1)); err != nil {
					// The client closed first, which the test reports itself.
					return nil
				}
			}
		}
	})

	conn := dialTestPeerWith(t, &Dialer{IdleTimeout: 200 * time.Millisecond}, peer.address)
	select {
	case <-conn.Done():
		t.Fatalf("connection ended while reports were arriving: %v", conn.Err())
	case <-time.After(600 * time.Millisecond):
	}
	if err := conn.Err(); err != nil {
		t.Fatalf("Err = %v, want nil", err)
	}

	close(stop)
	peer.wait(t)
}

func TestCommandTimeoutEndsConnection(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		// Read the command and never answer it.
		_, err := readTestCommand(socket)
		if err != nil {
			return err
		}
		<-release
		return nil
	})

	conn := dialTestPeerWith(t, &Dialer{CommandTimeout: 50 * time.Millisecond}, peer.address)
	_, err := conn.Info(t.Context())
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Info error = %T %v, want *ProtocolError", err, err)
	}
	if protocolErr.Operation != "wait for response" || protocolErr.MessageType != "get_version_info" {
		t.Fatalf("protocol error = %#v", protocolErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Info error = %v, want context.DeadlineExceeded", err)
	}
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("unanswered command did not end the connection")
	}

	close(release)
	peer.wait(t)
}

// TestReportBufferBoundsRetainedReports runs a command before reading the
// stream so the reader has demonstrably handled all five frames: the peer only
// answers after writing them, and the response is delivered on the same reader
// goroutine.
func TestReportBufferBoundsRetainedReports(t *testing.T) {
	release := make(chan struct{})
	peer := startTestPeer(t, func(socket net.Conn) error {
		for index := range 5 {
			if err := writeTestFrame(socket, velocityFrame(float64(index))); err != nil {
				return err
			}
		}
		if _, err := readTestCommand(socket); err != nil {
			return err
		}
		if err := writeTestFrame(socket, infoResponse(true, "")); err != nil {
			return err
		}
		<-release
		return nil
	})

	conn := dialTestPeerWith(t, &Dialer{ReportBuffer: 2}, peer.address)
	if _, err := conn.Info(t.Context()); err != nil {
		t.Fatal(err)
	}

	select {
	case sample := <-conn.VelocityReports():
		if sample.DroppedBefore != 3 {
			t.Fatalf("dropped = %d, want 3", sample.DroppedBefore)
		}
		if sample.Report.Interval != 3*time.Millisecond {
			t.Fatalf("first retained interval = %s, want 3ms", sample.Report.Interval)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retained report")
	}

	close(release)
	peer.wait(t)
}

// TestDialRejectsNegativeFields dials an address with no port, so a Dialer that
// validated after connecting would fail with an address error instead.
func TestDialRejectsNegativeFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field  string
		dialer Dialer
	}{
		{field: "IdleTimeout", dialer: Dialer{IdleTimeout: -time.Second}},
		{field: "CommandTimeout", dialer: Dialer{CommandTimeout: -time.Second}},
		{field: "ReportBuffer", dialer: Dialer{ReportBuffer: -1}},
	}
	for _, test := range tests {
		t.Run(test.field, func(t *testing.T) {
			t.Parallel()
			conn, err := test.dialer.Dial(t.Context(), "")
			if err == nil {
				_ = conn.Close()
				t.Fatalf("negative %s accepted", test.field)
			}
			if !strings.Contains(err.Error(), test.field) {
				t.Fatalf("error = %v, want it to name %s", err, test.field)
			}
		})
	}
}
