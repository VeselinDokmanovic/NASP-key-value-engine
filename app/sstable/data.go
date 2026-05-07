package sstable

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
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

func DeserializeEntry(r io.Reader) (*Entry, error) {

	header := make([]byte, 4+8+1+8+8+1)

	_, err := io.ReadFull(r, header)
	if err != nil {
		return nil, err
	}

	storedCRC := binary.LittleEndian.Uint32(header[0:4])

	e := &Entry{}

	e.Timestamp = int64(binary.LittleEndian.Uint64(header[4:12]))
	e.Tombstone = header[12]

	keySize := binary.LittleEndian.Uint64(header[13:21])
	valueSize := binary.LittleEndian.Uint64(header[21:29])

	e.Type = header[29]

	e.Key = make([]byte, keySize)
	_, err = io.ReadFull(r, e.Key)
	if err != nil {
		return nil, err
	}

	e.Value = make([]byte, valueSize)
	_, err = io.ReadFull(r, e.Value)
	if err != nil {
		return nil, err
	}

	payload := make([]byte, 0, len(header)-4+int(keySize)+int(valueSize))
	payload = append(payload, header[4:]...)
	payload = append(payload, e.Key...)
	payload = append(payload, e.Value...)

	computedCRC := crc32.ChecksumIEEE(payload)
	if computedCRC != storedCRC {
		return nil, fmt.Errorf("CRC nevalidan za kljuc '%s': ocekivan %08x, dobijen %08x",
			string(e.Key), storedCRC, computedCRC)
	}

	return e, nil
}
