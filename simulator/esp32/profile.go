// Package esp32 is a behavioural simulator of the AeroLab ESP32 controller.
//
// It mirrors the firmware architecture in firmware/esp32/components so the
// edge service can be developed and load-tested without hardware:
//
//	controller   autonomous irrigation state machine + interlocks
//	sensors      synthetic, deterministic, clearly SIMULATED readings
//	journal      bounded write-ahead buffer reclaimed only on Pi receipt
//	nvs          atomic programme persistence + bounded command dedup
//	network      MQTT with LWT, session nonce, backoff+jitter reconnect
//
// IMPORTANT: everything published carries data_source=SIMULATOR. Nothing here
// is a measurement and no value is an agronomic recommendation.
package esp32

import (
	"log/slog"
	"time"
)

// HardwareProfile holds the electrical/safety limits of the simulated board.
// The values are simulator defaults, NOT validated hardware limits.
type HardwareProfile struct {
	Name string
	// HardMaxContinuousOnSeconds is the firmware-enforced absolute pump ON cap.
	// A programme requesting more is rejected on the device regardless of what
	// the Pi, cloud or user asked for.
	HardMaxContinuousOnSeconds int
}

// SimProfile is the default simulated board.
var SimProfile = HardwareProfile{Name: "sim-board-v0", HardMaxContinuousOnSeconds: 120}

// Config configures one simulated controller.
type Config struct {
	SiteID, DeviceID, UnitID string
	BrokerURL                string
	Username, Password       string

	HeartbeatEvery time.Duration // default 5s  (spec: status every 5 s)
	TelemetryEvery time.Duration // default 60s (spec: snapshot every 60 s)
	ControlTick    time.Duration // default 100ms
	SelfCheckTime  time.Duration // default 300ms

	JournalCapacity int           // default 400 (360 snapshots/6 h + headroom for critical events)
	ReplayBatch     int           // default 20 (spec: bounded batches)
	MaxInFlight     int           // default 40 unreceipted records on the wire
	ReceiptTimeout  time.Duration // default 15s, then resend

	ReconnectMin time.Duration // default 1s
	ReconnectMax time.Duration // default 30s

	// RTC: if true the simulated board has a synced clock and stamps
	// sampled_at_utc; otherwise it is monotonic-only and never invents time.
	RTC bool

	Profile   HardwareProfile
	Seed      int64
	StatePath string // optional NVS file; empty = in-memory only
	Version   string // firmware version string
	Logger    *slog.Logger
}

func (c *Config) defaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	def(&c.HeartbeatEvery, 5*time.Second)
	def(&c.TelemetryEvery, 60*time.Second)
	def(&c.ControlTick, 100*time.Millisecond)
	def(&c.SelfCheckTime, 300*time.Millisecond)
	def(&c.ReceiptTimeout, 15*time.Second)
	def(&c.ReconnectMin, time.Second)
	def(&c.ReconnectMax, 30*time.Second)
	if c.JournalCapacity == 0 {
		c.JournalCapacity = 400
	}
	if c.ReplayBatch == 0 {
		c.ReplayBatch = 20
	}
	if c.MaxInFlight == 0 {
		c.MaxInFlight = 40
	}
	if c.Profile.HardMaxContinuousOnSeconds == 0 {
		c.Profile = SimProfile
	}
	if c.Version == "" {
		c.Version = "sim-0.1.0"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}
