package sstable

import (
	"bytes"
	"encoding/binary"
	"os"
)

type SummaryEntry struct {
	Key         []byte
	IndexOffset uint64
}

type Summary struct {
	MinKey []byte
	MaxKey []byte
}

func WriteSummaryHeader(file *os.File, minKey, maxKey []byte) error {
	buf := make([]byte, SUMMARY_HEADER_SIZE)

	copy(buf[:SUMMARY_KEY_SIZE], minKey)
	copy(buf[SUMMARY_KEY_SIZE:], maxKey)

	_, err := file.Write(buf)
	return err
}

func SerializeSummaryEntry(key []byte, indexOffset uint64) []byte {
	buf := make([]byte, SUMMARY_ENTRY_SIZE)

	copy(buf[:SUMMARY_KEY_SIZE], key)

	binary.LittleEndian.PutUint64(
		buf[SUMMARY_KEY_SIZE:SUMMARY_KEY_SIZE+8],
		indexOffset,
	)

	return buf
}

func ReadSummaryEntry(file *os.File) (*SummaryEntry, error) {
	buf := make([]byte, SUMMARY_ENTRY_SIZE)

	_, err := file.Read(buf)
	if err != nil {
		return nil, err
	}

	key := bytes.TrimRight(buf[:SUMMARY_KEY_SIZE], "\x00")

	offset := binary.LittleEndian.Uint64(
		buf[SUMMARY_KEY_SIZE : SUMMARY_KEY_SIZE+8],
	)

	return &SummaryEntry{
		Key:         key,
		IndexOffset: offset,
	}, nil
}

// ==================== COMPRESSED FORMAT V2 ====================
// SerializeSummaryEntryV2 enkodira Summary entry sa delta + varint:
// [Key:delta] [IndexOffset:varint]
func SerializeSummaryEntryV2(key []byte, indexOffset uint64, previousKey []byte) []byte {
	result := &bytes.Buffer{}

	// Enkodira ključ sa delta od prethodnog
	deltaKey := EncodeDeltaKey(key, previousKey)
	result.Write(deltaKey)

	// Enkodira IndexOffset kao varint
	offsetVarint := EncodeVarint(indexOffset)
	result.Write(offsetVarint)

	return result.Bytes()
}

// DeserializeSummaryEntryV2 dekodira komprimovani Summary entry
func DeserializeSummaryEntryV2(data []byte, offset int, previousKey []byte) (*SummaryEntry, int, error) {
	// Dekodira ključ sa delta od prethodnog
	key, pos, err := DecodeDeltaKey(data, offset, previousKey)
	if err != nil {
		return nil, pos, err
	}

	// Dekodira IndexOffset kao varint
	offsetVal, pos, err := DecodeVarint(data, pos)
	if err != nil {
		return nil, pos, err
	}

	return &SummaryEntry{
		Key:         key,
		IndexOffset: offsetVal,
	}, pos, nil
}
