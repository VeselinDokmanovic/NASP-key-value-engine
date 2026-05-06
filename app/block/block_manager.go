package block

import (
	"container/list"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	PageSize  int
	BlockSize int
	CacheSize int
}

type BlockManager struct {
	cfg   Config
	cache *BlockCache
}

func NewBlockManager(cfg Config) (*BlockManager, error) {
	if cfg.BlockSize%cfg.PageSize != 0 {
		return nil, errors.New("BlockSize mora biti umnozak PageSize")
	}
	if cfg.CacheSize <= 0 {
		return nil, errors.New("CacheSize mora biti vece od 0")
	}

	return &BlockManager{
		cfg:   cfg,
		cache: NewBlockCache(cfg.CacheSize),
	}, nil
}

type cacheEntry struct {
	key   string
	value []byte
}

type BlockCache struct {
	size  int
	list  *list.List
	items map[string]*list.Element
}

func NewBlockCache(size int) *BlockCache {
	return &BlockCache{
		size:  size,
		list:  list.New(),
		items: make(map[string]*list.Element),
	}
}

func (c *BlockCache) get(key string) ([]byte, bool) {
	if el, ok := c.items[key]; ok {
		c.list.MoveToFront(el)
		return el.Value.(*cacheEntry).value, true
	}
	return nil, false
}

func (c *BlockCache) put(key string, value []byte) {
	if el, ok := c.items[key]; ok {
		el.Value.(*cacheEntry).value = value
		c.list.MoveToFront(el)
		return
	}

	if c.list.Len() >= c.size {
		back := c.list.Back()
		if back != nil {
			delete(c.items, back.Value.(*cacheEntry).key)
			c.list.Remove(back)
		}
	}

	el := c.list.PushFront(&cacheEntry{key: key, value: value})
	c.items[key] = el
}

func (bm *BlockManager) ReadBlock(filename string, blockNum int) ([]byte, error) {
	key := fmt.Sprintf("%s:%d", filename, blockNum)

	if data, ok := bm.cache.get(key); ok {
		return data, nil
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	offset := int64(blockNum * bm.cfg.BlockSize)
	if offset >= info.Size() {
		return nil, errors.New("Zadati broj bloka nije u fajlu")
	}

	buf := make([]byte, bm.cfg.BlockSize)
	n, err := file.ReadAt(buf, offset)
	if err != nil && n == 0 {
		return nil, err
	}

	buf = buf[:n]

	bm.cache.put(key, buf)
	return buf, nil
}

func (bm *BlockManager) GetBlockSize() int {
	return bm.cfg.BlockSize
}

func (bm *BlockManager) WriteBlock(filename string, blockNum int, data []byte) error {
	if len(data) > bm.cfg.BlockSize {
		return errors.New("Veličina podataka je veća od bloka")
	}

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	offset := int64(blockNum * bm.cfg.BlockSize)

	block := make([]byte, bm.cfg.BlockSize)
	copy(block, data)

	_, err = file.WriteAt(block, offset)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s:%d", filename, blockNum)
	bm.cache.put(key, block)

	return nil
}
