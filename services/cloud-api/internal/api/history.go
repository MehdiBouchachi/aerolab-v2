package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
)

const (
	maxRange  = 31 * 24 * time.Hour
	maxPoints = 5000
	maxScan   = 100000
)

var resolutions = map[string]time.Duration{"raw": 0, "1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour}
var tqRank = map[string]int{"DEVICE_CLOCK": 0, "ESTIMATED_FROM_UPTIME": 1, "RECEIVED_AT_ONLY": 2}

type agg struct {
	sum     float64
	numeric int
	boolAll bool
	valid   int
	lastQ   mc.ReadingQuality
	unit    string
}

type bucket struct {
	t       time.Time
	n       int
	tq      string
	sensors map[string]*agg
}

// history: same contract as the edge endpoint (bounded range/points, explicit
// gaps, only VALID samples averaged, invalid stays visible). Duplicated from the
// edge on purpose: the cloud never imports Pi internals.
func (s *Server) history(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	unit := r.PathValue("unitId")
	q := r.URL.Query()
	to := time.Now().UTC()
	var err error
	if v := q.Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			writeErr(w, r, 400, "VALIDATION_FAILED", "to must be RFC3339", nil)
			return
		}
	}
	from := to.Add(-time.Hour)
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			writeErr(w, r, 400, "VALIDATION_FAILED", "from must be RFC3339", nil)
			return
		}
	}
	span := to.Sub(from)
	if span <= 0 || span > maxRange {
		writeErr(w, r, 400, "VALIDATION_FAILED", "range must be positive and at most 31 days", nil)
		return
	}
	name := q.Get("resolution")
	if name == "" {
		switch {
		case span <= time.Hour:
			name = "raw"
		case span <= 24*time.Hour:
			name = "5m"
		case span <= 7*24*time.Hour:
			name = "15m"
		default:
			name = "1h"
		}
	}
	res, ok := resolutions[name]
	if !ok {
		writeErr(w, r, 400, "VALIDATION_FAILED", "resolution must be raw, 1m, 5m, 15m or 1h", nil)
		return
	}
	if res > 0 && span/res > maxPoints {
		writeErr(w, r, 400, "VALIDATION_FAILED", "too many points for this range; choose a coarser resolution", map[string]any{"max_points": maxPoints})
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT effective_at,time_quality,source,readings FROM telemetry_snapshots WHERE site_id=$1 AND unit_id=$2 AND effective_at>=$3 AND effective_at<$4 ORDER BY effective_at LIMIT $5`,
		site, unit, from, to, maxScan)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	bs := map[int64]*bucket{}
	var order []int64
	trunc, sim, scanned := false, false, 0
	for rows.Next() {
		var t time.Time
		var tq, src string
		var rj []byte
		if rows.Scan(&t, &tq, &src, &rj) != nil {
			continue
		}
		scanned++
		sim = sim || src == "SIMULATOR"
		key := t.UnixMilli()
		if res > 0 {
			key = t.Truncate(res).UnixMilli()
		}
		b := bs[key]
		if b == nil {
			if len(bs) >= maxPoints {
				trunc = true
				break
			}
			b = &bucket{t: time.UnixMilli(key).UTC(), sensors: map[string]*agg{}, tq: tq}
			bs[key] = b
			order = append(order, key)
		}
		b.n++
		if tqRank[tq] > tqRank[b.tq] {
			b.tq = tq
		}
		var rd map[string]mc.Reading
		if json.Unmarshal(rj, &rd) != nil {
			continue
		}
		for k, v := range rd {
			a := b.sensors[k]
			if a == nil {
				a = &agg{boolAll: true, unit: v.Unit}
				b.sensors[k] = a
			}
			a.lastQ = v.Quality
			if v.Quality != mc.QualityValid {
				continue
			}
			a.valid++
			switch x := v.Value.(type) {
			case float64:
				a.sum += x
				a.numeric++
			case bool:
				a.boolAll = a.boolAll && x
			}
		}
	}
	if scanned >= maxScan {
		trunc = true
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	pts := make([]map[string]any, 0, len(order))
	for _, k := range order {
		b := bs[k]
		rd := map[string]any{}
		for n, a := range b.sensors {
			e := map[string]any{"samples": a.valid}
			switch {
			case a.valid == 0:
				e["value"], e["quality"] = nil, string(a.lastQ)
			case a.numeric > 0:
				e["value"], e["quality"] = float64(int64(a.sum/float64(a.numeric)*1000+0.5))/1000, "valid"
			default:
				e["value"], e["quality"] = a.boolAll, "valid"
			}
			if a.unit != "" {
				e["unit"] = a.unit
			}
			rd[n] = e
		}
		pts = append(pts, map[string]any{"t": b.t.Format("2006-01-02T15:04:05.000Z"), "snapshots": b.n, "time_quality": b.tq, "readings": rd})
	}
	exp := res
	if exp == 0 {
		exp = time.Minute
	}
	gaps := []map[string]string{}
	for i := 1; i < len(order); i++ {
		a, b := time.UnixMilli(order[i-1]).UTC(), time.UnixMilli(order[i]).UTC()
		if b.Sub(a) > exp*5/2 {
			gaps = append(gaps, map[string]string{"from": a.Format("2006-01-02T15:04:05.000Z"), "to": b.Format("2006-01-02T15:04:05.000Z")})
		}
	}
	writeJSON(w, 200, map[string]any{"unit_id": unit, "from": from.UTC().Format("2006-01-02T15:04:05.000Z"), "to": to.UTC().Format("2006-01-02T15:04:05.000Z"), "resolution": name,
		"points": pts, "gaps": gaps, "truncated": trunc, "contains_simulated_data": sim, "source": "cloud",
		"note": "Synchronised copy. Times marked ESTIMATED_FROM_UPTIME or RECEIVED_AT_ONLY come from a controller without a trusted clock."})
}
