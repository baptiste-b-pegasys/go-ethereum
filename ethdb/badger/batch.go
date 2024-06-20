package badger

import (
	"fmt"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
)

type Batch struct {
	db       *badger.DB
	b        *badger.WriteBatch
	size     int
	max_size int
	keyvalue map[string]string
	lock     sync.RWMutex
}

func (b *Batch) Put(key []byte, value []byte) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.db == nil {
		return nil
	}

	err := b.b.SetEntry(badger.NewEntry(common.CopyBytes(key), common.CopyBytes(value)).WithMeta(0))
	if err != nil {
		return err
	}
	b.size += len(key) + len(value)
	b.keyvalue[string(key)] = string(value)
	return nil
}

func (b *Batch) Delete(key []byte) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.db == nil {
		return fmt.Errorf("db is closed")
	}
	err := b.b.Delete(key)
	if err != nil {
		return err
	}
	b.size += len(key)
	delete(b.keyvalue, string(key))
	return nil
}

func (b *Batch) Write() error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.db == nil {
		return fmt.Errorf("db is closed")
	}
	defer func() {
		b.b.Cancel()
	}()

	return b.b.Flush()
}

func (b *Batch) ValueSize() int {
	b.lock.RLock()
	defer b.lock.RUnlock()
	return b.size
}

func (b *Batch) Reset() {
	b.lock.Lock()
	defer b.lock.Unlock()
	if b.db == nil {
		return
	}
	b.b = b.db.NewWriteBatch()
	b.keyvalue = make(map[string]string)
	b.size = 0

}

func (b *Batch) Replay(w ethdb.KeyValueWriter) error {
	b.lock.RLock()
	defer b.lock.RUnlock()
	if b.db == nil {
		return fmt.Errorf("db is closed")
	}
	for key, value := range b.keyvalue {
		if err := w.Put(common.CopyBytes([]byte(key)), common.CopyBytes([]byte(value))); err != nil {
			return err
		}
	}
	return nil
}
