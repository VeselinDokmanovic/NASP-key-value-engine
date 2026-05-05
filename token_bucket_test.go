package ratelimit

import (
	"errors"
	"testing"
	"time"
)

// TokenBucket testovi

func newBucket(t *testing.T, max int, interval string) (*TokenBucket, *MemStore) {
	t.Helper()
	store := NewMemStore()
	cfg := Config{MaxTokens: max, RefillInterval: interval}
	tb, err := New(cfg, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tb, store
}

func TestAllow_ConsumesTokens(t *testing.T) {
	tb, _ := newBucket(t, 3, "10s")
	defer tb.Stop()

	for i := 0; i < 3; i++ {
		if err := tb.Allow(); err != nil {
			t.Fatalf("Allow #%d unexpected error: %v", i+1, err)
		}
	}
	if tb.Tokens() != 0 {
		t.Fatalf("expected 0 tokens, got %d", tb.Tokens())
	}
}

func TestAllow_ReturnsErrorWhenEmpty(t *testing.T) {
	tb, _ := newBucket(t, 1, "10s")
	defer tb.Stop()

	_ = tb.Allow()
	err := tb.Allow()
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestRefill_ReplenishesTokens(t *testing.T) {
	tb, _ := newBucket(t, 5, "50ms")
	defer tb.Stop()

	for i := 0; i < 5; i++ {
		_ = tb.Allow()
	}
	if tb.Tokens() != 0 {
		t.Fatal("bucket should be empty")
	}

	time.Sleep(100 * time.Millisecond)

	if tb.Tokens() == 0 {
		t.Fatal("bucket should have been refilled")
	}
}

func TestPersistAndRestore(t *testing.T) {
	store := NewMemStore()
	cfg := Config{MaxTokens: 10, RefillInterval: "10s"}

	tb1, err := New(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_ = tb1.Allow()
	}
	remaining := tb1.Tokens()
	tb1.Stop()

	tb2, err := New(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer tb2.Stop()

	if tb2.Tokens() < remaining {
		t.Fatalf("restored tokens %d < expected %d", tb2.Tokens(), remaining)
	}
}

// Middleware testovi

func newMiddleware(t *testing.T, max int) (*RateLimitedStore, *TokenBucket) {
	t.Helper()
	store := NewMemStore()
	cfg := Config{MaxTokens: max, RefillInterval: "10s"}
	tb, err := New(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	return NewRateLimitedStore(store, tb), tb
}

func TestMiddleware_PutAndGet(t *testing.T) {
	rl, tb := newMiddleware(t, 10)
	defer tb.Stop()

	if err := rl.Put("hello", []byte("world")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	val, ok, err := rl.Get("hello")
	if err != nil || !ok || string(val) != "world" {
		t.Fatalf("Get: val=%q ok=%v err=%v", val, ok, err)
	}
}

func TestMiddleware_Delete(t *testing.T) {
	rl, tb := newMiddleware(t, 10)
	defer tb.Stop()

	_ = rl.Put("k", []byte("v"))
	if err := rl.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok, _ := rl.Get("k")
	if ok {
		t.Fatal("key should have been deleted")
	}
}

func TestMiddleware_BlocksReservedKeys(t *testing.T) {
	rl, tb := newMiddleware(t, 10)
	defer tb.Stop()

	cases := []string{
		"__sys__token_bucket",
		"__sys__anything",
		"__sys__",
	}
	for _, key := range cases {
		if err := rl.Put(key, []byte("x")); !errors.Is(err, ErrReservedKey) {
			t.Errorf("Put(%q): expected ErrReservedKey, got %v", key, err)
		}
		if _, _, err := rl.Get(key); !errors.Is(err, ErrReservedKey) {
			t.Errorf("Get(%q): expected ErrReservedKey, got %v", key, err)
		}
		if err := rl.Delete(key); !errors.Is(err, ErrReservedKey) {
			t.Errorf("Delete(%q): expected ErrReservedKey, got %v", key, err)
		}
	}
}

func TestMiddleware_RateLimitsOperations(t *testing.T) {
	rl, tb := newMiddleware(t, 2)
	defer tb.Stop()

	_ = rl.Put("a", []byte("1")) // token 1
	_, _, _ = rl.Get("a")        // token 2

	// Bucket bi trebalo da je prazana
	err := rl.Put("b", []byte("2"))
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}
}
