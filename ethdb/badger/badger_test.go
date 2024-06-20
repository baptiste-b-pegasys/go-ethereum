// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package badger

import (
	"os"
	"testing"

	"github.com/dgraph-io/badger/v4"

	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/dbtest"
)

func TestBadger(t *testing.T) {
	t.Run("DatabaseSuite", func(t *testing.T) {
		//tmpDir := os.TempDir()
		dbtest.TestDatabaseSuite(t, func() ethdb.KeyValueStore {
			/*dir, err := os.CreateTemp(tmpDir, "badger-*")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("dir: " + dir.Name())

			err = os.MkdirAll(dir.Name(), os.ModeDir|os.ModePerm)
			if err != nil {
				err = os.RemoveAll(dir.Name())
				if err != nil {
					t.Fatal(err)
				}
				err = os.MkdirAll(dir.Name(), os.ModeDir|os.ModePerm)
				if err != nil {
					t.Fatal(err)
				}
			}*/

			opt := badger.DefaultOptions("")
			opt.Dir = ""
			opt.ValueDir = ""
			opt.InMemory = true
			db, err := badger.Open(opt)
			if err != nil {
				t.Fatal(err)
			}

			return &Database{
				db: db,
			}
		})
	})
}

func BenchmarkBadger(b *testing.B) {
	dbtest.BenchDatabaseSuite(b, func() ethdb.KeyValueStore {
		tmpDir := os.TempDir()
		opt := badger.DefaultOptions(tmpDir)
		db, err := badger.Open(opt)
		if err != nil {
			b.Fatal(err)
		}
		return &Database{
			db: db,
		}
	})
}
