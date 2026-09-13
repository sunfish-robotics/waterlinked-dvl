package dvl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// DefaultPort is the TCP port used by the A50/A125 JSON protocol.
	DefaultPort = 16171

	reportBufferCapacity   = 64
	maximumFrameSize       = 1 << 20
	commandResponseTimeout = 30 * time.Second
)

var errNilContext = errors.New("dvl: nil context")

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

type connState struct {
	socket net.Conn

	velocity      reportStream[*VelocityReport]
	deadReckoning reportStream[*DeadReckoningReport]
	unknown       reportStream[*UnknownReport]
	commands      chan commandRequest
	workers       sync.WaitGroup

	// ctx is cancelled exactly once, when the connection ends.
	// context.Cause(ctx) is the terminal error.
	ctx    context.Context
	cancel context.CancelCauseFunc

	pendingMu sync.Mutex
	pending   *pendingCommand
}

type commandRequest struct {
	ctx      context.Context
	name     string
	payload  []byte
	validate func(commandResponse) error
	complete chan commandResult
}

type commandResult struct {
	response commandResponse
	err      error
}

type pendingCommand struct {
	name      string
	response  chan commandResponse
	processed chan struct{}
	delivered bool
}

// Dial connects to address, which must include a TCP port.
//
// Cancelling ctx while dialing returns an error comparable with ctx.Err. Once
// Dial succeeds, cancelling ctx has no effect on the returned connection.
func Dial(ctx context.Context, address string) (*Conn, error) {
	if ctx == nil {
		return nil, errNilContext
	}

	socket, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dvl: dial %s: %w", address, err)
	}

	// The connection's lifetime is deliberately rooted in Background rather
	// than ctx: cancelling ctx after Dial returns must not end the connection.
	lifetime, cancel := context.WithCancelCause(context.Background())
	state := &connState{
		socket:        socket,
		velocity:      newReportStream[*VelocityReport](),
		deadReckoning: newReportStream[*DeadReckoningReport](),
		unknown:       newReportStream[*UnknownReport](),
		commands:      make(chan commandRequest),
		ctx:           lifetime,
		cancel:        cancel,
	}
	conn := &Conn{state: state}

	start := func(worker func()) {
		state.workers.Add(1)
		go func() {
			defer state.workers.Done()
			worker()
		}()
	}
	start(func() { state.velocity.run(state.ctx.Done()) })
	start(func() { state.deadReckoning.run(state.ctx.Done()) })
	start(func() { state.unknown.run(state.ctx.Done()) })
	start(state.runCommands)
	start(state.readMessages)

	return conn, nil
}

// VelocityReports returns bottom- and water-relative velocity reports. Repeated
// calls return the same channel; multiple receivers divide the stream rather
// than broadcasting it.
//
// Reports are delivered through a bounded, drop-oldest queue so a slow or
// absent consumer cannot prevent command responses or other report types from
// being read. Each Sample reports velocity-stream loss. The channel closes when
// the connection ends, and buffered reports from that connection epoch are
// discarded.
func (c *Conn) VelocityReports() <-chan Sample[*VelocityReport] {
	return c.state.velocity.output
}

// DeadReckoningReports returns local position and orientation reports. Its
// buffering and receiver semantics match VelocityReports, with independent
// loss accounting.
func (c *Conn) DeadReckoningReports() <-chan Sample[*DeadReckoningReport] {
	return c.state.deadReckoning.output
}

// UnknownReports returns well-formed report types unknown to this version of
// the package. Its buffering and receiver semantics match VelocityReports, with
// independent loss accounting.
func (c *Conn) UnknownReports() <-chan Sample[*UnknownReport] {
	return c.state.unknown.output
}

// Done is closed when the connection ends. It is a broadcast lifecycle signal
// for supervisors and callers that do not consume report streams.
func (c *Conn) Done() <-chan struct{} {
	return c.state.ctx.Done()
}

// Err returns nil while the connection is active and its terminal cause after
// Done is closed. The cause is stable and is not consumed by reading it.
func (c *Conn) Err() error {
	return context.Cause(c.state.ctx)
}

// Info returns the connected device's identity, firmware version, and
// readiness state.
func (c *Conn) Info(ctx context.Context) (DeviceInfo, error) {
	var info DeviceInfo
	_, err := c.command(ctx, "get_version_info", nil, func(response commandResponse) error {
		var decodeErr error
		info, decodeErr = decodeDeviceInfo(response.Result, response.Format)
		return decodeErr
	})
	if err != nil {
		return DeviceInfo{}, err
	}
	return info, nil
}

// Config returns the complete configuration reported by the device.
func (c *Conn) Config(ctx context.Context) (Config, error) {
	var config Config
	_, err := c.command(ctx, "get_config", nil, func(response commandResponse) error {
		var decodeErr error
		config, decodeErr = decodeConfig(response.Result)
		return decodeErr
	})
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

// UpdateConfig applies the non-nil fields in update.
//
// The complete update is validated before anything is written. A successful
// response means the device accepted the command; callers that need verified
// application should read Config again and compare it with their desired state.
func (c *Conn) UpdateConfig(ctx context.Context, update ConfigUpdate) error {
	parameters, err := encodeConfigUpdate(update)
	if err != nil {
		return err
	}
	_, err = c.command(ctx, "set_config", parameters, nil)
	return err
}

// ResetDeadReckoning resets the origin and attitude of the device's local
// dead-reckoning frame. A successful response means the reset was accepted;
// reports may take approximately 50 milliseconds to reach zero.
func (c *Conn) ResetDeadReckoning(ctx context.Context) error {
	_, err := c.command(ctx, "reset_dead_reckoning", nil, nil)
	return err
}

// CalibrateGyro calibrates the A50/A125 gyroscope. The device must remain
// stationary while calibration is in progress, which may take up to 15
// seconds.
func (c *Conn) CalibrateGyro(ctx context.Context) error {
	_, err := c.command(ctx, "calibrate_gyro", nil, nil)
	return err
}

// Close terminates the connection and unblocks pending operations. It is safe
// to call Close more than once. When Close returns, every report stream is
// closed and the connection owns no running goroutines. Operations interrupted
// by Close return errors comparable with net.ErrClosed; unexpected transport
// failures retain their underlying EOF or network error.
func (c *Conn) Close() error {
	c.state.terminate(net.ErrClosed)
	c.state.workers.Wait()
	return nil
}

func (c *Conn) command(
	ctx context.Context,
	name string,
	parameters map[string]any,
	validate func(commandResponse) error,
) (commandResponse, error) {
	if ctx == nil {
		return commandResponse{}, errNilContext
	}

	message := commandMessage{Command: name, Parameters: parameters}
	payload, err := json.Marshal(message)
	if err != nil {
		return commandResponse{}, fmt.Errorf("dvl: encode command %q: %w", name, err)
	}

	request := commandRequest{
		ctx:      ctx,
		name:     name,
		payload:  payload,
		validate: validate,
		complete: make(chan commandResult, 1),
	}

	select {
	case c.state.commands <- request:
	case <-ctx.Done():
		return commandResponse{}, ctx.Err()
	case <-c.state.ctx.Done():
		return commandResponse{}, c.Err()
	}

	select {
	case result := <-request.complete:
		if result.err != nil {
			return commandResponse{}, result.err
		}
		if !result.response.Success {
			return commandResponse{}, &CommandError{
				Command: result.response.ResponseTo,
				Message: result.response.ErrorMessage,
			}
		}
		return result.response, nil
	case <-ctx.Done():
		return commandResponse{}, ctx.Err()
	}
}

func (s *connState) runCommands() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case request := <-s.commands:
			select {
			case <-s.ctx.Done():
				request.complete <- commandResult{err: context.Cause(s.ctx)}
				return
			default:
			}
			if err := request.ctx.Err(); err != nil {
				request.complete <- commandResult{err: err}
				continue
			}
			s.runCommand(request)
		}
	}
}

func (s *connState) runCommand(request commandRequest) {
	pending := &pendingCommand{
		name:      request.name,
		response:  make(chan commandResponse, 1),
		processed: make(chan struct{}),
	}
	s.pendingMu.Lock()
	s.pending = pending
	s.pendingMu.Unlock()

	if err := s.writeCommand(request.payload); err != nil {
		s.clearPending(pending)
		terminalErr := fmt.Errorf("dvl: write command %q: %w", request.name, err)
		s.terminate(terminalErr)
		request.complete <- commandResult{err: context.Cause(s.ctx)}
		return
	}

	timer := time.NewTimer(commandResponseTimeout)
	defer timer.Stop()

	select {
	case response := <-pending.response:
		s.finishCommand(request, pending, response)
	case <-timer.C:
		select {
		case response := <-pending.response:
			s.finishCommand(request, pending, response)
			return
		default:
		}
		s.clearPending(pending)
		terminalErr := &ProtocolError{
			Operation:   "wait for response",
			MessageType: request.name,
			Err:         context.DeadlineExceeded,
		}
		s.terminate(terminalErr)
		request.complete <- commandResult{err: context.Cause(s.ctx)}
	case <-s.ctx.Done():
		select {
		case response := <-pending.response:
			s.finishCommand(request, pending, response)
			return
		default:
		}
		s.clearPending(pending)
		request.complete <- commandResult{err: context.Cause(s.ctx)}
	}
}

func (s *connState) finishCommand(request commandRequest, pending *pendingCommand, response commandResponse) {
	defer close(pending.processed)
	s.clearPending(pending)
	if response.Success && request.validate != nil {
		if err := request.validate(response); err != nil {
			terminalErr := &ProtocolError{
				Operation:   "decode response",
				MessageType: response.ResponseTo,
				Err:         err,
			}
			s.terminate(terminalErr)
			request.complete <- commandResult{err: context.Cause(s.ctx)}
			return
		}
	}
	request.complete <- commandResult{response: response}
}

func (s *connState) writeCommand(payload []byte) (err error) {
	if err = s.socket.SetWriteDeadline(time.Now().Add(commandResponseTimeout)); err != nil {
		return err
	}
	defer func() {
		if resetErr := s.socket.SetWriteDeadline(time.Time{}); err == nil {
			err = resetErr
		}
	}()

	for len(payload) != 0 {
		var written int
		written, err = s.socket.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

func (s *connState) clearPending(pending *pendingCommand) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pending == pending {
		s.pending = nil
	}
}

func (s *connState) readMessages() {
	scanner := bufio.NewScanner(s.socket)
	scanner.Buffer(make([]byte, 4096), maximumFrameSize)

	for scanner.Scan() {
		frame := scanner.Bytes()
		if len(frame) == 0 {
			continue
		}

		message, err := decodeMessage(frame)
		if err != nil {
			s.terminate(&ProtocolError{
				Operation:   "decode message",
				MessageType: message.messageType,
				Err:         err,
			})
			return
		}

		if message.response != nil {
			if !s.deliverResponse(*message.response) {
				return
			}
			continue
		}
		if message.velocity != nil {
			if !s.velocity.publish(s.ctx.Done(), message.velocity) {
				return
			}
			continue
		}
		if message.deadReckoning != nil {
			if !s.deadReckoning.publish(s.ctx.Done(), message.deadReckoning) {
				return
			}
			continue
		}
		if message.unknown != nil {
			if !s.unknown.publish(s.ctx.Done(), message.unknown) {
				return
			}
			continue
		}

		s.terminate(&ProtocolError{
			Operation:   "decode message",
			MessageType: message.messageType,
			Err:         errors.New("decoded message has no payload"),
		})
		return
	}

	if err := scanner.Err(); err != nil {
		s.terminate(fmt.Errorf("dvl: read: %w", err))
		return
	}
	s.terminate(io.EOF)
}

func (s *connState) deliverResponse(response commandResponse) bool {
	s.pendingMu.Lock()
	pending := s.pending
	if pending == nil {
		s.pendingMu.Unlock()
		s.terminate(&ProtocolError{
			Operation:   "correlate response",
			MessageType: response.ResponseTo,
			Err:         errors.New("response has no pending command"),
		})
		return false
	}
	if response.ResponseTo != pending.name {
		expected := pending.name
		s.pendingMu.Unlock()
		s.terminate(&ProtocolError{
			Operation:   "correlate response",
			MessageType: response.ResponseTo,
			Err:         fmt.Errorf("response is for %q, want %q", response.ResponseTo, expected),
		})
		return false
	}
	if pending.delivered {
		s.pendingMu.Unlock()
		s.terminate(&ProtocolError{
			Operation:   "correlate response",
			MessageType: response.ResponseTo,
			Err:         errors.New("duplicate response"),
		})
		return false
	}
	pending.delivered = true
	responseChannel := pending.response
	s.pendingMu.Unlock()

	responseChannel <- response
	select {
	case <-pending.processed:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// terminate ends the connection with err as its terminal cause, mapping a nil
// err to io.EOF. Only the first call sets the cause; later calls leave it
// unchanged, so terminate is safe to call repeatedly and from any goroutine.
func (s *connState) terminate(err error) {
	if err == nil {
		err = io.EOF
	}
	s.cancel(err)
	_ = s.socket.Close()
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
