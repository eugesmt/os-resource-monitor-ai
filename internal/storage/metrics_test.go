package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/metrics"

	_ "modernc.org/sqlite"
)

func openMetricsTestStore(t *testing.T) (*sql.DB, *MetricsStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open metrics database: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920310000_metric_history.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read metrics migration: %v", err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("metrics migration has no Down marker")
	}
	if _, err := db.Exec(up); err != nil {
		_ = db.Close()
		t.Fatalf("apply metrics migration: %v", err)
	}
	store, err := NewMetricsStore(db)
	if err != nil {
		_ = db.Close()
		t.Fatalf("new metrics store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, store
}

func TestMetricsMigrationCreatesTablesAndIndexes(t *testing.T) {
	db, _ := openMetricsTestStore(t)
	for _, table := range []string{"metric_samples", "metric_rollups"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d", table, count)
		}
	}
	for _, index := range []string{"metric_samples_captured_at_idx", "metric_rollups_bucket_end_idx"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d", index, count)
		}
	}
}

func TestMetricsStoreSavesNullableSamplesAndIdempotentRollups(t *testing.T) {
	db, store := openMetricsTestStore(t)
	ctx := context.Background()
	base := metrics.BucketStart(time.Now().UTC().Add(-12 * time.Minute))
	samples := []collector.SystemSnapshot{
		{SnapshotID: "snap-1", CapturedAt: base.Add(10 * time.Second), CPUPercent: 10, MemPercent: 40, Network: collector.NetworkInfo{BytesRecv: 100, RecvPerSec: 2}},
		{SnapshotID: "snap-2", CapturedAt: base.Add(90 * time.Second), CPUPercent: 30, MemPercent: 50, Network: collector.NetworkInfo{BytesRecv: 300, RecvPerSec: 4}, MissingData: []string{"disk_io"}},
		{SnapshotID: "snap-3", CapturedAt: base.Add(3*time.Minute + 10*time.Second), CPUPercent: 50, MemPercent: 60, Network: collector.NetworkInfo{BytesRecv: 500, RecvPerSec: 6}},
	}
	for _, sample := range samples {
		if err := store.SaveSnapshot(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveSnapshot(ctx, samples[0]); err != nil {
		t.Fatalf("duplicate snapshot should be idempotent: %v", err)
	}
	var sampleCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_samples`).Scan(&sampleCount); err != nil {
		t.Fatal(err)
	}
	if sampleCount != 3 {
		t.Fatalf("sample count = %d, want 3", sampleCount)
	}
	var diskRead any
	if err := db.QueryRow(`SELECT disk_read_per_sec FROM metric_samples WHERE snapshot_id='snap-2'`).Scan(&diskRead); err != nil {
		t.Fatal(err)
	}
	if diskRead != nil {
		t.Fatalf("missing disk metric = %#v, want NULL", diskRead)
	}

	now := base.Add(9 * time.Minute)
	created, err := store.AggregateClosedBuckets(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatalf("created rollups = %d, want 2", created)
	}
	created, err = store.AggregateClosedBuckets(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("repeat aggregation count = %d, want 0", created)
	}
	var rollupCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_rollups`).Scan(&rollupCount); err != nil {
		t.Fatal(err)
	}
	if rollupCount != 2 {
		t.Fatalf("rollup count = %d, want 2", rollupCount)
	}
	var avg, min, max float64
	if err := db.QueryRow(`SELECT cpu_sum / cpu_count, cpu_min, cpu_max FROM metric_rollups WHERE bucket_start=?`, formatMetricTime(base)).Scan(&avg, &min, &max); err != nil {
		t.Fatal(err)
	}
	if avg != 20 || min != 10 || max != 30 {
		t.Fatalf("rollup CPU = avg %v min %v max %v", avg, min, max)
	}
	var missing string
	if err := db.QueryRow(`SELECT missing_data_json FROM metric_rollups WHERE bucket_start=?`, formatMetricTime(base)).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	var missingItems []string
	if err := json.Unmarshal([]byte(missing), &missingItems); err != nil {
		t.Fatal(err)
	}
	if len(missingItems) != 1 || missingItems[0] != "disk_io" {
		t.Fatalf("rollup missing data = %#v", missingItems)
	}
}

func TestMetricsSummaryUsesExactRawRangeAndNullableAggregates(t *testing.T) {
	_, store := openMetricsTestStore(t)
	ctx := context.Background()
	base := metrics.BucketStart(time.Now().UTC().Add(-9 * time.Minute))
	for i, cpu := range []float64{0, 20, 40} {
		snapshot := collector.SystemSnapshot{SnapshotID: "summary-" + string(rune('a'+i)), CapturedAt: base.Add(time.Duration(i+1) * time.Minute), CPUPercent: cpu, MissingData: []string{"memory"}}
		if err := store.SaveSnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.Summary(ctx, base, base.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if summary.CPU.Average == nil || *summary.CPU.Average != 20 || summary.CPU.Minimum == nil || *summary.CPU.Minimum != 0 || summary.CPU.Maximum == nil || *summary.CPU.Maximum != 40 {
		t.Fatalf("CPU summary = %#v", summary.CPU)
	}
	if summary.Memory.Average != nil || summary.Memory.Minimum != nil || summary.Memory.Maximum != nil {
		t.Fatalf("missing memory aggregate = %#v", summary.Memory)
	}
	if summary.Coverage.Samples != 3 || summary.Coverage.Rollups != 0 {
		t.Fatalf("coverage = %#v", summary.Coverage)
	}
}

func TestMetricsSummaryReadsRollupsWhenRawSamplesAreAbsent(t *testing.T) {
	db, store := openMetricsTestStore(t)
	ctx := context.Background()
	base := metrics.BucketStart(time.Now().UTC().Add(-6 * time.Hour))
	for i, cpu := range []float64{10, 30} {
		if _, err := db.Exec(`INSERT INTO metric_rollups (bucket_start, bucket_end, generated_at, sample_count, covered_from, covered_to, cpu_sum, cpu_count, cpu_min, cpu_max, missing_data_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]')`, formatMetricTime(base.Add(time.Duration(i)*metrics.BucketDuration)), formatMetricTime(base.Add(time.Duration(i+1)*metrics.BucketDuration)), formatMetricTime(time.Now()), 3, formatMetricTime(base), formatMetricTime(base.Add(metrics.BucketDuration)), cpu*3, 3, cpu, cpu); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.Summary(ctx, base, base.Add(2*metrics.BucketDuration))
	if err != nil {
		t.Fatal(err)
	}
	if summary.CPU.Average == nil || *summary.CPU.Average != 20 || summary.Coverage.Samples != 6 || summary.Coverage.Rollups != 2 {
		t.Fatalf("rollup summary = %#v", summary)
	}
}

func TestMetricsRetention(t *testing.T) {
	db, store := openMetricsTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	oldSample := collector.SystemSnapshot{SnapshotID: "old", CapturedAt: now.Add(-metrics.RawRetention - time.Hour), CPUPercent: 10}
	newSample := collector.SystemSnapshot{SnapshotID: "new", CapturedAt: now.Add(-time.Hour), CPUPercent: 20}
	if err := store.SaveSnapshot(ctx, oldSample); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshot(ctx, newSample); err != nil {
		t.Fatal(err)
	}
	oldBucket := metrics.BucketStart(now.Add(-metrics.RollupRetention - time.Hour))
	if _, err := db.Exec(`INSERT INTO metric_rollups (bucket_start, bucket_end, generated_at, sample_count, missing_data_json) VALUES (?, ?, ?, 1, '[]')`, formatMetricTime(oldBucket), formatMetricTime(oldBucket.Add(metrics.BucketDuration)), formatMetricTime(now)); err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	var samples, rollups int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_rollups`).Scan(&rollups); err != nil {
		t.Fatal(err)
	}
	if samples != 1 || rollups != 0 {
		t.Fatalf("retention counts: samples=%d rollups=%d", samples, rollups)
	}
}

func TestMetricsBucketsKeepSubsecondSamplesOnCorrectSideOfBoundary(t *testing.T) {
	base := metrics.BucketStart(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	db, store := openMetricsTestStore(t)
	ctx := context.Background()
	for _, snapshot := range []collector.SystemSnapshot{
		{SnapshotID: "first", CapturedAt: base.Add(500 * time.Millisecond), CPUPercent: 10},
		{SnapshotID: "second", CapturedAt: base.Add(metrics.BucketDuration + 500*time.Millisecond), CPUPercent: 20},
	} {
		if err := store.SaveSnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AggregateClosedBuckets(ctx, base.Add(2*metrics.BucketDuration)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT bucket_start, sample_count FROM metric_rollups ORDER BY bucket_start`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var counts []int
	for rows.Next() {
		var bucket string
		var count int
		if err := rows.Scan(&bucket, &count); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, count)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(counts, []int{1, 1}) {
		t.Fatalf("bucket sample counts = %v, want [1 1]", counts)
	}
}
