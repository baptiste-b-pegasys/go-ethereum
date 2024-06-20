// Copyright 2019 ChainSafe Systems (ON) Corp.
// This file is part of gossamer.
//
// The gossamer library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The gossamer library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the gossamer library. If not, see <http://www.gnu.org/licenses/>.

package badger

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/ethereum/go-ethereum/ethdb"
)

var errSnapshotReleased = errors.New("snapshot released")

const MaxInt = int(^uint(0) >> 1)

// Database contains directory path to data and db instance
type Database struct {
	file string
	db   *badger.DB
	lock sync.RWMutex
	//quitLock sync.Mutex      // Mutex protecting the quit channel access
	//quitChan chan chan error // Quit channel to stop the metrics collection before closing the database

}

/*func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}*/

// New initializes Database instance
func New(file string, cache int, handles int, namespace string, readonly bool) (*Database, error) {

	_, err := os.Stat(file)
	if err == nil {
		file = file + "+"
	}
	if os.IsNotExist(err) {
		os.MkdirAll(file, 0755)
	}
	opts := badger.DefaultOptions(file)
	opts.ValueDir = file
	opts.Dir = file
	if readonly {
		opts.ReadOnly = true
	}

	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	return &Database{
		file: file,
		db:   db,
		//quitChan: make(chan chan error),
	}, nil
}

// Path returns the path to the database directory.
func (db *Database) Path() string {
	return db.file
}

func (db *Database) NewBatch() ethdb.Batch {
	if db.db == nil {
		return &Batch{}
	}
	return &Batch{db: db.db, b: db.db.NewWriteBatch(), keyvalue: map[string]string{}, max_size: MaxInt}
}

func (db *Database) NewBatchWithSize(size int) ethdb.Batch {
	if db.db == nil {
		return &Batch{}
	}
	return &Batch{db: db.db, b: db.db.NewWriteBatch(), keyvalue: map[string]string{}, max_size: size}
}

func (db *Database) Stat(property string) (string, error) {
	return "", errors.New("unknown property")
}

// Put puts the given key / value to the queue
func (db *Database) Put(key []byte, value []byte) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	if db.db == nil {
		return fmt.Errorf("db is closed")
	}
	return db.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, value)
	})
}

// Has checks the given key exists already; returning true or false
func (db *Database) Has(key []byte) (exists bool, err error) {
	db.lock.RLock()
	defer db.lock.RUnlock()
	if db.db == nil {
		return false, fmt.Errorf("db is closed")
	}
	err = db.db.View(func(txn *badger.Txn) error {
		item, errr := txn.Get(key)
		if item != nil {
			exists = true
		}
		if errr == badger.ErrKeyNotFound {
			exists = false
			errr = nil
		}
		return errr
	})
	return exists, err
}

// Get returns the given key
func (db *Database) Get(key []byte) (data []byte, err error) {
	db.lock.RLock()
	defer db.lock.RUnlock()
	if db.db == nil {
		return nil, fmt.Errorf("db is closed")
	}
	err = db.db.View(func(txn *badger.Txn) error {
		item, e := txn.Get(key)
		if e != nil {
			return e
		}
		data, e = item.ValueCopy(nil)
		if e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Del removes the key from the queue and database
func (db *Database) Delete(key []byte) error {
	db.lock.Lock()
	defer db.lock.Unlock()
	if db.db == nil {
		return fmt.Errorf("db is closed")
	}
	return db.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(key)
		if err == badger.ErrKeyNotFound {
			err = nil
		}
		return err
	})
}

// Flush commits pending writes to disk
func (db *Database) Flush() error {
	db.lock.Lock()
	defer db.lock.Unlock()
	if db.db == nil {
		return fmt.Errorf("db is closed")
	}
	return db.db.Sync()
}

// Close closes a DB
func (db *Database) Close() error {
	//db.quitLock.Lock()
	//defer db.quitLock.Unlock()

	/*if db.quitChan != nil {
		errc := make(chan error)
		db.quitChan <- errc
		db.quitChan = nil
	}*/
	db.lock.Lock()
	defer db.lock.Unlock()
	if db.db == nil {
		return fmt.Errorf("db is closed")
	}
	defer func() { db.db = nil }()
	return db.db.Close()
}

// ClearAll would delete all the data stored in DB.
func (db *Database) ClearAll() error {
	db.lock.Lock()
	defer db.lock.Unlock()
	if db.db == nil {
		return fmt.Errorf("db is closed")
	}
	return db.db.DropAll()
}

func (db *Database) Compact(start []byte, limit []byte) error {
	return nil
}

// NewSnapshot creates a database snapshot based on the current state.
// The created snapshot will not be affected by all following mutations
// happened on the database.
// Note don't forget to release the snapshot once it's used up, otherwise
// the stale data will never be cleaned up by the underlying compactor.
func (db *Database) NewSnapshot() (ethdb.Snapshot, error) {

	if db.db == nil {
		return nil, fmt.Errorf("db is closed")
	}

	new_file := os.TempDir()
	new_file = new_file + "snap"
	new_db, errr := New(new_file, 1024, 1, "", false)
	if errr != nil {
		return nil, errr
	}

	err := db.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()
			err := item.Value(func(v []byte) error {
				new_db.Put(k, v)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return newSnapshot(new_db)
}
