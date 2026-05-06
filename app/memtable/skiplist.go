package memtable

import (
	"bytes"
	"math/rand"
)

const maxLevel = 16
const probability = 0.5

type skipNode struct {
	entry   *Entry
	forward []*skipNode
}

type SkipListMemtable struct {
	header      *skipNode
	level       int
	size        int
	memoryUsed  int64
	cfg         MemtableConfig
	maxLevel    int
	probability float64
}

func NewSkipListMemtable(cfg MemtableConfig) *SkipListMemtable {
	maxLevel := cfg.MaxLevel
	if maxLevel <= 0 {
		maxLevel = 16
	}
	probability := cfg.Probability
	if probability <= 0 || probability >= 1 {
		probability = 0.5
	}
	return &SkipListMemtable{
		header: &skipNode{
			forward: make([]*skipNode, maxLevel),
		},
		level:       0,
		cfg:         cfg,
		maxLevel:    maxLevel,
		probability: probability,
	}
}

func (s *SkipListMemtable) randomLevel() int {
	level := 0
	for rand.Float64() < s.probability && level < s.maxLevel-1 {
		level++
	}
	return level
}

func (s *SkipListMemtable) Put(key []byte, entry *Entry) bool {
	if s.IsFull() {
		return false
	}

	update := make([]*skipNode, s.maxLevel)
	current := s.header

	for i := s.level; i >= 0; i-- {
		for current.forward[i] != nil && bytes.Compare(current.forward[i].entry.Key, key) < 0 {
			current = current.forward[i]
		}
		update[i] = current
	}

	current = current.forward[0]

	if current != nil && bytes.Equal(current.entry.Key, key) {
		s.memoryUsed -= int64(len(current.entry.Key) + len(current.entry.Value) + 9)
		current.entry = entry
	} else {
		newLevel := s.randomLevel()
		if newLevel > s.level {
			for i := s.level + 1; i <= newLevel; i++ {
				update[i] = s.header
			}
			s.level = newLevel
		}

		newNode := &skipNode{
			entry:   entry,
			forward: make([]*skipNode, newLevel+1),
		}

		for i := 0; i <= newLevel; i++ {
			newNode.forward[i] = update[i].forward[i]
			update[i].forward[i] = newNode
		}
		s.size++
	}

	s.memoryUsed += int64(len(key) + len(entry.Value) + 9)
	return true
}

func (s *SkipListMemtable) Get(key []byte) (*Entry, bool) {
	current := s.header

	for i := s.level; i >= 0; i-- {
		for current.forward[i] != nil && bytes.Compare(current.forward[i].entry.Key, key) < 0 {
			current = current.forward[i]
		}
	}

	current = current.forward[0]

	if current != nil && bytes.Equal(current.entry.Key, key) {
		if current.entry.Tombstone != 0 {
			return current.entry, false
		}
		return current.entry, true
	}

	return nil, false
}

func (s *SkipListMemtable) Delete(key []byte) bool {
	entry, exists := s.Get(key)
	if !exists || entry == nil {
		return false
	}
	entry.Tombstone = 1
	entry.Type = EntryTypeDelete
	return true
}

func (s *SkipListMemtable) IsFull() bool {
	if s.size >= s.cfg.MaxEntries {
		return true
	}
	if s.memoryUsed >= s.cfg.MaxMemoryKB*1024 {
		return true
	}
	return false
}

func (s *SkipListMemtable) GetAllSorted() []*Entry {
	result := make([]*Entry, 0, s.size)
	current := s.header.forward[0]

	for current != nil {
		result = append(result, current.entry)
		current = current.forward[0]
	}

	return result
}

func (s *SkipListMemtable) Size() int {
	return s.size
}

func (s *SkipListMemtable) MemoryUsage() int {
	return int(s.memoryUsed)
}

func (s *SkipListMemtable) Clear() {
	s.header = &skipNode{
		forward: make([]*skipNode, maxLevel),
	}
	s.level = 0
	s.size = 0
	s.memoryUsed = 0
}
