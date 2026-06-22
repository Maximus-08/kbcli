package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type ImageCache struct {
	mu       sync.RWMutex
	path     string
	cache    map[string]string
	modified bool
}

func NewImageCache(path string) *ImageCache {
	ic := &ImageCache{
		path:  path,
		cache: make(map[string]string),
	}
	ic.Load()
	return ic
}

func (ic *ImageCache) Load() {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	data, err := os.ReadFile(ic.path)
	if err != nil {
		return
	}

	_ = json.Unmarshal(data, &ic.cache)
}

func (ic *ImageCache) Get(hash string) (string, bool) {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	desc, ok := ic.cache[hash]
	return desc, ok
}

func (ic *ImageCache) Set(hash string, desc string) {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.cache[hash] != desc {
		ic.cache[hash] = desc
		ic.modified = true
	}
}

func (ic *ImageCache) Save() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if !ic.modified {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(ic.path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(ic.cache, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(ic.path, data, 0644); err != nil {
		return err
	}

	ic.modified = false
	return nil
}

// ComputeSHA256 returns the hexadecimal SHA-256 hash of a file's content.
func ComputeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
