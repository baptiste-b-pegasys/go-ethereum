package badger

import (
	"fmt"

	"github.com/dgraph-io/badger/v4"
	"github.com/ethereum/go-ethereum/ethdb"
)

type Iterator struct {
	iter    *badger.Iterator
	first   []byte
	txn     *badger.Txn
	init    bool
	prefixx []byte
	opts    badger.IteratorOptions
}

func (db *Database) NewIterator(prefix []byte, start []byte) ethdb.Iterator {

	iteratorOptions := badger.IteratorOptions{
		Prefix: prefix,
	}

	var it *badger.Iterator
	badgerTxn := db.db.NewTransaction(false)
	it = badgerTxn.NewIterator(iteratorOptions)

	Iterator := &Iterator{
		iter:    it,
		first:   append(prefix, start...),
		txn:     badgerTxn,
		init:    false,
		prefixx: prefix,
		opts:    iteratorOptions,
	}
	return Iterator
}

func (iter *Iterator) Key() []byte {
	if iter.iter.Valid() {
		return iter.iter.Item().Key()
	}
	return nil
}

func (iter *Iterator) Value() []byte {

	if iter.iter.Valid() {
		item := iter.iter.Item()
		ival, err := item.ValueCopy(nil)
		if err != nil {
			return nil
		}
		return ival
	}
	fmt.Println("iterator is invalid.......")
	return nil
}

func (iter *Iterator) Next() bool {

	if !iter.init {
		iter.iter.Rewind()
		iter.Seek(iter.first)
		iter.init = true
	} else {
		if !iter.iter.Valid() {
			return false
		}
		iter.iter.Next()
	}
	return iter.iter.Valid() && iter.iter.ValidForPrefix(iter.prefixx)
}

func (iter *Iterator) Error() error {
	return nil
}

func (iter *Iterator) Release() {

	iter.iter.Close()
	iter.txn.Discard()
}

func (iter *Iterator) Seek(key []byte) {
	iter.iter.Seek(key)
}
