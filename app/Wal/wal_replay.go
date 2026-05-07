package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"key-value-engine/app/memtable"
)

// inserts wal records into memtable pool
// if a flush is required it lets the caller handle it and retry
func (w *WAL) InsertIntoMemtable(pool *memtable.MemtablePool) error {
	var (
		accActive bool
		accKey    []byte
		accValue  []byte
		accTs     int64
		accTomb   byte
	)

	for _, filePath := range w.files {
		var offset int64
		for {
			payload, err := w.readRecord(filePath, &offset, false)
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed reading wal %s: %w", filePath, err)
			}

			if len(payload) < TIMESTAMP_SIZE+TOMBSTONE_SIZE+KEY_SIZE_SIZE+VALUE_SIZE_SIZE+TYPE_SIZE {
				return fmt.Errorf("wal: malformed payload in %s", filePath)
			}
			p := 0
			ts := int64(binary.LittleEndian.Uint64(payload[p : p+TIMESTAMP_SIZE]))
			p += TIMESTAMP_SIZE
			tomb := payload[p]
			p += TOMBSTONE_SIZE
			keySize := int(binary.LittleEndian.Uint64(payload[p : p+KEY_SIZE_SIZE]))
			p += KEY_SIZE_SIZE
			valSize := int(binary.LittleEndian.Uint64(payload[p : p+VALUE_SIZE_SIZE]))
			p += VALUE_SIZE_SIZE
			typ := payload[p]
			p += TYPE_SIZE

			if len(payload) < p+keySize+valSize {
				return fmt.Errorf("wal: truncated payload in %s", filePath)
			}
			keyPart := make([]byte, keySize)
			copy(keyPart, payload[p:p+keySize])
			p += keySize
			var valuePart []byte
			if valSize > 0 {
				valuePart = make([]byte, valSize)
				copy(valuePart, payload[p:p+valSize])
			}

			switch typ {
			case RECORD_FULL:
				// complete record in one fragment
				if err := pool.PutWithTimestamp(keyPart, valuePart, ts, tomb, typ); err != nil {
					return err
				}
			case RECORD_START:
				// begin accumulation
				accActive = true
				accKey = append(accKey[:0], keyPart...)
				if len(valuePart) > 0 {
					accValue = append(accValue[:0], valuePart...)
				} else {
					accValue = accValue[:0]
				}
				accTs = ts
				accTomb = tomb
			case RECORD_MID:
				if !accActive {
					// treat as start if we never saw a start
					accActive = true
					accKey = append(accKey[:0], keyPart...)
					if len(valuePart) > 0 {
						accValue = append(accValue[:0], valuePart...)
					} else {
						accValue = accValue[:0]
					}
					accTs = ts
					accTomb = tomb
				} else {
					accKey = append(accKey, keyPart...)
					if len(valuePart) > 0 {
						accValue = append(accValue, valuePart...)
					}
				}
			case RECORD_END:
				if !accActive {
					// no prior start — treat this fragment as standalone
					if err := pool.PutWithTimestamp(keyPart, valuePart, ts, tomb, typ); err != nil {
						return err
					}
					continue
				}
				// finalize accumulation
				accKey = append(accKey, keyPart...)
				if len(valuePart) > 0 {
					accValue = append(accValue, valuePart...)
				}
				// use accTs/accTomb from the START fragment
				useTs := accTs
				useTomb := accTomb
				if useTs == 0 {
					useTs = ts
				}
				if err := pool.PutWithTimestamp(accKey, accValue, useTs, useTomb, typ); err != nil {
					return err
				}
				// reset accumulator
				accActive = false
				accKey = accKey[:0]
				accValue = accValue[:0]
				accTs = 0
				accTomb = 0
			default:
				return fmt.Errorf("wal: unknown fragment type %d in %s", typ, filePath)
			}
		}
	}

	if accActive {
		return fmt.Errorf("wal: truncated final fragmented record")
	}

	return nil
}
