package metrics

import (
	"errors"
	"time"
)

const (
	BucketDuration  = 3 * time.Minute
	RawRetention    = 7 * 24 * time.Hour
	RollupRetention = 90 * 24 * time.Hour
	MinRange        = 3 * time.Minute
	MaxRange        = 7 * 24 * time.Hour
)

var (
	ErrInvalidRange = errors.New("invalid metrics range")
	ErrFutureRange  = errors.New("metrics range ends in the future")
	ErrRangeShort   = errors.New("metrics range is shorter than three minutes")
	ErrRangeLong    = errors.New("metrics range is longer than seven days")
)

// Aggregate contains nullable statistics. A nil value means that no sample
// had a value for the metric, which is different from a measured zero.
type Aggregate struct {
	Average *float64 `json:"average"`
	Minimum *float64 `json:"minimum"`
	Maximum *float64 `json:"maximum"`
	Samples int64    `json:"samples"`
}

type Coverage struct {
	From    *time.Time `json:"from"`
	To      *time.Time `json:"to"`
	Samples int64      `json:"samples"`
	Rollups int64      `json:"rollups"`
}

// Summary is the storage-level response consumed by the HTTP and MCP layers.
// Time values are UTC when returned by the SQLite store.
type Summary struct {
	RequestedFrom time.Time `json:"requested_from"`
	RequestedTo   time.Time `json:"requested_to"`
	Coverage      Coverage  `json:"actual_coverage"`

	CPU                 Aggregate `json:"cpu"`
	Memory              Aggregate `json:"memory"`
	Load1               Aggregate `json:"load_1"`
	Load5               Aggregate `json:"load_5"`
	Load15              Aggregate `json:"load_15"`
	NetworkReceiveRate  Aggregate `json:"network_receive_per_sec"`
	NetworkSendRate     Aggregate `json:"network_send_per_sec"`
	DiskReadRate        Aggregate `json:"disk_read_per_sec"`
	DiskWriteRate       Aggregate `json:"disk_write_per_sec"`
	NetworkReceiveBytes *float64  `json:"network_received_bytes"`
	NetworkSendBytes    *float64  `json:"network_sent_bytes"`
	DiskReadBytes       *float64  `json:"disk_read_bytes"`
	DiskWriteBytes      *float64  `json:"disk_write_bytes"`

	MissingData []string  `json:"missing_data"`
	GeneratedAt time.Time `json:"generated_at"`
}

// BucketStart returns the UTC start of the fixed three-minute bucket
// containing t. Buckets are half-open: [start, start+BucketDuration).
func BucketStart(t time.Time) time.Time {
	u := t.UTC()
	seconds := u.Unix()
	width := int64(BucketDuration / time.Second)
	return time.Unix(seconds-seconds%width, 0).UTC()
}

func ValidateRange(from, to, now time.Time) error {
	from, to, now = from.UTC(), to.UTC(), now.UTC()
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return ErrInvalidRange
	}
	if to.After(now) {
		return ErrFutureRange
	}
	d := to.Sub(from)
	if d < MinRange {
		return ErrRangeShort
	}
	if d > MaxRange {
		return ErrRangeLong
	}
	return nil
}
