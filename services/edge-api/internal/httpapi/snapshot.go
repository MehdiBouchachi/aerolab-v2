package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// Snapshot is the P0 "current truth" pushed to the cloud: the same unit views
// the local UI shows, so the cloud serves identical shapes (one web app).
func (s *Server) Snapshot(ctx context.Context) (sc.State, error) {
	st := sc.State{ObservedAt: storage.Now(), SiteName: s.Cfg.SiteName, Units: []json.RawMessage{}}
	st.Connectivity.MQTTBrokerConnected = s.MQTT.Connected()
	rows, err := s.DB.R.QueryContext(ctx, `SELECT id,name,COALESCE(device_id,'') FROM growing_units WHERE site_id=? ORDER BY id LIMIT 201`, s.Cfg.SiteID)
	if err != nil {
		return st, err
	}
	var rs []unitRef
	for rows.Next() {
		var r unitRef
		if rows.Scan(&r.id, &r.name, &r.dev) == nil {
			rs = append(rs, r)
		}
	}
	rows.Close()
	st.UnitsComplete = len(rs) <= 200
	if len(rs) > 200 {
		rs = rs[:200] // bounded payload; the cloud is told the list is partial
	}
	views, err := s.buildUnits(ctx, rs, true)
	if err != nil {
		return st, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%v|%v|%s|", st.Connectivity.MQTTBrokerConnected, st.UnitsComplete, st.SiteName)
	for _, v := range views {
		b, _ := json.Marshal(v)
		st.Units = append(st.Units, b)
		// fingerprint the same view with the per-second age zeroed: only real changes count
		if v.LatestReadings != nil {
			age := v.LatestReadings.AgeSeconds
			v.LatestReadings.AgeSeconds = 0
			fb, _ := json.Marshal(v)
			v.LatestReadings.AgeSeconds = age
			h.Write(fb)
		} else {
			h.Write(b)
		}
	}
	st.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return st, nil
}
