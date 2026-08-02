package agentsessions

import (
	"sync"
	"testing"
)

func TestDiskCacheRoundTrip(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	key := CacheKey("User_1/2025-07-19/s1", "prompt text")
	if _, ok := c.Get(key); ok {
		t.Fatal("want miss before put")
	}
	if err := c.Put(key, `{"actions":[]}`); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get(key)
	if !ok || got != `{"actions":[]}` {
		t.Fatalf("want hit, got %q ok=%v", got, ok)
	}
}

func TestDiskCacheKeyChangesWithPrompt(t *testing.T) {
	if CacheKey("s", "p1") == CacheKey("s", "p2") {
		t.Fatal("key must depend on prompt")
	}
}

func TestDiskCacheEmptyDirIsNoop(t *testing.T) {
	c := &DiskCache{}
	if err := c.Put("k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("empty-dir cache must miss")
	}
}

func TestDiskCachePutConcurrentSameKey(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	key := CacheKey("scope", "prompt")
	const numGoroutines = 16
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			value := "concurrent-" + string(rune(id))
			if err := c.Put(key, value); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, ok := c.Get(key)
	if !ok {
		t.Fatal("want cache hit after concurrent puts")
	}
	if len(got) == 0 {
		t.Fatal("want non-empty value, got empty string")
	}
}
