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

func BuildMerkleTree(leaves [][]byte) [][][]byte {
	if len(leaves) == 0 {
		return nil
	}

	current := make([][]byte, len(leaves))
	copy(current, leaves)

	levels := make([][][]byte, 0)
	levels = append(levels, current)

	for len(current) > 1 {
		var next [][]byte

		for i := 0; i < len(current); i += 2 {
			var combined []byte
			if i+1 < len(current) {
				combined = append(current[i], current[i+1]...)
			} else {
				combined = append(current[i], current[i]...)
			}
			next = append(next, hashData(combined))
		}

		levels = append(levels, next)
		current = next
	}

	return levels
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

func ValidateMerkle(oldLeafHashes [][]byte, newValues [][]byte) (bool, []int) {
	newLeafHashes := make([][]byte, len(newValues))
	for i, v := range newValues {
		newLeafHashes[i] = hashData(v)
	}

	oldTree := BuildMerkleTree(oldLeafHashes)
	newTree := BuildMerkleTree(newLeafHashes)

	if oldTree == nil && newTree == nil {
		return true, nil
	}
	if oldTree == nil || newTree == nil || len(oldLeafHashes) != len(newLeafHashes) {
		maxLen := len(oldLeafHashes)
		if len(newLeafHashes) > maxLen {
			maxLen = len(newLeafHashes)
		}
		all := make([]int, maxLen)
		for i := range all {
			all[i] = i
		}
		return false, all
	}

	oldRoot := oldTree[len(oldTree)-1][0]
	newRoot := newTree[len(newTree)-1][0]
	if bytes.Equal(oldRoot, newRoot) {
		return true, nil
	}

	var changed []int
	traverseForChanges(oldTree, newTree, len(oldTree)-1, 0, &changed)
	return false, changed
}

func traverseForChanges(oldTree, newTree [][][]byte, level, nodeIdx int, changed *[]int) {
	if nodeIdx >= len(oldTree[level]) || nodeIdx >= len(newTree[level]) {
		return
	}
	if bytes.Equal(oldTree[level][nodeIdx], newTree[level][nodeIdx]) {
		return
	}
	if level == 0 {
		*changed = append(*changed, nodeIdx)
		return
	}

	prevLevel := level - 1
	leftChild := nodeIdx * 2
	rightChild := nodeIdx*2 + 1

	traverseForChanges(oldTree, newTree, prevLevel, leftChild, changed)
	traverseForChanges(oldTree, newTree, prevLevel, rightChild, changed)
}

func HashTestValue(value []byte) []byte {
	return hashData(value)
}
