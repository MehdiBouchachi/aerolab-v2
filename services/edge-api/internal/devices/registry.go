// Package devices holds the device/unit registry: durable identity in SQLite
// and the bounded in-memory "latest state" per registered device. Only
// registered devices get a runtime slot, so a flood of unknown device ids
// cannot grow memory.
package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

var (
	ErrExists   = errors.New("device or unit already exists")
	ErrNotFound = errors.New("not found")
)

// Connection is the controller-link state (distinct from control state).
type Connection string

const (
	ConnNeverSeen Connection = "NEVER_SEEN"
	ConnOnline    Connection = "ONLINE"
	ConnStale     Connection = "STALE"
	ConnOffline   Connection = "OFFLINE" // broker delivered the last will
)

type Device struct {
	ID, SiteID, UnitID, DataSource, Hardware string
	Disabled                                 bool
}

type Unit struct {
	ID, SiteID, DeviceID, Name string
	CreatedAt                  string
}

// Runtime is the volatile view of one device.
type Runtime struct {
	Device
	Seen            bool
	State           mc.State
	StateAt         time.Time
	PresenceOffline bool
	WasStale        bool
	lastFaults      string
	lastControl     mc.ControlState
	lastPublish     time.Time
	lastTouch       time.Time
	BootID          string
}

type Registry struct {
	mu     sync.RWMutex
	byDev  map[string]*Runtime
	byUnit map[string]string // unit -> device
	db     *storage.Store
	siteID string
}

func NewRegistry(db *storage.Store, siteID string) *Registry {
	return &Registry{byDev: map[string]*Runtime{}, byUnit: map[string]string{}, db: db, siteID: siteID}
}

// Load reads provisioned devices from SQLite.
func (r *Registry) Load(ctx context.Context) error {
	rows, err := r.db.R.QueryContext(ctx, `SELECT id,site_id,COALESCE(unit_id,''),data_source,COALESCE(hardware_profile,''),disabled_at IS NOT NULL FROM devices WHERE site_id=?`, r.siteID)
	if err != nil {
		return err
	}
	defer rows.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.SiteID, &d.UnitID, &d.DataSource, &d.Hardware, &d.Disabled); err != nil {
			return err
		}
		r.byDev[d.ID] = &Runtime{Device: d}
		if d.UnitID != "" {
			r.byUnit[d.UnitID] = d.ID
		}
	}
	return rows.Err()
}

// EnsureSite creates the (single) site row for this Pi.
func EnsureSite(ctx context.Context, db *storage.Store, id, name string, simulated bool) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		s := 0
		if simulated {
			s = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO sites(id,name,simulated,created_at) VALUES (?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name`, id, name, s, storage.Now())
		return err
	})
}

// Provision registers a device and its growing unit (Admin, audited).
func (r *Registry) Provision(ctx context.Context, deviceID, unitID, unitName, hardware, dataSource, actor string) (Device, Unit, error) {
	if !mc.ValidID(deviceID) || !mc.ValidID(unitID) {
		return Device{}, Unit{}, fmt.Errorf("invalid device or unit id")
	}
	if dataSource != string(mc.SourceDevice) && dataSource != string(mc.SourceSimulator) {
		return Device{}, Unit{}, fmt.Errorf("data_source must be DEVICE or SIMULATOR")
	}
	if len(unitName) == 0 || len(unitName) > 80 {
		return Device{}, Unit{}, fmt.Errorf("unit name required (max 80)")
	}
	now := storage.Now()
	d := Device{ID: deviceID, SiteID: r.siteID, UnitID: unitID, DataSource: dataSource, Hardware: hardware}
	u := Unit{ID: unitID, SiteID: r.siteID, DeviceID: deviceID, Name: unitName, CreatedAt: now}
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id,site_id,unit_id,hardware_profile,data_source,provisioned_at) VALUES (?,?,?,?,?,?)`,
			deviceID, r.siteID, unitID, nullStr(hardware), dataSource, now); err != nil {
			return mapUnique(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO growing_units(id,site_id,device_id,name,created_at) VALUES (?,?,?,?,?)`, unitID, r.siteID, deviceID, unitName, now); err != nil {
			return mapUnique(err)
		}
		return auth.Audit(ctx, tx, r.siteID, actor, "local", "device.provision", "device", deviceID, "", fmt.Sprintf(`{"unit_id":%q,"data_source":%q}`, unitID, dataSource))
	})
	if err != nil {
		return Device{}, Unit{}, err
	}
	r.mu.Lock()
	r.byDev[deviceID] = &Runtime{Device: d}
	r.byUnit[unitID] = deviceID
	r.mu.Unlock()
	return d, u, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mapUnique(err error) error {
	if err != nil && (strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY KEY")) {
		return ErrExists
	}
	return err
}

// Lookup returns a copy of the runtime for a registered device.
func (r *Registry) Lookup(deviceID string) (Runtime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.byDev[deviceID]
	if !ok {
		return Runtime{}, false
	}
	return *rt, true
}

func (r *Registry) ByUnit(unitID string) (Runtime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dev, ok := r.byUnit[unitID]
	if !ok {
		return Runtime{}, false
	}
	return *r.byDev[dev], true
}

func (r *Registry) All() []Runtime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Runtime, 0, len(r.byDev))
	for _, rt := range r.byDev {
		out = append(out, *rt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Count() int { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.byDev) }

// Connection derives link state from freshness - a retained "ONLINE" is not
// proof of liveness (spec 9.2).
func (rt Runtime) Connection(now time.Time, staleAfter time.Duration) Connection {
	switch {
	case !rt.Seen:
		return ConnNeverSeen
	case now.Sub(rt.StateAt) > staleAfter:
		if rt.PresenceOffline {
			return ConnOffline
		}
		return ConnStale
	default:
		return ConnOnline
	}
}

// StateChange tells the caller what a heartbeat changed so that only
// meaningful transitions reach SQLite / SSE.
type StateChange struct {
	First, FaultsChanged, ControlChanged, Recovered bool
	TouchIncidents, PublishSSE                      bool
	Faults                                          []string
	UnitID, BootChanged                             string
}

// ApplyState stores the latest heartbeat in memory. at is receive time.
func (r *Registry) ApplyState(deviceID string, s mc.State, at time.Time) (StateChange, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.byDev[deviceID]
	if !ok || rt.Disabled {
		return StateChange{}, false
	}
	ch := StateChange{UnitID: rt.UnitID, First: !rt.Seen}
	fk := faultKey(s.FaultCodes)
	ch.FaultsChanged = !rt.Seen || fk != rt.lastFaults
	ch.ControlChanged = !rt.Seen || s.ControlState != rt.lastControl || (rt.State.ProgramVersionApplied == nil) != (s.ProgramVersionApplied == nil) ||
		(s.ProgramVersionApplied != nil && rt.State.ProgramVersionApplied != nil && *s.ProgramVersionApplied != *rt.State.ProgramVersionApplied)
	ch.Recovered = rt.WasStale || rt.PresenceOffline
	ch.Faults = append([]string(nil), s.FaultCodes...)
	if len(s.FaultCodes) > 0 && at.Sub(rt.lastTouch) >= 60*time.Second {
		ch.TouchIncidents = true
		rt.lastTouch = at
	}
	// coalesce SSE: transitions immediately, otherwise at most every 5 s
	if ch.First || ch.FaultsChanged || ch.ControlChanged || ch.Recovered || s.PumpCommanded != rt.State.PumpCommanded || at.Sub(rt.lastPublish) >= 5*time.Second {
		ch.PublishSSE = true
		rt.lastPublish = at
	}
	rt.Seen, rt.State, rt.StateAt, rt.WasStale, rt.PresenceOffline = true, s, at, false, false
	rt.lastFaults, rt.lastControl, rt.BootID = fk, s.ControlState, s.BootID
	return ch, true
}

func faultKey(c []string) string {
	cp := append([]string(nil), c...)
	sort.Strings(cp)
	k := ""
	for _, x := range cp {
		k += x + ","
	}
	return k
}

// SetPresence records a birth/LWT message. Presence never claims "healthy";
// OFFLINE just short-circuits the stale wait.
func (r *Registry) SetPresence(deviceID string, offline bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.byDev[deviceID]
	if !ok || rt.Disabled {
		return false
	}
	rt.PresenceOffline = offline
	return true
}

// StaleTransitions flips devices whose heartbeat is overdue (or whose LWT
// arrived) into STALE and returns them once.
func (r *Registry) StaleTransitions(now time.Time, staleAfter time.Duration) []Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Runtime
	for _, rt := range r.byDev {
		if rt.Seen && !rt.WasStale && (now.Sub(rt.StateAt) > staleAfter || rt.PresenceOffline) {
			rt.WasStale = true
			out = append(out, *rt)
		}
	}
	return out
}
