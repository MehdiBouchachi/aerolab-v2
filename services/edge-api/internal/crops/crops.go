// Package crops holds crop profiles: per-crop reading ranges (and an optional irrigation programme)
// that a grower assigns to growing units, so a tomato unit and a lettuce unit on the same Pi are
// watched against different numbers.
//
// Honesty rules (spec + CLAUDE.md): this package contains NO crop values. Every number is typed in
// by the grower or their agronomist, and value_source records who supplied them. Range alerts are
// advisory incidents on the Pi; they never actuate anything. The controller's own safety limits (and
// the programme validation in contracts/mqtt) are unchanged by a profile.
package crops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

var (
	ErrNotFound = errors.New("crop profile not found")
	ErrConflict = errors.New("crop profile was changed by someone else; reload it")
	ErrInUse    = errors.New("crop profile is assigned to units; assign them another profile first")
	ErrName     = errors.New("a crop profile with this name already exists")
)

// Range bounds one reading. Either side may be absent. Values are in the unit the controller reports.
type Range struct {
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

type Profile struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Crop        string           `json:"crop"`
	Variety     string           `json:"variety,omitempty"`
	Notes       string           `json:"notes,omitempty"`
	ValueSource string           `json:"value_source"`
	Thresholds  map[string]Range `json:"thresholds"`
	Program     *mc.Program      `json:"program,omitempty"`
	Version     int              `json:"version"`
	CreatedBy   string           `json:"created_by"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	UnitCount   int              `json:"unit_count"`
}

const maxThresholds = 32

// Validate checks shape only. It deliberately does not judge whether a number is agronomically right.
func (p *Profile) Validate() error {
	p.Name, p.Crop = strings.TrimSpace(p.Name), strings.TrimSpace(p.Crop)
	p.Variety, p.ValueSource = strings.TrimSpace(p.Variety), strings.TrimSpace(p.ValueSource)
	switch {
	case p.Name == "" || len(p.Name) > 80:
		return fieldErr("name", "required, at most 80 characters")
	case p.Crop == "" || len(p.Crop) > 80:
		return fieldErr("crop", "required, at most 80 characters (e.g. what is planted)")
	case len(p.Variety) > 80:
		return fieldErr("variety", "at most 80 characters")
	case len(p.Notes) > 2000:
		return fieldErr("notes", "at most 2000 characters")
	case p.ValueSource == "" || len(p.ValueSource) > 200:
		return fieldErr("value_source", "say who supplied these numbers (agronomist, supplier sheet, own trials); at most 200 characters")
	case len(p.Thresholds) > maxThresholds:
		return fieldErr("thresholds", fmt.Sprintf("at most %d readings", maxThresholds))
	}
	if p.Thresholds == nil {
		p.Thresholds = map[string]Range{}
	}
	for name, r := range p.Thresholds {
		f := "thresholds." + name
		if !mc.ValidReadingName(name) {
			return fieldErr(f, "reading names are lowercase letters, digits and _ (as the controller reports them)")
		}
		if r.Min == nil && r.Max == nil {
			return fieldErr(f, "set a min, a max, or both")
		}
		for _, v := range []*float64{r.Min, r.Max} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || math.Abs(*v) > 1e9) {
				return fieldErr(f, "not a finite number")
			}
		}
		if r.Min != nil && r.Max != nil && *r.Min >= *r.Max {
			return fieldErr(f, "min must be below max")
		}
	}
	if p.Program != nil {
		if err := p.Program.Validate(); err != nil {
			var ve *mc.ValidationError
			if errors.As(err, &ve) {
				return fieldErr(ve.Field, err.Error())
			}
			return err
		}
	}
	return nil
}

// FieldError carries the offending field for the API's VALIDATION_FAILED details.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }
func fieldErr(f, m string) error    { return &FieldError{f, m} }

const cols = `p.id,p.name,p.crop,COALESCE(p.variety,''),COALESCE(p.notes,''),p.value_source,p.thresholds_json,COALESCE(p.program_json,''),p.version,p.created_by,p.created_at,p.updated_at,
	(SELECT COUNT(*) FROM growing_units g WHERE g.crop_profile_id=p.id AND g.retired_at IS NULL)`

type scanner interface{ Scan(...any) error }

func scan(r scanner) (Profile, error) {
	var p Profile
	var th, prog string
	if err := r.Scan(&p.ID, &p.Name, &p.Crop, &p.Variety, &p.Notes, &p.ValueSource, &th, &prog, &p.Version, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &p.UnitCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	if err := json.Unmarshal([]byte(th), &p.Thresholds); err != nil {
		return p, err
	}
	if prog != "" {
		p.Program = &mc.Program{}
		if err := json.Unmarshal([]byte(prog), p.Program); err != nil {
			return p, err
		}
	}
	return p, nil
}

func List(ctx context.Context, db *sql.DB, siteID string) ([]Profile, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+cols+` FROM crop_profiles p WHERE p.site_id=? AND p.archived_at IS NULL ORDER BY p.crop, p.name`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func Get(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, siteID, id string) (Profile, error) {
	return scan(q.QueryRowContext(ctx, `SELECT `+cols+` FROM crop_profiles p WHERE p.site_id=? AND p.id=? AND p.archived_at IS NULL`, siteID, id))
}

func encode(p *Profile) (th string, prog any) {
	b, _ := json.Marshal(p.Thresholds)
	th = string(b)
	if p.Program != nil {
		pb, _ := json.Marshal(p.Program)
		prog = string(pb)
	}
	return
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

// Create stores a new profile (Admin, audited).
func Create(ctx context.Context, tx *sql.Tx, siteID string, p Profile, actor string) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	now := storage.Now()
	p.ID, p.Version, p.CreatedBy, p.CreatedAt, p.UpdatedAt = "crop-"+auth.NewID(), 1, actor, now, now
	th, prog := encode(&p)
	if _, err := tx.ExecContext(ctx, `INSERT INTO crop_profiles(id,site_id,name,crop,variety,notes,value_source,thresholds_json,program_json,version,created_by,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,1,?,?,?)`, p.ID, siteID, p.Name, p.Crop, nullStr(p.Variety), nullStr(p.Notes), p.ValueSource, th, prog, actor, now, now); err != nil {
		if isUnique(err) {
			return Profile{}, ErrName
		}
		return Profile{}, err
	}
	if err := auth.Audit(ctx, tx, siteID, actor, "local", "crop_profile.create", "crop_profile", p.ID, "", th); err != nil {
		return Profile{}, err
	}
	return Get(ctx, tx, siteID, p.ID)
}

// Update replaces a profile if expectedVersion still matches (optimistic concurrency: two people
// editing the same profile must not silently overwrite each other).
func Update(ctx context.Context, tx *sql.Tx, siteID, id string, expectedVersion int, p Profile, actor string) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	prev, err := Get(ctx, tx, siteID, id)
	if err != nil {
		return Profile{}, err
	}
	if prev.Version != expectedVersion {
		return Profile{}, ErrConflict
	}
	th, prog := encode(&p)
	if _, err := tx.ExecContext(ctx, `UPDATE crop_profiles SET name=?,crop=?,variety=?,notes=?,value_source=?,thresholds_json=?,program_json=?,version=version+1,updated_at=? WHERE id=? AND version=?`,
		p.Name, p.Crop, nullStr(p.Variety), nullStr(p.Notes), p.ValueSource, th, prog, storage.Now(), id, expectedVersion); err != nil {
		if isUnique(err) {
			return Profile{}, ErrName
		}
		return Profile{}, err
	}
	pj, _ := json.Marshal(prev.Thresholds)
	if err := auth.Audit(ctx, tx, siteID, actor, "local", "crop_profile.update", "crop_profile", id, string(pj), th); err != nil {
		return Profile{}, err
	}
	return Get(ctx, tx, siteID, id)
}

// Archive hides a profile that no unit uses any more. History keeps its id.
func Archive(ctx context.Context, tx *sql.Tx, siteID, id, actor string) error {
	p, err := Get(ctx, tx, siteID, id)
	if err != nil {
		return err
	}
	if p.UnitCount > 0 {
		return ErrInUse
	}
	if _, err := tx.ExecContext(ctx, `UPDATE crop_profiles SET archived_at=? WHERE id=?`, storage.Now(), id); err != nil {
		return err
	}
	return auth.Audit(ctx, tx, siteID, actor, "local", "crop_profile.archive", "crop_profile", id, "", "")
}

// Assign sets (profileID != "") or clears a unit's profile.
func Assign(ctx context.Context, tx *sql.Tx, siteID, unitID, profileID, actor string) error {
	var prev string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(crop_profile_id,'') FROM growing_units WHERE id=? AND site_id=? AND COALESCE(status,'')<>'RETIRED'`, unitID, siteID).Scan(&prev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("unit: %w", ErrNotFound)
		}
		return err
	}
	if profileID != "" {
		if _, err := Get(ctx, tx, siteID, profileID); err != nil {
			return err
		}
	}
	if prev == profileID {
		return nil
	}
	var assignedAt any
	if profileID != "" {
		assignedAt = storage.Now()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE growing_units SET crop_profile_id=?, crop_assigned_at=? WHERE id=?`, nullStr(profileID), assignedAt, unitID); err != nil {
		return err
	}
	return auth.Audit(ctx, tx, siteID, actor, "local", "unit.crop_assign", "unit", unitID, fmt.Sprintf(`{"crop_profile_id":%q}`, prev), fmt.Sprintf(`{"crop_profile_id":%q}`, profileID))
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ------------------------------------------------------------------ range alerts

// Incident codes owned by the edge (never reconciled away by a controller heartbeat).
const (
	CodeHighPrefix = "RANGE_HIGH:"
	CodeLowPrefix  = "RANGE_LOW:"
)

// IsRangeCode reports whether an incident code is a crop-profile range alert.
func IsRangeCode(code string) bool {
	return strings.HasPrefix(code, CodeHighPrefix) || strings.HasPrefix(code, CodeLowPrefix)
}

// Debounce is the number of consecutive samples needed to open or close a range alert, so one noisy
// sample does not page anyone and a value sitting on the boundary does not flap.
const Debounce = 3

// Transition asks the caller to open (Active) or resolve a range incident.
type Transition struct {
	UnitID, Code string
	Active       bool
}

type key struct{ unit, code string }

// Evaluator holds each unit's ranges in memory (bounded by units x readings) and debounces.
type Evaluator struct {
	mu     sync.Mutex
	ranges map[string]map[string]Range // unit -> reading -> range
	open   map[key]bool                // range incidents currently open
	streak map[key]int                 // consecutive samples disagreeing with open[k]
}

func NewEvaluator() *Evaluator {
	return &Evaluator{ranges: map[string]map[string]Range{}, open: map[key]bool{}, streak: map[key]int{}}
}

// Load (re)reads every unit's ranges and the currently open range incidents. It returns resolutions
// for open range incidents whose bound no longer exists (profile edited or unassigned), so the caller
// can close them in the same place it handles all other transitions.
func (e *Evaluator) Load(ctx context.Context, db *sql.DB, siteID string) ([]Transition, error) {
	rows, err := db.QueryContext(ctx, `SELECT g.id, p.thresholds_json FROM growing_units g JOIN crop_profiles p ON p.id=g.crop_profile_id
		WHERE g.site_id=? AND g.retired_at IS NULL`, siteID)
	if err != nil {
		return nil, err
	}
	ranges := map[string]map[string]Range{}
	for rows.Next() {
		var u, th string
		if err := rows.Scan(&u, &th); err != nil {
			rows.Close()
			return nil, err
		}
		m := map[string]Range{}
		if err := json.Unmarshal([]byte(th), &m); err != nil {
			rows.Close()
			return nil, err
		}
		ranges[u] = m
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT unit_id, code FROM incidents WHERE site_id=? AND status IN ('ACTIVE_UNACKNOWLEDGED','ACTIVE_ACKNOWLEDGED') AND (code LIKE 'RANGE_HIGH:%' OR code LIKE 'RANGE_LOW:%')`, siteID)
	if err != nil {
		return nil, err
	}
	open := map[key]bool{}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.unit, &k.code); err != nil {
			rows.Close()
			return nil, err
		}
		open[k] = true
	}
	rows.Close()

	var out []Transition
	for k := range open {
		if !boundExists(ranges[k.unit], k.code) {
			out = append(out, Transition{k.unit, k.code, false})
			delete(open, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UnitID+out[i].Code < out[j].UnitID+out[j].Code })
	e.mu.Lock()
	e.ranges, e.open, e.streak = ranges, open, map[key]int{}
	e.mu.Unlock()
	return out, nil
}

func boundExists(r map[string]Range, code string) bool {
	if name, ok := strings.CutPrefix(code, CodeHighPrefix); ok {
		return r[name].Max != nil
	}
	if name, ok := strings.CutPrefix(code, CodeLowPrefix); ok {
		return r[name].Min != nil
	}
	return false
}

// Evaluate checks one telemetry snapshot. Only VALID numeric readings count: a missing or invalid
// sensor is reported by the controller's own fault codes, and is never treated as in or out of range.
func (e *Evaluator) Evaluate(unitID string, readings map[string]mc.Reading) []Transition {
	e.mu.Lock()
	defer e.mu.Unlock()
	rs := e.ranges[unitID]
	if len(rs) == 0 {
		return nil
	}
	var out []Transition
	names := make([]string, 0, len(rs))
	for n := range rs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		rd, ok := readings[name]
		if !ok || rd.Quality != mc.QualityValid {
			continue
		}
		v, ok := rd.Value.(float64)
		if !ok {
			continue
		}
		r := rs[name]
		if r.Max != nil {
			out = e.step(out, key{unitID, CodeHighPrefix + name}, v > *r.Max)
		}
		if r.Min != nil {
			out = e.step(out, key{unitID, CodeLowPrefix + name}, v < *r.Min)
		}
	}
	return out
}

func (e *Evaluator) step(out []Transition, k key, outside bool) []Transition {
	if outside == e.open[k] {
		delete(e.streak, k)
		return out
	}
	e.streak[k]++
	if e.streak[k] < Debounce {
		return out
	}
	delete(e.streak, k)
	if outside {
		e.open[k] = true
	} else {
		delete(e.open, k)
	}
	return append(out, Transition{k.unit, k.code, outside})
}
