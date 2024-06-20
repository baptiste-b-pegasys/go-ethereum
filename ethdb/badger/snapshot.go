package badger

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"github.com/dgraph-io/badger/v4"
)

// snapshot wraps a batch of key-value entries deep copied from the in-memory
// database for implementing the Snapshot interface.
type snapshot struct {
	db   *Database
	lock sync.RWMutex
}

// newSnapshot initializes the snapshot with the given database instance.
func newSnapshot(db *Database) (*snapshot, error) {
	db.lock.RLock()
	defer db.lock.RUnlock()

	if db.db == nil {
		return nil, fmt.Errorf("db is closed")
	}

	err := db.db.Sync()
	if err != nil {
		return nil, err
	}

	buf := bytes.NewBuffer(make([]byte, 0))
	_, err = db.db.Backup(buf, 1)
	if err != nil {
		return nil, err
	}

	tmpDir := os.TempDir()
	snapshotDir, err := os.CreateTemp(tmpDir, "snapshot-*")
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(snapshotDir.Name(), os.ModePerm|os.ModeDir)
	if err != nil {
		return nil, err
	}

	opts := badger.Options{
		Dir:      snapshotDir.Name(),
		ValueDir: snapshotDir.Name(),
	}
	bdb2, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	err = bdb2.Load(buf, 5)
	if err != nil {
		return nil, err
	}

	db2 := &Database{
		db: bdb2,
	}

	return &snapshot{db: db2}, nil
}

// Has retrieves if a key is present in the snapshot backing by a key-value
// data store.
func (snap *snapshot) Has(key []byte) (exists bool, err error) {
	snap.lock.RLock()
	defer snap.lock.RUnlock()

	if snap.db == nil {
		return false, errSnapshotReleased
	}
	return snap.db.Has(key)
}

// Get retrieves the given key if it's present in the snapshot backing by
// key-value data store.
func (snap *snapshot) Get(key []byte) (data []byte, err error) {
	snap.lock.RLock()
	defer snap.lock.RUnlock()

	if snap.db == nil {
		return nil, errSnapshotReleased
	}
	return snap.db.Get(key)

}

// Release releases associated resources. Release should always succeed and can
// be called multiple times without causing error.
func (snap *snapshot) Release() {
	snap.lock.Lock()
	defer snap.lock.Unlock()
	snap.db = nil
}
