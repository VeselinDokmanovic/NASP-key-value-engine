package sstable

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"os"
)

func hashData(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func BuildMerkleTree(leaves [][]byte) [][]byte {
	if len(leaves) == 0 {
		return nil
	}

	level := leaves

	for len(level) > 1 {
		var next [][]byte

		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				combined := make([]byte, 0, len(level[i])+len(level[i+1]))
				combined = append(combined, level[i]...)
				combined = append(combined, level[i+1]...)
				next = append(next, hashData(combined))
			} else {
				combined := make([]byte, 0, len(level[i])*2)
				combined = append(combined, level[i]...)
				combined = append(combined, level[i]...)
				next = append(next, hashData(combined))
			}
		}
		level = next
	}

	return level
}

func WriteMerkleMetadata(file *os.File, leafHashes [][]byte) error {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(len(leafHashes)))

	if _, err := file.Write(buf); err != nil {
		return err
	}

	for _, h := range leafHashes {
		if _, err := file.Write(h); err != nil {
			return err
		}
	}

	return nil
}

func ReadMerkleMetadata(file *os.File) ([][]byte, error) {
	header := make([]byte, 8)

	_, err := file.Read(header)
	if err != nil {
		return nil, err
	}

	count := binary.LittleEndian.Uint64(header)

	hashes := make([][]byte, count)

	for i := uint64(0); i < count; i++ {
		h := make([]byte, MERKLE_HASH_SIZE)
		_, err := file.Read(h)
		if err != nil {
			return nil, err
		}
		hashes[i] = h
	}

	return hashes, nil
}

func ValidateMerkle(
	oldHashes [][]byte,
	newValues [][]byte,
) (bool, []int) {

	var changed []int

	for i := range oldHashes {
		newHash := hashData(newValues[i])
		if !bytes.Equal(oldHashes[i], newHash) {
			changed = append(changed, i)
		}
	}

	return len(changed) == 0, changed
}
func HashTestValue(value []byte) []byte {
	return hashData(value)
}
