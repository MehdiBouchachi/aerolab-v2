package httpapi

import (
	"context"
	"encoding/json"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// Snapshot is the P0 "current truth" pushed to the cloud: the same unit views
// the local UI shows, so the cloud serves identical shapes (one web app).
func (s *Server) Snapshot(ctx context.Context) (sc.State, error) {
	st := sc.State{ObservedAt: storage.Now(), SiteName: s.Cfg.SiteName, Units: []json.RawMessage{}}
	st.Connectivity.MQTTBrokerConnected = s.MQTT.Connected()
	rows, err := s.DB.R.QueryContext(ctx, `SELECT id,name,COALESCE(device_id,'') FROM growing_units WHERE site_id=? ORDER BY id LIMIT 200`, s.Cfg.SiteID)
	if err != nil {
		return st, err
	}
	type row struct{ id, name, dev string }
	var rs []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.name, &r.dev) == nil {
			rs = append(rs, r)
		}
	}
	rows.Close()
	for _, r := range rs {
		v, err := s.buildUnit(ctx, r.id, r.name, r.dev, true)
		if err != nil {
			return st, err
		}
		b, _ := json.Marshal(v)
		st.Units = append(st.Units, b)
	}
	return st, nil
}
