package sstable

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

type Entry struct {
	Timestamp int64
	Tombstone byte
	Type      byte
	Key       []byte
	Value     []byte
}

func (e *Entry) Serialize() []byte {
	keySize := uint64(len(e.Key))
	valueSize := uint64(len(e.Value))

	payload := make([]byte, 0)

	tmp8 := make([]byte, 8)

	binary.LittleEndian.PutUint64(tmp8, uint64(e.Timestamp))
	payload = append(payload, tmp8...)
	payload = append(payload, e.Tombstone)
	binary.LittleEndian.PutUint64(tmp8, keySize)
	payload = append(payload, tmp8...)
	binary.LittleEndian.PutUint64(tmp8, valueSize)
	payload = append(payload, tmp8...)
	payload = append(payload, e.Type)
	payload = append(payload, e.Key...)
	payload = append(payload, e.Value...)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)

	return append(crcBytes, payload...)
}

func DeserializeEntry(file *os.File) (*Entry, error) {
	header := make([]byte, 4+8+1+8+8+1)

	_, err := file.Read(header)
	if err != nil {
		return nil, err
	}

	e := &Entry{}

	e.Timestamp = int64(binary.LittleEndian.Uint64(header[4:12]))
	e.Tombstone = header[12]

	keySize := binary.LittleEndian.Uint64(header[13:21])
	valueSize := binary.LittleEndian.Uint64(header[21:29])

	e.Type = header[29]

	e.Key = make([]byte, keySize)
	_, err = file.Read(e.Key)
	if err != nil {
		return nil, err
	}

	e.Value = make([]byte, valueSize)
	_, err = file.Read(e.Value)
	if err != nil {
		return nil, err
	}

	return e, nil
}
