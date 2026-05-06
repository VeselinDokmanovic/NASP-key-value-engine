package engine

import (
	"errors"
	"kv-engine/cache"
	"sync"
)

type Engine struct {
	mu    sync.RWMutex
	data  map[string][]byte
	cache *cache.LRUCache
}

func NewEngine(cacheSize int) *Engine {
	return &Engine{
		data:  make(map[string][]byte),
		cache: cache.NewLRU(cacheSize),
	}
}

func (e *Engine) Put(key string, value []byte) {
	e.mu.Lock()
	e.data[key] = value
	e.mu.Unlock()

	e.cache.Put(key, value)
}

func (e *Engine) Get(key string) ([]byte, error) {
	// 1.Cache
	if val, ok := e.cache.Get(key); ok {
		return val, nil
	}

	// 2.Storage
	e.mu.RLock()
	val, ok := e.data[key]
	e.mu.RUnlock()

	if !ok {
		return nil, errors.New("not found")
	}

	// 3.Ubaci u cache
	e.cache.Put(key, val)

	return val, nil
}

func (e *Engine) Delete(key string) {
	e.mu.Lock()
	delete(e.data, key)
	e.mu.Unlock()

	e.cache.Delete(key)
}
