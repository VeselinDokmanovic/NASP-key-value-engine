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
