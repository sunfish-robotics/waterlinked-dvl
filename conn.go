package dvl

import (
	"context"
	"errors"
	"fmt"
)

// DefaultPort is the TCP port used by the A50/A125 JSON protocol.
const DefaultPort = 16171

var errAPIScaffold = errors.New("dvl: protocol implementation is not available")

// Conn is one TCP connection epoch with a Water Linked DVL.
//
// A Conn does not reconnect. Callers replace it after the connection ends.
// Command methods may be called concurrently; the connection serialises them
// because protocol responses do not carry unique request IDs. Close may be
// called concurrently with any operation. If a command is cancelled after
// transmission, its response is retired before the next command begins so
// correlation remains unambiguous.
type Conn struct {
	state *connState
}

type connState struct{}

// Dial connects to address, which must include a TCP port.
//
// Cancelling ctx while dialing returns ctx.Err. Once Dial succeeds, cancelling
// ctx has no effect on the returned connection.
func Dial(ctx context.Context, address string) (*Conn, error) {
	return nil, errAPIScaffold
}

// Reports returns the connection's report stream. Repeated calls return the
// same channel; multiple receivers divide the stream rather than broadcasting
// it.
//
// Reports are delivered through a bounded, drop-oldest queue so a slow or
// absent consumer cannot prevent command responses from being read. Each
// Sample reports any resulting loss. The channel closes when the connection
// ends, and buffered reports from that connection epoch are discarded.
func (c *Conn) Reports() <-chan Sample {
	return nil
}

// Done is closed when the connection ends. It is a broadcast lifecycle signal
// for supervisors and callers that do not consume Reports.
func (c *Conn) Done() <-chan struct{} {
	return nil
}

// Err returns nil while the connection is active and its terminal cause after
// Done is closed. The cause is stable and is not consumed by reading it.
func (c *Conn) Err() error {
	return errAPIScaffold
}

// Info returns the connected device's identity, firmware version, and
// readiness state.
func (c *Conn) Info(ctx context.Context) (DeviceInfo, error) {
	return DeviceInfo{}, errAPIScaffold
}

// Config returns the complete configuration reported by the device.
func (c *Conn) Config(ctx context.Context) (Config, error) {
	return Config{}, errAPIScaffold
}

// UpdateConfig applies the non-nil fields in update.
//
// The complete update is validated before anything is written. A successful
// response means the device accepted the command; callers that need verified
// application should read Config again and compare it with their desired state.
func (c *Conn) UpdateConfig(ctx context.Context, update ConfigUpdate) error {
	return errAPIScaffold
}

// ResetDeadReckoning resets the origin and attitude of the device's local
// dead-reckoning frame. A successful response means the reset was accepted;
// reports may take approximately 50 milliseconds to reach zero.
func (c *Conn) ResetDeadReckoning(ctx context.Context) error {
	return errAPIScaffold
}

// CalibrateGyro calibrates the A50/A125 gyroscope. The device must remain
// stationary while calibration is in progress, which may take up to 15
// seconds.
func (c *Conn) CalibrateGyro(ctx context.Context) error {
	return errAPIScaffold
}

// Close terminates the connection and unblocks pending operations. It is safe
// to call Close more than once. Operations interrupted by Close return errors
// comparable with net.ErrClosed; unexpected transport failures retain their
// underlying EOF or network error.
func (c *Conn) Close() error {
	return errAPIScaffold
}

// CommandError reports a syntactically valid command rejected by the device.
type CommandError struct {
	Command string
	Message string
}

func (e *CommandError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("dvl: command %q rejected", e.Command)
	}
	return fmt.Sprintf("dvl: command %q rejected: %s", e.Command, e.Message)
}

// ProtocolError reports malformed or incompatible data received from the
// device. Err retains the underlying decoding or validation error.
type ProtocolError struct {
	Operation   string
	MessageType string
	Err         error
}

func (e *ProtocolError) Error() string {
	message := "dvl: protocol error"
	if e.Operation != "" {
		message += " during " + e.Operation
	}
	if e.MessageType != "" {
		message += " for " + e.MessageType
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *ProtocolError) Unwrap() error {
	return e.Err
}
