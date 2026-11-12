// Package telemetry is the durable ingest path:
//
//	MQTT handler (validate, enqueue, return)  ->  bounded queue
//	  -> single writer goroutine: batch INSERT OR IGNORE in ONE SQLite tx
//	  -> COMMIT succeeded?  yes: publish PERSISTED receipt   no: publish nothing
//
// A receipt is the only thing that lets an ESP32 reclaim flash, so it is
// never sent for data that is not on disk (spec 2.3, 9.7, A10).
package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// Publisher sends receipts.
type Publisher interface {
	Publish(topic string, qos byte, retained bool, payload []byte) error
}

type recKind int

const (
	kindTelemetry recKind = iota
	kindEvent
	kindReject // unusable record: tell the device to drop it, store nothing
)

type recordJob struct {
	kind      recKind
	siteID    string
	deviceID  string
	unitID    string // registered unit (authoritative)
	bootID    string
	seq       int64
	source    string
	received  time.Time
	effective time.Time
	timeQ     string
	tel       *mc.Telemetry
	ev        *mc.Event
}

// Op is a non-record database mutation (incident changes, command results...).
// It runs in its own transaction; the returned func runs after commit.
type Op struct {
	Name string
	Fn   func(ctx context.Context, tx *sql.Tx) (after func(), err error)
}

type Writer struct {
	db         *storage.Store
	recs       chan recordJob
	ops        chan Op
	pub        Publisher
	hub        *sse.Hub
	m          *diagnostics.Metrics
	log        *slog.Logger
	siteID     string
	syncOutbox bool
}

func NewWriter(db *storage.Store, pub Publisher, hub *sse.Hub, m *diagnostics.Metrics, log *slog.Logger, siteID string, queue int, syncOutbox bool) *Writer {
	return &Writer{db: db, recs: make(chan recordJob, queue), ops: make(chan Op, 512), pub: pub, hub: hub, m: m, log: log, siteID: siteID, syncOutbox: syncOutbox}
}

// Enqueue never blocks the MQTT dispatcher. A full queue drops the record:
// no receipt is issued, so the device keeps it and re-sends (bounded memory
// on the Pi, durability on the device).
func (w *Writer) enqueue(j recordJob) bool {
	select {
	case w.recs <- j:
		return true
	default:
		w.m.QueueDrops.Add(1)
		return false
	}
}

// Do queues an Op; ops are idempotent/regenerable so dropping under overload
// is safe (the next heartbeat or retry recreates the intent).
func (w *Writer) Do(op Op) bool {
	select {
	case w.ops <- op:
		return true
	default:
		w.m.OpsDropped.Add(1)
		return false
	}
}

func (w *Writer) QueueDepth() (records, ops int) { return len(w.recs), len(w.ops) }

// Run processes jobs until ctx is cancelled, then drains what is queued.
func (w *Writer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.drain()
			return
		case j := <-w.recs:
			w.commitBatch(ctx, w.collect(j))
		case op := <-w.ops:
			w.runOp(ctx, op)
		}
	}
}

func (w *Writer) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case j := <-w.recs:
			w.commitBatch(ctx, w.collect(j))
		case op := <-w.ops:
			w.runOp(ctx, op)
		default:
			return
		}
	}
}

func (w *Writer) collect(first recordJob) []recordJob {
	batch := []recordJob{first}
	for len(batch) < 200 {
		select {
		case j := <-w.recs:
			batch = append(batch, j)
		default:
			return batch
		}
	}
	return batch
}

func (w *Writer) runOp(ctx context.Context, op Op) {
	var after func()
	err := w.db.Tx(ctx, func(tx *sql.Tx) error {
		var e error
		after, e = op.Fn(ctx, tx)
		return e
	})
	if err != nil {
		w.log.Error("db op failed", "op", op.Name, "err", err)
		w.noteCommit(err)
		return
	}
	if after != nil {
		after()
	}
}

func (w *Writer) noteCommit(err error) {
	if err != nil {
		w.m.CommitErrors.Add(1)
		w.m.StorageOK.Store(false)
	} else {
		w.m.StorageOK.Store(true)
	}
}

type devBoot struct{ device, boot string }

func (w *Writer) commitBatch(ctx context.Context, batch []recordJob) {
	start := time.Now()
	acked := map[devBoot][]int64{}
	rejected := map[devBoot][]int64{}
	latest := map[string]*recordJob{} // newest telemetry per unit, for SSE
	var persisted, dup int64

	err := w.db.IngestTx(ctx, func(tx *sql.Tx) error {
		persisted, dup = 0, 0
		for k := range acked {
			delete(acked, k)
		}
		for k := range rejected {
			delete(rejected, k)
		}
		for i := range batch {
			j := &batch[i]
			key := devBoot{j.deviceID, j.bootID}
			var res sql.Result
			var err error
			eventID := fmt.Sprintf("%s/%s/%d", j.deviceID, j.bootID, j.seq)
			switch j.kind {
			case kindReject:
				rejected[key] = append(rejected[key], j.seq)
				continue
			case kindTelemetry:
				rj, _ := json.Marshal(j.tel.Readings)
				res, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO telemetry_snapshots(event_id,site_id,device_id,unit_id,boot_id,sequence_number,uptime_ms,sampled_at_utc,clock_quality,received_at_utc,effective_at_utc,time_quality,program_version_applied,readings_json,source)
					VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					eventID, j.siteID, j.deviceID, nullIfEmpty(j.unitID), j.bootID, j.seq, j.tel.UptimeMS, j.tel.SampledAtUTC, string(j.tel.ClockQuality),
					storage.FormatTime(j.received), storage.FormatTime(j.effective), j.timeQ, j.tel.ProgramVersionApplied, string(rj), j.source)
			case kindEvent:
				dj, _ := json.Marshal(j.ev.Details)
				if j.ev.Details == nil {
					dj = []byte("{}")
				}
				res, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO device_events(event_id,site_id,device_id,unit_id,boot_id,sequence_number,uptime_ms,sampled_at_utc,clock_quality,received_at_utc,effective_at_utc,time_quality,event_type,severity,code,details_json,source)
					VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					eventID, j.siteID, j.deviceID, nullIfEmpty(j.unitID), j.bootID, j.seq, j.ev.UptimeMS, j.ev.SampledAtUTC, string(j.ev.ClockQuality),
					storage.FormatTime(j.received), storage.FormatTime(j.effective), j.timeQ, string(j.ev.EventType), string(j.ev.Severity), j.ev.Code, string(dj), j.source)
			}
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				dup++ // already durable from an earlier delivery: still safe to ack
			} else {
				persisted++
				if w.syncOutbox {
					if err := w.outbox(ctx, tx, j, eventID); err != nil {
						return err
					}
				}
			}
			acked[key] = append(acked[key], j.seq)
			if j.kind == kindTelemetry && j.unitID != "" {
				if cur := latest[j.unitID]; cur == nil || j.effective.After(cur.effective) {
					latest[j.unitID] = j
				}
			}
		}
		return nil
	})
	w.noteCommit(err)
	if err != nil {
		// Nothing durable => nothing acknowledged. Devices keep their records
		// and retry after their receipt timeout.
		w.log.Error("batch commit failed; NO receipts sent", "records", len(batch), "err", err)
		return
	}
	w.m.RecordsPersisted.Add(persisted)
	w.m.DuplicatesIgnored.Add(dup)
	w.m.LastCommitMicros.Store(time.Since(start).Microseconds())
	w.sendReceipts(batch, acked, rejected)

	for unit, j := range latest {
		w.hub.Publish("unit.reading", map[string]any{"unit_id": unit, "effective_at_utc": storage.FormatTime(j.effective),
			"time_quality": j.timeQ, "data_source": j.source, "readings": j.tel.Readings})
	}
}

func (w *Writer) outbox(ctx context.Context, tx *sql.Tx, j *recordJob, eventID string) error {
	var typ string
	var pri int
	var payload []byte
	var rec []byte
	if j.kind == kindTelemetry {
		typ, pri = sc.EventTelemetry, sc.P3
		rec, _ = json.Marshal(j.tel)
	} else {
		typ, pri = sc.EventDeviceEvent, sc.P1
		if j.ev.Severity == mc.SevCritical {
			pri = sc.P0
		}
		rec, _ = json.Marshal(j.ev)
	}
	// original device fields stay untouched; the Pi's placement in time and
	// the durable-store time travel beside them
	payload, _ = json.Marshal(sc.RecordEnvelope{Record: rec, EffectiveAtUTC: j.effective.UTC().Format(time.RFC3339Nano), TimeQuality: j.timeQ, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO outbox(event_id,event_type,priority,site_id,payload_json,created_at) VALUES (?,?,?,?,?,?)`,
		eventID, typ, pri, j.siteID, string(payload), storage.Now())
	return err
}

func (w *Writer) sendReceipts(batch []recordJob, acked, rejected map[devBoot][]int64) {
	keys := map[devBoot]struct{}{}
	for k := range acked {
		keys[k] = struct{}{}
	}
	for k := range rejected {
		keys[k] = struct{}{}
	}
	ordered := make([]devBoot, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].device+ordered[i].boot < ordered[j].device+ordered[j].boot })
	for _, k := range ordered {
		a, r := acked[k], rejected[k]
		for len(a) > 0 || len(r) > 0 {
			rc := mc.Receipt{SchemaVersion: 1, SiteID: w.siteID, DeviceID: k.device, BootID: k.boot, ReceiptID: "rcpt-" + auth.NewID(), Status: "PERSISTED",
				AckedSequences: []int64{}, RejectedSequences: []int64{}}
			n := min(len(a), 100)
			rc.AckedSequences, a = a[:n], a[n:]
			if n == 0 {
				m := min(len(r), 100)
				rc.RejectedSequences, r = r[:m], r[m:]
			}
			b, _ := json.Marshal(rc)
			if err := w.pub.Publish(mc.DeviceTopic(k.device, mc.TopicReceipts), 1, false, b); err != nil {
				// Data is durable; the device will simply resend, and the
				// duplicate is acknowledged again idempotently.
				w.log.Warn("receipt publish failed", "device", k.device, "err", err)
				continue
			}
			w.m.ReceiptsSent.Add(1)
		}
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var _ = strings.TrimSpace
