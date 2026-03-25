package sstable

import (
	"bytes"
	"encoding/binary"
	"os"
)

type IndexEntry struct {
	Key    []byte
	Offset uint64
}

func SerializeIndexEntry(key []byte, offset uint64) []byte {
	buf := make([]byte, INDEX_ENTRY_SIZE)

	copy(buf[:INDEX_KEY_SIZE], key)

	binary.LittleEndian.PutUint64(
		buf[INDEX_KEY_SIZE:INDEX_KEY_SIZE+8],
		offset,
	)

	return buf
}

func ReadIndexEntry(file *os.File) (*IndexEntry, error) {
	buf := make([]byte, INDEX_ENTRY_SIZE)

	_, err := file.Read(buf)
	if err != nil {
		return nil, err
	}

	rawKey := buf[:INDEX_KEY_SIZE]
	key := bytes.TrimRight(rawKey, "\x00")

	offset := binary.LittleEndian.Uint64(
		buf[INDEX_KEY_SIZE : INDEX_KEY_SIZE+8],
	)

	return &IndexEntry{
		Key:    key,
		Offset: offset,
	}, nil
}

// ==================== COMPRESSED FORMAT V2 ====================
// SerializeIndexEntryV2 enkodira Index entry sa delta + varint:
// [Key:delta] [Offset:varint]
// previousKey je potreban za delta kompresiju
func SerializeIndexEntryV2(key []byte, offset uint64, previousKey []byte) []byte {
	result := &bytes.Buffer{}

	// Enkodira ključ sa delta od prethodnog
	deltaKey := EncodeDeltaKey(key, previousKey)
	result.Write(deltaKey)

	// Enkodira offset kao varint
	offsetVarint := EncodeVarint(offset)
	result.Write(offsetVarint)

	return result.Bytes()
}

// DeserializeIndexEntryV2 dekodira komprimovani Index entry
func DeserializeIndexEntryV2(data []byte, offset int, previousKey []byte) (*IndexEntry, int, error) {
	// Dekodira ključ sa delta od prethodnog
	key, pos, err := DecodeDeltaKey(data, offset, previousKey)
	if err != nil {
		return nil, pos, err
	}

	// Dekodira offset kao varint
	offsetVal, pos, err := DecodeVarint(data, pos)
	if err != nil {
		return nil, pos, err
	}

	return &IndexEntry{
		Key:    key,
		Offset: offsetVal,
	}, pos, nil
}
