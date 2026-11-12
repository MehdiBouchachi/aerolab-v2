package esp32

import (
	"math"
	"math/rand"
	"sync"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

// Faults is the set of injectable hardware faults.
type Faults struct {
	LowWater           bool `json:"low_water"`
	LevelSensorInvalid bool `json:"level_sensor_invalid"`
	PHInvalid          bool `json:"ph_invalid"`
	ECInvalid          bool `json:"ec_invalid"`
}

// sensorBank produces deterministic synthetic readings. Per-device phase and
// offsets make units distinguishable. Values are NOT measurements.
type sensorBank struct {
	mu     sync.Mutex
	rng    *rand.Rand
	phase  float64
	faults Faults
}

func newSensorBank(seed int64) *sensorBank {
	r := rand.New(rand.NewSource(seed))
	return &sensorBank{rng: r, phase: r.Float64() * 2 * math.Pi}
}

func (s *sensorBank) setFaults(f Faults) { s.mu.Lock(); s.faults = f; s.mu.Unlock() }
func (s *sensorBank) getFaults() Faults  { s.mu.Lock(); defer s.mu.Unlock(); return s.faults }

// essential reports the interlock inputs (level ok, level sensor valid).
func (s *sensorBank) essential() (levelOK, sensorValid bool) {
	f := s.getFaults()
	return !f.LowWater, !f.LevelSensorInvalid
}

// sample returns a full reading set at the given uptime (seconds).
func (s *sensorBank) sample(uptimeSec float64) map[string]mc.Reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.faults
	wave := func(base, amp, periodSec float64) float64 {
		return round2(base + amp*math.Sin(2*math.Pi*uptimeSec/periodSec+s.phase) + (s.rng.Float64()-0.5)*0.05)
	}
	out := map[string]mc.Reading{
		"air_temp_c":            {Value: wave(24, 1.5, 3600), Quality: mc.QualityValid, Unit: "C"},
		"relative_humidity_pct": {Value: wave(58, 4, 5400), Quality: mc.QualityValid, Unit: "%"},
		"water_temp_c":          {Value: wave(22, 0.8, 7200), Quality: mc.QualityValid, Unit: "C"},
		"ph":                    {Value: wave(6.1, 0.1, 9000), Quality: mc.QualityValid},
		"ec_ms_cm":              {Value: wave(1.7, 0.05, 9000), Quality: mc.QualityValid, Unit: "mS/cm"},
		"reservoir_level_ok":    {Value: !f.LowWater, Quality: mc.QualityValid},
		// No light sensor fitted in the simulated profile: reported as MISSING,
		// never as 0.
		"light_lux": {Value: nil, Quality: mc.QualityMissing},
	}
	if f.PHInvalid {
		out["ph"] = mc.Reading{Value: nil, Quality: mc.QualityInvalid}
	}
	if f.ECInvalid {
		out["ec_ms_cm"] = mc.Reading{Value: nil, Quality: mc.QualityInvalid, Unit: "mS/cm"}
	}
	if f.LevelSensorInvalid {
		out["reservoir_level_ok"] = mc.Reading{Value: nil, Quality: mc.QualityInvalid}
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
