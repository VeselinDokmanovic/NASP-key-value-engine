package sstable

import (
	"crypto/md5"
	"encoding/binary"
	"math"
	"time"
)

type HashWithSeed struct {
	Seed []byte
}

func (h HashWithSeed) Hash(data []byte) uint64 {
	fn := md5.New()
	fn.Write(data)
	fn.Write(h.Seed)
	return binary.BigEndian.Uint64(fn.Sum(nil))
}
func CreateHashFunctions(k uint32) []HashWithSeed {
	hashes := make([]HashWithSeed, k)
	ts := uint32(time.Now().Unix())

	for i := uint32(0); i < k; i++ {
		seed := make([]byte, 4)
		binary.BigEndian.PutUint32(seed, ts+i)
		hashes[i] = HashWithSeed{Seed: seed}
	}
	return hashes
}
func CalculateM(expectedElements int, falsePositiveRate float64) uint {
	return uint(math.Ceil(
		float64(expectedElements) *
			math.Abs(math.Log(falsePositiveRate)) /
			math.Pow(math.Log(2), 2),
	))
}
func CalculateK(expectedElements int, m uint) uint {
	return uint(math.Ceil(
		(float64(m) / float64(expectedElements)) *
			math.Log(2),
	))
}
