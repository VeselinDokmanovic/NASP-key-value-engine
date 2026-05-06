package ratelimit

import "sync"

type MemStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemStore() *MemStore {
	return &MemStore{data: make(map[string][]byte)}
}

func (m *MemStore) RawGet(key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok
}

func (m *MemStore) RawPut(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *MemStore) RawDelete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}
