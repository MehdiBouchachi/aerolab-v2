// Package incidents implements the incident lifecycle (spec 8.6):
//
//	ACTIVE_UNACKNOWLEDGED -> ACTIVE_ACKNOWLEDGED -> RESOLVED_ACKNOWLEDGED
//	ACTIVE_UNACKNOWLEDGED -> RESOLVED_UNACKNOWLEDGED -> RESOLVED_ACKNOWLEDGED
//
// Repeating conditions update ONE active row (enforced by a partial unique
// index). Acknowledging means "a person saw it", never "it is fixed".
package incidents

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

const CodeDeviceStale = "DEVICE_STALE"

var ErrNotFound = errors.New("incident not found")

type Info struct {
	Severity mc.Severity
	Title    string
	Cause    string
	Action   string
}

// catalogue turns machine codes into plain language for growers.
var catalogue = map[string]Info{
	mc.FaultLowWater: {mc.SevCritical, "Reservoir water level is low",
		"The level input reports low water. The controller stopped this unit's pump to protect it (Protected Stop).",
		"Check and refill the reservoir, inspect for leaks, then confirm recovery at the controller. Other units keep running."},
	mc.FaultLevelSensor: {mc.SevCritical, "Water-level sensor reading is not trustworthy",
		"The essential level feedback is invalid, so the controller fails safe and keeps the pump stopped.",
		"Inspect the level sensor and its wiring. Do not bypass it."},
	mc.FaultPHInvalid: {mc.SevWarning, "pH reading is invalid",
		"The pH sensor is not delivering a valid value. Irrigation continues in degraded mode.",
		"Inspect or recalibrate the pH probe and measure pH manually until it is restored."},
	mc.FaultECInvalid: {mc.SevWarning, "EC reading is invalid",
		"The EC sensor is not delivering a valid value. Irrigation continues in degraded mode.",
		"Inspect or recalibrate the EC probe and measure EC manually until it is restored."},
	CodeDeviceStale: {mc.SevWarning, "Controller is not reporting",
		"No recent status has been received from this controller. Its last known state is shown as outdated. It may still be irrigating on its own saved programme - this does not prove the pump stopped.",
		"Check the controller's power and Wi-Fi. Inspect the unit physically if the outage continues."},
}

// Describe returns plain-language text for a code (generic for unknown codes).
func Describe(code string) Info {
	if i, ok := catalogue[code]; ok {
		return i
	}
	return Info{mc.SevWarning, "Controller reported " + code, "The controller reported a condition this version does not describe.", "Inspect the unit and review its recent events."}
}

type Incident struct {
	ID              string      `json:"id"`
	SiteID          string      `json:"site_id"`
	UnitID          string      `json:"unit_id"`
	Severity        mc.Severity `json:"severity"`
	Code            string      `json:"code"`
	Status          string      `json:"status"`
	Title           string      `json:"title"`
	Cause           string      `json:"cause"`
	SuggestedAction string      `json:"suggested_action"`
	FirstSeenAt     string      `json:"first_seen_at"`
	LastSeenAt      string      `json:"last_seen_at"`
	Occurrences     int         `json:"occurrences"`
	AcknowledgedBy  *string     `json:"acknowledged_by"`
	AcknowledgedAt  *string     `json:"acknowledged_at"`
	ResolvedAt      *string     `json:"resolved_at"`
}

const cols = `id,site_id,unit_id,severity,code,status,first_seen_at,last_seen_at,occurrences,acknowledged_by,acknowledged_at,resolved_at`

type scanner interface{ Scan(...any) error }

func scan(r scanner) (Incident, error) {
	var i Incident
	var sev string
	var ab, aa, ra sql.NullString
	if err := r.Scan(&i.ID, &i.SiteID, &i.UnitID, &sev, &i.Code, &i.Status, &i.FirstSeenAt, &i.LastSeenAt, &i.Occurrences, &ab, &aa, &ra); err != nil {
		return i, err
	}
	i.Severity = mc.Severity(sev)
	if ab.Valid {
		i.AcknowledgedBy = &ab.String
	}
	if aa.Valid {
		i.AcknowledgedAt = &aa.String
	}
	if ra.Valid {
		i.ResolvedAt = &ra.String
	}
	d := Describe(i.Code)
	i.Title, i.Cause, i.SuggestedAction = d.Title, d.Cause, d.Action
	return i, nil
}

// Change is a lifecycle transition worth telling clients about.
type Change struct{ Incident Incident }

// SetCondition opens (or touches) the active incident for (unit, code), or
// resolves it. It is idempotent and safe to call repeatedly.
func SetCondition(ctx context.Context, tx *sql.Tx, siteID, unitID, code string, active bool, now string) (*Change, error) {
	var id, status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM incidents WHERE unit_id=? AND code=? AND status IN ('ACTIVE_UNACKNOWLEDGED','ACTIVE_ACKNOWLEDGED')`, unitID, code).Scan(&id, &status)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	switch {
	case active && found:
		if _, err := tx.ExecContext(ctx, `UPDATE incidents SET last_seen_at=?, occurrences=occurrences+1 WHERE id=?`, now, id); err != nil {
			return nil, err
		}
		return nil, nil // unchanged lifecycle: no client notification
	case active && !found:
		id = auth.NewID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO incidents(id,site_id,unit_id,severity,code,status,first_seen_at,last_seen_at) VALUES (?,?,?,?,?,'ACTIVE_UNACKNOWLEDGED',?,?)`,
			id, siteID, unitID, string(Describe(code).Severity), code, now, now); err != nil {
			return nil, err
		}
	case !active && found:
		next := "RESOLVED_UNACKNOWLEDGED"
		if status == "ACTIVE_ACKNOWLEDGED" {
			next = "RESOLVED_ACKNOWLEDGED"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE incidents SET status=?, resolved_at=?, last_seen_at=? WHERE id=?`, next, now, now, id); err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}
	inc, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM incidents WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	return &Change{inc}, nil
}

// Reconcile aligns the unit's device-reported incidents with the heartbeat's
// current fault set (the device is authoritative). DEVICE_STALE is edge-owned
// and untouched here.
func Reconcile(ctx context.Context, tx *sql.Tx, siteID, unitID string, faults []string, now string) ([]Change, error) {
	set := map[string]bool{}
	for _, f := range faults {
		if f == mc.FaultNoProgram {
			continue // a setup state, not an incident
		}
		set[f] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT code FROM incidents WHERE unit_id=? AND status IN ('ACTIVE_UNACKNOWLEDGED','ACTIVE_ACKNOWLEDGED') AND code<>?`, unitID, CodeDeviceStale)
	if err != nil {
		return nil, err
	}
	var open []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		open = append(open, c)
	}
	rows.Close()
	codes := map[string]bool{}
	for c := range set {
		codes[c] = true
	}
	for _, c := range open {
		codes[c] = true
	}
	ordered := make([]string, 0, len(codes))
	for c := range codes {
		ordered = append(ordered, c)
	}
	sort.Strings(ordered)
	var out []Change
	for _, c := range ordered {
		ch, err := SetCondition(ctx, tx, siteID, unitID, c, set[c], now)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			out = append(out, *ch)
		}
	}
	return out, nil
}

// Acknowledge marks an incident as seen by a person. It never resolves it.
func Acknowledge(ctx context.Context, tx *sql.Tx, siteID, id, userID string) (Incident, bool, error) {
	cur, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM incidents WHERE id=? AND site_id=?`, id, siteID))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, false, ErrNotFound
	}
	if err != nil {
		return Incident{}, false, err
	}
	var next string
	switch cur.Status {
	case "ACTIVE_UNACKNOWLEDGED":
		next = "ACTIVE_ACKNOWLEDGED"
	case "RESOLVED_UNACKNOWLEDGED":
		next = "RESOLVED_ACKNOWLEDGED"
	default:
		return cur, false, nil // already acknowledged: idempotent
	}
	now := storage.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE incidents SET status=?, acknowledged_by=?, acknowledged_at=? WHERE id=?`, next, userID, now, id); err != nil {
		return Incident{}, false, err
	}
	if err := auth.Audit(ctx, tx, siteID, userID, "local", "incident.acknowledge", "incident", id, `{"status":"`+cur.Status+`"}`, `{"status":"`+next+`"}`); err != nil {
		return Incident{}, false, err
	}
	cur.Status, cur.AcknowledgedBy, cur.AcknowledgedAt = next, &userID, &now
	return cur, true, nil
}

type Filter struct {
	Active   *bool
	Severity string
	UnitID   string
	Limit    int
	Offset   int
}

func List(ctx context.Context, db *sql.DB, siteID string, f Filter) ([]Incident, error) {
	q := `SELECT ` + cols + ` FROM incidents WHERE site_id=?`
	args := []any{siteID}
	if f.Active != nil {
		if *f.Active {
			q += ` AND status LIKE 'ACTIVE%'`
		} else {
			q += ` AND status LIKE 'RESOLVED%'`
		}
	}
	if f.Severity != "" {
		q += ` AND severity=?`
		args = append(args, f.Severity)
	}
	if f.UnitID != "" {
		q += ` AND unit_id=?`
		args = append(args, f.UnitID)
	}
	q += ` ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, first_seen_at DESC LIMIT ? OFFSET ?`
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func Get(ctx context.Context, db *sql.DB, siteID, id string) (Incident, error) {
	i, err := scan(db.QueryRowContext(ctx, `SELECT `+cols+` FROM incidents WHERE id=? AND site_id=?`, id, siteID))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

// CountActive returns active incident counts by severity.
func CountActive(ctx context.Context, db *sql.DB, siteID string) (critical, warning, total int, err error) {
	rows, err := db.QueryContext(ctx, `SELECT severity, COUNT(*) FROM incidents WHERE site_id=? AND status LIKE 'ACTIVE%' GROUP BY severity`, siteID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if err = rows.Scan(&s, &n); err != nil {
			return
		}
		total += n
		switch s {
		case "critical":
			critical = n
		case "warning":
			warning = n
		}
	}
	err = rows.Err()
	return
}
