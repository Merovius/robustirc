// Package raft_store implements a storage backend for raft on top of LevelDB.
//
// LevelDBStore implements the LogStore and StableStore interfaces of
// https://godoc.org/github.com/hashicorp/raft by using
// https://godoc.org/github.com/syndtr/goleveldb as a storage backend.
package raft_store

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hashicorp/raft"
	"github.com/syndtr/goleveldb/leveldb"
	leveldb_errors "github.com/syndtr/goleveldb/leveldb/errors"
)

// LevelDBStore implements the raft.LogStore and raft.StableStore interfaces on
// top of leveldb.
type LevelDBStore struct {
	mu sync.RWMutex
	db *leveldb.DB
}

// NewLevelDBStore opens a leveldb at the given directory to be used as a log-
// and stable storage for raft.
func NewLevelDBStore(dir string) (*LevelDBStore, error) {
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		if _, ok := err.(*leveldb_errors.ErrCorrupted); !ok {
			return nil, fmt.Errorf("could not open: %v", err)
		}
		db, err = leveldb.RecoverFile(dir, nil)
		if err != nil {
			return nil, fmt.Errorf("could not recover: %v", err)
		}
	}

	return &LevelDBStore{db: db}, nil
}

// Close closes the LevelDBStore. No other methods may be called after this.
func (s *LevelDBStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.db.Close()
	s.db = nil
	return err
}

// FirstIndex implements raft.LogStore.
func (s *LevelDBStore) FirstIndex() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	i := s.db.NewIterator(nil, nil)
	defer i.Release()
	if !i.First() {
		return 0, nil
	}
	for bytes.HasPrefix(i.Key(), []byte("stablestore-")) {
		if !i.Next() {
			return 0, nil
		}
	}
	return binary.BigEndian.Uint64(i.Key()), nil
}

// LastIndex implements raft.LogStore.
func (s *LevelDBStore) LastIndex() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	i := s.db.NewIterator(nil, nil)
	defer i.Release()
	if !i.Last() {
		return 0, nil
	}
	for bytes.HasPrefix(i.Key(), []byte("stablestore-")) {
		if !i.Prev() {
			return 0, nil
		}
	}
	return binary.BigEndian.Uint64(i.Key()), nil
}

// GetLog implements raft.LogStore.
func (s *LevelDBStore) GetLog(index uint64, rlog *raft.Log) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := make([]byte, binary.Size(index))
	binary.BigEndian.PutUint64(key, index)
	value, err := s.db.Get(key, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return raft.ErrLogNotFound
		}
		return err
	}
	return json.Unmarshal(value, rlog)
}

// StoreLog implements raft.LogStore.
func (s *LevelDBStore) StoreLog(entry *raft.Log) error {
	return s.StoreLogs([]*raft.Log{entry})
}

// StoreLogs implements raft.LogStore.
func (s *LevelDBStore) StoreLogs(logs []*raft.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var batch leveldb.Batch
	key := make([]byte, binary.Size(uint64(0)))

	for _, entry := range logs {
		binary.BigEndian.PutUint64(key, entry.Index)
		v, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		batch.Put(key, v)
	}

	if err := s.db.Write(&batch, nil); err != nil {
		return err
	}
	return nil
}

// DeleteRange implements raft.LogStore.
func (s *LevelDBStore) DeleteRange(min, max uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var batch leveldb.Batch
	key := make([]byte, binary.Size(uint64(0)))
	for n := min; n <= max; n++ {
		binary.BigEndian.PutUint64(key, n)
		batch.Delete(key)
	}
	if err := s.db.Write(&batch, nil); err != nil {
		return err
	}
	return nil
}

// Set implements raft.StableStore.
func (s *LevelDBStore) Set(key []byte, val []byte) error {
	key = append([]byte("stablestore-"), key...)
	return s.db.Put(key, val, nil)
}

// Get implements raft.StableStore.
func (s *LevelDBStore) Get(key []byte) ([]byte, error) {
	key = append([]byte("stablestore-"), key...)
	value, err := s.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	return value, err
}

// SetUint64 implements raft.StableStore.
func (s *LevelDBStore) SetUint64(key []byte, val uint64) error {
	key = append([]byte("stablestore-"), key...)

	v := make([]byte, binary.Size(val))
	binary.BigEndian.PutUint64(v, val)

	return s.db.Put(key, v, nil)
}

// GetUint64 implements raft.StableStore.
func (s *LevelDBStore) GetUint64(key []byte) (uint64, error) {
	key = append([]byte("stablestore-"), key...)
	v, err := s.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return 0, nil
	}
	return binary.BigEndian.Uint64(v), err
}
