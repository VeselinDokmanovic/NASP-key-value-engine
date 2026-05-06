package memtable

import (
	"bytes"
	"sort"
)

type HashMapMemtable struct {
	data       map[string]*Entry
	size       int
	memoryUsed int64
	cfg        MemtableConfig
	// Mutex?
}

func NewHashMapMemtable(cfg MemtableConfig) *HashMapMemtable {
	return &HashMapMemtable{
		data: make(map[string]*Entry),
		size: 0,
		cfg:  cfg,
	}
}

func (h *HashMapMemtable) Put(key []byte, entry *Entry) bool {
	strKey := string(key)

	if h.IsFull() {
		return false
	}

	if old, exists := h.data[strKey]; exists {
		h.memoryUsed -= int64(len(old.Key) + len(old.Value) + 9)
	} else {
		h.size++
	}

	h.data[strKey] = entry
	h.memoryUsed += int64(len(key) + len(entry.Value) + 9)

	return true
}

func (h *HashMapMemtable) Get(key []byte) (*Entry, bool) {
	entry := h.data[string(key)]
	if entry == nil || entry.Tombstone != 0 {
		return entry, false
	} else {
		return entry, true
	}
}

func (h *HashMapMemtable) Delete(key []byte) bool {
	entry := h.data[string(key)]
	if entry == nil || entry.Tombstone != 0 {
		return false
	} else {
		entry.Tombstone = 1
		entry.Type = EntryTypeDelete
		return true
	}
}

func (h *HashMapMemtable) IsFull() bool {
	if h.size >= h.cfg.MaxEntries {
		return true
	}
	if h.memoryUsed >= h.cfg.MaxMemoryKB*1024 {
		return true
	}
	return false
}

func (h *HashMapMemtable) GetAllSorted() []*Entry {

	result := make([]*Entry, 0, len(h.data))

	for _, entry := range h.data {
		result = append(result, entry)
	}

	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i].Key, result[j].Key) < 0
	})
	return result
}

func (h *HashMapMemtable) Size() int {
	return h.size
}

func (h *HashMapMemtable) MemoryUsage() int {
	return int(h.memoryUsed)
}

func (h *HashMapMemtable) Clear() {
	h.data = make(map[string]*Entry)
	h.size = 0
	h.memoryUsed = 0
}
