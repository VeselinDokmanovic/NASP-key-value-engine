package cache

import (
	"container/list"
	"sync"
)

type entry struct {
	key   string
	value []byte
}

type LRUCache struct {
	capacity int
	ll       *list.List
	cache    map[string]*list.Element
	mu       sync.Mutex
}

func NewLRU(cap int) *LRUCache {
	if cap <= 0 {
		cap = 1
	}
	return &LRUCache{
		capacity: cap,
		ll:       list.New(),
		cache:    make(map[string]*list.Element),
	}
}

func (c *LRUCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.cache[key]; ok {
		c.ll.MoveToFront(ele)
		return ele.Value.(*entry).value, true
	}
	return nil, false
}

func (c *LRUCache) Put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.cache[key]; ok {
		ele.Value.(*entry).value = value
		c.ll.MoveToFront(ele)
		return
	}

	ele := c.ll.PushFront(&entry{key: key, value: value})
	c.cache[key] = ele

	if c.ll.Len() > c.capacity {
		old := c.ll.Back()
		if old != nil {
			c.ll.Remove(old)
			delete(c.cache, old.Value.(*entry).key)
		}
	}
}

func (c *LRUCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ele, ok := c.cache[key]; ok {
		c.ll.Remove(ele)
		delete(c.cache, key)
	}
}

func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
