package esp32

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

func randToken(prefix string, nbytes int) string {
	b := make([]byte, nbytes)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Device is one simulated ESP32 controller. The control loop (tick) never
// calls into the network layer synchronously: it only appends to the bounded
// journal and pokes a non-blocking notify channel.
type Device struct {
	cfg Config
	mu  sync.Mutex

	bootID     string
	bootStart  time.Time
	seq        int64
	state      mc.ControlState
	phaseStart time.Time
	pumpOn     bool
	onSince    time.Time
	checkUntil time.Time
	latched    bool // PROTECTED_STOP latched: leaving needs local confirmation

	nvs     *nvsData
	jr      *journal
	sensors *sensorBank
	raised  map[string]bool // fault codes currently raised (for event edges)

	// observability for tests / control API (not part of the wire contract)
	pumpCycles   int
	pumpOnTotal  time.Duration
	dropResults  int
	rejectedRecs int

	link   *netLink
	notify chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds a device. Call Start to run it.
func New(cfg Config) *Device {
	cfg.defaults()
	d := &Device{
		cfg:     cfg,
		nvs:     loadNVS(cfg.StatePath, cfg.Profile.HardMaxContinuousOnSeconds),
		jr:      newJournal(cfg.JournalCapacity),
		sensors: newSensorBank(cfg.Seed),
		notify:  make(chan struct{}, 1),
	}
	d.link = newNetLink(d)
	d.boot(time.Now())
	return d
}

// boot resets volatile state. Outputs are OFF before anything else happens.
func (d *Device) boot(now time.Time) {
	d.bootID = randToken("boot-", 8)
	d.bootStart = now
	d.seq = 0
	d.pumpOn = false
	d.state = mc.StateBootSafe
	d.latched = false
	d.raised = map[string]bool{}
	d.checkUntil = now.Add(d.cfg.SelfCheckTime)
	d.phaseStart = now
	d.emitEvent(mc.EventBoot, mc.SevInfo, "BOOT", map[string]any{"firmware": d.cfg.Version}, now)
	d.state = mc.StateSelfCheck
}

// Start launches the control loop, telemetry sampler and network link.
func (d *Device) Start(ctx context.Context) {
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(3)
	go func() { defer d.wg.Done(); d.controlLoop(ctx) }()
	go func() { defer d.wg.Done(); d.telemetryLoop(ctx) }()
	go func() { defer d.wg.Done(); d.link.run(ctx) }()
}

// Stop shuts the device down gracefully.
func (d *Device) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
	d.mu.Lock()
	d.setPumpLocked(false, time.Now())
	d.mu.Unlock()
}

func (d *Device) controlLoop(ctx context.Context) {
	t := time.NewTicker(d.cfg.ControlTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.tick(now)
		}
	}
}

func (d *Device) telemetryLoop(ctx context.Context) {
	first := time.NewTimer(minDur(time.Second, d.cfg.TelemetryEvery))
	defer first.Stop()
	t := time.NewTicker(d.cfg.TelemetryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			d.sampleTelemetry(time.Now())
		case now := <-t.C:
			d.sampleTelemetry(now)
		}
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// ------------------------------------------------------------ control logic

func (d *Device) setState(s mc.ControlState, now time.Time) {
	if d.state == s {
		return
	}
	d.state = s
	d.emitEvent(mc.EventStateChange, mc.SevInfo, string(s), nil, now)
}

func (d *Device) setPumpLocked(on bool, now time.Time) {
	if on == d.pumpOn {
		return
	}
	if on {
		d.onSince = now
		d.pumpCycles++
	} else {
		d.pumpOnTotal += now.Sub(d.onSince)
	}
	d.pumpOn = on
}

// activeFaults derives the fault codes from sensors + state.
func (d *Device) activeFaults() (codes []string, essential bool) {
	f := d.sensors.getFaults()
	if f.LowWater {
		codes = append(codes, mc.FaultLowWater)
		essential = true
	}
	if f.LevelSensorInvalid {
		codes = append(codes, mc.FaultLevelSensor)
		essential = true // invalid level feedback: fail safe, stop the pump
	}
	if f.PHInvalid {
		codes = append(codes, mc.FaultPHInvalid)
	}
	if f.ECInvalid {
		codes = append(codes, mc.FaultECInvalid)
	}
	if d.state == mc.StateNeedsSetup {
		codes = append(codes, mc.FaultNoProgram)
	}
	sort.Strings(codes)
	return
}

func (d *Device) tick(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	codes, essential := d.activeFaults()
	d.syncFaultEvents(codes, now)

	// Essential protection overrides everything, in every state, instantly.
	if essential && d.state != mc.StateProtectedStop && d.state != mc.StateBootSafe {
		d.setPumpLocked(false, now)
		d.latched = true
		d.setState(mc.StateProtectedStop, now)
		return
	}

	switch d.state {
	case mc.StateBootSafe:
		d.setPumpLocked(false, now)
	case mc.StateSelfCheck:
		d.setPumpLocked(false, now)
		if now.Before(d.checkUntil) {
			return
		}
		switch {
		case essential:
			d.latched = true
			d.setState(mc.StateProtectedStop, now)
		case d.nvs.Program == nil:
			d.setState(mc.StateNeedsSetup, now)
		default:
			d.phaseStart = now // always start a cycle in the OFF phase
			d.setState(mc.StateRunning, now)
		}
	case mc.StateNeedsSetup:
		d.setPumpLocked(false, now) // no valid programme => no irrigation (A20)
	case mc.StateProtectedStop:
		d.setPumpLocked(false, now) // leaves only via ConfirmRecovery
	case mc.StateRunning, mc.StateDegraded:
		d.runCycle(now)
		if f := d.sensors.getFaults(); f.PHInvalid || f.ECInvalid {
			d.setState(mc.StateDegraded, now) // non-critical: keep irrigating
		} else {
			d.setState(mc.StateRunning, now)
		}
	}
}

// runCycle implements the timed OFF->ON cycle on the monotonic clock only.
func (d *Device) runCycle(now time.Time) {
	p := d.nvs.Program
	if p == nil {
		d.setPumpLocked(false, now)
		d.setState(mc.StateNeedsSetup, now)
		return
	}
	onFor := minInt(p.OnDurationSeconds, p.MaxContinuousOnSecs, d.cfg.Profile.HardMaxContinuousOnSeconds)
	if d.pumpOn {
		// Bounded ON: never trust the schedule alone (watchdog).
		if now.Sub(d.onSince) >= time.Duration(onFor)*time.Second {
			d.setPumpLocked(false, now)
			d.phaseStart = now
		}
		return
	}
	if now.Sub(d.phaseStart) >= time.Duration(p.OffDurationSeconds)*time.Second {
		d.setPumpLocked(true, now)
	}
}

func minInt(a int, rest ...int) int {
	for _, r := range rest {
		if r < a {
			a = r
		}
	}
	return a
}

func (d *Device) syncFaultEvents(codes []string, now time.Time) {
	cur := map[string]bool{}
	for _, c := range codes {
		if c == mc.FaultNoProgram {
			continue // a state, not a hardware fault event
		}
		cur[c] = true
		if !d.raised[c] {
			sev := mc.SevWarning
			if c == mc.FaultLowWater || c == mc.FaultLevelSensor {
				sev = mc.SevCritical
			}
			d.emitEvent(mc.EventFaultRaised, sev, c, nil, now)
		}
	}
	for c := range d.raised {
		if !cur[c] {
			d.emitEvent(mc.EventFaultCleared, mc.SevInfo, c, nil, now)
		}
	}
	d.raised = cur
}

// ConfirmRecovery is the local physical confirmation required to leave
// PROTECTED_STOP. It is deliberately NOT reachable over MQTT.
func (d *Device) ConfirmRecovery() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != mc.StateProtectedStop {
		return false
	}
	if _, essential := d.activeFaults(); essential {
		return false // fault still present
	}
	now := time.Now()
	d.latched = false
	d.emitEvent(mc.EventRecoveryConfirm, mc.SevInfo, "RECOVERY_CONFIRMED", nil, now)
	d.checkUntil = now.Add(d.cfg.SelfCheckTime)
	d.setState(mc.StateSelfCheck, now)
	return true
}

// SetFaults injects hardware faults (simulator control only).
func (d *Device) SetFaults(f Faults) { d.sensors.setFaults(f) }
func (d *Device) Faults() Faults     { return d.sensors.getFaults() }

// Reboot simulates a reset / power cycle: new boot_id, seq restarts, outputs
// OFF, NVS programme and the flash journal survive. Forces a reconnect.
func (d *Device) Reboot() {
	d.mu.Lock()
	now := time.Now()
	d.setPumpLocked(false, now)
	d.boot(now)
	d.mu.Unlock()
	d.link.dropConnection()
}

// ---------------------------------------------------------------- journaling

func (d *Device) header() mc.Header {
	h := mc.Header{SchemaVersion: mc.SchemaVersion, SiteID: d.cfg.SiteID, DeviceID: d.cfg.DeviceID, BootID: d.bootID, DataSource: mc.SourceSimulator}
	if d.cfg.UnitID != "" {
		u := d.cfg.UnitID
		h.UnitID = &u
	}
	return h
}

func (d *Device) clock(now time.Time) (mc.ClockQuality, *string) {
	if !d.cfg.RTC {
		return mc.ClockMonotonicOnly, nil // never invent a timestamp
	}
	s := now.UTC().Format(time.RFC3339)
	return mc.ClockRTCSynced, &s
}

func (d *Device) appendRecord(leaf string, critical bool, build func(seq int64) any) {
	d.seq++
	b, err := json.Marshal(build(d.seq))
	if err != nil {
		return
	}
	// overflow is counted by the journal and reported in the state heartbeat
	d.jr.append(&record{bootID: d.bootID, seq: d.seq, topic: leaf, payload: b, critical: critical})
	select {
	case d.notify <- struct{}{}:
	default:
	}
}

func (d *Device) uptimeMS(now time.Time) int64 { return now.Sub(d.bootStart).Milliseconds() }

// emitEvent must be called with d.mu held.
func (d *Device) emitEvent(t mc.EventType, sev mc.Severity, code string, details map[string]any, now time.Time) {
	cq, ts := d.clock(now)
	crit := sev == mc.SevCritical
	d.appendRecord(mc.TopicEvents, crit, func(seq int64) any {
		return mc.Event{Header: d.header(), Type: "event", Seq: seq, UptimeMS: d.uptimeMS(now), SampledAtUTC: ts, ClockQuality: cq,
			EventType: t, Severity: sev, Code: code, Details: details}
	})
}

func (d *Device) sampleTelemetry(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cq, ts := d.clock(now)
	readings := d.sensors.sample(now.Sub(d.bootStart).Seconds())
	var pv *int
	if d.nvs.Program != nil {
		v := d.nvs.Version
		pv = &v
	}
	d.appendRecord(mc.TopicTelemetry, false, func(seq int64) any {
		return mc.Telemetry{Header: d.header(), Type: "telemetry", Seq: seq, UptimeMS: d.uptimeMS(now), SampledAtUTC: ts, ClockQuality: cq,
			ProgramVersionApplied: pv, Readings: readings}
	})
}

// buildState produces the 5 s heartbeat (ephemeral, not journaled).
func (d *Device) buildState(nonce string) mc.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	codes, essential := d.activeFaults()
	var pv *int
	if d.nvs.Program != nil {
		v := d.nvs.Version
		pv = &v
	}
	var last *string
	if d.nvs.LastCommandID != "" {
		l := d.nvs.LastCommandID
		last = &l
	}
	pump := "OFF"
	if d.pumpOn {
		pump = "ON"
	}
	if codes == nil {
		codes = []string{}
	}
	return mc.State{Header: d.header(), UptimeMS: d.uptimeMS(time.Now()), ControlState: d.state, PumpCommanded: pump,
		WaterDelivery: "NOT_VERIFIED", ProgramVersionApplied: pv, LastCommandID: last,
		EssentialInterlocksOK: !essential, FaultCodes: codes, FreeHeapBytes: 120000 + int64(d.jr.occupancy()),
		WifiRSSIDBM: -60, FirmwareVersion: d.cfg.Version, SessionNonce: nonce,
		Buffer: mc.BufferStatus{Occupancy: d.jr.occupancy(), Capacity: d.jr.cap, Unreceipted: d.jr.unreceipted(), Overflowed: d.jr.overflow}}
}

func (d *Device) handleReceipt(raw []byte) {
	var r mc.Receipt
	if mc.DecodeStrict(raw, &r) != nil || r.Validate() != nil {
		return
	}
	if r.DeviceID != d.cfg.DeviceID || r.SiteID != d.cfg.SiteID {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.jr.reclaim(r.BootID, r.AckedSequences)
	n := d.jr.reclaim(r.BootID, r.RejectedSequences)
	d.rejectedRecs += n
}

// Status is a read-only snapshot for tests and the control API.
type Status struct {
	DeviceID       string          `json:"device_id"`
	UnitID         string          `json:"unit_id"`
	BootID         string          `json:"boot_id"`
	ControlState   mc.ControlState `json:"control_state"`
	PumpOn         bool            `json:"pump_on"`
	PumpCycles     int             `json:"pump_cycles"`
	PumpOnMS       int64           `json:"pump_on_ms_total"`
	ProgramVersion int             `json:"program_version"`
	Seq            int64           `json:"seq"`
	JournalOcc     int             `json:"journal_occupancy"`
	JournalCap     int             `json:"journal_capacity"`
	Overflow       int64           `json:"journal_overflow"`
	Connected      bool            `json:"mqtt_connected"`
	NetworkEnabled bool            `json:"network_enabled"`
	Faults         Faults          `json:"faults"`
	Processed      int             `json:"processed_commands"`
	Reconnects     int64           `json:"reconnects"`
}

func (d *Device) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	on := d.pumpOnTotal
	if d.pumpOn {
		on += time.Since(d.onSince)
	}
	return Status{DeviceID: d.cfg.DeviceID, UnitID: d.cfg.UnitID, BootID: d.bootID, ControlState: d.state, PumpOn: d.pumpOn,
		PumpCycles: d.pumpCycles, PumpOnMS: on.Milliseconds(), ProgramVersion: d.nvs.Version, Seq: d.seq,
		JournalOcc: d.jr.occupancy(), JournalCap: d.jr.cap, Overflow: d.jr.overflow, Connected: d.link.isConnected(),
		NetworkEnabled: d.link.enabled.Load(), Faults: d.sensors.getFaults(), Processed: len(d.nvs.Processed), Reconnects: d.link.reconnects.Load()}
}

// SetNetwork simulates Wi-Fi / broker reachability. Disabling drops the TCP
// connection abruptly (so the broker publishes the last will). The control
// loop is unaffected - that is the point.
func (d *Device) SetNetwork(up bool) {
	d.link.enabled.Store(up)
	if !up {
		d.link.dropConnection()
	}
}

// DropNextResults makes the device apply commands but "lose" the next n result
// messages, to exercise the lost-acknowledgement path (A05).
func (d *Device) DropNextResults(n int) { d.mu.Lock(); d.dropResults = n; d.mu.Unlock() }

// Provision installs a programme out of band (tests / factory), bypassing MQTT.
func (d *Device) Provision(p mc.Program, version int) error {
	if err := p.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nvs.Previous, d.nvs.PrevVersion = d.nvs.Program, d.nvs.Version
	d.nvs.Program, d.nvs.Version = &p, version
	_ = d.nvs.save(d.cfg.StatePath)
	if d.state == mc.StateNeedsSetup {
		d.checkUntil = time.Now().Add(d.cfg.SelfCheckTime)
		d.setState(mc.StateSelfCheck, time.Now())
	}
	return nil
}
