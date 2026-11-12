package esp32

import (
	"encoding/json"
	"os"
	"path/filepath"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

const maxProcessed = 32 // bounded command-result memory (spec: last bounded set)

type processedCmd struct {
	CommandID string          `json:"command_id"`
	Status    mc.ResultStatus `json:"status"`
	Reason    string          `json:"reason,omitempty"`
}

// nvsData is the persisted configuration (stands in for NVS): one complete,
// validated programme selected atomically, plus the dedup set.
type nvsData struct {
	Program       *mc.Program    `json:"program,omitempty"`
	Version       int            `json:"version"`
	Previous      *mc.Program    `json:"previous,omitempty"`
	PrevVersion   int            `json:"previous_version"`
	LastCommandID string         `json:"last_command_id,omitempty"`
	Processed     []processedCmd `json:"processed,omitempty"`
}

func (n *nvsData) lookup(id string) (processedCmd, bool) {
	for _, p := range n.Processed {
		if p.CommandID == id {
			return p, true
		}
	}
	return processedCmd{}, false
}

func (n *nvsData) remember(p processedCmd) {
	n.Processed = append(n.Processed, p)
	if len(n.Processed) > maxProcessed {
		n.Processed = n.Processed[len(n.Processed)-maxProcessed:]
	}
}

// save writes atomically (temp file + rename) so a power cut never leaves a
// half-written programme. No path = volatile.
func (n *nvsData) save(path string) error {
	if path == "" {
		return nil
	}
	b, err := json.Marshal(n)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadNVS returns a verified record. A corrupt or invalid programme is
// discarded (the device then stays in NEEDS_SETUP), never run.
func loadNVS(path string, hardMax int) *nvsData {
	n := &nvsData{}
	if path == "" {
		return n
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return n
	}
	if json.Unmarshal(b, n) != nil {
		return &nvsData{}
	}
	if n.Program != nil {
		if n.Program.Validate() != nil || n.Program.MaxContinuousOnSecs > hardMax || n.Program.OnDurationSeconds > hardMax || n.Version < 1 {
			if n.Previous != nil && n.Previous.Validate() == nil && n.PrevVersion >= 1 {
				n.Program, n.Version = n.Previous, n.PrevVersion
			} else {
				return &nvsData{}
			}
		}
	}
	return n
}
