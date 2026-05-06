package ratelimit

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var ErrRateLimitExceeded = errors.New("rate limit exceeded: no tokens available")

const ReservedKeyPrefix = "__sys__"

const TokenBucketKey = ReservedKeyPrefix + "token_bucket"

type Config struct {
	MaxTokens      int    `json:"max_tokens"`
	RefillInterval string `json:"refill_interval"`
}

func DefaultConfig() Config {
	return Config{
		MaxTokens:      100,
		RefillInterval: "1s",
	}
}

type state struct {
	Tokens         int       `json:"tokens"`
	LastRefill     time.Time `json:"last_refill"`
	MaxTokens      int       `json:"max_tokens"`
	RefillInterval string    `json:"refill_interval"`
}

type Storage interface {
	RawGet(key string) ([]byte, bool)
	RawPut(key string, value []byte) error
}

type TokenBucket struct {
	mu       sync.Mutex
	cfg      Config
	interval time.Duration
	tokens   int
	store    Storage
	stop     chan struct{}
}

func New(cfg Config, store Storage) (*TokenBucket, error) {
	interval, err := time.ParseDuration(cfg.RefillInterval)
	if err != nil {
		return nil, err
	}

	tb := &TokenBucket{
		cfg:      cfg,
		interval: interval,
		tokens:   cfg.MaxTokens,
		store:    store,
		stop:     make(chan struct{}),
	}

	tb.load()

	go tb.refillLoop()
	return tb, nil
}

func (tb *TokenBucket) Allow() error {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.tokens <= 0 {
		return ErrRateLimitExceeded
	}
	tb.tokens--
	tb.persist()
	return nil
}

func (tb *TokenBucket) Tokens() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.tokens
}

func (tb *TokenBucket) Stop() {
	close(tb.stop)
}

func (tb *TokenBucket) refillLoop() {
	ticker := time.NewTicker(tb.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tb.mu.Lock()
			tb.tokens = tb.cfg.MaxTokens
			tb.persist()
			tb.mu.Unlock()
		case <-tb.stop:
			return
		}
	}
}

func (tb *TokenBucket) persist() {
	s := state{
		Tokens:         tb.tokens,
		LastRefill:     time.Now(),
		MaxTokens:      tb.cfg.MaxTokens,
		RefillInterval: tb.cfg.RefillInterval,
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}

	_ = tb.store.RawPut(TokenBucketKey, data)
}

func (tb *TokenBucket) load() {
	data, ok := tb.store.RawGet(TokenBucketKey)
	if !ok || len(data) == 0 {
		return
	}

	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}

	if s.MaxTokens != tb.cfg.MaxTokens || s.RefillInterval != tb.cfg.RefillInterval {
		return
	}

	elapsed := time.Since(s.LastRefill)
	refills := int(elapsed / tb.interval)
	restored := s.Tokens + refills*tb.cfg.MaxTokens
	if restored > tb.cfg.MaxTokens {
		restored = tb.cfg.MaxTokens
	}
	tb.tokens = restored
}
