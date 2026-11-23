// Copyright 2011 Evan Shaw. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// These tests are adapted from gommap: http://labix.org/gommap
// Copyright (c) 2010, Gustavo Niemeyer <gustavo@niemeyer.net>

package mmap

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var testData = []byte("0123456789ABCDEF")
var testPath = filepath.Join(os.TempDir(), "testdata")

func init() {
	f := openFile(os.O_RDWR | os.O_CREATE | os.O_TRUNC)
	f.Write(testData)
	f.Close()
}

func openFile(flags int) *os.File {
	f, err := os.OpenFile(testPath, flags, 0644)
	if err != nil {
		panic(err.Error())
	}
	return f
}

func unmap(t *testing.T, m MMap) {
	t.Helper()
	if err := m.Unmap(); err != nil {
		t.Errorf("error unmapping: %s", err)
	}
}

func TestUnmap(t *testing.T) {
	f := openFile(os.O_RDONLY)
	defer f.Close()
	mmap, err := Map(f, RDONLY, 0)
	if err != nil {
		t.Errorf("error mapping: %s", err)
	}
	if err := mmap.Unmap(); err != nil {
		t.Errorf("error unmapping: %s", err)
	}
}

func TestReadWrite(t *testing.T) {
	f := openFile(os.O_RDWR)
	defer f.Close()
	mmap, err := Map(f, RDWR, 0)
	if err != nil {
		t.Errorf("error mapping: %s", err)
	}
	defer unmap(t, mmap)
	if !bytes.Equal(testData, mmap) {
		t.Errorf("mmap != testData: %q, %q", mmap, testData)
	}

	mmap[9] = 'X'
	mmap.Flush()

	fileData, err := ioutil.ReadAll(f)
	if err != nil {
		t.Errorf("error reading file: %s", err)
	}
	if !bytes.Equal(fileData, []byte("012345678XABCDEF")) {
		t.Errorf("file wasn't modified")
	}

	// leave things how we found them
	mmap[9] = '9'
	mmap.Flush()
}

func TestProtFlagsAndErr(t *testing.T) {
	f := openFile(os.O_RDONLY)
	defer f.Close()
	if _, err := Map(f, RDWR, 0); err == nil {
		t.Errorf("expected error")
	}
}

func TestFlags(t *testing.T) {
	f := openFile(os.O_RDWR)
	defer f.Close()
	mmap, err := Map(f, COPY, 0)
	if err != nil {
		t.Errorf("error mapping: %s", err)
	}

	mmap[9] = 'X'
	if err := mmap.Flush(); err != nil {
		t.Errorf("error flushing: %s", err)
	}

	fileData, err := ioutil.ReadAll(f)
	if err != nil {
		t.Errorf("error reading file: %s", err)
	}
	if !bytes.Equal(fileData, testData) {
		t.Errorf("file was modified")
	}
	unmap(t, mmap)
}

// Test that we can map files from non-0 offsets
// The page size on most Unixes is 4KB, but on Windows it's 64KB
func TestNonZeroOffset(t *testing.T) {
	const pageSize = 65536

	// Create a 2-page sized file
	bigFilePath := filepath.Join(os.TempDir(), "nonzero")
	fileobj, err := os.OpenFile(bigFilePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		panic(err.Error())
	}

	bigData := make([]byte, 2*pageSize, 2*pageSize)
	fileobj.Write(bigData)
	fileobj.Close()

	// Map the first page by itself
	fileobj, err = os.OpenFile(bigFilePath, os.O_RDONLY, 0)
	if err != nil {
		panic(err.Error())
	}
	m, err := MapRegion(fileobj, pageSize, RDONLY, 0, 0)
	if err != nil {
		t.Errorf("error mapping file: %s", err)
	}
	unmap(t, m)
	fileobj.Close()

	// Map the second page by itself
	fileobj, err = os.OpenFile(bigFilePath, os.O_RDONLY, 0)
	if err != nil {
		panic(err.Error())
	}
	m, err = MapRegion(fileobj, pageSize, RDONLY, 0, pageSize)
	if err != nil {
		t.Errorf("error mapping file: %s", err)
	}
	err = m.Unmap()
	if err != nil {
		t.Error(err)
	}

	m, err = MapRegion(fileobj, pageSize, RDONLY, 0, 1)
	if err == nil {
		t.Error("expect error because offset is not multiple of page size")
	}

	fileobj.Close()
}

func TestAnonymousMapping(t *testing.T) {
	const size = 4 * 1024

	// Make an anonymous region
	mem, err := MapRegion(nil, size, RDWR, ANON, 0)
	if err != nil {
		t.Fatalf("failed to allocate memory for buffer: %v", err)
	}

	// Check memory writable
	for i := 0; i < size; i++ {
		mem[i] = 0x55
	}

	// And unmap it
	err = mem.Unmap()
	if err != nil {
		t.Fatalf("failed to unmap memory for buffer: %v", err)
	}
}

func TestFaultError(t *testing.T) {
	var size = os.Getpagesize()

	// Make an anonymous region
	faultyMmap, err := MapRegion(nil, size, RDWR, ANON, 0)
	if err != nil {
		t.Fatalf("failed to allocate memory for buffer: %v", err)
	}
	reader := faultyMmap.Reader()

	// To cause a fault on our reads, unmap the region
	unmap(t, faultyMmap)

	var buffer = make([]byte, size)

	// Test Read
	_, err = reader.Read(buffer)
	if err == nil {
		t.Fatalf("No error on Read() on invalid MMAP")
	}
	t.Log(err)

	// Test ReadAt
	_, err = reader.ReadAt(buffer, 10)
	if err == nil {
		t.Fatalf("No error on ReadAt() on invalid MMAP")
	}
	t.Log(err)

	// Test WriteTo
	_, err = reader.WriteTo(bytes.NewBuffer(buffer))
	if err == nil {
		t.Fatalf("No error on WriteTo() on invalid MMAP")
	}
	t.Log(err)

	// Test that other, non-IO functions don't fault
	reader.Size()
	reader.Seek(10, io.SeekStart)
	reader.Len()
}

// TestUnmapReleasesFile checks that the file is no longer mapped after Unmap.
func TestUnmapReleasesFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to truncate a file that is still mapped")
	}
	tests := []struct {
		name string
		flag int
		prot int
	}{
		{"RDONLY", os.O_RDONLY, RDONLY},
		{"RDWR", os.O_RDWR, RDWR},
		// Unmap of a COPY mapping used to fail and leak on Windows when the file was opened read-only.
		{"COPY", os.O_RDONLY, COPY},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "testdata")
			if err := os.WriteFile(path, testData, 0644); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, test.flag, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			mmap, err := Map(f, test.prot, 0)
			if err != nil {
				t.Fatalf("error mapping: %s", err)
			}
			if err := mmap.Unmap(); err != nil {
				t.Errorf("error unmapping: %s", err)
			}
			// In Windows we can't truncate a file while it is still mapped,
			// so if Unmap doesn't release the mapping completely, the truncation will fail.
			// https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-setendoffile
			if err := os.Truncate(path, 0); err != nil {
				t.Errorf("file is still mapped after Unmap: %s", err)
			}

		})
	}
}

func TestUnmapCopyAfterFileClosed(t *testing.T) {
	f := openFile(os.O_RDONLY)
	mmap, err := Map(f, COPY, 0)
	f.Close()
	if err != nil {
		t.Fatalf("error mapping: %s", err)
	}
	if err := mmap.Unmap(); err != nil {
		t.Errorf("error unmapping: %s", err)
	}
}

func TestFlushAfterFileClosed(t *testing.T) {
	// t.TempDir cleanup fails the test on Windows if Unmap leaks a file handle
	// since Go's default handle share rights on Windows prevent the file from being deleted.
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, testData, 0644); err != nil {
		t.Fatalf("error writing file: %s", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("error opening file: %s", err)
	}
	mmap, err := Map(f, RDWR, 0)
	f.Close()
	if err != nil {
		t.Fatalf("error mapping: %s", err)
	}

	mmap[9] = 'X'
	if err := mmap.Flush(); err != nil {
		t.Errorf("error flushing: %s", err)
	}
	if err := mmap.Unmap(); err != nil {
		t.Errorf("error unmapping: %s", err)
	}

	fileData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("error reading file: %s", err)
	}
	if !bytes.Equal(fileData, []byte("012345678XABCDEF")) {
		t.Errorf("file wasn't modified: %q", fileData)
	}
}

func TestFlushAnonymousMapping(t *testing.T) {
	mem, err := MapRegion(nil, os.Getpagesize(), RDWR, ANON, 0)
	if err != nil {
		t.Fatalf("error mapping: %s", err)
	}
	mem[0] = 0x55
	if err := mem.Flush(); err != nil {
		t.Errorf("error flushing: %s", err)
	}
	if err := mem.Unmap(); err != nil {
		t.Errorf("error unmapping: %s", err)
	}
}
