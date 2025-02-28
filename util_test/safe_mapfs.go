package util_test

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"sync"
	"testing/fstest"
)

var (
	ErrDuplicatePath = errors.New("duplicate path")
	ErrPathNotFound  = errors.New("path not found")
)

// SafeMapFS is a thread-safe wrapper around fstest.MapFS
type SafeMapFS struct {
	sync.Mutex
	mapFS fstest.MapFS
}

func NewSafeMapFS() *SafeMapFS {
	return &SafeMapFS{
		mapFS: make(fstest.MapFS),
	}
}

func UnrootedPath(pathStr string) string {
	return strings.TrimLeft(path.Clean(strings.TrimSpace(pathStr)), "/")
}

func (mfs *SafeMapFS) AddMapFile(pathStr string, mapf *fstest.MapFile) error {
	pathStr = UnrootedPath(pathStr)

	mfs.Lock()
	defer mfs.Unlock()

	_, found := mfs.mapFS[pathStr]
	if found {
		return ErrDuplicatePath
	}

	mfs.mapFS[pathStr] = mapf
	return nil
}

func (mfs *SafeMapFS) UpsertMapFile(pathStr string, mapf *fstest.MapFile) error {
	pathStr = UnrootedPath(pathStr)

	mfs.Lock()
	defer mfs.Unlock()

	mfs.mapFS[pathStr] = mapf
	return nil
}

func (mfs *SafeMapFS) GetMapFile(pathStr string) (*fstest.MapFile, error) {
	pathStr = UnrootedPath(pathStr)

	mfs.Lock()
	defer mfs.Unlock()

	mapf, found := mfs.mapFS[pathStr]
	if found {
		// Return a copy for thread safety
		copyf := *mapf
		return &copyf, nil
	}

	return nil, ErrPathNotFound
}

func (mfs *SafeMapFS) RemoveMapFile(pathStr string) (*fstest.MapFile, error) {
	pathStr = UnrootedPath(pathStr)

	mfs.Lock()
	defer mfs.Unlock()

	mapf, found := mfs.mapFS[pathStr]
	if found {
		delete(mfs.mapFS, pathStr)
		return mapf, nil
	}

	return nil, ErrPathNotFound
}

// Glob implements io/fs.GlobFS interface
func (mfs *SafeMapFS) Glob(pattern string) ([]string, error) {
	mfs.Lock()
	defer mfs.Unlock()
	return mfs.mapFS.Glob(pattern)
}

// Open implements io/fs.FS interface
func (mfs *SafeMapFS) Open(name string) (fs.File, error) {
	mfs.Lock()
	defer mfs.Unlock()
	return mfs.mapFS.Open(name)
}

// ReadDir implements io/fs.ReadDirFS interface
func (mfs *SafeMapFS) ReadDir(name string) ([]fs.DirEntry, error) {
	mfs.Lock()
	defer mfs.Unlock()
	return mfs.mapFS.ReadDir(name)
}

// ReadFile implements io/fs.ReadFileFS interface
func (mfs *SafeMapFS) ReadFile(name string) ([]byte, error) {
	mfs.Lock()
	defer mfs.Unlock()
	return mfs.mapFS.ReadFile(name)
}

// Stat implements io/fs.StatFS interface
func (mfs *SafeMapFS) Stat(name string) (fs.FileInfo, error) {
	mfs.Lock()
	defer mfs.Unlock()
	return mfs.mapFS.Stat(name)
}

// Sub implements io/fs.SubFS interface
func (mfs *SafeMapFS) Sub(dir string) (fs.FS, error) {
	mfs.Lock()
	defer mfs.Unlock()
	return mfs.mapFS.Sub(dir)
}
