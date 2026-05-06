package sstable

import (
	"encoding/binary"
	"os"
)

// Format fajla: [M:8][K:8][K*4 bajta seedova][Bits...]
// Seedovi se moraju cuvati jer su generisani nasumicno pri kreiranju filtera.
// Bez toga, pri ucitavanju bi se generisali novi seedovi pa bi hash funkcije bile
// drugacije i MightContain bi vracao pogresne rezultate.

func (bf *BloomFilter) Serialize() []byte {
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(bf.M))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(bf.K))

	// Sacuvaj seedove (svaki seed je 4 bajta)
	for _, h := range bf.Hashes {
		buf = append(buf, h.Seed...)
	}

	buf = append(buf, bf.Bits...)
	return buf
}

func DeserializeBloomFilter(data []byte) *BloomFilter {
	m := uint(binary.LittleEndian.Uint64(data[0:8]))
	k := uint(binary.LittleEndian.Uint64(data[8:16]))

	// Ucitaj seedove (svaki seed je 4 bajta, pocevsi od bajta 16)
	hashes := make([]HashWithSeed, k)
	seedStart := 16
	for i := uint(0); i < k; i++ {
		seed := make([]byte, 4)
		copy(seed, data[seedStart+int(i)*4:seedStart+int(i)*4+4])
		hashes[i] = HashWithSeed{Seed: seed}
	}

	// Bits pocinje nakon svih seedova
	bitsStart := seedStart + int(k)*4
	bits := make([]byte, len(data)-bitsStart)
	copy(bits, data[bitsStart:])

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
