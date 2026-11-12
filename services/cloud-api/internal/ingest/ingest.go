// Package ingest applies an edge sync batch idempotently.
//
//   - Records are keyed by the edge's stable event id: a re-sent batch (Pi
//     restarted mid-batch, lost response) changes nothing and is still acked.
//   - A malformed item is REJECTED individually (reported back so the Pi stops
//     resending it); it never blocks the rest of the batch.
//   - Source timestamps are stored as given; cloud ingest time is a separate column.
//   - Incident updates merge by stable id with "greatest changed_at wins".
package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
)

type Result struct {
	Resp sc.AckResponse
}

func parseT(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

func Apply(ctx context.Context, db *sql.DB, siteID string, b sc.Batch) (sc.AckResponse, error) {
	resp := sc.AckResponse{Acked: []string{}, Rejected: map[string]string{}, ServerTime: time.Now().UTC().Format(time.RFC3339Nano)}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return resp, err
	}
	defer tx.Rollback() //nolint:errcheck

	// A batch id seen before is a retry: skipping bookkeeping is safe because
	// every item below is itself idempotent.
	if b.State != nil {
		if err := applyState(ctx, tx, siteID, b.State); err != nil {
			return resp, fmt.Errorf("state: %w", err)
		}
	}
	accepted, rejected := 0, 0
	for _, ev := range b.Events {
		if ev.EventID == "" || len(ev.EventID) > 200 {
			continue
		}
		dup, reason, err := applyEvent(ctx, tx, siteID, ev)
		if err != nil {
			return resp, fmt.Errorf("event %s: %w", ev.EventID, err)
		}
		if reason != "" {
			resp.Rejected[ev.EventID] = reason
			rejected++
			continue
		}
		resp.Acked = append(resp.Acked, ev.EventID)
		if dup {
			resp.Duplicates++
		} else {
			accepted++
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_sync_ingest(site_id,batch_id,accepted,duplicates,rejected) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (site_id,batch_id) DO UPDATE SET accepted=EXCLUDED.accepted, duplicates=EXCLUDED.duplicates, rejected=EXCLUDED.rejected, received_at=now()`,
		siteID, b.BatchID, accepted, resp.Duplicates, rejected); err != nil {
		return resp, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET last_edge_seen_at=now(), last_sync_at=now(), edge_version=$2 WHERE id=$1`, siteID, b.EdgeVersion); err != nil {
		return resp, err
	}
	return resp, tx.Commit()
}

func applyState(ctx context.Context, tx *sql.Tx, siteID string, st *sc.State) error {
	obs, err := parseT(st.ObservedAt)
	if err != nil {
		return fmt.Errorf("observed_at: %w", err)
	}
	// Ignore an older snapshot arriving after a newer one (retry reordering).
	whole, _ := json.Marshal(st)
	res, err := tx.ExecContext(ctx, `INSERT INTO latest_site_status(site_id,state,observed_at,received_at) VALUES ($1,$2,$3,now())
		ON CONFLICT (site_id) DO UPDATE SET state=EXCLUDED.state, observed_at=EXCLUDED.observed_at, received_at=now() WHERE latest_site_status.observed_at <= EXCLUDED.observed_at`,
		siteID, whole, obs)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // stale snapshot: keep the newer one
	}
	if st.SiteName != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET name=$2 WHERE id=$1 AND name<>$2`, siteID, st.SiteName); err != nil {
			return err
		}
	}
	seen := make([]string, 0, len(st.Units))
	for _, raw := range st.Units {
		var h struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &h) != nil || !mc.ValidID(h.ID) {
			continue
		}
		seen = append(seen, h.ID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO units(site_id,id,name,view,updated_at) VALUES ($1,$2,$3,$4,now())
			ON CONFLICT (site_id,id) DO UPDATE SET name=EXCLUDED.name, view=EXCLUDED.view, updated_at=now()`, siteID, h.ID, h.Name, []byte(raw)); err != nil {
			return err
		}
	}
	return nil
}

// applyEvent returns (duplicate, rejectReason, error). Only infrastructure
// errors are returned as error; content problems become a reject reason.
func applyEvent(ctx context.Context, tx *sql.Tx, siteID string, ev sc.Event) (bool, string, error) {
	switch ev.EventType {
	case sc.EventTelemetry, sc.EventDeviceEvent:
		var env sc.RecordEnvelope
		if json.Unmarshal(ev.Payload, &env) != nil {
			return false, "MALFORMED_PAYLOAD", nil
		}
		eff, e1 := parseT(env.EffectiveAtUTC)
		rec, e2 := parseT(env.RecordedAt)
		if e1 != nil || e2 != nil {
			return false, "INVALID_TIMESTAMP", nil
		}
		if ev.EventType == sc.EventTelemetry {
			var t mc.Telemetry
			if mc.DecodeStrict(env.Record, &t) != nil || t.Validate() != nil {
				return false, "INVALID_TELEMETRY", nil
			}
			var sampled any
			if t.SampledAtUTC != nil {
				st, err := time.Parse(time.RFC3339, *t.SampledAtUTC)
				if err != nil {
					return false, "INVALID_TIMESTAMP", nil
				}
				sampled = st
			}
			rj, _ := json.Marshal(t.Readings)
			var unit any
			if t.UnitID != nil {
				unit = *t.UnitID
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO telemetry_snapshots(site_id,event_id,unit_id,device_id,boot_id,sequence_number,sampled_at_utc,clock_quality,effective_at,time_quality,edge_recorded_at,source,readings)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT DO NOTHING`,
				siteID, ev.EventID, unit, t.DeviceID, t.BootID, t.Seq, sampled, string(t.ClockQuality), eff, env.TimeQuality, rec, string(t.DataSource), rj)
			if err != nil {
				return false, "", err
			}
			n, _ := res.RowsAffected()
			return n == 0, "", nil
		}
		var e mc.Event
		if mc.DecodeStrict(env.Record, &e) != nil || e.Validate() != nil {
			return false, "INVALID_EVENT", nil
		}
		dj, _ := json.Marshal(e.Details)
		if e.Details == nil {
			dj = []byte("{}")
		}
		var unit any
		if e.UnitID != nil {
			unit = *e.UnitID
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO device_events(site_id,event_id,unit_id,device_id,boot_id,sequence_number,event_type,severity,code,details,effective_at,time_quality,edge_recorded_at,source)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT DO NOTHING`,
			siteID, ev.EventID, unit, e.DeviceID, e.BootID, e.Seq, string(e.EventType), string(e.Severity), e.Code, dj, eff, env.TimeQuality, rec, string(e.DataSource))
		if err != nil {
			return false, "", err
		}
		n, _ := res.RowsAffected()
		return n == 0, "", nil

	case sc.EventIncident:
		var u sc.IncidentUpdate
		if json.Unmarshal(ev.Payload, &u) != nil || u.ID == "" || u.UnitID == "" {
			return false, "MALFORMED_PAYLOAD", nil
		}
		ch, err := parseT(u.ChangedAt)
		if err != nil {
			return false, "INVALID_TIMESTAMP", nil
		}
		data, _ := json.Marshal(u)
		res, err := tx.ExecContext(ctx, `INSERT INTO incidents(site_id,id,unit_id,severity,code,status,data,changed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (site_id,id) DO UPDATE SET status=EXCLUDED.status, severity=EXCLUDED.severity, data=EXCLUDED.data, changed_at=EXCLUDED.changed_at
			WHERE incidents.changed_at < EXCLUDED.changed_at`, siteID, u.ID, u.UnitID, u.Severity, u.Code, u.Status, data, ch)
		if err != nil {
			return false, "", err
		}
		n, _ := res.RowsAffected()
		return n == 0, "", nil // 0 => an equal/newer version already stored

	case sc.EventCommandResult:
		return false, "", nil // informational; intent results travel on their own endpoint

	default:
		return false, "UNKNOWN_EVENT_TYPE", nil
	}
}
