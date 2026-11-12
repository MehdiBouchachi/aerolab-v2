// Package app wires the edge service together. It is separate from cmd/edge
// so integration tests can run the complete service in-process.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/httpapi"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	edgesync "github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/telemetry"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/migrations"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/static"
)

type App struct {
	Cfg     *config.Config
	Log     *slog.Logger
	DB      *storage.Store
	Auth    *auth.Service
	Reg     *devices.Registry
	Hub     *sse.Hub
	Metrics *diagnostics.Metrics
	Writer  *telemetry.Writer
	Ingest  *telemetry.Ingest
	Cmds    *commands.Manager
	MQTT    *mqtt.Client
	HTTP    *httpapi.Server

	ln net.Listener
}

// New opens + migrates the database and wires every component. It does not
// block on the broker: the local UI must come up even if MQTT is down.
func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	debug.SetMemoryLimit(int64(cfg.MemSoftLimitMiB) << 20) // soft ceiling inside the 256 MiB cgroup budget
	db, err := storage.Open(cfg.DBPath, cfg.MinFreeBytes)
	if err != nil {
		return nil, err
	}
	applied, err := db.Migrate(ctx, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "versions", applied)
	}
	if err := db.IntegrityCheck(ctx); err != nil {
		return nil, err
	}
	if err := devices.EnsureSite(ctx, db, cfg.SiteID, cfg.SiteName, cfg.AllowSimulatedData); err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Log: log, DB: db, Hub: sse.NewHub(), Metrics: diagnostics.New()}
	a.Auth = auth.New(db, cfg.SiteID)
	a.Reg = devices.NewRegistry(db, cfg.SiteID)
	if err := a.Reg.Load(ctx); err != nil {
		return nil, err
	}
	if err := a.bootstrapAdmin(ctx); err != nil {
		return nil, err
	}
	if err := a.seedProvisioned(ctx); err != nil {
		return nil, err
	}

	// The MQTT handler needs Ingest, Ingest needs Writer/Cmds which need the
	// publisher (the MQTT client): break the cycle with a late-bound handler.
	var ingest *telemetry.Ingest
	a.MQTT = mqtt.New(mqtt.Options{URL: cfg.MQTTURL, User: cfg.MQTTUser, Password: cfg.MQTTPassword, SiteID: cfg.SiteID, Log: log,
		Handler: func(t string, p []byte) {
			if ingest != nil {
				ingest.HandleMessage(t, p)
			}
		},
		OnConnectionChange: func(c bool) {
			log.Info("mqtt broker connection", "connected", c)
			a.Hub.Publish("site.connectivity", map[string]any{"mqtt_broker_connected": c, "observed_at": storage.Now()})
		}})
	a.Cmds = commands.New(db, a.Reg, a.MQTT, a.Hub, commands.Options{SiteID: cfg.SiteID, StaleAfter: cfg.StaleAfter, Retry: cfg.CommandRetry,
		MaxTries: cfg.CommandMaxTries, TTL: cfg.CommandTTL, SyncOutbox: cfg.CloudSyncEnabled}, log)
	if err := a.Cmds.LoadPending(ctx); err != nil {
		return nil, err
	}
	a.Writer = telemetry.NewWriter(db, a.MQTT, a.Hub, a.Metrics, log, cfg.SiteID, cfg.RecordQueueSize, cfg.CloudSyncEnabled)
	ingest = telemetry.NewIngest(cfg.SiteID, cfg.AllowSimulatedData, cfg.StaleAfter, a.Reg, a.Writer, a.Cmds, a.Hub, a.Metrics, log)
	a.Ingest = ingest
	a.HTTP = httpapi.New(httpapi.Deps{Cfg: cfg, DB: db, Auth: a.Auth, Reg: a.Reg, Cmds: a.Cmds, Hub: a.Hub, Metrics: a.Metrics, Writer: a.Writer, MQTT: a.MQTT, Static: static.FS, Log: log})
	if a.ln, err = net.Listen("tcp", cfg.HTTPAddr); err != nil {
		return nil, err
	}
	return a, nil
}

// Addr is the bound HTTP address (useful when HTTPAddr uses port 0).
func (a *App) Addr() string { return a.ln.Addr().String() }

func (a *App) bootstrapAdmin(ctx context.Context) error {
	has, err := a.Auth.HasUsers(ctx)
	if err != nil || has {
		return err
	}
	if a.Cfg.BootstrapAdminUser == "" || a.Cfg.BootstrapAdminPassword == "" {
		a.Log.Warn("no local users exist; create the first admin with: edge create-user -role ADMIN")
		return nil
	}
	if _, err := a.Auth.CreateUser(ctx, a.Cfg.BootstrapAdminUser, a.Cfg.BootstrapAdminPassword, roles.Admin, "bootstrap"); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	a.Log.Info("bootstrap admin created; remove AEROLAB_BOOTSTRAP_ADMIN_PASSWORD from the environment", "user", a.Cfg.BootstrapAdminUser)
	return nil
}

// seedProvisioned registers devices from a dev provisioning file. Refused
// unless simulated data is explicitly allowed - real sites provision through
// the Admin API.
func (a *App) seedProvisioned(ctx context.Context) error {
	if a.Cfg.ProvisionFile == "" {
		return nil
	}
	if !a.Cfg.AllowSimulatedData {
		return errors.New("AEROLAB_PROVISION_FILE is a SIMULATOR feature; set AEROLAB_ALLOW_SIMULATED_DATA=true or unset it")
	}
	raw, err := os.ReadFile(a.Cfg.ProvisionFile)
	if err != nil {
		return err
	}
	var pf struct {
		Devices []struct {
			DeviceID string `json:"device_id"`
			UnitID   string `json:"unit_id"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &pf); err != nil {
		return err
	}
	for i, d := range pf.Devices {
		_, _, err := a.Reg.Provision(ctx, d.DeviceID, d.UnitID, fmt.Sprintf("Growing Unit %02d (SIMULATED)", i+1), "sim-board-v0", "SIMULATOR", "bootstrap")
		if err != nil && !errors.Is(err, devices.ErrExists) {
			return err
		}
	}
	return nil
}

// Run starts all workers and blocks until ctx is cancelled, then shuts down
// in order: stop accepting work -> drain the writer -> close the database.
func (a *App) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wctx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	spawn := func(f func(context.Context)) { wg.Add(1); go func() { defer wg.Done(); f(wctx) }() }

	writerCtx, stopWriter := context.WithCancel(context.Background())
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() { defer writerWG.Done(); a.Writer.Run(writerCtx) }()

	spawn(a.Cmds.Run)
	spawn(a.Ingest.Watchdog)
	spawn(a.retention)
	a.MQTT.Start()

	a.Log.Info("edge service ready", "http", a.Addr(), "site", a.Cfg.SiteID, "simulated_data_accepted", a.Cfg.AllowSimulatedData)
	err := a.HTTP.Serve(ctx, a.ln)

	a.MQTT.Stop() // no new messages
	stopWorkers()
	wg.Wait()
	stopWriter() // writer drains what is queued, then exits
	writerWG.Wait()
	if cerr := a.DB.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

// retention keeps the database bounded (spec 11.4: 30-day target) and the WAL small.
func (a *App) retention(ctx context.Context) {
	run := func() {
		cut := storage.FormatTime(time.Now().Add(-time.Duration(a.Cfg.RetentionDays) * 24 * time.Hour))
		for _, t := range []string{"telemetry_snapshots", "device_events"} {
			for { // short transactions, never one giant delete
				res, err := a.DB.W.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE received_at_utc < ? LIMIT 5000)`, t, t), cut)
				if err != nil {
					a.Log.Error("retention delete", "table", t, "err", err)
					break
				}
				if n, _ := res.RowsAffected(); n < 5000 {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
		_, _ = a.DB.W.ExecContext(ctx, `DELETE FROM local_sessions WHERE absolute_expires_at < ? OR expires_at < ?`, storage.Now(), storage.Now())
		if d, err := edgesync.Trim(ctx, a.DB.W); err != nil {
			a.Log.Error("outbox trim", "err", err)
		} else if d > 0 {
			a.Log.Warn("outbox over capacity: dropped lowest-priority rows", "dropped", d)
		}
		if err := a.DB.Checkpoint(ctx); err != nil {
			a.Log.Warn("wal checkpoint", "err", err)
		}
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
