// Copyright (c) 2013, Suryandaru Triandana <syndtr@gmail.com>
// All rights reserved.
//
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package storage

import (
	"bytes"
	"os"
	"sync"
)

const typeShift = 3

type memStorageLock struct {
	ms *memStorage
}

func (lock *memStorageLock) Unlock() {
	ms := lock.ms
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.slock == lock {
		ms.slock = nil
	}
	return
}

// memStorage is a memory-backed storage.
type memStorage struct {
	mu    sync.Mutex
	slock *memStorageLock
	files map[uint64]*IndexedDBFile
	meta  FileDesc
}

// NewMemStorage returns a new memory-backed storage implementation.
func NewMemStorage() Storage {
	ms := &memStorage{
		files: make(map[uint64]*IndexedDBFile),
	}
	//manifestName := FileDesc{
	//	Type: TypeManifest,
	//	Num:  0,
	//}
	//
	//err := IsExisted("tidb", "file", manifestName.String())
	//if err == nil {
	//	m, err := Open("tidb", "file", manifestName.String())
	//	if err != nil {
	//		return nil
	//	}
	//	ms.files[packFile(manifestName)] = m
	//	ms.meta = manifestName
	//}
	//
	//journalName := FileDesc{
	//	Type: TypeJournal,
	//	Num:  1,
	//}
	//err = IsExisted("tidb", "file", journalName.String())
	//if err == nil {
	//	_, err := Open("tidb", "file", journalName.String())
	//	if err != nil {
	//		return nil
	//	}
	//	ms.files[packFile(journalName)] = m
	//}
	//
	return ms
}

func (ms *memStorage) Lock() (Locker, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.slock != nil {
		return nil, ErrLocked
	}
	ms.slock = &memStorageLock{ms: ms}
	return ms.slock, nil
}

func (*memStorage) Log(str string) {}

func (ms *memStorage) SetMeta(fd FileDesc) error {
	if !FileDescOk(fd) {
		return ErrInvalidFile
	}

	ms.mu.Lock()
	ms.meta = fd
	ms.mu.Unlock()
	return nil
}

func (ms *memStorage) GetMeta() (FileDesc, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if ms.meta.Zero() {
		return FileDesc{}, os.ErrNotExist
	}
	return ms.meta, nil
}

func (ms *memStorage) List(ft FileType) ([]FileDesc, error) {
	ms.mu.Lock()
	var fds []FileDesc
	for x := range ms.files {
		fd := unpackFile(x)
		if fd.Type&ft != 0 {
			fds = append(fds, fd)
		}
	}
	ms.mu.Unlock()
	return fds, nil
}

func (ms *memStorage) Open(fd FileDesc) (Reader, error) {
	if !FileDescOk(fd) {
		return nil, ErrInvalidFile
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if m, exist := ms.files[packFile(fd)]; exist {
		if m.open {
			return nil, errFileOpen
		}
		m.open = true
		return &memReader{Reader: m.Reader(), ms: ms, m: m}, nil
	} else {
		err := IsExisted("tidb", "file", fd.String())
		if err != nil {
			return nil, os.ErrNotExist
		}

		m, err = Open("tidb", "file", fd.String())
		if err != nil {
			return nil, err
		}

		m.open = true
		ms.files[packFile(fd)] = m
		return &memReader{Reader: m.Reader(), ms: ms, m: m}, nil
	}
	return nil, os.ErrNotExist
}

func (ms *memStorage) Create(fd FileDesc) (Writer, error) {
	if !FileDescOk(fd) {
		return nil, ErrInvalidFile
	}

	x := packFile(fd)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	m, exist := ms.files[x]
	if exist {
		if m.open {
			return nil, errFileOpen
		}
		m.buff.Reset()
	} else {
		var err error
		m, err = Open("tidb", "file", fd.String())
		if err != nil {
			return nil, err
		}
		ms.files[x] = m
	}
	m.open = true
	return &memWriter{IndexedDBFile: m, ms: ms}, nil
}

func (ms *memStorage) Remove(fd FileDesc) error {
	if !FileDescOk(fd) {
		return ErrInvalidFile
	}

	x := packFile(fd)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if _, exist := ms.files[x]; exist {
		delete(ms.files, x)
		return nil
	}
	return os.ErrNotExist
}

func (ms *memStorage) Rename(oldfd, newfd FileDesc) error {
	if FileDescOk(oldfd) || FileDescOk(newfd) {
		return ErrInvalidFile
	}
	if oldfd == newfd {
		return nil
	}

	oldx := packFile(oldfd)
	newx := packFile(newfd)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	oldm, exist := ms.files[oldx]
	if !exist {
		return os.ErrNotExist
	}
	newm, exist := ms.files[newx]
	if (exist && newm.open) || oldm.open {
		return errFileOpen
	}
	delete(ms.files, oldx)
	ms.files[newx] = oldm
	return nil
}

func (ms *memStorage) Close() error {
	var err error
	for _, v := range ms.files {
		er := v.Close()
		if er != nil {
			err = er
		}
	}
	return err
}

type memFile struct {
	bytes.Buffer
	open bool
}

type memReader struct {
	*bytes.Reader
	ms     *memStorage
	m      *IndexedDBFile
	closed bool
}

func (mr *memReader) Close() error {
	mr.ms.mu.Lock()
	defer mr.ms.mu.Unlock()
	if mr.closed {
		return ErrClosed
	}
	mr.m.open = false
	return nil
}

type memWriter struct {
	*IndexedDBFile
	ms     *memStorage
	closed bool
}

func (mw *memWriter) Sync() error {
	return mw.IndexedDBFile.Sync()
}

func (mw *memWriter) Close() error {
	mw.ms.mu.Lock()
	defer mw.ms.mu.Unlock()
	if mw.closed {
		return ErrClosed
	}
	mw.IndexedDBFile.open = false
	err := mw.IndexedDBFile.Close()
	return err
}

func packFile(fd FileDesc) uint64 {
	return uint64(fd.Num)<<typeShift | uint64(fd.Type)
}

func unpackFile(x uint64) FileDesc {
	return FileDesc{FileType(x) & TypeAll, int64(x >> typeShift)}
}
