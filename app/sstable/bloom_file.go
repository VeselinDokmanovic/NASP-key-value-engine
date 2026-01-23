package sstable

import (
	"encoding/binary"
	"os"
)

func (bf *BloomFilter) Serialize() []byte {
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(bf.M))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(bf.K))
	buf = append(buf, bf.Bits...)
	return buf
}
func DeserializeBloomFilter(data []byte) *BloomFilter {
	m := uint(binary.LittleEndian.Uint64(data[0:8]))
	k := uint(binary.LittleEndian.Uint64(data[8:16]))

	bits := data[16:]

	return &BloomFilter{
		M:      m,
		K:      k,
		Bits:   bits,
		Hashes: CreateHashFunctions(uint32(k)),
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
