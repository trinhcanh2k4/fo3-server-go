package game

import "fo3server/internal/store"

// readFileCached serves immutable gamedata/responses documents from
// the store package's mtime-validated in-memory cache instead of
// re-reading them from disk on every call.
func readFileCached(path string) ([]byte, error) {
	return store.ReadFileCached(path)
}
