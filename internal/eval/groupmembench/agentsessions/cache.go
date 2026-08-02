package agentsessions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

type DiskCache struct{ Dir string }

func CacheKey(scope, prompt string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + prompt))
	return hex.EncodeToString(sum[:])
}

func (c *DiskCache) path(key string) string {
	return filepath.Join(c.Dir, key+".json")
}

func (c *DiskCache) Get(key string) (string, bool) {
	if c.Dir == "" {
		return "", false
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (c *DiskCache) Put(key, value string) error {
	if c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	tmp := c.path(key) + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path(key))
}
