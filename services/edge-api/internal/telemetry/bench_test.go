package telemetry

import (
	"context"
	"testing"
)

// BenchmarkCommitBatch100: one ingest transaction of 100 telemetry records (synchronous=FULL, real file).
func BenchmarkCommitBatch100(b *testing.B) {
	t := &testing.T{}
	w, _, _, _ := newW(t)
	ctx := context.Background()
	seq := int64(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch := make([]recordJob, 100)
		for k := range batch {
			seq++
			batch[k] = mkJob(seq)
		}
		w.commitBatch(ctx, batch)
	}
	b.ReportMetric(float64(b.N*100)/b.Elapsed().Seconds(), "records/s")
}
