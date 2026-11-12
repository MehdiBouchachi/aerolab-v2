package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/incidents"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// Ingest turns raw MQTT messages into validated, authorised work. It runs on
// the broker dispatch goroutine, so it only validates and enqueues.
type Ingest struct {
	siteID     string
	allowSim   bool
	staleAfter time.Duration
	reg        *devices.Registry
	w          *Writer
	cmds       *commands.Manager
	hub        *sse.Hub
	m          *diagnostics.Metrics
	log        *slog.Logger
}

func NewIngest(siteID string, allowSim bool, staleAfter time.Duration, reg *devices.Registry, w *Writer, cmds *commands.Manager, hub *sse.Hub, m *diagnostics.Metrics, log *slog.Logger) *Ingest {
	return &Ingest{siteID: siteID, allowSim: allowSim, staleAfter: staleAfter, reg: reg, w: w, cmds: cmds, hub: hub, m: m, log: log}
}

// authorize checks everything that is not payload syntax: the topic's device
// id must equal the payload's, the device must be registered + enabled, the
// site must match, and a device may not change data_source (simulated data can
// never masquerade as real hardware, or vice versa).
func (i *Ingest) authorize(topicDev string, h mc.Header) (devices.Runtime, bool) {
	if h.DeviceID != topicDev {
		i.m.Reject("TOPIC_DEVICE_MISMATCH")
		return devices.Runtime{}, false
	}
	rt, ok := i.reg.Lookup(topicDev)
	if !ok || rt.Disabled {
		i.m.UnknownDevice.Add(1)
		return devices.Runtime{}, false
	}
	if h.SiteID != i.siteID {
		i.m.Reject("SITE_MISMATCH")
		return devices.Runtime{}, false
	}
	if string(h.DataSource) != rt.DataSource {
		i.m.SourceMismatch.Add(1)
		i.m.Reject("DATA_SOURCE_MISMATCH")
		return devices.Runtime{}, false
	}
	if h.DataSource == mc.SourceSimulator && !i.allowSim {
		i.m.Reject("SIMULATED_DATA_NOT_ALLOWED")
		return devices.Runtime{}, false
	}
	if h.UnitID != nil && rt.UnitID != "" && *h.UnitID != rt.UnitID {
		i.m.Reject("UNIT_MISMATCH")
		return devices.Runtime{}, false
	}
	return rt, true
}

// HandleMessage is the MQTT dispatcher entry point.
func (i *Ingest) HandleMessage(topic string, payload []byte) {
	i.m.MQTTIn.Add(1)
	dev, leaf, err := mc.ParseDeviceTopic(topic)
	if err != nil {
		i.m.Reject("BAD_TOPIC")
		return
	}
	now := time.Now()
	switch leaf {
	case mc.TopicTelemetry:
		i.handleTelemetry(dev, payload, now)
	case mc.TopicEvents:
		i.handleEvent(dev, payload, now)
	case mc.TopicState:
		i.handleState(dev, payload, now)
	case mc.TopicPresence:
		i.handlePresence(dev, payload)
	case mc.TopicCommandResult:
		i.handleResult(dev, payload)
	default:
		i.m.Reject("UNEXPECTED_TOPIC")
	}
}

// effective chooses the time used for charts and never alters the original
// fields: device clock if it has one, else an estimate anchored on the most
// recent heartbeat of the SAME boot, else the receive time (labelled).
func effective(sampled *string, uptimeMS int64, bootID string, rt devices.Runtime, recv time.Time) (time.Time, string) {
	if sampled != nil {
		if t, err := time.Parse(time.RFC3339, *sampled); err == nil {
			return t.UTC(), "DEVICE_CLOCK"
		}
	}
	if rt.Seen && rt.BootID == bootID {
		est := rt.StateAt.Add(-time.Duration(rt.State.UptimeMS-uptimeMS) * time.Millisecond)
		if est.After(recv) {
			est = recv
		}
		return est.UTC(), "ESTIMATED_FROM_UPTIME"
	}
	return recv.UTC(), "RECEIVED_AT_ONLY"
}

// seqOf extracts only identity + seq from a payload that failed validation,
// so the device can be told to drop a poisoned record instead of resending it
// forever. Returns ok=false when identity itself is unusable.
func (i *Ingest) reject(dev string, h mc.Header, seq int64, code string, recv time.Time) {
	i.m.Reject(code)
	if seq < 1 || !mc.ValidID(h.DeviceID) || h.DeviceID != dev || h.BootID == "" {
		return
	}
	if rt, ok := i.reg.Lookup(dev); !ok || rt.Disabled || h.SiteID != i.siteID {
		return
	}
	i.w.enqueue(recordJob{kind: kindReject, siteID: h.SiteID, deviceID: dev, bootID: h.BootID, seq: seq, received: recv})
}

func (i *Ingest) handleTelemetry(dev string, payload []byte, now time.Time) {
	var t mc.Telemetry
	if err := mc.DecodeStrict(payload, &t); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if err := t.Validate(); err != nil {
		i.reject(dev, t.Header, t.Seq, codeOf(err), now)
		return
	}
	rt, ok := i.authorize(dev, t.Header)
	if !ok {
		return
	}
	eff, q := effective(t.SampledAtUTC, t.UptimeMS, t.BootID, rt, now)
	i.w.enqueue(recordJob{kind: kindTelemetry, siteID: i.siteID, deviceID: dev, unitID: rt.UnitID, bootID: t.BootID, seq: t.Seq,
		source: string(t.DataSource), received: now, effective: eff, timeQ: q, tel: &t})
}

func (i *Ingest) handleEvent(dev string, payload []byte, now time.Time) {
	var e mc.Event
	if err := mc.DecodeStrict(payload, &e); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if err := e.Validate(); err != nil {
		i.reject(dev, e.Header, e.Seq, codeOf(err), now)
		return
	}
	rt, ok := i.authorize(dev, e.Header)
	if !ok {
		return
	}
	eff, q := effective(e.SampledAtUTC, e.UptimeMS, e.BootID, rt, now)
	i.w.enqueue(recordJob{kind: kindEvent, siteID: i.siteID, deviceID: dev, unitID: rt.UnitID, bootID: e.BootID, seq: e.Seq,
		source: string(e.DataSource), received: now, effective: eff, timeQ: q, ev: &e})
}

func (i *Ingest) handlePresence(dev string, payload []byte) {
	var p mc.Presence
	if err := mc.DecodeStrict(payload, &p); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if err := p.Validate(); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if _, ok := i.authorize(dev, p.Header); !ok {
		return
	}
	// Presence only short-circuits staleness; it never asserts health.
	i.reg.SetPresence(dev, p.Status == mc.PresenceOffline)
}

func (i *Ingest) handleState(dev string, payload []byte, now time.Time) {
	var s mc.State
	if err := mc.DecodeStrict(payload, &s); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if err := s.Validate(); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if _, ok := i.authorize(dev, s.Header); !ok {
		return
	}
	ch, ok := i.reg.ApplyState(dev, s, now)
	if !ok {
		return
	}
	unit := ch.UnitID
	if ch.FaultsChanged || ch.TouchIncidents || ch.Recovered || ch.First {
		faults, recovered := ch.Faults, ch.Recovered || ch.First // first beat after an edge restart also clears a persisted DEVICE_STALE
		i.w.Do(Op{Name: "incidents.reconcile", Fn: func(ctx context.Context, tx *sql.Tx) (func(), error) {
			ts := storage.FormatTime(now)
			var changes []incidents.Change
			if recovered { // the controller is talking again: the "not reporting" incident ends
				c, err := incidents.SetCondition(ctx, tx, i.siteID, unit, incidents.CodeDeviceStale, false, ts)
				if err != nil {
					return nil, err
				}
				if c != nil {
					changes = append(changes, *c)
				}
			}
			cs, err := incidents.Reconcile(ctx, tx, i.siteID, unit, faults, ts)
			if err != nil {
				return nil, err
			}
			changes = append(changes, cs...)
			return func() {
				for _, c := range changes {
					i.hub.Publish("incident.updated", c.Incident)
				}
			}, nil
		}})
	}
	if ch.ControlChanged && unit != "" {
		st := s
		i.w.Do(Op{Name: "unit.status", Fn: func(ctx context.Context, tx *sql.Tx) (func(), error) {
			var pv any
			if st.ProgramVersionApplied != nil {
				pv = *st.ProgramVersionApplied
			}
			_, err := tx.ExecContext(ctx, `UPDATE growing_units SET status=?, applied_program_version=COALESCE(?,applied_program_version), last_seen_at=? WHERE id=?`,
				string(st.ControlState), pv, storage.FormatTime(now), unit)
			return nil, err
		}})
	}
	if s.LastCommandID != nil && i.cmds.HasPending(*s.LastCommandID) {
		st := s
		i.w.Do(Op{Name: "command.reconcile", Fn: func(ctx context.Context, tx *sql.Tx) (func(), error) {
			return i.cmds.ReconcileFromState(ctx, tx, dev, st)
		}})
	}
	if ch.PublishSSE {
		i.hub.Publish("unit.state", i.unitStateEvent(dev, s, now))
	}
}

func (i *Ingest) unitStateEvent(dev string, s mc.State, now time.Time) map[string]any {
	return map[string]any{"unit_id": s.UnitID, "device_id": dev, "connection": devices.ConnOnline, "control_state": s.ControlState,
		"pump_commanded": s.PumpCommanded, "water_delivery": s.WaterDelivery, "program_version_applied": s.ProgramVersionApplied,
		"fault_codes": s.FaultCodes, "essential_interlocks_ok": s.EssentialInterlocksOK, "observed_at": storage.FormatTime(now), "data_source": s.DataSource}
}

func (i *Ingest) handleResult(dev string, payload []byte) {
	var r mc.CommandResult
	if err := mc.DecodeStrict(payload, &r); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if err := r.Validate(); err != nil {
		i.m.Reject(codeOf(err))
		return
	}
	if r.DeviceID != dev {
		i.m.Reject("TOPIC_DEVICE_MISMATCH")
		return
	}
	rt, ok := i.reg.Lookup(dev)
	if !ok || rt.Disabled || r.SiteID != i.siteID || string(r.DataSource) != rt.DataSource {
		i.m.Reject("RESULT_NOT_AUTHORISED")
		return
	}
	i.w.Do(Op{Name: "command.result", Fn: func(ctx context.Context, tx *sql.Tx) (func(), error) {
		return i.cmds.ApplyResult(ctx, tx, r)
	}})
}

// Watchdog flags controllers whose heartbeat is overdue (spec: ~15 s) and
// opens a DEVICE_STALE incident. It only observes; it never changes control.
func (i *Ingest) Watchdog(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, rt := range i.reg.StaleTransitions(now, i.staleAfter) {
				rt := rt
				i.hub.Publish("unit.state", map[string]any{"unit_id": rt.UnitID, "device_id": rt.ID, "connection": rt.Connection(now, i.staleAfter),
					"last_state_at": storage.FormatTime(rt.StateAt), "note": "outdated: last known values only"})
				i.w.Do(Op{Name: "incidents.stale", Fn: func(ctx context.Context, tx *sql.Tx) (func(), error) {
					c, err := incidents.SetCondition(ctx, tx, i.siteID, rt.UnitID, incidents.CodeDeviceStale, true, storage.FormatTime(now))
					if err != nil || c == nil {
						return nil, err
					}
					return func() { i.hub.Publish("incident.updated", c.Incident) }, nil
				}})
			}
		}
	}
}

func codeOf(err error) string {
	var ve *mc.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return "INVALID"
}
