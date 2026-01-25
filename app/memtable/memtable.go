package memtable

import (
	"fmt"
	"time"
)

type Entry struct {
	Key       []byte
	Value     []byte
	Timestamp int64
	Tombstone bool
}

type MemtableConfig struct {
	Type        string
	MaxEntries  int
	MaxMemoryKB int64
}

type Memtable interface {
	Put(key []byte, entry *Entry) bool
	Get(key []byte) (*Entry, bool)
	Delete(key []byte) bool
	IsFull() bool
	GetAllSorted() []*Entry
	Size() int
	MemoryUsage() int
	Clear()
}

func NewMemtable(config MemtableConfig) (Memtable, bool) {
	if config.MaxEntries <= 0 || config.MaxMemoryKB <= 0 {
		fmt.Println("MaxEntries i MaxMemoryKB ne smeju biti 0")
		return nil, false
	}

	switch config.Type {
	case "hashmap":
		return NewHashMapMemtable(config), true
	// case "skiplist":
	// 	return NewSkipListMemtable(config), true
	// case "btree":
	// 	return NewBTreeMemtable(config), true
	default:
		fmt.Println("Pogresan tip strukture!")
		return nil, false
	}

}

func NewMemtableEntry(key []byte, value []byte, tombstone bool) *Entry {
	return &Entry{
		Key:       key,
		Value:     value,
		Timestamp: time.Now().UnixNano(),
		Tombstone: tombstone,
	}
}
