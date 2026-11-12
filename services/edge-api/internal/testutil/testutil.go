// Package testutil holds shared helpers for edge-api tests.
package testutil

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/migrations"
)

func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Store opens a fresh migrated database with a site row.
func Store(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "t.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background(), migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO sites(id,name,simulated,created_at) VALUES ('site-test-01','Test',1,?)`, storage.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// FakePub records published messages and can simulate a downed broker.
type FakePub struct {
	mu   sync.Mutex
	Msgs []Msg
	Up   bool
}

type Msg struct {
	Topic    string
	QoS      byte
	Retained bool
	Payload  []byte
}

func NewFakePub() *FakePub { return &FakePub{Up: true} }

func (f *FakePub) Publish(topic string, qos byte, retained bool, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.Up {
		return io.ErrClosedPipe
	}
	f.Msgs = append(f.Msgs, Msg{topic, qos, retained, append([]byte(nil), payload...)})
	return nil
}

func (f *FakePub) Connected() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.Up }

func (f *FakePub) Take(topicSuffix string) []Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Msg
	for _, m := range f.Msgs {
		if len(m.Topic) >= len(topicSuffix) && m.Topic[len(m.Topic)-len(topicSuffix):] == topicSuffix {
			out = append(out, m)
		}
	}
	return out
}

func (f *FakePub) Reset() { f.mu.Lock(); f.Msgs = nil; f.mu.Unlock() }
