package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/cespare/xxhash/v2"
)

// FileMeta holds basic metadata for a file
type FileMeta struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Hash    string    `json:"hash"`
	IsDir   bool      `json:"is_dir"`
	// Path is the absolute path to the file on disk (forward slashes)
	Path string `json:"path"`
	// Rel is the path relative to the indexed root (if available)
	Rel string `json:"rel,omitempty"`
	// Checked indicates whether this entry was checked during a comparison
	Checked int `json:"checked,omitempty"`
}

// IndexMap maps relative path -> FileMeta
type IndexMap map[string]FileMeta

// BuildIndex walks root and builds an IndexMap. It skips the root itself.
// bypassIgnore: if true, skip ignore pattern checking and index all files
func BuildIndex(root string, bypassIgnore bool) (IndexMap, error) {
	idx := IndexMap{}

	// create ignore cache only if not bypassing
	var ic *SimpleIgnoreCache
	if !bypassIgnore {
		ic = NewSimpleIgnoreCache(root)
	}

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// skip problematic entries but continue
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return nil
		}
		// skip ignored entries (only if not bypassing)
		if !bypassIgnore && ic.MatchWithManualTransfer(p, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// compute absolute path
		abs, err := filepath.Abs(p)
		if err != nil {
			// fallback to p
			abs = p
		}
		abs = filepath.ToSlash(abs)

		meta := FileMeta{
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
			Hash:    "",
			Path:    abs,
			Rel:     rel,
		}

		if !info.IsDir() {
			h, err := hashFile(p)
			if err == nil {
				meta.Hash = h
			} else {
				// if hashing fails, continue without hash
				fmt.Fprintf(os.Stderr, "warning: failed to hash %s: %v\n", p, err)
			}
		}

		// use absolute path as the map key (recommended)
		idx[meta.Path] = meta
		return nil
	})

	return idx, err
}

// BuildIndexSubtree walks only the subtree starting at 'start' and builds an IndexMap
// Paths' Rel field are computed relative to root. If start does not exist, return empty map and error.
func BuildIndexSubtree(root, start string, bypassIgnore bool) (IndexMap, error) {
	idx := IndexMap{}

	// ensure start exists
	stInfo, err := os.Stat(start)
	if err != nil {
		return nil, err
	}

	// create ignore cache only if not bypassing
	var ic *SimpleIgnoreCache
	if !bypassIgnore {
		ic = NewSimpleIgnoreCache(root)
	}

	// Walk starting at start
	err = filepath.WalkDir(start, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// compute rel relative to root
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// skip ignored entries (only if not bypassing)
		if !bypassIgnore && ic.MatchWithManualTransfer(p, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// compute absolute path
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		abs = filepath.ToSlash(abs)

		meta := FileMeta{
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
			Hash:    "",
			Path:    abs,
			Rel:     rel,
		}

		if !info.IsDir() {
			h, err := hashFile(p)
			if err == nil {
				meta.Hash = h
			} else {
				// if hashing fails, continue without hash
				fmt.Fprintf(os.Stderr, "warning: failed to hash %s: %v\n", p, err)
			}
		}

		idx[meta.Path] = meta
		return nil
	})

	// If start is a single file and not a directory, ensure it was included
	if !stInfo.IsDir() {
		// WalkDir above will still handle the file, so nothing special
	}

	return idx, err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// SaveIndexFile writes the index as JSON to dbPath (overwrites)
func SaveIndexFile(dbPath string, idx IndexMap) error {
	tmp := dbPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, dbPath)
}

// LoadIndexFile reads JSON index from dbPath
func LoadIndexFile(dbPath string) (IndexMap, error) {
	idx := IndexMap{}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return idx, nil
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// CompareIndices returns slices of added, modified, removed relative paths
func CompareIndices(oldIdx, newIdx IndexMap) (added, modified, removed []string) {
	if oldIdx == nil {
		oldIdx = IndexMap{}
	}
	seen := map[string]bool{}
	for p, m := range newIdx {
		seen[p] = true
		if old, ok := oldIdx[p]; !ok {
			added = append(added, p)
		} else {
			// modified if size or hash or modtime differ
			if old.Size != m.Size || old.Hash != m.Hash || !old.ModTime.Equal(m.ModTime) {
				modified = append(modified, p)
			}
		}
	}
	for p := range oldIdx {
		if !seen[p] {
			removed = append(removed, p)
		}
	}
	return
}

// SaveIndexDB saves the index into a sqlite database at dbPath.
// Schema: files(path TEXT PRIMARY KEY, rel TEXT, size INTEGER, mod_time INTEGER, hash TEXT, is_dir INTEGER, checked INTEGER)
func SaveIndexDB(dbPath string, idx IndexMap) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS files (
		path TEXT PRIMARY KEY,
		rel TEXT,
		size INTEGER,
		mod_time INTEGER,
		hash TEXT,
		is_dir INTEGER,
		checked INTEGER DEFAULT 0
	)`); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files`); err != nil {
		tx.Rollback()
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO files(path, rel, size, mod_time, hash, is_dir, checked) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, m := range idx {
		if _, err := stmt.Exec(m.Path, m.Rel, m.Size, m.ModTime.UnixNano(), m.Hash, boolToInt(m.IsDir), m.Checked); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// LoadIndexDB loads index from sqlite DB at dbPath
func LoadIndexDB(dbPath string) (IndexMap, error) {
	idx := IndexMap{}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// Try the newer schema first (with checked column)
	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir, checked FROM files`)
	if err != nil {
		// Fallback to older schema without checked
		rows2, err2 := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
		if err2 != nil {
			return nil, err // return original error
		}
		defer rows2.Close()
		for rows2.Next() {
			var path, rel, hash string
			var size int64
			var modNano int64
			var isDirInt int
			if err := rows2.Scan(&path, &rel, &size, &modNano, &hash, &isDirInt); err != nil {
				return nil, err
			}
			idx[path] = FileMeta{
				Path:    path,
				Rel:     rel,
				Size:    size,
				ModTime: time.Unix(0, modNano),
				Hash:    hash,
				IsDir:   intToBool(isDirInt),
				Checked: 0,
			}
		}
		if err := rows2.Err(); err != nil {
			return nil, err
		}
		return idx, nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, rel, hash string
		var size int64
		var modNano int64
		var isDirInt int
		var checkedInt int
		if err := rows.Scan(&path, &rel, &size, &modNano, &hash, &isDirInt, &checkedInt); err != nil {
			return nil, err
		}
		idx[path] = FileMeta{
			Path:    path,
			Rel:     rel,
			Size:    size,
			ModTime: time.Unix(0, modNano),
			Hash:    hash,
			IsDir:   intToBool(isDirInt),
			Checked: checkedInt,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return idx, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i != 0
}
