package sstable

type BloomFilter struct {
	M      uint
	K      uint
	Bits   []byte
	Hashes []HashWithSeed
}

func NewBloomFilter(expectedElements int, falsePositiveRate float64) *BloomFilter {
	m := CalculateM(expectedElements, falsePositiveRate)
	k := CalculateK(expectedElements, m)

	byteSize := (m + 7) / 8

	return &BloomFilter{
		M:      m,
		K:      k,
		Bits:   make([]byte, byteSize),
		Hashes: CreateHashFunctions(uint32(k)),
	}
}
func (bf *BloomFilter) Add(key []byte) {
	for _, h := range bf.Hashes {
		hash := h.Hash(key)
		index := uint(hash % uint64(bf.M))

		byteIndex := index / 8
		bitIndex := index % 8

		bf.Bits[byteIndex] |= 1 << bitIndex
	}
}
func (bf *BloomFilter) MightContain(key []byte) bool {
	for _, h := range bf.Hashes {
		hash := h.Hash(key)
		index := uint(hash % uint64(bf.M))

		byteIndex := index / 8
		bitIndex := index % 8

		if bf.Bits[byteIndex]&(1<<bitIndex) == 0 {
			return false
		}
	}
	return true
}
