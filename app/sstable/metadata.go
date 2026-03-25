package sstable

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
)

func hashData(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// HashData je javna verzija hashData funkcije
func HashData(data []byte) []byte {
	return hashData(data)
}

// MerkleNode predstavlja čvor u Merkle stablu
type MerkleNode struct {
	Hash    []byte
	Left    *MerkleNode
	Right   *MerkleNode
	IsLeaf  bool
	LeafIdx int // Indeks list ako je leaf
}

// BuildMerkleTree gradi kompletno Merkle stablo od leaf hash-eva
func BuildMerkleTree(leaves [][]byte) *MerkleNode {
	if len(leaves) == 0 {
		return nil
	}

	// Kreiraj leaf nodes
	nodes := make([]*MerkleNode, len(leaves))
	for i, leaf := range leaves {
		nodes[i] = &MerkleNode{
			Hash:    leaf,
			IsLeaf:  true,
			LeafIdx: i,
		}
	}

	// Izgradi stablo od dna prema vrhu
	for len(nodes) > 1 {
		var nextLevel []*MerkleNode

		for i := 0; i < len(nodes); i += 2 {
			var left, right *MerkleNode
			left = nodes[i]

			if i+1 < len(nodes) {
				right = nodes[i+1]
			} else {
				// Ako je neparan broj, dupliraj poslednji
				right = nodes[i]
			}

			// Kreiraj parent node
			combined := make([]byte, 0, len(left.Hash)+len(right.Hash))
			combined = append(combined, left.Hash...)
			combined = append(combined, right.Hash...)

			parent := &MerkleNode{
				Hash:   hashData(combined),
				Left:   left,
				Right:  right,
				IsLeaf: false,
			}

			nextLevel = append(nextLevel, parent)
		}

		nodes = nextLevel
	}

	return nodes[0]
}

// BuildMerkleTreeSimple je stara verzija koja samo vraća list hash-eva
func BuildMerkleTreeSimple(leaves [][]byte) [][]byte {
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

// WriteMerkleTree čuva kompletno Merkle stablo
func WriteMerkleTree(file *os.File, root *MerkleNode) error {
	if root == nil {
		return fmt.Errorf("merkle tree is nil")
	}

	// Prvo čuvaj sve leaf hash-eve (za brzu validaciju)
	var leafHashes [][]byte
	collectLeafHashes(root, &leafHashes)

	// Čuvaj broj listova
	countBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(countBuf, uint64(len(leafHashes)))
	if _, err := file.Write(countBuf); err != nil {
		return err
	}

	// Čuvaj sve leaf hash-eve
	for _, h := range leafHashes {
		if _, err := file.Write(h); err != nil {
			return err
		}
	}

	return nil
}

// collectLeafHashes prikuplja sve leaf hash-eve iz stabla
func collectLeafHashes(node *MerkleNode, hashes *[][]byte) {
	if node == nil {
		return
	}

	if node.IsLeaf {
		*hashes = append(*hashes, node.Hash)
		return
	}

	if node.Left != nil {
		collectLeafHashes(node.Left, hashes)
	}
	if node.Right != nil {
		collectLeafHashes(node.Right, hashes)
	}
}

// CollectLeafHashesForBuffer je javna verzija za korišćenje u drugim paketa
func CollectLeafHashesForBuffer(node *MerkleNode, hashes *[][]byte) {
	collectLeafHashes(node, hashes)
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

// ReadMerkleTree čita kompletno Merkle stablo
func ReadMerkleTree(file *os.File) (*MerkleNode, error) {
	// Čitaj broj listova
	countBuf := make([]byte, 8)
	_, err := file.Read(countBuf)
	if err != nil {
		return nil, err
	}

	count := binary.LittleEndian.Uint64(countBuf)

	// Čitaj sve leaf hash-eve
	leafHashes := make([][]byte, count)
	for i := uint64(0); i < count; i++ {
		h := make([]byte, MERKLE_HASH_SIZE)
		_, err := file.Read(h)
		if err != nil {
			return nil, err
		}
		leafHashes[i] = h
	}

	// Ponovo izgradi stablo
	root := BuildMerkleTree(leafHashes)
	return root, nil
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

// MerkleValidationResult sadrži detaljne rezultate Merkle validacije
type MerkleValidationResult struct {
	IsValid        bool
	ChangedIndices []int
	Details        string
}

// ValidateMerkleDetailed vrši detaljnu validaciju sa više informacija
func ValidateMerkleDetailed(
	storedLeafHashes [][]byte,
	currentValues [][]byte,
) MerkleValidationResult {
	result := MerkleValidationResult{
		IsValid:        true,
		ChangedIndices: make([]int, 0),
	}

	if len(storedLeafHashes) != len(currentValues) {
		result.IsValid = false
		result.Details = fmt.Sprintf(
			"Entry count mismatch: stored=%d, current=%d",
			len(storedLeafHashes),
			len(currentValues),
		)
		return result
	}

	// Pronađi sve izmenjene vrednosti
	for i := 0; i < len(storedLeafHashes); i++ {
		currentHash := hashData(currentValues[i])
		if !bytes.Equal(storedLeafHashes[i], currentHash) {
			result.IsValid = false
			result.ChangedIndices = append(result.ChangedIndices, i)
		}
	}

	if !result.IsValid {
		result.Details = fmt.Sprintf(
			"Detected %d corrupted entries at indices: %v",
			len(result.ChangedIndices),
			result.ChangedIndices,
		)
	} else {
		result.Details = "✓ All entries are intact"
	}

	return result
}

// ValidateMerkleTree vrši validaciju koristeći kompletno Merkle stablo
// Očekuje originalne vrednosti kao ulaz, ne hash-eve
func ValidateMerkleTree(
	storedRoot *MerkleNode,
	currentValues [][]byte,
) MerkleValidationResult {
	result := MerkleValidationResult{
		ChangedIndices: make([]int, 0),
	}

	if storedRoot == nil {
		result.IsValid = true
		result.Details = "No Merkle tree stored"
		return result
	}

	// Konvertuj originalne vrednosti u hash-eve
	var currentHashes [][]byte
	for _, v := range currentValues {
		currentHashes = append(currentHashes, hashData(v))
	}

	// Ponovo izgradi stablo sa trenutnim hash-evima
	currentRoot := BuildMerkleTree(currentHashes)

	// Poredi root hash-eve
	if !bytes.Equal(storedRoot.Hash, currentRoot.Hash) {
		result.IsValid = false
		// Pronađi izmenjene listove
		findChangedLeaves(storedRoot, currentRoot, &result.ChangedIndices)
		result.Details = fmt.Sprintf(
			"Root hash mismatch. Changed %d entries at indices: %v",
			len(result.ChangedIndices),
			result.ChangedIndices,
		)
	} else {
		result.IsValid = true
		result.Details = "✓ Merkle tree root hashes match"
	}

	return result
}

// findChangedLeaves pronalazi koje listove su izmenjene upoređujući dva stabla
func findChangedLeaves(stored, current *MerkleNode, changedIndices *[]int) {
	if stored == nil || current == nil {
		return
	}

	if stored.IsLeaf && current.IsLeaf {
		if !bytes.Equal(stored.Hash, current.Hash) {
			*changedIndices = append(*changedIndices, stored.LeafIdx)
		}
		return
	}

	// Ako hash-evi a berbeda, pretraži podstabla
	if !bytes.Equal(stored.Hash, current.Hash) {
		if stored.Left != nil && current.Left != nil {
			findChangedLeaves(stored.Left, current.Left, changedIndices)
		}
		if stored.Right != nil && current.Right != nil {
			findChangedLeaves(stored.Right, current.Right, changedIndices)
		}
	}
}

func HashTestValue(value []byte) []byte {
	return hashData(value)
}
