package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	wal "key-value-engine/app/Wal"
	"key-value-engine/app/block"
	"key-value-engine/app/cache"
	"key-value-engine/app/memtable"
	"key-value-engine/app/ratelimit"
	"key-value-engine/app/sstable"
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
	BloomFilter      *sstable.BloomFilter
	sstableLoaded    bool
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
		config.WAL.Block.CacheSize,
	)
	if err != nil {
		return nil, err
	}

	sst := sstable.NewSSTable(sstable.SSTableConfig{
		ID:               1,
		Dir:              sstableDir,
		BlockManager:     blockManager,
		CompressionLevel: 0,
		SummaryStep:      config.SSTable.Summary.Step,
	})

	lruSize := config.BlockManager.DefaultConfig.CacheSize
	if lruSize <= 0 {
		lruSize = 1
	}

	bloomExpected := config.SSTable.Bloom.ExpectedElements
	if bloomExpected <= 0 {
		bloomExpected = config.Memtable.HashMap.MaxEntries
	}

	return &Engine{
		WAL:              walEngine,
		HashMap:          hashMap,
		SkipList:         skipList,
		BTree:            btree,
		MemtablePool:     memtablePool,
		BlockManager:     blockManager,
		LRUCache:         cache.NewLRU(lruSize),
		MemStore:         memStore,
		TokenBucket:      tokenBucket,
		RateLimitedStore: ratelimit.NewRateLimitedStore(memStore, tokenBucket),
		SSTable:          sst,
		BloomFilter:      sstable.NewBloomFilter(bloomExpected, config.SSTable.Bloom.FalsePositiveRate),
	}, nil
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

func (e *Engine) ensureSSTableLoaded() error {
	if e == nil || e.SSTable == nil || e.sstableLoaded {
		return nil
	}

	if err := e.SSTable.Read(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	e.sstableLoaded = true
	return nil
}

func (e *Engine) Get(key []byte) ([]byte, bool, error) {
	if e == nil {
		return nil, false, errors.New("engine is nil")
	}

	if e.MemtablePool != nil {
		if entry, found := e.MemtablePool.Get(key); found {
			if e.LRUCache != nil && entry != nil && entry.Value != nil {
				e.LRUCache.Put(string(key), append([]byte(nil), entry.Value...))
			}
			return entry.Value, true, nil
		}
	}

	if e.LRUCache != nil {
		if value, found := e.LRUCache.Get(string(key)); found {
			return value, true, nil
		}
	}

	if e.SSTable == nil {
		return nil, false, nil
	}

	if err := e.ensureSSTableLoaded(); err != nil {
		return nil, false, err
	}
	if !e.sstableLoaded {
		return nil, false, nil
	}

	value, found, err := e.SSTable.Search(key)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	if value == nil {
		if e.LRUCache != nil {
			e.LRUCache.Delete(string(key))
		}
		return nil, false, nil
	}

	if e.LRUCache != nil {
		e.LRUCache.Put(string(key), append([]byte(nil), value...))
	}

	return value, true, nil
}

func (e *Engine) Close() {
	if e == nil {
		return
	}
	if e.TokenBucket != nil {
		e.TokenBucket.Stop()
	}
}
