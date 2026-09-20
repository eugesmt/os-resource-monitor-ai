// Package memory contains the explicit, educational memory model used by the
// memory/state learning branch. It deliberately does not auto-promote records
// between layers.
package memory

import (
	"errors"
	"strings"
	"sync"
	"time"
	"uuid"
)

type Layer string

const (
	ShortTerm Layer = "short_term"
	Working   Layer = "working"
	LongTerm  Layer = "long_term"
)

type Record struct {
	ID        string    `json:"id"`
	Layer     Layer     `json:"layer"`
	Scope     string    `json:"scope"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type MemorySelection struct {
	ConversationID string
	TaskID         string
	LongTermScope  string
	IncludeShort   bool
	IncludeWorking bool
	IncludeLong    bool
}

type Context struct {
	ShortTerm []Record `json:"short_term"`
	Working   []Record `json:"working"`
	LongTerm  []Record `json:"long_term"`
}

func (c Context) Empty() bool {
	return len(c.ShortTerm) == 0 && len(c.Working) == 0 && len(c.LongTerm) == 0
}

// Render makes the selected layers visible to an agent prompt. The labels are
// intentionally explicit so callers can inspect exactly which memory was used.
func (c Context) Render() string {
	var builder strings.Builder
	write := func(name string, records []Record) {
		if len(records) == 0 {
			return
		}
		builder.WriteString("[MEMORY ")
		builder.WriteString(name)
		builder.WriteString("]\n")
		for _, record := range records {
			builder.WriteString(record.Kind)
			builder.WriteString(": ")
			builder.WriteString(record.Content)
			builder.WriteByte('\n')
		}
	}
	write(string(ShortTerm), c.ShortTerm)
	write(string(Working), c.Working)
	write(string(LongTerm), c.LongTerm)
	return builder.String()
}

type layerStore struct {
	mu      sync.RWMutex
	records []Record
}

func (s *layerStore) add(record Record) Record {
	s.mu.Lock()
	s.records = append(s.records, record)
	s.mu.Unlock()
	return record
}

func (s *layerStore) list(scope string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		if record.Scope == scope {
			result = append(result, record)
		}
	}
	return result
}

// Model owns physically separate stores for the three memory layers.
type Model struct {
	shortTerm layerStore
	working   layerStore
	longTerm  layerStore
}

func NewModel() *Model { return &Model{} }

func newRecord(layer Layer, scope, kind, content string) (Record, error) {
	if strings.TrimSpace(scope) == "" {
		return Record{}, errors.New("memory scope is empty")
	}
	if strings.TrimSpace(kind) == "" {
		return Record{}, errors.New("memory kind is empty")
	}
	if strings.TrimSpace(content) == "" {
		return Record{}, errors.New("memory content is empty")
	}
	return Record{ID: uuid.NewV4().String(), Layer: layer, Scope: scope, Kind: kind, Content: content, CreatedAt: time.Now().UTC()}, nil
}

// RememberShortTerm stores an event only in the current conversation layer.
func (m *Model) RememberShortTerm(conversationID, kind, content string) (Record, error) {
	record, err := newRecord(ShortTerm, conversationID, kind, content)
	if err != nil {
		return Record{}, err
	}
	return m.shortTerm.add(record), nil
}

// SetWorking stores a task fact/constraint/decision only in working memory.
func (m *Model) SetWorking(taskID, kind, content string) (Record, error) {
	record, err := newRecord(Working, taskID, kind, content)
	if err != nil {
		return Record{}, err
	}
	return m.working.add(record), nil
}

// RememberLongTerm is explicit durable-memory opt-in for profile or knowledge.
func (m *Model) RememberLongTerm(scope, kind, content string) (Record, error) {
	record, err := newRecord(LongTerm, scope, kind, content)
	if err != nil {
		return Record{}, err
	}
	return m.longTerm.add(record), nil
}

// Add hydrates a record loaded from durable storage without changing its ID or timestamp.
func (m *Model) Add(record Record) error {
	if record.ID == "" || record.CreatedAt.IsZero() {
		return errors.New("memory record identity is incomplete")
	}
	if strings.TrimSpace(record.Scope) == "" || strings.TrimSpace(record.Kind) == "" || strings.TrimSpace(record.Content) == "" {
		return errors.New("memory record is incomplete")
	}
	switch record.Layer {
	case ShortTerm:
		m.shortTerm.add(record)
	case Working:
		m.working.add(record)
	case LongTerm:
		m.longTerm.add(record)
	default:
		return errors.New("invalid memory layer")
	}
	return nil
}

func (m *Model) Remove(id string) {
	for _, store := range []*layerStore{&m.shortTerm, &m.working, &m.longTerm} {
		store.mu.Lock()
		for i, record := range store.records {
			if record.ID == id {
				store.records = append(store.records[:i], store.records[i+1:]...)
				break
			}
		}
		store.mu.Unlock()
	}
}

// BuildContext selects memory explicitly; no layer is included by default.
func (m *Model) BuildContext(selection MemorySelection) Context {
	var context Context
	if selection.IncludeShort {
		context.ShortTerm = m.shortTerm.list(selection.ConversationID)
	}
	if selection.IncludeWorking {
		context.Working = m.working.list(selection.TaskID)
	}
	if selection.IncludeLong {
		context.LongTerm = m.longTerm.list(selection.LongTermScope)
	}
	return context
}
