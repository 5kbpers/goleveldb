// Copyright (c) 2013, Suryandaru Triandana <syndtr@gmail.com>
// All rights reserved.
//
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package storage

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"syscall/js"
	"time"
)

type IndexedDBFile struct {
	sync.Mutex

	db       *js.Value
	dbName   string
	store    string
	filename string
	buff     *bytes.Buffer
	open     bool
	everRead bool
}

func IsExisted(dbName, store, filename string) (err error) {
	c := make(chan error)
	go isExisted(c, dbName, store, filename)
	err = <-c
	return
}

func isExisted(c chan error, dbName, store, filename string) {
	defer func() {
		c <- errors.New("file not found")
	}()

	idb := js.Global().Get("window").Get("indexedDB")
	req := idb.Call("open", dbName)

	var onError js.Func
	onError = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go func() {
			onError.Release()
			c <- errors.New("file not found")
		}()
		return nil
	})

	var onSuccess js.Func
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go func() {
			event := args[0]
			db := event.Get("target").Get("result")

			if !db.Get("objectStoreNames").Call("contains", store).Bool() {
				onSuccess.Release()
				c <- errors.New("file not found")
				return
			}

			onSuccess.Release()
			c <- nil
		}()
		return nil
	})

	req.Set("onsuccess", onSuccess)
	req.Set("onerror", onError)
}

func Open(dbName, store, filename string) (file *IndexedDBFile, err error) {
	var init bool

	dbChan := make(chan *js.Value)
	go open(dbChan, dbName, store, &init)
	db := <-dbChan
	if db == nil {
		err = errors.New("open db error")
		return
	}

	file = &IndexedDBFile{
		db:       db,
		dbName:   dbName,
		store:    store,
		filename: filename,
		buff:     new(bytes.Buffer),
	}

	if !init {
		file.load()
	}

	fmt.Printf("read files %+v\n", len(file.buff.String()))

	go func() {
		t := time.NewTicker(time.Second * 5)
		defer t.Stop()

		for {
			select {
			case <-t.C:
				file.Sync()
			}
		}
	}()
	return
}

func open(c chan *js.Value, dbName, store string, init *bool) {
	idb := js.Global().Get("window").Get("indexedDB")
	req := idb.Call("open", dbName)

	var onError js.Func
	onError = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go func() {
			onError.Release()
			c <- nil
		}()
		return nil
	})

	var onSuccess js.Func
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go func() {
			result := req.Get("result")
			onSuccess.Release()
			c <- &result
		}()
		return nil
	})

	var onUpgradeNeeded js.Func
	onUpgradeNeeded = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		*init = true
		event := args[0]
		db := event.Get("target").Get("result")

		dbOnError := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			go func() {
				onError.Release()
				c <- nil
			}()
			return nil
		})
		db.Set("onerror", dbOnError)

		if !db.Get("objectStoreNames").Call("contains", store).Bool() {
			db.Call("createObjectStore", store, map[string]interface{}{
				"keyPath": "id",
			})

			transaction := event.Get("target").Get("transaction")
			var onComplete js.Func
			onComplete = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				go func() {
					onComplete.Release()
					onUpgradeNeeded.Release()
					c <- &db
				}()
				return nil
			})
			transaction.Set("oncomplete", onComplete)
		} else {
			go func() {
				onUpgradeNeeded.Release()
				c <- &db
			}()
		}
		return nil
	})

	req.Set("onupgradeneeded", onUpgradeNeeded)
	req.Set("onsuccess", onSuccess)
	req.Set("onerror", onError)
}

func (file *IndexedDBFile) Write(data []byte) (n int, err error) {
	file.Lock()
	defer file.Unlock()

	return file.buff.Write(data)
}

func (file *IndexedDBFile) Sync() (err error) {
	file.Lock()
	defer file.Unlock()

	c := make(chan struct{})
	go file.write(c)
	<-c
	return
}

func (file *IndexedDBFile) write(c chan struct{}) {
	defer func() {
		err := recover()
		if err != nil {
			fmt.Println(err)
		}
		c <- struct{}{}
	}()

	str := base64.StdEncoding.EncodeToString(file.buff.Bytes())
	req := file.db.Call("transaction", []interface{}{file.store}, "readwrite").
		Call("objectStore", file.store).
		Call("put", map[string]interface{}{
			"id":   file.filename,
			"file": str,
		})

	var onError js.Func
	onError = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go func() {
			onError.Release()
			c <- struct{}{}
		}()
		return nil
	})

	var onSuccess js.Func
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		event := args[0]
		transaction := event.Get("target").Get("transaction")
		var onComplete js.Func
		onComplete = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			go func() {
				onComplete.Release()
				onSuccess.Release()
				c <- struct{}{}
			}()
			return nil
		})
		transaction.Set("oncomplete", onComplete)

		// onSuccess.Release()
		return nil
	})

	req.Set("onsuccess", onSuccess)
	req.Set("onerror", onError)
}

func (file *IndexedDBFile) Read(data []byte) (n int, err error) {
	file.Lock()
	defer file.Unlock()

	return file.buff.Read(data)
}

func (file *IndexedDBFile) load() {
	file.Lock()
	defer file.Unlock()

	c := make(chan struct{}, 4)
	go file.readData(c)
	<-c
	return
}

func (file *IndexedDBFile) readData(c chan struct{}) {
	defer func() {
		err := recover()
		if err != nil {
			fmt.Println(err)
		}

		c <- struct{}{}
	}()

	req := file.db.Call("transaction", []interface{}{file.store}).
		Call("objectStore", file.store).
		Call("get", file.filename)

	var onError js.Func
	onError = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go func() {
			onError.Release()
			c <- struct{}{}
		}()
		return nil
	})

	var onSuccess js.Func
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		result := req.Get("result")
		if result != js.Null() && result != js.Undefined() {
			f := result.Get("file").String()

			data, err := base64.StdEncoding.DecodeString(f)
			if err != nil {
				go func() {
					onSuccess.Release()
					c <- struct{}{}
				}()
				return nil
			}
			file.buff = bytes.NewBuffer(data)
		}
		go func() {
			onSuccess.Release()
			c <- struct{}{}
		}()
		return nil
	})

	req.Set("onsuccess", onSuccess)
	req.Set("onerror", onError)

}

func (file *IndexedDBFile) Remove() {
	c := make(chan struct{})
	go file.remove(c)
	<-c
}

func (file *IndexedDBFile) remove(c chan struct{}) {
	req := file.db.Call("transaction", []interface{}{file.store}, "readwrite").
		Call("objectStore", file.store).
		Call("delete", file.filename)

	var onError js.Func
	onError = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		onError.Release()
		return nil
	})

	var onSuccess js.Func
	onSuccess = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		onSuccess.Release()
		return nil
	})

	req.Set("onsuccess", onSuccess)
	req.Set("onerror", onError)
}

func (file *IndexedDBFile) Close() (err error) {
	return file.Sync()
}

func (file *IndexedDBFile) Reader() (reader *bytes.Reader) {
	if !file.everRead {
		go file.load()
		file.everRead = true
	}

	file.Lock()
	defer file.Unlock()

	reader = bytes.NewReader(file.buff.Bytes())
	return
}
