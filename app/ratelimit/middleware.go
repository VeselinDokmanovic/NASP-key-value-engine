package ratelimit

import (
	"errors"
	"strings"
)

var ErrReservedKey = errors.New("access denied: key is reserved for internal use")

type KVStore interface {
	Storage
	RawDelete(key string) error
}

type RateLimitedStore struct {
	inner  KVStore
	bucket *TokenBucket
}

func NewRateLimitedStore(inner KVStore, bucket *TokenBucket) *RateLimitedStore {
	return &RateLimitedStore{inner: inner, bucket: bucket}
}

func isReserved(key string) bool {
	return strings.HasPrefix(key, ReservedKeyPrefix)
}

func (r *RateLimitedStore) Get(key string) ([]byte, bool, error) {
	if isReserved(key) {
		return nil, false, ErrReservedKey
	}
	if err := r.bucket.Allow(); err != nil {
		return nil, false, err
	}
	v, ok := r.inner.RawGet(key)
	return v, ok, nil
}

func (r *RateLimitedStore) Put(key string, value []byte) error {
	if isReserved(key) {
		return ErrReservedKey
	}
	if err := r.bucket.Allow(); err != nil {
		return err
	}
	return r.inner.RawPut(key, value)
}

func (r *RateLimitedStore) Delete(key string) error {
	if isReserved(key) {
		return ErrReservedKey
	}
	if err := r.bucket.Allow(); err != nil {
		return err
	}
	return r.inner.RawDelete(key)
}
