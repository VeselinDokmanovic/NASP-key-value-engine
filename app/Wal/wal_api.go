package wal

import "fmt"

// append writes a record to the latest WAL segment
// if no file exists or segment is full, creates a new one
func (w *WAL) Append(key, value string, tombstone bool) (bool, error) {
	if len(w.files) == 0 {
		_, err := w.newSegment()
		if err != nil {
			return false, err
		}
	}
	lastFile, _ := w.lastFile()
	w.currentFile = lastFile
	err := w.writeRecordFragments(lastFile, key, value, tombstone)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (w *WAL) EnsureCurrentBlock(filePath string) error {
	return w.ensureCurrentBlock(filePath)
}

func (w *WAL) DebugInfo() string {
	return fmt.Sprintf("WAL(dir=%s files=%d current=%s)", w.dir, len(w.files), w.currentFile)
}
