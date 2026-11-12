package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

const (
	maxHistoryRange  = 31 * 24 * time.Hour
	maxHistoryPoints = 5000
	maxScanRows      = 100000
)

var resolutions = map[string]time.Duration{"raw": 0, "1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour}

type bucket struct {
	t       time.Time
	n       int
	timeQ   string
	sensors map[string]*agg
}

type agg struct {
	sum     float64
	numeric int
	boolAll bool
	boolN   int
	lastQ   mc.ReadingQuality
	valid   int
	unit    string
}

var timeQRank = map[string]int{"DEVICE_CLOCK": 0, "ESTIMATED_FROM_UPTIME": 1, "RECEIVED_AT_ONLY": 2}

// history returns bounded, server-aggregated points plus explicit gaps. The
// chart must draw gaps as gaps; nothing is interpolated or invented here.
func (s *Server) history(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _, _, ok := s.unitRow(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	to := time.Now().UTC()
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, r, 400, "VALIDATION_FAILED", "to must be RFC3339", map[string]any{"field": "to"})
			return
		}
		to = t.UTC()
	}
	from := to.Add(-time.Hour)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, r, 400, "VALIDATION_FAILED", "from must be RFC3339", map[string]any{"field": "from"})
			return
		}
		from = t.UTC()
	}
	span := to.Sub(from)
	if span <= 0 || span > maxHistoryRange {
		writeError(w, r, 400, "VALIDATION_FAILED", "range must be positive and at most 31 days", map[string]any{"max_days": 31})
		return
	}
	resName := q.Get("resolution")
	if resName == "" {
		switch {
		case span <= time.Hour:
			resName = "raw"
		case span <= 24*time.Hour:
			resName = "5m"
		case span <= 7*24*time.Hour:
			resName = "15m"
		default:
			resName = "1h"
		}
	}
	res, okRes := resolutions[resName]
	if !okRes {
		writeError(w, r, 400, "VALIDATION_FAILED", "resolution must be raw, 1m, 5m, 15m or 1h", map[string]any{"field": "resolution"})
		return
	}
	if res > 0 && span/res > maxHistoryPoints {
		writeError(w, r, 400, "VALIDATION_FAILED", "too many points for this range; choose a coarser resolution", map[string]any{"max_points": maxHistoryPoints, "resolution": resName})
		return
	}

	rows, err := s.DB.R.QueryContext(r.Context(), `SELECT effective_at_utc,time_quality,source,readings_json FROM telemetry_snapshots WHERE unit_id=? AND effective_at_utc>=? AND effective_at_utc<? ORDER BY effective_at_utc LIMIT ?`,
		id, storage.FormatTime(from), storage.FormatTime(to), maxScanRows)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	defer rows.Close()

	buckets := map[int64]*bucket{}
	var order []int64
	truncated, anySim := false, false
	scanned := 0
	for rows.Next() {
		var eff, tq, src, rj string
		if rows.Scan(&eff, &tq, &src, &rj) != nil {
			continue
		}
		scanned++
		t, err := time.Parse(storage.TimeFormat, eff)
		if err != nil {
			continue
		}
		anySim = anySim || src == string(mc.SourceSimulator)
		key := t.UnixMilli()
		if res > 0 {
			key = t.Truncate(res).UnixMilli()
		}
		b := buckets[key]
		if b == nil {
			if len(buckets) >= maxHistoryPoints {
				truncated = true
				break
			}
			b = &bucket{t: time.UnixMilli(key).UTC(), sensors: map[string]*agg{}, timeQ: tq}
			buckets[key] = b
			order = append(order, key)
		}
		b.n++
		if timeQRank[tq] > timeQRank[b.timeQ] {
			b.timeQ = tq // report the weakest time evidence in the bucket
		}
		var rd map[string]mc.Reading
		if json.Unmarshal([]byte(rj), &rd) != nil {
			continue
		}
		for name, v := range rd {
			a := b.sensors[name]
			if a == nil {
				a = &agg{boolAll: true, unit: v.Unit}
				b.sensors[name] = a
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
				a.boolN++
				a.boolAll = a.boolAll && x
			}
		}
	}
	if scanned >= maxScanRows {
		truncated = true
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	points := make([]map[string]any, 0, len(order))
	for _, k := range order {
		b := buckets[k]
		rd := map[string]any{}
		for name, a := range b.sensors {
			e := map[string]any{"samples": a.valid}
			switch {
			case a.valid == 0:
				e["value"], e["quality"] = nil, string(a.lastQ) // invalid/missing stays visible, never zero
			case a.numeric > 0:
				e["value"], e["quality"] = round(a.sum/float64(a.numeric)), "valid"
			default:
				e["value"], e["quality"] = a.boolAll, "valid" // all samples true, or any false
			}
			if a.unit != "" {
				e["unit"] = a.unit
			}
			rd[name] = e
		}
		points = append(points, map[string]any{"t": storage.FormatTime(b.t), "snapshots": b.n, "time_quality": b.timeQ, "readings": rd})
	}

	// gaps: more than 2.5 expected periods between consecutive points
	expect := res
	if expect == 0 {
		expect = time.Minute
	}
	gaps := []map[string]string{}
	for i := 1; i < len(order); i++ {
		prev, cur := time.UnixMilli(order[i-1]), time.UnixMilli(order[i])
		if cur.Sub(prev) > expect*5/2 {
			gaps = append(gaps, map[string]string{"from": storage.FormatTime(prev), "to": storage.FormatTime(cur)})
		}
	}
	writeJSON(w, 200, map[string]any{"unit_id": id, "from": storage.FormatTime(from), "to": storage.FormatTime(to), "resolution": resName, "points": points, "gaps": gaps,
		"truncated": truncated, "contains_simulated_data": anySim, "observed_at": storage.Now(),
		"note": "Times marked ESTIMATED_FROM_UPTIME or RECEIVED_AT_ONLY come from a controller without a trusted clock."})
}

func round(v float64) float64 { return float64(int64(v*1000+copysign(0.5, v))) / 1000 }

func copysign(a, b float64) float64 {
	if b < 0 {
		return -a
	}
	return a
}
