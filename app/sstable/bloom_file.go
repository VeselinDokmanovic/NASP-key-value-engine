package sstable

import (
	"encoding/binary"
	"os"
)

func (bf *BloomFilter) Serialize() []byte {
	buf := make([]byte, 24)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(bf.M))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(bf.K))
	binary.LittleEndian.PutUint64(buf[16:24], uint64(len(bf.Bits)))

	for _, h := range bf.Hashes {
		buf = append(buf, h.Seed...)
	}

	buf = append(buf, bf.Bits...)
	return buf
}

func DeserializeBloomFilter(data []byte) *BloomFilter {
	m := uint(binary.LittleEndian.Uint64(data[0:8]))
	k := uint(binary.LittleEndian.Uint64(data[8:16]))
	bitsLen := int(binary.LittleEndian.Uint64(data[16:24]))

	hashes := make([]HashWithSeed, k)
	seedStart := 24
	for i := uint(0); i < k; i++ {
		seed := make([]byte, 4)
		copy(seed, data[seedStart+int(i)*4:seedStart+int(i)*4+4])
		hashes[i] = HashWithSeed{Seed: seed}
	}

	bitsStart := seedStart + int(k)*4
	bits := make([]byte, bitsLen)
	copy(bits, data[bitsStart:bitsStart+bitsLen])

	return &BloomFilter{
		M:      m,
		K:      k,
		Bits:   bits,
		Hashes: hashes,
	}
}

func WriteBloomFilter(path string, bf *BloomFilter) error {
	return os.WriteFile(path, bf.Serialize(), 0644)
}

func LoadBloomFilter(path string) (*BloomFilter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DeserializeBloomFilter(data), nil
}
