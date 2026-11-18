package app

// Connecting this Pi to a cloud at runtime ("Connect to cloud" in the local dashboard): the Pi trades
// a one-time claim code for its own edge token, stores it in its database, and starts syncing without
// a restart and without anyone editing environment files. Local operation never depends on it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/httpapi"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	edgesync "github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sync"
)

var errEnvManaged = errors.New("this Pi's cloud connection is set by its installer (AEROLAB_CLOUD_* settings); change it there")

// startSync wires a worker for (url, token) and turns every outbox writer on. Caller holds a.linkMu.
func (a *App) startSync(url, token string) {
	w := edgesync.NewWorker(edgesync.Options{SiteID: a.Cfg.SiteID, URL: url, Token: token, Interval: a.Cfg.CloudSyncInterval,
		EdgeVersion: "edge-" + Version, RemotePrograms: a.Cfg.RemotePrograms}, a.DB, a.Cmds, nil, a.Log)
	w.SetStateFunc(a.HTTP.Snapshot)
	if a.Cfg.RemoteManagement {
		w.SetRelay(a.HTTP.ServeRelay)
	}
	a.Writer.SetSyncOutbox(true)
	a.Ingest.SetSyncOutbox(true)
	a.Cmds.SetSyncOutbox(true)
	a.Sync = w
	a.HTTP.SetCloud(w)
	if a.spawn != nil { // already running: start it now (otherwise Run starts it)
		ctx, cancel := context.WithCancel(a.wctx)
		a.syncCancel = cancel
		a.spawn(func(context.Context) { w.Run(ctx) })
	}
}

func (a *App) stopSync() {
	if a.syncCancel != nil {
		a.syncCancel()
		a.syncCancel = nil
	}
	a.Writer.SetSyncOutbox(false)
	a.Ingest.SetSyncOutbox(false)
	a.Cmds.SetSyncOutbox(false)
	a.HTTP.SetCloud(nil)
	a.Sync = nil
}

// loadStoredLink enables sync from a link made earlier in the dashboard (env configuration wins).
func (a *App) loadStoredLink(ctx context.Context) error {
	var url, token string
	err := a.DB.R.QueryRowContext(ctx, `SELECT url, token FROM cloud_link WHERE id=1`).Scan(&url, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := edgesync.ValidateCloudURL(url, a.Cfg.CloudAllowInsecure); err != nil {
		a.Log.Error("stored cloud link ignored", "err", err)
		return nil
	}
	a.startSync(url, token)
	return nil
}

func (a *App) CloudLinkInfo(ctx context.Context) httpapi.LinkInfo {
	if a.Cfg.CloudSyncEnabled {
		return httpapi.LinkInfo{Linked: true, URL: a.Cfg.CloudURL, FromInstaller: true}
	}
	var li httpapi.LinkInfo
	var org sql.NullString
	if err := a.DB.R.QueryRowContext(ctx, `SELECT url, COALESCE(org_name,''), linked_by, linked_at FROM cloud_link WHERE id=1`).Scan(&li.URL, &org, &li.LinkedBy, &li.LinkedAt); err == nil {
		li.Linked, li.OrgName = true, org.String
	}
	return li
}

func (a *App) LinkCloud(ctx context.Context, rawURL, code, actor string) (httpapi.LinkInfo, error) {
	if a.Cfg.CloudSyncEnabled {
		return httpapi.LinkInfo{}, errEnvManaged
	}
	url, err := edgesync.ValidateCloudURL(rawURL, a.Cfg.CloudAllowInsecure)
	if err != nil {
		return httpapi.LinkInfo{}, &edgesync.ClaimError{Status: 400, Code: "VALIDATION_FAILED", Message: err.Error()}
	}
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	if a.Sync != nil {
		return httpapi.LinkInfo{}, &edgesync.ClaimError{Status: 409, Code: "ALREADY_LINKED", Message: "this Pi is already connected to a cloud; disconnect it first"}
	}
	tok, org, err := edgesync.Claim(ctx, url, code, a.Cfg.SiteID, a.Cfg.SiteName, "edge-"+Version)
	if err != nil {
		return httpapi.LinkInfo{}, err
	}
	now := storage.Now()
	if err := a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO cloud_link(id,url,token,org_name,linked_by,linked_at) VALUES (1,?,?,?,?,?)`, url, tok, org, actor, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET cloud_linked=1 WHERE id=?`, a.Cfg.SiteID); err != nil {
			return err
		}
		return auth.Audit(ctx, tx, a.Cfg.SiteID, actor, "local", "cloud.link", "site", a.Cfg.SiteID, "", fmt.Sprintf(`{"url":%q,"organization":%q}`, url, org))
	}); err != nil {
		// the cloud already issued a token; without storing it the Pi cannot use it. Say so plainly.
		return httpapi.LinkInfo{}, fmt.Errorf("the cloud accepted the code but the Pi could not save the link (%w); create a new code and try again", err)
	}
	a.startSync(url, tok)
	a.Log.Info("connected to cloud", "url", url, "organization", org)
	return httpapi.LinkInfo{Linked: true, URL: url, OrgName: org, LinkedBy: actor, LinkedAt: now}, nil
}

// UnlinkCloud stops syncing and forgets the token. The cloud keeps what it already received; an
// organisation owner can revoke the old token there by rotating the site's edge credential.
func (a *App) UnlinkCloud(ctx context.Context, actor string) error {
	if a.Cfg.CloudSyncEnabled {
		return errEnvManaged
	}
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	if err := a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cloud_link WHERE id=1`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET cloud_linked=0 WHERE id=?`, a.Cfg.SiteID); err != nil {
			return err
		}
		return auth.Audit(ctx, tx, a.Cfg.SiteID, actor, "local", "cloud.unlink", "site", a.Cfg.SiteID, "", "")
	}); err != nil {
		return err
	}
	a.stopSync()
	return nil
}
