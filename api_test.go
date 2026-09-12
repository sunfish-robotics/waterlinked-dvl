package dvl_test

import (
	"context"
	"errors"
	"io"
	"testing"

	dvl "github.com/sunfish-robotics/waterlinked-dvl"
)

var (
	_ io.Closer                                        = (*dvl.Conn)(nil)
	_ func(context.Context, string) (*dvl.Conn, error) = dvl.Dial
	_ dvl.Report                                       = (*dvl.VelocityReport)(nil)
	_ dvl.Report                                       = (*dvl.DeadReckoningReport)(nil)
	_ dvl.Report                                       = (*dvl.UnknownReport)(nil)
	_ interface {
		Reports() <-chan dvl.Sample
		Done() <-chan struct{}
		Err() error
	} = (*dvl.Conn)(nil)
)

// These compile-only consumers exercise the proposed API from outside the
// package. Behaviour belongs to the protocol implementation tests.
func readVelocity(ctx context.Context, conn *dvl.Conn) (dvl.Vector3, error) {
	for {
		select {
		case <-ctx.Done():
			return dvl.Vector3{}, ctx.Err()
		case sample, ok := <-conn.Reports():
			if !ok {
				return dvl.Vector3{}, conn.Err()
			}

			switch report := sample.Report.(type) {
			case *dvl.VelocityReport:
				if report.Valid {
					return report.Velocity, nil
				}
			case *dvl.DeadReckoningReport:
				continue
			case *dvl.UnknownReport:
				continue
			}
		}
	}
}

func updateSpeedOfSound(ctx context.Context, conn *dvl.Conn, value float64) error {
	return conn.UpdateConfig(ctx, dvl.ConfigUpdate{SpeedOfSound: &value})
}

func TestStatusHas(t *testing.T) {
	t.Parallel()

	status := dvl.StatusHighTemperature | dvl.Status(1<<7)
	if !status.Has(dvl.StatusHighTemperature) {
		t.Fatal("high-temperature flag not found")
	}
	if status.Has(dvl.Status(1 << 3)) {
		t.Fatal("unset flag reported as present")
	}
}

func TestProtocolErrorUnwrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("invalid covariance")
	err := &dvl.ProtocolError{
		Operation:   "decode",
		MessageType: "velocity",
		Err:         cause,
	}

	if !errors.Is(err, cause) {
		t.Fatalf("ProtocolError does not retain its cause: %v", err)
	}
}

func TestCommandErrorIncludesDeviceMessage(t *testing.T) {
	t.Parallel()

	err := (&dvl.CommandError{
		Command: "set_config",
		Message: "speed_of_sound out of range",
	}).Error()
	want := `dvl: command "set_config" rejected: speed_of_sound out of range`
	if err != want {
		t.Fatalf("CommandError.Error() = %q, want %q", err, want)
	}
}
