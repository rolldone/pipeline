package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	var dbPath string
	flag.StringVar(&dbPath, "db", ".sync_temp/indexing_files.db", "path to indexing_files.db")
	flag.Parse()

	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot stat db file %s: %v\n", dbPath, err)
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to open db: %v\n", err)
		os.Exit(2)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files ORDER BY path`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: query failed: %v\n", err)
		os.Exit(2)
	}
	defer rows.Close()

	fmt.Printf("Indexed files in %s:\n", dbPath)
	for rows.Next() {
		var path, rel, hash string
		var size int64
		var modNano int64
		var isDirInt int
		if err := rows.Scan(&path, &rel, &size, &modNano, &hash, &isDirInt); err != nil {
			fmt.Fprintf(os.Stderr, "error: scan failed: %v\n", err)
			os.Exit(2)
		}
		mod := time.Unix(0, modNano).UTC().Format(time.RFC3339)
		isDir := "file"
		if isDirInt != 0 {
			isDir = "dir"
		}
		fmt.Printf("- %s (%s) rel=%s size=%d hash=%s mtime=%s\n", path, isDir, rel, size, hash, mod)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: rows iteration: %v\n", err)
		os.Exit(2)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: rows iteration: %v\n", err)
		os.Exit(2)
	}
}
