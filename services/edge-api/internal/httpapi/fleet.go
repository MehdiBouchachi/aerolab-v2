package httpapi

// Self-service fleet management on the Pi: add a unit (the edge issues the controller's broker
// credential and returns a one-time setup code), rename or retire it, and manage crop profiles.
// See docs/architecture/fleet-management.md.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	edgesync "github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sync"
	"net"
	"net/http"
	"strings"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/broker"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/crops"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
)

func (s *Server) registerFleet() {
	s.protected("POST", "/api/v1/sites/{siteId}/units", roles.ManageDevices, s.addUnit)
	s.protected("PATCH", "/api/v1/units/{unitId}", roles.ManageDevices, s.renameUnit)
	s.protected("POST", "/api/v1/units/{unitId}/retire", roles.ManageDevices, s.retireUnit)
	s.protected("POST", "/api/v1/units/{unitId}/setup-code", roles.ManageDevices, s.newSetupCode)
	s.protected("PUT", "/api/v1/units/{unitId}/crop-profile", roles.EditProgram, s.assignCrop)

	s.protected("GET", "/api/v1/cloud-link", roles.ManageDevices, s.getCloudLink)
	s.protected("POST", "/api/v1/cloud-link", roles.ManageDevices, s.postCloudLink)
	s.protected("DELETE", "/api/v1/cloud-link", roles.ManageDevices, s.deleteCloudLink)

	s.protected("GET", "/api/v1/sites/{siteId}/crop-profiles", roles.ViewData, s.listCrops)
	s.protected("POST", "/api/v1/sites/{siteId}/crop-profiles", roles.EditProgram, s.createCrop)
	s.protected("GET", "/api/v1/crop-profiles/{profileId}", roles.ViewData, s.getCrop)
	s.protected("PUT", "/api/v1/crop-profiles/{profileId}", roles.EditProgram, s.updateCrop)
	s.protected("DELETE", "/api/v1/crop-profiles/{profileId}", roles.EditProgram, s.archiveCrop)
}

// ------------------------------------------------------------------ units

// setupBundle is what a new controller needs to join this site. The firmware's setup page accepts
// SetupCode (base64url of the JSON) so a grower can paste or scan one string.
type setupBundle struct {
	Version  int    `json:"v"`
	SiteID   string `json:"site_id"`
	DeviceID string `json:"device_id"`
	UnitID   string `json:"unit_id"`
	MQTTURI  string `json:"mqtt_uri"`
	MQTTUser string `json:"mqtt_user"`
	MQTTPass string `json:"mqtt_pass"`
}

func (s *Server) brokerURI(r *http.Request) string {
	if s.Cfg.BrokerPublicURL != "" {
		return s.Cfg.BrokerPublicURL
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.Contains(host, ":") { // bare IPv6
		host = "[" + host + "]"
	}
	return "mqtt://" + host + ":1883"
}

func (s *Server) bundle(r *http.Request, deviceID, unitID, password string) map[string]any {
	b := setupBundle{1, s.Cfg.SiteID, deviceID, unitID, s.brokerURI(r), deviceID, password}
	j, _ := json.Marshal(b)
	return map[string]any{"setup": b, "setup_code": base64.RawURLEncoding.EncodeToString(j),
		"shown_once": "This code contains the controller's password. It is not stored on the Pi and cannot be shown again; create a new one if it is lost."}
}

func (s *Server) brokerUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if !s.Broker.Enabled() {
		writeError(w, r, 409, "BROKER_NOT_MANAGED", "this Pi was installed without broker credential management (AEROLAB_BROKER_PASSWD_FILE); add controllers with the installer", nil)
		return true
	}
	return false
}

func (s *Server) syncBroker(ctx context.Context, resp map[string]any) {
	if err := s.Broker.Sync(ctx, s.DB.R); err != nil {
		s.Log.Error("broker credential sync failed", "err", err)
		resp["warning"] = "The unit is saved, but the broker has not accepted its credential yet. The Pi retries on restart; if the controller cannot connect, restart the Pi."
	}
}

func (s *Server) addUnit(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) || s.brokerUnavailable(w, r) {
		return
	}
	var in struct {
		Name            string `json:"name"`
		HardwareProfile string `json:"hardware_profile"`
		CropProfileID   string `json:"crop_profile_id"`
		DataSource      string `json:"data_source"` // SIMULATOR only on sites that accept simulated data
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	switch in.DataSource {
	case "", string(mc.SourceDevice):
		in.DataSource = string(mc.SourceDevice)
	case string(mc.SourceSimulator):
		if !s.Cfg.AllowSimulatedData {
			writeError(w, r, 400, "VALIDATION_FAILED", "simulated units are disabled on this site", map[string]any{"field": "data_source"})
			return
		}
	default:
		writeError(w, r, 400, "VALIDATION_FAILED", "data_source must be DEVICE or SIMULATOR", map[string]any{"field": "data_source"})
		return
	}
	suffix := strings.ToLower(auth.NewID())
	if len(suffix) > 10 {
		suffix = suffix[:10]
	}
	deviceID, unitID := "esp32-"+suffix, "unit-"+suffix
	pass := broker.NewPassword()
	hash, err := broker.Hash(pass)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	_, un, err := s.Reg.ProvisionWithCredential(r.Context(), deviceID, unitID, in.Name, in.HardwareProfile, in.DataSource, u.ID, hash)
	switch {
	case errors.Is(err, devices.ErrExists):
		writeError(w, r, 409, "VALIDATION_FAILED", "unit already exists, try again", nil)
		return
	case err != nil:
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), map[string]any{"field": "name"})
		return
	}
	resp := s.bundle(r, deviceID, unitID, pass)
	resp["unit"] = map[string]any{"id": un.ID, "name": un.Name, "device_id": deviceID}
	if in.CropProfileID != "" {
		if err := s.DB.Tx(r.Context(), func(tx *sql.Tx) error {
			return crops.Assign(r.Context(), tx, s.Cfg.SiteID, unitID, in.CropProfileID, u.ID)
		}); err != nil {
			resp["warning"] = "The unit was added, but the crop profile could not be assigned: " + err.Error()
		} else {
			s.reloadRanges(r.Context())
		}
	}
	s.syncBroker(r.Context(), resp)
	s.Hub.Publish("unit.added", map[string]any{"unit_id": unitID})
	writeJSON(w, 201, resp)
}

func (s *Server) newSetupCode(w http.ResponseWriter, r *http.Request, u auth.User) {
	if s.brokerUnavailable(w, r) {
		return
	}
	pass := broker.NewPassword()
	hash, err := broker.Hash(pass)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	unitID := r.PathValue("unitId")
	deviceID, err := s.Reg.SetCredential(r.Context(), unitID, hash, u.ID)
	if errors.Is(err, devices.ErrNotFound) {
		writeError(w, r, 404, "NOT_FOUND", "unit not found or retired", nil)
		return
	} else if err != nil {
		s.mapErr(w, r, err)
		return
	}
	resp := s.bundle(r, deviceID, unitID, pass)
	resp["note"] = "The previous setup code no longer works. A controller already running must be set up again with this code."
	s.syncBroker(r.Context(), resp)
	writeJSON(w, 200, resp)
}

func (s *Server) renameUnit(w http.ResponseWriter, r *http.Request, u auth.User) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := s.Reg.Rename(r.Context(), r.PathValue("unitId"), strings.TrimSpace(in.Name), u.ID)
	switch {
	case errors.Is(err, devices.ErrNotFound):
		writeError(w, r, 404, "NOT_FOUND", "unit not found", nil)
	case err != nil && strings.Contains(err.Error(), "name"):
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), map[string]any{"field": "name"})
	case err != nil:
		s.mapErr(w, r, err)
	default:
		s.Hub.Publish("unit.updated", map[string]any{"unit_id": r.PathValue("unitId")})
		w.WriteHeader(204)
	}
}

func (s *Server) retireUnit(w http.ResponseWriter, r *http.Request, u auth.User) {
	var in struct {
		ConfirmName string `json:"confirm_name"`
	}
	if !decode(w, r, &in) {
		return
	}
	unitID := r.PathValue("unitId")
	var name string
	if err := s.DB.R.QueryRowContext(r.Context(), `SELECT name FROM growing_units WHERE id=? AND site_id=? AND retired_at IS NULL`, unitID, s.Cfg.SiteID).Scan(&name); err != nil {
		writeError(w, r, 404, "NOT_FOUND", "unit not found or already retired", nil)
		return
	}
	if in.ConfirmName != name { // a typed confirmation: retiring is not undoable from the UI
		writeError(w, r, 400, "VALIDATION_FAILED", "type the unit's name to confirm", map[string]any{"field": "confirm_name"})
		return
	}
	if _, err := s.Reg.Retire(r.Context(), unitID, u.ID); err != nil {
		s.mapErr(w, r, err)
		return
	}
	resp := map[string]any{"unit_id": unitID, "status": "RETIRED"}
	if s.Broker.Enabled() {
		s.syncBroker(r.Context(), resp)
	}
	s.reloadRanges(r.Context())
	s.Hub.Publish("unit.updated", map[string]any{"unit_id": unitID})
	writeJSON(w, 200, resp)
}

// --------------------------------------------------------------- crop profiles

func (s *Server) cropErr(w http.ResponseWriter, r *http.Request, err error) {
	var fe *crops.FieldError
	switch {
	case errors.As(err, &fe):
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), map[string]any{"field": fe.Field})
	case errors.Is(err, crops.ErrNotFound):
		writeError(w, r, 404, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, crops.ErrConflict):
		writeError(w, r, 409, "VERSION_CONFLICT", err.Error(), nil)
	case errors.Is(err, crops.ErrInUse), errors.Is(err, crops.ErrName):
		writeError(w, r, 409, "VALIDATION_FAILED", err.Error(), nil)
	default:
		s.mapErr(w, r, err)
	}
}

// reloadRanges refreshes the in-memory ranges after any profile/assignment change and closes alerts
// whose bound no longer exists.
func (s *Server) reloadRanges(ctx context.Context) {
	if s.Ranges == nil {
		return
	}
	ts, err := s.Ranges.Load(ctx, s.DB.R, s.Cfg.SiteID)
	if err != nil {
		s.Log.Error("reload crop ranges", "err", err)
		return
	}
	if len(ts) > 0 && s.Writer != nil {
		s.Writer.Do(s.Writer.RangeOp(ts))
	}
}

func (s *Server) listCrops(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	ps, err := crops.List(r.Context(), s.DB.R, s.Cfg.SiteID)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": ps,
		"note": "Crop values are entered by the grower or their agronomist. AeroLab ships no crop presets."})
}

func (s *Server) getCrop(w http.ResponseWriter, r *http.Request, u auth.User) {
	p, err := crops.Get(r.Context(), s.DB.R, s.Cfg.SiteID, r.PathValue("profileId"))
	if err != nil {
		s.cropErr(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) createCrop(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	var in crops.Profile
	if !decode(w, r, &in) {
		return
	}
	var out crops.Profile
	err := s.DB.Tx(r.Context(), func(tx *sql.Tx) (err error) {
		out, err = crops.Create(r.Context(), tx, s.Cfg.SiteID, in, u.ID)
		return err
	})
	if err != nil {
		s.cropErr(w, r, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) updateCrop(w http.ResponseWriter, r *http.Request, u auth.User) {
	var in struct {
		ExpectedVersion int `json:"expected_version"`
		crops.Profile
	}
	if !decode(w, r, &in) {
		return
	}
	var out crops.Profile
	err := s.DB.Tx(r.Context(), func(tx *sql.Tx) (err error) {
		out, err = crops.Update(r.Context(), tx, s.Cfg.SiteID, r.PathValue("profileId"), in.ExpectedVersion, in.Profile, u.ID)
		return err
	})
	if err != nil {
		s.cropErr(w, r, err)
		return
	}
	s.reloadRanges(r.Context())
	s.Hub.Publish("crop_profile.updated", map[string]any{"id": out.ID})
	writeJSON(w, 200, out)
}

func (s *Server) archiveCrop(w http.ResponseWriter, r *http.Request, u auth.User) {
	err := s.DB.Tx(r.Context(), func(tx *sql.Tx) error {
		return crops.Archive(r.Context(), tx, s.Cfg.SiteID, r.PathValue("profileId"), u.ID)
	})
	if err != nil {
		s.cropErr(w, r, err)
		return
	}
	w.WriteHeader(204)
}

// assignCrop sets which profile watches a unit. It does NOT send the profile's programme: that stays
// an explicit, versioned programme request (PENDING is never APPLIED), which the UI offers next.
func (s *Server) assignCrop(w http.ResponseWriter, r *http.Request, u auth.User) {
	var in struct {
		CropProfileID string `json:"crop_profile_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	unitID := r.PathValue("unitId")
	err := s.DB.Tx(r.Context(), func(tx *sql.Tx) error {
		return crops.Assign(r.Context(), tx, s.Cfg.SiteID, unitID, in.CropProfileID, u.ID)
	})
	if err != nil {
		s.cropErr(w, r, err)
		return
	}
	s.reloadRanges(r.Context())
	s.Hub.Publish("unit.updated", map[string]any{"unit_id": unitID})
	w.WriteHeader(204)
}

// --------------------------------------------------------------- cloud link

func (s *Server) noLinker(w http.ResponseWriter, r *http.Request) bool {
	if s.Linker == nil {
		writeError(w, r, 404, "NOT_FOUND", "cloud connection is not available on this Pi", nil)
		return true
	}
	return false
}

func (s *Server) getCloudLink(w http.ResponseWriter, r *http.Request, u auth.User) {
	if s.noLinker(w, r) {
		return
	}
	writeJSON(w, 200, map[string]any{"link": s.Linker.CloudLinkInfo(r.Context()), "sync": s.cloudInfo()})
}

func (s *Server) postCloudLink(w http.ResponseWriter, r *http.Request, u auth.User) {
	if s.noLinker(w, r) {
		return
	}
	var in struct {
		CloudURL string `json:"cloud_url"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Code) == "" {
		writeError(w, r, 400, "VALIDATION_FAILED", "enter the code from the cloud dashboard", map[string]any{"field": "code"})
		return
	}
	li, err := s.Linker.LinkCloud(r.Context(), in.CloudURL, in.Code, u.ID)
	var ce *edgesync.ClaimError
	switch {
	case errors.As(err, &ce):
		st := ce.Status
		if st < 400 || st >= 500 {
			st = 502
		}
		writeError(w, r, st, ce.Code, ce.Message, nil)
	case err != nil && strings.Contains(err.Error(), "installer"):
		writeError(w, r, 409, "CONFIGURED_BY_INSTALLER", err.Error(), nil)
	case err != nil:
		s.Log.Warn("cloud link failed", "err", err)
		writeError(w, r, 502, "CLOUD_UNREACHABLE", err.Error(), nil)
	default:
		s.Hub.Publish("site.cloud_link", li)
		writeJSON(w, 201, map[string]any{"link": li, "note": "Readings recorded before connecting stay on this Pi; the cloud receives new data and the current state from now on."})
	}
}

func (s *Server) deleteCloudLink(w http.ResponseWriter, r *http.Request, u auth.User) {
	if s.noLinker(w, r) {
		return
	}
	if err := s.Linker.UnlinkCloud(r.Context(), u.ID); err != nil {
		if strings.Contains(err.Error(), "installer") {
			writeError(w, r, 409, "CONFIGURED_BY_INSTALLER", err.Error(), nil)
			return
		}
		s.mapErr(w, r, err)
		return
	}
	s.Hub.Publish("site.cloud_link", LinkInfo{})
	w.WriteHeader(204)
}
