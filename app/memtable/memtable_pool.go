package memtable

import (
	"errors"
	// "time"
)

type MemtablePool struct {
	tables      []Memtable
	activeIndex int
	maxTables   int
	cfg         MemtableConfig
}

func NewMemtablePool(n int, cfg MemtableConfig) (*MemtablePool, bool) {
	if n < 1 {
		n = 1
	}

	pool := &MemtablePool{
		tables:      make([]Memtable, n),
		activeIndex: 0,
		maxTables:   n,
		cfg:         cfg,
	}

	// Kreiraj prvu tabelu
	table, ok := NewMemtable(cfg)
	if !ok {
		return nil, false
	}
	pool.tables[0] = table

	return pool, true
}

func (p *MemtablePool) Put(key, value []byte) error {
	entry := NewMemtableEntry(key, value, 0, EntryTypeData)

	// Pokušaj dodati u aktivnu tabelu
	if p.tables[p.activeIndex].Put(key, entry) {
		return nil
	}

	// Aktivna tabela je puna, rotacija
	if err := p.rotate(); err != nil {
		return err
	}

	// Pokušaj ponovo sa novom tabelom
	if !p.tables[p.activeIndex].Put(key, entry) {
		return errors.New("failed to put after rotation")
	}

	return nil
}

func (p *MemtablePool) PutWithTimestamp(key, value []byte, timestamp int64, tombstone byte, entryType byte) error {
	entry := &Entry{
		Key:       key,
		Value:     value,
		Timestamp: timestamp,
		Tombstone: tombstone,
		Type:      entryType,
	}

	if p.tables[p.activeIndex].Put(key, entry) {
		return nil
	}

	if err := p.rotate(); err != nil {
		return err
	}

	if !p.tables[p.activeIndex].Put(key, entry) {
		return errors.New("failed to put after rotation")
	}

	return nil
}

func (p *MemtablePool) Get(key []byte) (*Entry, bool) {
	// Prvo aktivna tabela
	if entry, found := p.tables[p.activeIndex].Get(key); found {
		return entry, true
	}

	// Zatim read-only tabele (od najnovije ka najstarijoj)
	for i := p.activeIndex - 1; i >= 0; i-- {
		if p.tables[i] == nil {
			continue
		}
		if entry, found := p.tables[i].Get(key); found {
			return entry, true
		}
	}

	return nil, false
}

func (p *MemtablePool) Delete(key []byte) error {
	entry := NewMemtableEntry(key, nil, 1, EntryTypeDelete)

	if p.tables[p.activeIndex].Put(key, entry) {
		return nil
	}

	if err := p.rotate(); err != nil {
		return err
	}

	if !p.tables[p.activeIndex].Put(key, entry) {
		return errors.New("failed to delete after rotation")
	}

	return nil
}

func (p *MemtablePool) rotate() error {
	// Ako smo popunili sve N tabele, vreme je za flush
	if p.activeIndex >= p.maxTables-1 {
		return errors.New("all memtables full - flush required")
	}

	// Kreiraj novu aktivnu tabelu
	p.activeIndex++
	table, ok := NewMemtable(p.cfg)
	if !ok {
		return errors.New("failed to create new memtable")
	}
	p.tables[p.activeIndex] = table

	return nil
}

func (p *MemtablePool) ShouldFlush() bool {
	return p.activeIndex >= p.maxTables-1 && p.tables[p.activeIndex].IsFull()
}

func (p *MemtablePool) GetAllForFlush() []*Entry {
	allEntries := make([]*Entry, 0)

	for i := 0; i <= p.activeIndex; i++ {
		if p.tables[i] == nil {
			continue
		}
		entries := p.tables[i].GetAllSorted()
		allEntries = append(allEntries, entries...)
	}

	return allEntries
}

func (p *MemtablePool) Clear() {
	p.tables = make([]Memtable, p.maxTables)
	table, _ := NewMemtable(p.cfg)
	p.tables[0] = table
	p.activeIndex = 0
}

func (p *MemtablePool) Size() int {
	total := 0
	for i := 0; i <= p.activeIndex; i++ {
		if p.tables[i] != nil {
			total += p.tables[i].Size()
		}
	}
	return total
}

func (p *MemtablePool) MemoryUsage() int {
	total := 0
	for i := 0; i <= p.activeIndex; i++ {
		if p.tables[i] != nil {
			total += p.tables[i].MemoryUsage()
		}
	}
	return total
}
