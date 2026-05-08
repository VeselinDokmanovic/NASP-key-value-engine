package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"key-value-engine/app/block"
	"key-value-engine/app/cache"
	"key-value-engine/app/memtable"
	"key-value-engine/app/ratelimit"
	"key-value-engine/app/sstable"
	wal "key-value-engine/app/wal"
)

type engineConfig struct {
	WAL struct {
		Block struct {
			BlockFactor int64 `json:"block_factor"`
			BlockSize   int64 `json:"block_size"`
			CacheSize   int64 `json:"cache_size"`
		} `json:"block"`
	} `json:"wal"`
	SSTable struct {
		Summary struct {
			Step int `json:"step"`
		} `json:"summary"`
		Bloom struct {
			ExpectedElements  int     `json:"expected_elements"`
			FalsePositiveRate float64 `json:"false_positive_rate"`
		} `json:"bloom"`
	} `json:"sstable"`
	Memtable struct {
		HashMap struct {
			MaxEntries  int   `json:"max_entries"`
			MaxMemoryKB int64 `json:"max_memory_kb"`
			BTreeOrder  int   `json:"btree_order"`
		} `json:"hashmap"`
		Pool struct {
			Type string `json:"type"`
			Size int    `json:"size"`
		} `json:"pool"`
		SkipList struct {
			MaxLevel    int     `json:"max_level"`
			Probability float64 `json:"probability"`
		} `json:"skiplist"`
	} `json:"memtable"`
	Ratelimit struct {
		MaxTokens      int    `json:"max_tokens"`
		RefillInterval string `json:"refill_interval"`
	} `json:"ratelimit"`
	BlockManager struct {
		DefaultConfig struct {
			PageSize  int `json:"page_size"`
			BlockSize int `json:"block_size"`
			CacheSize int `json:"cache_size"`
		} `json:"default_config"`
	} `json:"block_manager"`
}

type Engine struct {
	WAL              *wal.WAL
	HashMap          *memtable.HashMapMemtable
	SkipList         *memtable.SkipListMemtable
	BTree            *memtable.BTreeMemtable
	MemtablePool     *memtable.MemtablePool
	BlockManager     *block.BlockManager
	LRUCache         *cache.LRUCache
	MemStore         *ratelimit.MemStore
	TokenBucket      *ratelimit.TokenBucket
	RateLimitedStore *ratelimit.RateLimitedStore
	SSTable          *sstable.SSTable
	SSTables         []*sstable.SSTable
	BloomFilter      *sstable.BloomFilter
}

func NewEngine() (*Engine, error) {
	dataDir := "./data"
	configPath := filepath.Join(dataDir, "config.json")
	walDir := filepath.Join(dataDir, "wal_logs")
	sstableDir := filepath.Join(dataDir, "sstables")

	config, err := loadEngineConfig(configPath)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sstableDir, 0o755); err != nil {
		return nil, err
	}

	memCfg := memtable.MemtableConfig{
		Type:        config.Memtable.Pool.Type,
		MaxEntries:  config.Memtable.HashMap.MaxEntries,
		MaxMemoryKB: config.Memtable.HashMap.MaxMemoryKB,
		BTreeOrder:  config.Memtable.HashMap.BTreeOrder,
		MaxLevel:    config.Memtable.SkipList.MaxLevel,
		Probability: config.Memtable.SkipList.Probability,
	}

	hashMap := memtable.NewHashMapMemtable(memCfg)
	skipList := memtable.NewSkipListMemtable(memCfg)
	btree := memtable.NewBTreeMemtable(memCfg)

	memtablePool, ok := memtable.NewMemtablePool(config.Memtable.Pool.Size, memCfg)
	if !ok {
		return nil, errors.New("failed to initialize memtable pool")
	}

	blockManager, err := block.NewBlockManager(block.Config{
		PageSize:  config.BlockManager.DefaultConfig.PageSize,
		BlockSize: config.BlockManager.DefaultConfig.BlockSize,
		CacheSize: config.BlockManager.DefaultConfig.CacheSize,
	})
	if err != nil {
		return nil, err
	}

	memStore := ratelimit.NewMemStore()
	tokenBucket, err := ratelimit.New(ratelimit.Config{
		MaxTokens:      config.Ratelimit.MaxTokens,
		RefillInterval: config.Ratelimit.RefillInterval,
	}, memStore)
	if err != nil {
		return nil, err
	}

	walEngine, err := wal.WALInitConfig(
		walDir,
		config.WAL.Block.BlockSize,
		config.WAL.Block.BlockFactor,
		blockManager,
	)
	if err != nil {
		return nil, err
	}

	sstables, err := loadExistingSSTables(sstableDir, blockManager)
	if err != nil {
		return nil, err
	}

	var latestSSTable *sstable.SSTable
	if len(sstables) > 0 {
		latestSSTable = sstables[len(sstables)-1]
	}

	lruSize := config.BlockManager.DefaultConfig.CacheSize
	if lruSize <= 0 {
		lruSize = 1
	}

	bloomExpected := config.SSTable.Bloom.ExpectedElements
	if bloomExpected <= 0 {
		bloomExpected = config.Memtable.HashMap.MaxEntries
	}

	e := &Engine{
		WAL:          walEngine,
		HashMap:      hashMap,
		SkipList:     skipList,
		BTree:        btree,
		MemtablePool: memtablePool,
		BlockManager: blockManager,
		LRUCache:     cache.NewLRU(lruSize),
		MemStore:     memStore,
		TokenBucket:  tokenBucket,
		SSTable:      latestSSTable,
		SSTables:     sstables,
		BloomFilter:  sstable.NewBloomFilter(bloomExpected, config.SSTable.Bloom.FalsePositiveRate),
	}

	e.RateLimitedStore = ratelimit.NewRateLimitedStore(e, tokenBucket)

	if err := walEngine.InsertIntoMemtable(e.MemtablePool); err != nil {
		if strings.Contains(err.Error(), "flush required") {
			if ferr := e.flushMemtablesToSSTable(); ferr != nil {
				return nil, fmt.Errorf("flush during wal replay failed: %w", ferr)
			}
			if rerr := walEngine.InsertIntoMemtable(e.MemtablePool); rerr != nil {
				return nil, fmt.Errorf("wal replay failed after flush: %w", rerr)
			}
		} else {
			return nil, fmt.Errorf("wal replay failed: %w", err)
		}
	}

	return e, nil
}

func loadExistingSSTables(dir string, blockManager *block.BlockManager) ([]*sstable.SSTable, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "table-*.data"))
	if err != nil {
		return nil, fmt.Errorf("scan sstables: %w", err)
	}

	type tableInfo struct {
		id int64
		st *sstable.SSTable
	}

	tables := make([]tableInfo, 0, len(matches))
	for _, match := range matches {
		base := filepath.Base(match)
		num := strings.TrimSuffix(strings.TrimPrefix(base, "table-"), ".data")
		id, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			continue
		}

		st := sstable.NewSSTable(sstable.SSTableConfig{
			ID:           id,
			Dir:          dir,
			BlockManager: blockManager,
			SummaryStep:  0,
		})
		if err := st.Read(); err != nil {
			return nil, fmt.Errorf("load sstable %d: %w", id, err)
		}

		tables = append(tables, tableInfo{id: id, st: st})
	}

	sort.Slice(tables, func(i, j int) bool { return tables[i].id < tables[j].id })

	result := make([]*sstable.SSTable, 0, len(tables))
	for _, table := range tables {
		result = append(result, table.st)
	}

	return result, nil
}

func loadEngineConfig(path string) (*engineConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var config engineConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if config.Memtable.Pool.Size <= 0 {
		config.Memtable.Pool.Size = 1
	}
	if config.Memtable.Pool.Type == "" {
		config.Memtable.Pool.Type = "hashmap"
	}
	if config.Memtable.HashMap.MaxEntries <= 0 {
		return nil, errors.New("memtable.hashmap.max_entries must be greater than 0")
	}
	if config.Memtable.HashMap.MaxMemoryKB <= 0 {
		return nil, errors.New("memtable.hashmap.max_memory_kb must be greater than 0")
	}
	if config.Memtable.HashMap.BTreeOrder <= 0 {
		config.Memtable.HashMap.BTreeOrder = 3
	}
	if config.Memtable.SkipList.MaxLevel <= 0 {
		config.Memtable.SkipList.MaxLevel = 16
	}
	if config.Memtable.SkipList.Probability <= 0 || config.Memtable.SkipList.Probability >= 1 {
		config.Memtable.SkipList.Probability = 0.5
	}
	if config.SSTable.Summary.Step <= 0 {
		config.SSTable.Summary.Step = 4
	}
	if config.SSTable.Bloom.ExpectedElements <= 0 {
		config.SSTable.Bloom.ExpectedElements = config.Memtable.HashMap.MaxEntries
	}
	if config.SSTable.Bloom.FalsePositiveRate <= 0 {
		config.SSTable.Bloom.FalsePositiveRate = 0.01
	}
	if config.Ratelimit.MaxTokens <= 0 {
		config.Ratelimit.MaxTokens = 100
	}
	if config.Ratelimit.RefillInterval == "" {
		config.Ratelimit.RefillInterval = "1s"
	}
	if config.BlockManager.DefaultConfig.PageSize <= 0 {
		config.BlockManager.DefaultConfig.PageSize = 4096
	}
	if config.BlockManager.DefaultConfig.BlockSize <= 0 {
		config.BlockManager.DefaultConfig.BlockSize = config.BlockManager.DefaultConfig.PageSize
	}
	if config.BlockManager.DefaultConfig.CacheSize <= 0 {
		config.BlockManager.DefaultConfig.CacheSize = 1
	}
	if config.WAL.Block.BlockSize <= 0 {
		config.WAL.Block.BlockSize = 2048
	}
	if config.WAL.Block.BlockFactor <= 0 {
		config.WAL.Block.BlockFactor = 5
	}
	if config.WAL.Block.CacheSize <= 0 {
		config.WAL.Block.CacheSize = 16
	}

	return &config, nil
}

func (e *Engine) Put(key []byte, value []byte) error {
	if e == nil {
		return errors.New("engine is nil")
	}
	if len(key) == 0 {
		return errors.New("key cannot be empty")
	}

	_, err := e.WAL.Append(string(key), string(value), false)
	if err != nil {
		return fmt.Errorf("wal append failed: %w", err)
	}

	if err := e.MemtablePool.Put(key, value); err != nil {
		if strings.Contains(err.Error(), "flush required") {
			if ferr := e.flushMemtablesToSSTable(); ferr != nil {
				return fmt.Errorf("flush failed: %w", ferr)
			}

			if err2 := e.MemtablePool.Put(key, value); err2 != nil {
				return fmt.Errorf("memtable put failed after flush: %w", err2)
			}

			return nil
		}

		return fmt.Errorf("memtable put failed: %w", err)
	}

	e.LRUCache.Put(string(key), value)
	return nil
}

func (e *Engine) flushMemtablesToSSTable() error {
	if e == nil {
		return errors.New("engine is nil")
	}
	if e.MemtablePool == nil {
		return errors.New("memtable pool is nil")
	}

	entries := e.MemtablePool.GetAllForFlush()
	if len(entries) == 0 {
		return nil
	}

	dir := "./data/sstables"
	if e.SSTable != nil && e.SSTable.Dir != "" {
		dir = e.SSTable.Dir
	}

	pattern := filepath.Join(dir, "table-*.data")
	matches, _ := filepath.Glob(pattern)

	maxID := int64(0)
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, "table-") && strings.HasSuffix(base, ".data") {
			num := strings.TrimSuffix(strings.TrimPrefix(base, "table-"), ".data")
			if id, err := strconv.ParseInt(num, 10, 64); err == nil {
				if id > maxID {
					maxID = id
				}
			}
		}
	}

	nextID := maxID + 1

	newSt := sstable.NewSSTable(sstable.SSTableConfig{
		ID:           nextID,
		Dir:          dir,
		BlockManager: e.BlockManager,
		SummaryStep:  0,
	})

	sstEntries := make([]*sstable.Entry, 0, len(entries))
	var flushTs int64 = 0
	for _, me := range entries {
		sstEntries = append(sstEntries, &sstable.Entry{
			Timestamp: me.Timestamp,
			Tombstone: me.Tombstone,
			Type:      me.Type,
			Key:       me.Key,
			Value:     me.Value,
		})
		if me.Timestamp > flushTs {
			flushTs = me.Timestamp
		}
	}

	if err := newSt.Write(sstEntries); err != nil {
		return fmt.Errorf("sstable write failed: %w", err)
	}

	if err := newSt.Read(); err != nil {
		return fmt.Errorf("sstable read metadata failed: %w", err)
	}

	if bf, err := sstable.LoadBloomFilter(newSt.FilterPath); err == nil {
		e.BloomFilter = bf
	}

	e.SSTable = newSt
	e.SSTables = append(e.SSTables, newSt)

	e.MemtablePool.Clear()

	if e.WAL != nil {
		if err := e.WAL.Flush(); err != nil {
			return fmt.Errorf("wal flush failed: %w", err)
		}
		if err := e.WAL.DeleteSegmentsBeforeTimestamp(flushTs); err != nil {
			fmt.Printf("wal delete failed: %v\n", err)
		}
	}

	return nil
}

func (e *Engine) Delete(key []byte) error {
	if e == nil {
		return errors.New("engine is nil")
	}
	if len(key) == 0 {
		return errors.New("key cannot be empty")
	}

	_, err := e.WAL.Append(string(key), "", true)
	if err != nil {
		return fmt.Errorf("wal delete failed: %w", err)
	}

	if err := e.MemtablePool.Delete(key); err != nil {
		if strings.Contains(err.Error(), "flush required") {
			if ferr := e.flushMemtablesToSSTable(); ferr != nil {
				return fmt.Errorf("flush failed: %w", ferr)
			}

			if err2 := e.MemtablePool.Delete(key); err2 != nil {
				return fmt.Errorf("memtable delete failed after flush: %w", err2)
			}

			return nil
		}

		return fmt.Errorf("memtable delete failed: %w", err)
	}

	e.LRUCache.Delete(string(key))
	return nil
}

func (e *Engine) Get(key []byte) ([]byte, error) {
	if e == nil {
		return nil, errors.New("engine is nil")
	}
	if len(key) == 0 {
		return nil, errors.New("key cannot be empty")
	}

	entry, found := e.MemtablePool.Get(key)
	if found && entry != nil {
		if entry.Tombstone == 0 {
			return entry.Value, nil
		}
		return nil, errors.New("key not found")
	}

	keyStr := string(key)
	if cachedValue, found := e.LRUCache.Get(keyStr); found {
		return cachedValue, nil
	}

	for i := len(e.SSTables) - 1; i >= 0; i-- {
		table := e.SSTables[i]
		if table == nil || !table.MightContain(key) {
			continue
		}

		value, found, err := table.Search(key)
		if err != nil {
			continue
		}
		if found {
			if value != nil {
				e.LRUCache.Put(keyStr, value)
				return value, nil
			}
			return nil, errors.New("key not found")
		}
	}

	return nil, errors.New("key not found")
}

func (e *Engine) Close() {
	if e == nil {
		return
	}
	if err := e.flushMemtablesToSSTable(); err != nil {
		fmt.Printf("flush on close failed: %v\n", err)
	}
	if e.WAL != nil {
		if err := e.WAL.Flush(); err != nil {
			fmt.Printf("wal flush on close failed: %v\n", err)
		}
	}
	if e.TokenBucket != nil {
		e.TokenBucket.Stop()
	}
}

func (e *Engine) RawGet(key string) ([]byte, bool) {
	val, err := e.Get([]byte(key))
	return val, err == nil
}

func (e *Engine) RawPut(key string, value []byte) error {
	return e.Put([]byte(key), value)
}

func (e *Engine) RawDelete(key string) error {
	return e.Delete([]byte(key))
}
