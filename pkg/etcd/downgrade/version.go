// Package downgrade implements orchestration for downgrading the managed etcd
// datastore across a minor version boundary (e.g. etcd 3.7 -> 3.6 when the k8s
// snap is refreshed from 1.37 to 1.36).
//
// etcd requires an explicit downgrade protocol before any member's binary may
// be replaced with an older minor version: the downgrade must be validated and
// enabled cluster-wide, after which every member migrates its storage version
// down to the target. An older etcd binary refuses to start against a data
// directory stamped with a newer storage version, so skipping this protocol
// leaves etcd crash-looping after a snap downgrade.
package downgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coreos/go-semver/semver"
	bolt "go.etcd.io/bbolt"
)

// metaBucketName and storageVersionKeyName mirror etcd's storage schema
// (server/storage/schema): the backend bbolt database keeps the storage
// version in the "meta" bucket under the "storageVersion" key.
var (
	metaBucketName        = []byte("meta")
	storageVersionKeyName = []byte("storageVersion")
)

// Version is a major.minor etcd version, e.g. {Major: 3, Minor: 6}.
type Version struct {
	Major int64
	Minor int64
}

// ParseVersion parses an etcd version string. It accepts the forms produced by
// "etcd --version" output lines ("etcd Version: 3.7.1"), raw semver strings
// ("3.7.1", "v3.7.1") and major.minor strings ("3.7"). Only the major and
// minor components are significant for downgrade decisions.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "etcd Version:")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return Version{}, fmt.Errorf("empty version string")
	}
	// Allow major.minor input by appending a patch component.
	if parts := strings.Split(s, "."); len(parts) == 2 {
		s += ".0"
	}
	v, err := semver.NewVersion(s)
	if err != nil {
		return Version{}, fmt.Errorf("failed to parse version %q: %w", s, err)
	}
	return Version{Major: v.Major, Minor: v.Minor}, nil
}

// String returns the major.minor representation, e.g. "3.6".
func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// LessThan returns true if v is an older major.minor version than other.
func (v Version) LessThan(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	return v.Minor < other.Minor
}

// Equal returns true if v and other are the same major.minor version.
func (v Version) Equal(other Version) bool {
	return v.Major == other.Major && v.Minor == other.Minor
}

// IsValidDowngradeTarget reports whether other is a supported downgrade target
// for v. etcd only supports downgrading one minor version at a time within the
// same major version.
func (v Version) IsValidDowngradeTarget(other Version) bool {
	return v.Major == other.Major && v.Minor == other.Minor+1
}

// StorageVersionFromDataDir reads the etcd storage version stamped into the
// backend bbolt database of the given etcd data directory, without requiring a
// running etcd server.
//
// It returns nil (and no error) if no storage version is recorded, which is
// the case for data directories created by etcd 3.5 and older.
func StorageVersionFromDataDir(dataDir string) (*Version, error) {
	dbPath := filepath.Join(dataDir, "member", "snap", "db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to stat etcd backend database %q: %w", dbPath, err)
	}

	db, err := bolt.Open(dbPath, 0o400, &bolt.Options{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("failed to open etcd backend database %q: %w", dbPath, err)
	}
	defer db.Close()

	var raw []byte
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(metaBucketName)
		if b == nil {
			return nil
		}
		if v := b.Get(storageVersionKeyName); v != nil {
			raw = append([]byte(nil), v...)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to read storage version from %q: %w", dbPath, err)
	}

	if len(raw) == 0 {
		return nil, nil
	}

	v, err := ParseVersion(string(raw))
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage version %q from %q: %w", string(raw), dbPath, err)
	}
	return &v, nil
}

// VersionFromBOM reads the etcd component version recorded in the snap's
// bom.json (generated at build time by build-scripts/generate-bom.py).
// snapDir is the snap directory of the revision to inspect, e.g.
// /snap/k8s/current or /snap/k8s/1234.
func VersionFromBOM(snapDir string) (Version, error) {
	bomPath := filepath.Join(snapDir, "bom.json")
	data, err := os.ReadFile(bomPath)
	if err != nil {
		return Version{}, fmt.Errorf("failed to read %q: %w", bomPath, err)
	}

	var bom struct {
		Components struct {
			Etcd struct {
				Version string `json:"version"`
			} `json:"etcd"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &bom); err != nil {
		return Version{}, fmt.Errorf("failed to unmarshal %q: %w", bomPath, err)
	}
	if bom.Components.Etcd.Version == "" {
		return Version{}, fmt.Errorf("etcd version not found in %q", bomPath)
	}
	return ParseVersion(bom.Components.Etcd.Version)
}
