// Package dvl implements the TCP JSON client for the Water Linked DVL
// A50/A125, firmware 2.4.0 or later (protocol json_v3.1 and later; Info needs
// 2.7.2, where get_version_info was added).
//
// Dial, or a Dialer for timeouts and buffering, opens one Conn per TCP
// connection epoch; a Conn does not reconnect, so callers redial after it
// ends. Velocity, dead-reckoning, and unhandled-frame reports arrive on
// independent typed channels read concurrently with commands, so a slow
// report consumer cannot block command responses or other report types. Info,
// Config, UpdateConfig, ResetDeadReckoning, and CalibrateGyro send a command
// and wait for its response.
//
// A connection ends on Close or one of the failures documented on Conn; Done
// and Err report when and why. Undecodable frames go to UnhandledFrames.
package dvl
