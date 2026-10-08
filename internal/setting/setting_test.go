package setting

import (
	"ai_analyze_guokao/internal/store"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestExistingInvalidKeyIsNeverOverwritten(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	path := filepath.Join(t.TempDir(), "key")
	original := []byte("malformed but must remain")
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(db, path); e == nil {
		t.Fatal("invalid key accepted and overwritten")
	}
	b, e := os.ReadFile(path)
	if e != nil || string(b) != string(original) {
		t.Fatal("key changed", e)
	}
}
func TestMissingKeyWithEncryptedSettingsRefusesInitialization(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "key")
	s, e := Open(db, path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Set(ctx, KeyAPIKey, "synthetic", "test"); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(db, path); e == nil {
		t.Fatal("lost key silently replaced")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("replacement file created")
	}
}
func TestConcurrentInitializationSharesOneKey(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	path := filepath.Join(t.TempDir(), "key")
	var wg sync.WaitGroup
	results := make(chan *Store, 10)
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := Open(db, path)
			if e != nil {
				errs <- e
			} else {
				results <- s
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	var first *Store
	for s := range results {
		if first == nil {
			first = s
		} else if string(first.key) != string(s.key) {
			t.Fatal("different keys issued")
		}
	}
}
