package downgrade

import (
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    Version
		wantErr bool
	}{
		{name: "semver", in: "3.7.1", want: Version{Major: 3, Minor: 7}},
		{name: "v prefix", in: "v3.6.13", want: Version{Major: 3, Minor: 6}},
		{name: "etcd --version line", in: "etcd Version: 3.7.1", want: Version{Major: 3, Minor: 7}},
		{name: "major.minor", in: "3.6", want: Version{Major: 3, Minor: 6}},
		{name: "with whitespace", in: "  3.7.0\n", want: Version{Major: 3, Minor: 7}},
		{name: "empty", in: "", wantErr: true},
		{name: "garbage", in: "not-a-version", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseVersion(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseVersion(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestVersionCompare(t *testing.T) {
	v36 := Version{Major: 3, Minor: 6}
	v37 := Version{Major: 3, Minor: 7}

	if !v36.LessThan(v37) {
		t.Fatal("3.6 should be less than 3.7")
	}
	if v37.LessThan(v36) {
		t.Fatal("3.7 should not be less than 3.6")
	}
	if !v36.Equal(Version{Major: 3, Minor: 6}) {
		t.Fatal("3.6 should equal 3.6")
	}
	if v36.Equal(v37) {
		t.Fatal("3.6 should not equal 3.7")
	}
	if !v37.IsValidDowngradeTarget(v36) {
		t.Fatal("3.6 should be a valid downgrade target for 3.7")
	}
	if v37.IsValidDowngradeTarget(Version{Major: 3, Minor: 5}) {
		t.Fatal("3.5 should not be a valid downgrade target for 3.7 (skips a minor)")
	}
	if v36.IsValidDowngradeTarget(v37) {
		t.Fatal("3.7 should not be a valid downgrade target for 3.6 (upgrade)")
	}
}

func writeStorageVersion(t *testing.T, dataDir string, version *string) {
	t.Helper()
	dbDir := filepath.Join(dataDir, "member", "snap")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(filepath.Join(dbDir, "db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(metaBucketName)
		if err != nil {
			return err
		}
		if version != nil {
			return b.Put(storageVersionKeyName, []byte(*version))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStorageVersionFromDataDir(t *testing.T) {
	t.Run("missing data dir", func(t *testing.T) {
		v, err := StorageVersionFromDataDir(filepath.Join(t.TempDir(), "does-not-exist"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != nil {
			t.Fatalf("expected nil version, got %v", v)
		}
	})

	t.Run("stamped 3.7 data dir", func(t *testing.T) {
		dataDir := t.TempDir()
		writeStorageVersion(t, dataDir, ptr("3.7.0"))
		v, err := StorageVersionFromDataDir(dataDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v == nil || *v != (Version{Major: 3, Minor: 7}) {
			t.Fatalf("expected 3.7, got %v", v)
		}
	})

	t.Run("pre-3.6 data dir without storage version", func(t *testing.T) {
		dataDir := t.TempDir()
		writeStorageVersion(t, dataDir, nil)
		v, err := StorageVersionFromDataDir(dataDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != nil {
			t.Fatalf("expected nil version for pre-3.6 data dir, got %v", v)
		}
	})
}

func TestVersionFromBOM(t *testing.T) {
	dir := t.TempDir()
	bom := `{"components": {"etcd": {"version": "v3.7.1"}, "kubernetes": {"version": "v1.37.0"}}}`
	if err := os.WriteFile(filepath.Join(dir, "bom.json"), []byte(bom), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := VersionFromBOM(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != (Version{Major: 3, Minor: 7}) {
		t.Fatalf("expected 3.7, got %v", v)
	}

	if _, err := VersionFromBOM(t.TempDir()); err == nil {
		t.Fatal("expected error for missing bom.json")
	}
}

func ptr[T any](v T) *T { return &v }
