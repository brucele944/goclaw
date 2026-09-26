package providerresolve

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// fakeProviderStore is a ProviderStore test double that models exactly what
// provider resolution reads and writes: provider rows (settings.fallback_chain)
// and the durable cooldown/health state (provider_health, migration 000100).
// Its cooldown semantics mirror the real stores: RecordProviderFailure stores the
// deadline without touching the probe stamp, MarkProviderProbe stamps the probe
// without touching the deadline, RecordProviderSuccess clears the deadline and
// keeps the error-class history.
type fakeProviderStore struct {
	mu     sync.Mutex
	rows   map[string]*store.LLMProviderData
	health map[uuid.UUID]*store.ProviderHealth

	failures []fakeCooldownWrite
	probes   int
	clears   int
}

type fakeCooldownWrite struct {
	providerID    uuid.UUID
	errorClass    string
	cooldownUntil time.Time
}

func newFakeProviderStore() *fakeProviderStore {
	return &fakeProviderStore{
		rows:   map[string]*store.LLMProviderData{},
		health: map[uuid.UUID]*store.ProviderHealth{},
	}
}

// addProvider registers a provider row with the given settings JSON.
func (s *fakeProviderStore) addProvider(name string, settings string) *store.LLMProviderData {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()},
		Name:      name,
		Enabled:   true,
		Settings:  []byte(settings),
	}
	s.rows[name] = row
	return row
}

func (s *fakeProviderStore) GetProviderByName(_ context.Context, name string) (*store.LLMProviderData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[name]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return row, nil
}

func (s *fakeProviderStore) GetProviderHealth(_ context.Context, providerID uuid.UUID) (*store.ProviderHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.health[providerID]
	if !ok {
		return store.NewProviderHealth(providerID), nil
	}
	out := *row
	return &out, nil
}

func (s *fakeProviderStore) RecordProviderFailure(_ context.Context, providerID uuid.UUID, errorClass string, cooldownUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.healthRowLocked(providerID)
	class := store.NormalizeErrorClass(errorClass)
	until := cooldownUntil
	row.ConsecutiveFailures++
	row.CooldownUntil = &until
	row.LastErrorClass = class
	row.UpdatedAt = time.Now().UTC()
	row.ErrorCounts[class]++
	s.failures = append(s.failures, fakeCooldownWrite{providerID: providerID, errorClass: class, cooldownUntil: cooldownUntil})
	return nil
}

func (s *fakeProviderStore) RecordProviderSuccess(_ context.Context, providerID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.healthRowLocked(providerID)
	row.ConsecutiveFailures = 0
	row.CooldownUntil = nil
	row.UpdatedAt = time.Now().UTC()
	s.clears++
	return nil
}

func (s *fakeProviderStore) MarkProviderProbe(_ context.Context, providerID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.healthRowLocked(providerID)
	now := time.Now().UTC()
	row.LastProbeAt = &now
	row.UpdatedAt = now
	s.probes++
	return nil
}

func (s *fakeProviderStore) ResetProviderHealth(_ context.Context, providerID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.health, providerID)
	return nil
}

func (s *fakeProviderStore) healthRowLocked(providerID uuid.UUID) *store.ProviderHealth {
	row, ok := s.health[providerID]
	if !ok {
		row = store.NewProviderHealth(providerID)
		s.health[providerID] = row
	}
	return row
}

// lastFailure returns the most recent persisted failure write.
func (s *fakeProviderStore) lastFailure() (fakeCooldownWrite, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failures) == 0 {
		return fakeCooldownWrite{}, false
	}
	return s.failures[len(s.failures)-1], true
}

func (s *fakeProviderStore) probeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}

// --- remaining ProviderStore surface: not exercised by provider resolution ---

func (s *fakeProviderStore) CreateProvider(context.Context, *store.LLMProviderData) error { return nil }
func (s *fakeProviderStore) GetProvider(context.Context, uuid.UUID) (*store.LLMProviderData, error) {
	return nil, sql.ErrNoRows
}
func (s *fakeProviderStore) ListProviders(context.Context) ([]store.LLMProviderData, error) {
	return nil, nil
}
func (s *fakeProviderStore) ListAllProviders(context.Context) ([]store.LLMProviderData, error) {
	return nil, nil
}
func (s *fakeProviderStore) UpdateProvider(context.Context, uuid.UUID, map[string]any) error {
	return nil
}
func (s *fakeProviderStore) DeleteProvider(context.Context, uuid.UUID) error { return nil }
func (s *fakeProviderStore) ListModels(context.Context, uuid.UUID) ([]store.LLMModel, error) {
	return nil, nil
}
func (s *fakeProviderStore) UpsertModels(context.Context, uuid.UUID, []store.LLMModel) error {
	return nil
}
func (s *fakeProviderStore) SetModelEnabled(context.Context, uuid.UUID, string, bool) error {
	return nil
}
func (s *fakeProviderStore) ListQuirks(context.Context, string) ([]store.ProviderQuirk, error) {
	return nil, nil
}
