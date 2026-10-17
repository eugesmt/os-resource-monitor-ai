package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/metrics"
)

// MetricsStore persists compact collector samples and their fixed-width
// three-minute rollups in the same SQLite database as the conversation store.
type MetricsStore struct{ db *sql.DB }

const metricTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatMetricTime(value time.Time) string {
	return value.UTC().Format(metricTimeLayout)
}

func NewMetricsStore(db *sql.DB) (*MetricsStore, error) {
	if db == nil {
		return nil, errors.New("metrics database is nil")
	}
	return &MetricsStore{db: db}, nil
}

// Metrics returns a metrics repository backed by the already-open conversation
// database. Opening a second connection to the same SQLite file is avoided.
func (s *ConversationStore) Metrics() *MetricsStore { return &MetricsStore{db: s.db} }

// The forwarding methods keep ConversationStore compatible with the metrics
// worker while retaining one SQLite connection in the composition root.
func (s *ConversationStore) SaveMetricSample(ctx context.Context, snapshot collector.SystemSnapshot) error {
	return s.Metrics().SaveSnapshot(ctx, snapshot)
}

func (s *ConversationStore) BuildMetricRollups(ctx context.Context, now time.Time) error {
	_, err := s.Metrics().AggregateClosedBuckets(ctx, now)
	return err
}

func (s *ConversationStore) PruneMetricHistory(ctx context.Context, now time.Time) error {
	return s.Metrics().Prune(ctx, now)
}

func (s *ConversationStore) MetricsSummary(ctx context.Context, from, to time.Time) (metrics.Summary, error) {
	return s.Metrics().Summary(ctx, from, to)
}

func (s *MetricsStore) SaveSnapshot(ctx context.Context, snapshot collector.SystemSnapshot) error {
	if s == nil || s.db == nil {
		return errors.New("metrics store is not initialized")
	}
	if strings.TrimSpace(snapshot.SnapshotID) == "" {
		return errors.New("snapshot id is required")
	}
	capturedAt := snapshot.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = snapshot.Timestamp
	}
	if capturedAt.IsZero() {
		return errors.New("snapshot captured time is required")
	}
	missing := make(map[string]struct{}, len(snapshot.MissingData))
	for _, item := range snapshot.MissingData {
		missing[strings.TrimSpace(item)] = struct{}{}
	}
	missingJSON, err := json.Marshal(sortedMissing(missing))
	if err != nil {
		return fmt.Errorf("encode metric missing data: %w", err)
	}
	value := func(section string, v float64) any {
		if _, ok := missing[section]; ok {
			return nil
		}
		return v
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO metric_samples (
			snapshot_id, captured_at, hostname, cpu_percent, mem_percent,
			load_1, load_5, load_15, network_bytes_recv, network_bytes_sent,
			network_recv_per_sec, network_send_per_sec, disk_read_bytes,
			disk_write_bytes, disk_read_per_sec, disk_write_per_sec, missing_data_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(snapshot_id) DO NOTHING
	`, snapshot.SnapshotID, formatMetricTime(capturedAt), snapshot.Hostname,
		value("cpu", snapshot.CPUPercent), value("memory", snapshot.MemPercent),
		value("load", snapshot.Load1), value("load", snapshot.Load5), value("load", snapshot.Load15),
		value("network", float64(snapshot.Network.BytesRecv)), value("network", float64(snapshot.Network.BytesSent)),
		value("network", snapshot.Network.RecvPerSec), value("network", snapshot.Network.SendPerSec),
		value("disk_io", float64(snapshot.DiskIO.ReadBytes)), value("disk_io", float64(snapshot.DiskIO.WriteBytes)),
		value("disk_io", snapshot.DiskIO.ReadPerSec), value("disk_io", snapshot.DiskIO.WritePerSec), string(missingJSON))
	return err
}

// AggregateClosedBuckets materializes all non-empty buckets that ended before
// now. It is safe to call repeatedly; each bucket is replaced atomically.
func (s *MetricsStore) AggregateClosedBuckets(ctx context.Context, now time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("metrics store is not initialized")
	}
	cutoff := metrics.BucketStart(now)
	var earliest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(captured_at) FROM metric_samples WHERE captured_at < ?`, formatMetricTime(cutoff)).Scan(&earliest); err != nil {
		return 0, err
	}
	if !earliest.Valid {
		return 0, nil
	}
	first, err := time.Parse(time.RFC3339Nano, earliest.String)
	if err != nil {
		return 0, fmt.Errorf("parse earliest metric sample: %w", err)
	}
	start := metrics.BucketStart(first)
	var latest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(bucket_end) FROM metric_rollups WHERE bucket_end <= ?`, formatMetricTime(cutoff)).Scan(&latest); err != nil {
		return 0, err
	}
	if latest.Valid {
		lastEnd, parseErr := time.Parse(time.RFC3339Nano, latest.String)
		if parseErr != nil {
			return 0, fmt.Errorf("parse latest metric rollup: %w", parseErr)
		}
		if lastEnd.After(start) {
			start = lastEnd
		}
	}
	count := 0
	for ; start.Before(cutoff); start = start.Add(metrics.BucketDuration) {
		created, err := s.aggregateBucket(ctx, start)
		if err != nil {
			return count, err
		}
		if created {
			count++
		}
	}
	return count, nil
}

type rollupValues struct {
	BucketStart, BucketEnd, GeneratedAt, CoveredFrom, CoveredTo string
	SampleCount                                                 int64
	Groups                                                      [9]statValues
	NetworkRecvDelta, NetworkSendDelta                          any
	DiskReadDelta, DiskWriteDelta                               any
	MissingJSON                                                 string
}

type statValues struct {
	Sum, Min, Max sql.NullFloat64
	Count         int64
}

func (s *MetricsStore) aggregateBucket(ctx context.Context, start time.Time) (bool, error) {
	end := start.Add(metrics.BucketDuration)
	var v rollupValues
	v.BucketStart = formatMetricTime(start)
	v.BucketEnd = formatMetricTime(end)
	v.GeneratedAt = formatMetricTime(time.Now())
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(captured_at), MAX(captured_at),
		SUM(cpu_percent), COUNT(cpu_percent), MIN(cpu_percent), MAX(cpu_percent),
		SUM(mem_percent), COUNT(mem_percent), MIN(mem_percent), MAX(mem_percent),
		SUM(load_1), COUNT(load_1), MIN(load_1), MAX(load_1),
		SUM(load_5), COUNT(load_5), MIN(load_5), MAX(load_5),
		SUM(load_15), COUNT(load_15), MIN(load_15), MAX(load_15),
		SUM(network_recv_per_sec), COUNT(network_recv_per_sec), MIN(network_recv_per_sec), MAX(network_recv_per_sec),
		SUM(network_send_per_sec), COUNT(network_send_per_sec), MIN(network_send_per_sec), MAX(network_send_per_sec),
		SUM(disk_read_per_sec), COUNT(disk_read_per_sec), MIN(disk_read_per_sec), MAX(disk_read_per_sec),
		SUM(disk_write_per_sec), COUNT(disk_write_per_sec), MIN(disk_write_per_sec), MAX(disk_write_per_sec)
		FROM metric_samples WHERE captured_at >= ? AND captured_at < ?
	`, v.BucketStart, v.BucketEnd)
	var coveredFrom, coveredTo sql.NullString
	if err := row.Scan(&v.SampleCount, &coveredFrom, &coveredTo,
		&v.Groups[0].Sum, &v.Groups[0].Count, &v.Groups[0].Min, &v.Groups[0].Max,
		&v.Groups[1].Sum, &v.Groups[1].Count, &v.Groups[1].Min, &v.Groups[1].Max,
		&v.Groups[2].Sum, &v.Groups[2].Count, &v.Groups[2].Min, &v.Groups[2].Max,
		&v.Groups[3].Sum, &v.Groups[3].Count, &v.Groups[3].Min, &v.Groups[3].Max,
		&v.Groups[4].Sum, &v.Groups[4].Count, &v.Groups[4].Min, &v.Groups[4].Max,
		&v.Groups[5].Sum, &v.Groups[5].Count, &v.Groups[5].Min, &v.Groups[5].Max,
		&v.Groups[6].Sum, &v.Groups[6].Count, &v.Groups[6].Min, &v.Groups[6].Max,
		&v.Groups[7].Sum, &v.Groups[7].Count, &v.Groups[7].Min, &v.Groups[7].Max,
		&v.Groups[8].Sum, &v.Groups[8].Count, &v.Groups[8].Min, &v.Groups[8].Max); err != nil {
		return false, err
	}
	if v.SampleCount == 0 {
		return false, nil
	}
	if coveredFrom.Valid {
		v.CoveredFrom = coveredFrom.String
	}
	if coveredTo.Valid {
		v.CoveredTo = coveredTo.String
	}

	deltas, err := s.counterDeltas(ctx, v.BucketStart, v.BucketEnd)
	if err != nil {
		return false, err
	}
	v.NetworkRecvDelta, v.NetworkSendDelta = deltas[0], deltas[1]
	v.DiskReadDelta, v.DiskWriteDelta = deltas[2], deltas[3]
	v.MissingJSON, err = s.missingJSON(ctx, v.BucketStart, v.BucketEnd)
	if err != nil {
		return false, err
	}
	args := []any{v.BucketStart, v.BucketEnd, v.GeneratedAt, v.SampleCount, v.CoveredFrom, v.CoveredTo}
	args = append(args, statArgs(v.Groups)...)
	args = append(args, v.NetworkRecvDelta, v.NetworkSendDelta, v.DiskReadDelta, v.DiskWriteDelta)
	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO metric_rollups (
			bucket_start, bucket_end, generated_at, sample_count, covered_from, covered_to,
			cpu_sum, cpu_count, cpu_min, cpu_max, memory_sum, memory_count, memory_min, memory_max,
			load1_sum, load1_count, load1_min, load1_max, load5_sum, load5_count, load5_min, load5_max,
			load15_sum, load15_count, load15_min, load15_max,
			network_recv_rate_sum, network_recv_rate_count, network_recv_rate_min, network_recv_rate_max,
			network_send_rate_sum, network_send_rate_count, network_send_rate_min, network_send_rate_max,
			disk_read_rate_sum, disk_read_rate_count, disk_read_rate_min, disk_read_rate_max,
			disk_write_rate_sum, disk_write_rate_count, disk_write_rate_min, disk_write_rate_max,
			network_recv_delta_bytes, network_send_delta_bytes, disk_read_delta_bytes, disk_write_delta_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, args...)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE metric_rollups SET missing_data_json=? WHERE bucket_start=?`, v.MissingJSON, v.BucketStart)
	}
	return err == nil, err
}

func statArgs(groups [9]statValues) []any {
	args := make([]any, 0, 36)
	for _, group := range groups {
		args = append(args, nullableFloat(group.Sum), group.Count, nullableFloat(group.Min), nullableFloat(group.Max))
	}
	return args
}

func nullableFloat(v sql.NullFloat64) any {
	if !v.Valid || math.IsNaN(v.Float64) || math.IsInf(v.Float64, 0) {
		return nil
	}
	return v.Float64
}

func (s *MetricsStore) counterDeltas(ctx context.Context, from, to string) ([4]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT network_bytes_recv, network_bytes_sent, disk_read_bytes, disk_write_bytes FROM metric_samples WHERE captured_at >= ? AND captured_at < ? ORDER BY captured_at, snapshot_id`, from, to)
	if err != nil {
		return [4]any{}, err
	}
	defer rows.Close()
	var first, last [4]sql.NullFloat64
	for rows.Next() {
		var current [4]sql.NullFloat64
		if err := rows.Scan(&current[0], &current[1], &current[2], &current[3]); err != nil {
			return [4]any{}, err
		}
		for i := range current {
			if current[i].Valid {
				if !first[i].Valid {
					first[i] = current[i]
				}
				last[i] = current[i]
			}
		}
	}
	if err := rows.Err(); err != nil {
		return [4]any{}, err
	}
	var result [4]any
	for i := range result {
		if first[i].Valid && last[i].Valid && last[i].Float64 >= first[i].Float64 {
			result[i] = last[i].Float64 - first[i].Float64
		}
	}
	return result, nil
}

func (s *MetricsStore) missingJSON(ctx context.Context, from, to string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT missing_data_json FROM metric_samples WHERE captured_at >= ? AND captured_at < ?`, from, to)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	set := map[string]struct{}{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return "", err
		}
		var items []string
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return "", fmt.Errorf("decode metric missing data: %w", err)
		}
		for _, item := range items {
			set[item] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(sortedMissing(set))
	return string(encoded), err
}

func sortedMissing(set map[string]struct{}) []string {
	items := make([]string, 0, len(set))
	for item := range set {
		if item != "" {
			items = append(items, item)
		}
	}
	slices.Sort(items)
	return items
}

type accumulator struct {
	sum, min, max float64
	count         int64
	initialized   bool
}

func (a *accumulator) Add(v sql.NullFloat64) {
	if !v.Valid || math.IsNaN(v.Float64) || math.IsInf(v.Float64, 0) {
		return
	}
	if !a.initialized || v.Float64 < a.min {
		a.min = v.Float64
	}
	if !a.initialized || v.Float64 > a.max {
		a.max = v.Float64
	}
	a.sum += v.Float64
	a.count++
	a.initialized = true
}

func (a accumulator) Aggregate() metrics.Aggregate {
	if a.count == 0 {
		return metrics.Aggregate{}
	}
	return metrics.Aggregate{Average: ptr(a.sum / float64(a.count)), Minimum: ptr(a.min), Maximum: ptr(a.max), Samples: a.count}
}

func ptr(v float64) *float64 { return &v }

func (s *MetricsStore) Summary(ctx context.Context, from, to time.Time) (metrics.Summary, error) {
	from, to = from.UTC(), to.UTC()
	if err := metrics.ValidateRange(from, to, time.Now().UTC()); err != nil {
		return metrics.Summary{}, err
	}
	summary, rows, err := s.rawSummary(ctx, from, to)
	if err != nil {
		return metrics.Summary{}, err
	}
	if rows > 0 {
		return summary, nil
	}
	return s.rollupSummary(ctx, from, to)
}

func (s *MetricsStore) rawSummary(ctx context.Context, from, to time.Time) (metrics.Summary, int64, error) {
	summary := metrics.Summary{RequestedFrom: from, RequestedTo: to, GeneratedAt: time.Now().UTC(), MissingData: []string{}}
	rows, err := s.db.QueryContext(ctx, `SELECT captured_at, cpu_percent, mem_percent, load_1, load_5, load_15, network_bytes_recv, network_bytes_sent, network_recv_per_sec, network_send_per_sec, disk_read_bytes, disk_write_bytes, disk_read_per_sec, disk_write_per_sec, missing_data_json FROM metric_samples WHERE captured_at >= ? AND captured_at < ? ORDER BY captured_at, snapshot_id`, formatMetricTime(from), formatMetricTime(to))
	if err != nil {
		return metrics.Summary{}, 0, err
	}
	defer rows.Close()
	var cpu, memory, load1, load5, load15, recvRate, sendRate, readRate, writeRate accumulator
	var first, last [4]sql.NullFloat64
	missing := map[string]struct{}{}
	var count int64
	var actualFrom, actualTo time.Time
	for rows.Next() {
		var captured string
		var values [13]sql.NullFloat64
		var missingRaw string
		if err := rows.Scan(&captured, &values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6], &values[7], &values[8], &values[9], &values[10], &values[11], &values[12], &missingRaw); err != nil {
			return metrics.Summary{}, 0, err
		}
		at, err := time.Parse(time.RFC3339Nano, captured)
		if err != nil {
			return metrics.Summary{}, 0, err
		}
		if count == 0 {
			actualFrom = at
		}
		actualTo = at
		count++
		cpu.Add(values[0])
		memory.Add(values[1])
		load1.Add(values[2])
		load5.Add(values[3])
		load15.Add(values[4])
		recvRate.Add(values[7])
		sendRate.Add(values[8])
		readRate.Add(values[11])
		writeRate.Add(values[12])
		for i := 0; i < 4; i++ {
			if values[5+i].Valid {
				if !first[i].Valid {
					first[i] = values[5+i]
				}
				last[i] = values[5+i]
			}
		}
		var items []string
		if err := json.Unmarshal([]byte(missingRaw), &items); err != nil {
			return metrics.Summary{}, 0, err
		}
		for _, item := range items {
			missing[item] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return metrics.Summary{}, 0, err
	}
	if count == 0 {
		return summary, 0, nil
	}
	summary.Coverage = metrics.Coverage{From: &actualFrom, To: &actualTo, Samples: count}
	summary.CPU, summary.Memory = cpu.Aggregate(), memory.Aggregate()
	summary.Load1, summary.Load5, summary.Load15 = load1.Aggregate(), load5.Aggregate(), load15.Aggregate()
	summary.NetworkReceiveRate, summary.NetworkSendRate = recvRate.Aggregate(), sendRate.Aggregate()
	summary.DiskReadRate, summary.DiskWriteRate = readRate.Aggregate(), writeRate.Aggregate()
	summary.NetworkReceiveBytes, summary.NetworkSendBytes, summary.DiskReadBytes, summary.DiskWriteBytes = deltasFromNull(first, last)
	summary.MissingData = sortedMissing(missing)
	return summary, count, nil
}

func deltasFromNull(first, last [4]sql.NullFloat64) (*float64, *float64, *float64, *float64) {
	result := [4]*float64{}
	for i := range result {
		if first[i].Valid && last[i].Valid && last[i].Float64 >= first[i].Float64 {
			v := last[i].Float64 - first[i].Float64
			result[i] = &v
		}
	}
	return result[0], result[1], result[2], result[3]
}

func (s *MetricsStore) rollupSummary(ctx context.Context, from, to time.Time) (metrics.Summary, error) {
	summary := metrics.Summary{RequestedFrom: from, RequestedTo: to, GeneratedAt: time.Now().UTC(), MissingData: []string{}}
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_start, covered_from, covered_to, sample_count, cpu_sum, cpu_count, cpu_min, cpu_max, memory_sum, memory_count, memory_min, memory_max, load1_sum, load1_count, load1_min, load1_max, load5_sum, load5_count, load5_min, load5_max, load15_sum, load15_count, load15_min, load15_max, network_recv_rate_sum, network_recv_rate_count, network_recv_rate_min, network_recv_rate_max, network_send_rate_sum, network_send_rate_count, network_send_rate_min, network_send_rate_max, disk_read_rate_sum, disk_read_rate_count, disk_read_rate_min, disk_read_rate_max, disk_write_rate_sum, disk_write_rate_count, disk_write_rate_min, disk_write_rate_max, network_recv_delta_bytes, network_send_delta_bytes, disk_read_delta_bytes, disk_write_delta_bytes, missing_data_json FROM metric_rollups WHERE bucket_end > ? AND bucket_start < ? ORDER BY bucket_start`, formatMetricTime(from), formatMetricTime(to))
	if err != nil {
		return metrics.Summary{}, err
	}
	defer rows.Close()
	var groups [9]accumulator
	var bytes [4]float64
	var bytesValid [4]bool
	var count, rollups int64
	var actualFrom, actualTo time.Time
	missing := map[string]struct{}{}
	for rows.Next() {
		var bucket, coveredFrom, coveredTo, missingRaw string
		var sampleCount int64
		var values [40]sql.NullFloat64
		dest := []any{&bucket, &coveredFrom, &coveredTo, &sampleCount}
		for i := range values {
			dest = append(dest, &values[i])
		}
		dest = append(dest, &missingRaw)
		if err := rows.Scan(dest...); err != nil {
			return metrics.Summary{}, err
		}
		rollups++
		count += sampleCount
		if coveredFrom != "" {
			at, parseErr := time.Parse(time.RFC3339Nano, coveredFrom)
			if parseErr == nil && (actualFrom.IsZero() || at.Before(actualFrom)) {
				actualFrom = at
			}
		}
		if coveredTo != "" {
			at, parseErr := time.Parse(time.RFC3339Nano, coveredTo)
			if parseErr == nil && at.After(actualTo) {
				actualTo = at
			}
		}
		for i := 0; i < 9; i++ {
			// Rollup columns are sum, count, min, max. The accumulator's sum
			// and count are intentionally reconstructed from those columns.
			base := i * 4
			if values[base+1].Valid && values[base+1].Float64 > 0 {
				groups[i].sum += nullValue(values[base])
				groups[i].count += int64(values[base+1].Float64)
				if values[base+2].Valid && (!groups[i].initialized || values[base+2].Float64 < groups[i].min) {
					groups[i].min = values[base+2].Float64
				}
				if values[base+3].Valid && (!groups[i].initialized || values[base+3].Float64 > groups[i].max) {
					groups[i].max = values[base+3].Float64
				}
				groups[i].initialized = true
			}
		}
		for i := 0; i < 4; i++ {
			if values[36+i].Valid {
				bytes[i] += values[36+i].Float64
				bytesValid[i] = true
			}
		}
		var items []string
		if err := json.Unmarshal([]byte(missingRaw), &items); err != nil {
			return metrics.Summary{}, err
		}
		for _, item := range items {
			missing[item] = struct{}{}
		}
		_ = bucket
	}
	if err := rows.Err(); err != nil {
		return metrics.Summary{}, err
	}
	if rollups == 0 {
		return summary, nil
	}
	summary.Coverage = metrics.Coverage{Samples: count, Rollups: rollups}
	if !actualFrom.IsZero() {
		summary.Coverage.From = &actualFrom
	}
	if !actualTo.IsZero() {
		summary.Coverage.To = &actualTo
	}
	summary.CPU, summary.Memory = groups[0].Aggregate(), groups[1].Aggregate()
	summary.Load1, summary.Load5, summary.Load15 = groups[2].Aggregate(), groups[3].Aggregate(), groups[4].Aggregate()
	summary.NetworkReceiveRate, summary.NetworkSendRate = groups[5].Aggregate(), groups[6].Aggregate()
	summary.DiskReadRate, summary.DiskWriteRate = groups[7].Aggregate(), groups[8].Aggregate()
	if bytesValid[0] {
		summary.NetworkReceiveBytes = &bytes[0]
	}
	if bytesValid[1] {
		summary.NetworkSendBytes = &bytes[1]
	}
	if bytesValid[2] {
		summary.DiskReadBytes = &bytes[2]
	}
	if bytesValid[3] {
		summary.DiskWriteBytes = &bytes[3]
	}
	summary.MissingData = sortedMissing(missing)
	return summary, nil
}

func nullValue(v sql.NullFloat64) float64 {
	if !v.Valid {
		return 0
	}
	return v.Float64
}

func (s *MetricsStore) Prune(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("metrics store is not initialized")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM metric_samples WHERE captured_at < ?`, formatMetricTime(now.Add(-metrics.RawRetention))); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM metric_rollups WHERE bucket_end < ?`, formatMetricTime(now.Add(-metrics.RollupRetention)))
	return err
}
