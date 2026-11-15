package store

import (
	"os"
	"sync"
	"time"
)

// ReadFileCached is os.ReadFile with an in-memory cache keyed by
// path, validated by mtime+size on every call (one stat, no disk
// read on a hit). The game serves many immutable JSON documents
// (responses/, gamedata/) that handlers used to re-read from disk
// on every request. The returned slice is shared: callers must not
// mutate it (JSON unmarshal and HTTP writes never do).
var (
	fileCacheMu sync.Mutex
	fileCache   = map[string]fileCacheEntry{}
)

type fileCacheEntry struct {
	modTime time.Time
	size    int64
	data    []byte
}

func ReadFileCached(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	fileCacheMu.Lock()
	if e, ok := fileCache[path]; ok && e.size == fi.Size() && e.modTime.Equal(fi.ModTime()) {
		data := e.data
		fileCacheMu.Unlock()
		return data, nil
	}
	fileCacheMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fileCacheMu.Lock()
	fileCache[path] = fileCacheEntry{modTime: fi.ModTime(), size: fi.Size(), data: data}
	fileCacheMu.Unlock()
	return data, nil
}
