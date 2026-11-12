package esp32

import (
	"sort"
	"time"
)

// record is one durable, sequenced message awaiting a Pi receipt. In
// firmware this is a LittleFS circular log; here it is an in-memory ring
// that survives simulated reboots.
type record struct {
	bootID   string
	seq      int64
	topic    string // leaf: telemetry | events
	payload  []byte
	critical bool
	lastSent time.Time // zero = never sent
}

// journal is the bounded write-ahead buffer. A record is removed ONLY by
// reclaim() (called on a Pi receipt) - never on MQTT PUBACK. When full, the
// oldest non-critical record is evicted and counted as a real gap.
type journal struct {
	cap      int
	recs     []*record
	overflow int64
}

func newJournal(capacity int) *journal { return &journal{cap: capacity} }

func (j *journal) append(r *record) (evicted bool) {
	if len(j.recs) >= j.cap {
		idx := -1
		for i, x := range j.recs { // oldest non-critical first
			if !x.critical {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0 // everything is critical: drop the oldest, still counted
		}
		j.recs = append(j.recs[:idx], j.recs[idx+1:]...)
		j.overflow++
		evicted = true
	}
	j.recs = append(j.recs, r)
	return evicted
}

func (j *journal) occupancy() int { return len(j.recs) }

func (j *journal) unreceipted() int { return len(j.recs) } // every stored record is unreceipted by construction

// reclaim removes records covered by a receipt for bootID and returns how many.
func (j *journal) reclaim(bootID string, seqs []int64) int {
	if len(seqs) == 0 {
		return 0
	}
	set := make(map[int64]struct{}, len(seqs))
	for _, s := range seqs {
		set[s] = struct{}{}
	}
	kept := j.recs[:0]
	n := 0
	for _, r := range j.recs {
		if _, ok := set[r.seq]; ok && r.bootID == bootID {
			n++
			continue
		}
		kept = append(kept, r)
	}
	j.recs = kept
	return n
}

// sendable returns up to limit records that need (re)sending: never-sent or
// older than resendAfter. Critical first, then oldest first. inflight is how
// many already-sent records still await a receipt.
func (j *journal) sendable(now time.Time, resendAfter time.Duration, limit, maxInFlight int) []*record {
	inflight := 0
	var cand []*record
	for _, r := range j.recs {
		if !r.lastSent.IsZero() && now.Sub(r.lastSent) < resendAfter {
			inflight++
			continue
		}
		cand = append(cand, r)
	}
	room := maxInFlight - inflight
	if room <= 0 {
		return nil
	}
	if room < limit {
		limit = room
	}
	sort.SliceStable(cand, func(a, b int) bool {
		if cand[a].critical != cand[b].critical {
			return cand[a].critical
		}
		return false
	})
	if len(cand) > limit {
		cand = cand[:limit]
	}
	return cand
}
