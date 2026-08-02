package agentsessions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

func (c *DiskCache) Put(key, value string) (err error) {
	if c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, key+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temp cache file %s: %w", tmpPath, removeErr))
		}
	}()
	if _, writeErr := tmp.WriteString(value); writeErr != nil {
		if closeErr := tmp.Close(); closeErr != nil {
			return errors.Join(writeErr, closeErr)
		}
		return writeErr
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, c.path(key))
}
