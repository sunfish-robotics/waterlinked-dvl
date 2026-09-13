package dvl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestCloseReleasesAllGoroutines checks the Close contract that "the
// connection owns no running goroutines", for both local Close and peer EOF.
func TestCloseReleasesAllGoroutines(t *testing.T) {
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for index := range 10 {
		peerClosesFirst := index%2 == 0
		peer := startTestPeer(t, func(socket net.Conn) error {
			if err := writeTestFrame(socket, velocityFrame(1)); err != nil {
				return err
			}
			if peerClosesFirst {
				return nil
			}
			_, err := io.Copy(io.Discard, socket)
			return err
		})
		conn := dialTestPeer(t, peer.address)
		if peerClosesFirst {
			// The buffered report may be discarded when EOF wins the race, which
			// the stream contract permits, so only wait for termination here.
			<-conn.Done()
		} else if _, ok := <-conn.VelocityReports(); !ok {
			t.Fatal("report stream closed before first report")
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		peer.wait(t)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline {
			return
		}
		if time.Now().After(deadline) {
			stacks := make([]byte, 1<<16)
			n := runtime.Stack(stacks, true)
			t.Fatalf("goroutines = %d, baseline %d\n%s", runtime.NumGoroutine(), baseline, stacks[:n])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestConcurrentCommandsSurviveCancellationAndClose hammers the command path
// with short deadlines from several goroutines while reports stream and Close
// races in, then checks that every caller returns promptly with a documented
// error and that the connection terminates with net.ErrClosed.
func TestConcurrentCommandsSurviveCancellationAndClose(t *testing.T) {
	peer := startTestPeer(t, func(socket net.Conn) error {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if writeTestFrame(socket, velocityFrame(1)) != nil {
						return
					}
				}
			}
		}()

		decoder := json.NewDecoder(socket)
		for {
			var command testCommand
			if err := decoder.Decode(&command); err != nil {
				return nil // the client closed; that is the expected end
			}
			var result any
			switch command.Command {
			case "get_config":
				result = configResponse()["result"]
			case "get_version_info":
				result = infoResponse(true, "")["result"]
			}
			// Vary latency so some responses land after the caller gave up.
			time.Sleep(time.Duration(len(command.Command)%5) * time.Millisecond)
			if writeTestFrame(socket, successResponse(command.Command, result)) != nil {
				return nil
			}
		}
	})
	conn := dialTestPeer(t, peer.address)

	var callers sync.WaitGroup
	unexpected := make(chan error, 64)
	stop := time.Now().Add(300 * time.Millisecond)
	for worker := range 8 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			timeout := time.Duration(1+worker) * 3 * time.Millisecond
			for time.Now().Before(stop) {
				ctx, cancel := context.WithTimeout(t.Context(), timeout)
				var err error
				if worker%2 == 0 {
					_, err = conn.Info(ctx)
				} else {
					_, err = conn.Config(ctx)
				}
				cancel()
				switch {
				case err == nil,
					errors.Is(err, context.DeadlineExceeded),
					errors.Is(err, net.ErrClosed):
				default:
					select {
					case unexpected <- err:
					default:
					}
				}
			}
		}()
	}
	callers.Add(1)
	go func() {
		defer callers.Done()
		for range conn.VelocityReports() {
		}
	}()

	time.Sleep(150 * time.Millisecond)
	go func() { _ = conn.Close() }()
	go func() { _ = conn.Close() }()

	finished := make(chan struct{})
	go func() { callers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		stacks := make([]byte, 1<<16)
		n := runtime.Stack(stacks, true)
		t.Fatalf("callers did not return after Close\n%s", stacks[:n])
	}
	close(unexpected)
	for err := range unexpected {
		t.Errorf("unexpected command error: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(conn.Err(), net.ErrClosed) {
		t.Fatalf("terminal error = %v, want net.ErrClosed", conn.Err())
	}
	peer.wait(t)
}
