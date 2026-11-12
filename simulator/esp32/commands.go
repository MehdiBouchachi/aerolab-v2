package esp32

import (
	"encoding/json"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

// handleCommand is the device-side command_handler: validate, authorize the
// session, deduplicate, version-check, bound-check, persist atomically,
// then answer. A repeated command_id returns the ORIGINAL outcome and never
// re-applies.
func (d *Device) handleCommand(raw []byte, nonce string) {
	var c mc.Command
	if err := mc.DecodeStrict(raw, &c); err != nil {
		return // unparseable: no command identity to answer
	}
	if c.DeviceID != d.cfg.DeviceID || c.SiteID != d.cfg.SiteID {
		return // not for me
	}
	if err := c.Validate(); err != nil {
		reason := "INVALID_COMMAND"
		if ve, ok := err.(*mc.ValidationError); ok {
			reason = ve.Code
		}
		d.mu.Lock()
		res := d.finishLocked(c.CommandID, mc.ResultRejected, reason, false)
		d.mu.Unlock()
		d.link.publishResult(res)
		return
	}

	d.mu.Lock()
	now := time.Now()

	// 1. idempotency: known command id -> original outcome, no re-application.
	if prev, ok := d.nvs.lookup(c.CommandID); ok {
		pv := d.versionPtr()
		res := mc.CommandResult{SchemaVersion: 1, CommandID: c.CommandID, SiteID: d.cfg.SiteID, DeviceID: d.cfg.DeviceID, BootID: d.bootID,
			DataSource: mc.SourceSimulator, Status: mc.ResultAlreadyProcessed, OriginalStatus: prev.Status, CurrentProgramVersion: pv}
		if prev.Reason != "" {
			r := prev.Reason
			res.ReasonCode = &r
		}
		d.mu.Unlock()
		d.link.publishResult(res)
		return
	}

	// 2. a command must belong to the CURRENT MQTT session: anything parked in
	// the broker across a reconnect carries an old nonce and is refused.
	if c.SessionNonce != nonce {
		res := d.finishLocked(c.CommandID, mc.ResultRejected, "STALE_SESSION", true)
		d.mu.Unlock()
		d.link.publishResult(res)
		return
	}
	// 3. freshness on the device's own monotonic clock cannot be established
	// for an absolute expiry; ttl is enforced from receipt and the nonce binds
	// the session. If an RTC is present, also honour the absolute expiry.
	if d.cfg.RTC {
		if exp, err := time.Parse(time.RFC3339, c.ExpiresAtUTC); err == nil && now.After(exp) {
			res := d.finishLocked(c.CommandID, mc.ResultRejected, "COMMAND_EXPIRED", true)
			d.mu.Unlock()
			d.link.publishResult(res)
			return
		}
	}

	switch c.CommandType {
	case mc.CmdQueryState:
		res := d.finishLocked(c.CommandID, mc.ResultApplied, "", true)
		d.mu.Unlock()
		d.link.publishResult(res)
		d.link.publishStateNow()
		return

	case mc.CmdApplyProgram:
		cur := d.nvs.Version // 0 when no programme
		hard := d.cfg.Profile.HardMaxContinuousOnSeconds
		var reason string
		switch {
		case c.ExpectedProgramVersion != cur:
			reason = "VERSION_CONFLICT"
		case c.Program.OnDurationSeconds > hard || c.Program.MaxContinuousOnSecs > hard:
			reason = "PROGRAM_EXCEEDS_HARDWARE_LIMIT"
		}
		if reason != "" {
			res := d.finishLocked(c.CommandID, mc.ResultRejected, reason, true)
			d.mu.Unlock()
			d.link.publishResult(res)
			return
		}
		// Atomic selection: previous revision kept as fallback.
		next := *c.Program
		backup := *d.nvs
		d.nvs.Previous, d.nvs.PrevVersion = d.nvs.Program, d.nvs.Version
		d.nvs.Program, d.nvs.Version = &next, c.NewProgramVersion
		d.nvs.LastCommandID = c.CommandID
		d.nvs.remember(processedCmd{CommandID: c.CommandID, Status: mc.ResultApplied})
		if err := d.nvs.save(d.cfg.StatePath); err != nil {
			*d.nvs = backup // persistence failed: nothing changed
			res := d.finishLocked(c.CommandID, mc.ResultRejected, "PERSIST_FAILED", true)
			d.mu.Unlock()
			d.link.publishResult(res)
			return
		}
		// New programme starts in the OFF phase; pump never jumps ON from a command.
		d.setPumpLocked(false, now)
		d.phaseStart = now
		if d.state == mc.StateNeedsSetup {
			d.checkUntil = now.Add(d.cfg.SelfCheckTime)
			d.setState(mc.StateSelfCheck, now)
		}
		d.emitEvent(mc.EventProgramApplied, mc.SevInfo, "PROGRAM_APPLIED", map[string]any{"version": c.NewProgramVersion}, now)
		res := d.resultLocked(c.CommandID, mc.ResultApplied, "")
		d.mu.Unlock()
		d.link.publishResult(res)
		d.link.publishStateNow()
	}
}

func (d *Device) versionPtr() *int {
	if d.nvs.Program == nil {
		return nil
	}
	v := d.nvs.Version
	return &v
}

func (d *Device) resultLocked(id string, st mc.ResultStatus, reason string) mc.CommandResult {
	r := mc.CommandResult{SchemaVersion: 1, CommandID: id, SiteID: d.cfg.SiteID, DeviceID: d.cfg.DeviceID, BootID: d.bootID,
		DataSource: mc.SourceSimulator, Status: st, CurrentProgramVersion: d.versionPtr()}
	if reason != "" {
		r.ReasonCode = &reason
	}
	return r
}

// finishLocked records the outcome in the bounded dedup set (when remember)
// and returns the result message.
func (d *Device) finishLocked(id string, st mc.ResultStatus, reason string, remember bool) mc.CommandResult {
	if remember {
		d.nvs.remember(processedCmd{CommandID: id, Status: st, Reason: reason})
		_ = d.nvs.save(d.cfg.StatePath)
	}
	return d.resultLocked(id, st, reason)
}

// shouldDropResult implements the lost-ack fault injection.
func (d *Device) shouldDropResult() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dropResults > 0 {
		d.dropResults--
		return true
	}
	return false
}

var _ = json.Marshal
