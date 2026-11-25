// content-scan is a read-only dry run of the upload content policy over an
// existing localfs blob directory: it prints aggregate verdicts and the
// object keys of non-ok blobs (never content). Run it on a server before
// rolling out stricter rules, then map keys to owners/mime via file_blobs.
//
//	content-scan <data/blobs dir>
//
// Expected non-ok entries: secret-chat files ("enc:" location keys, E2E
// encrypted by design and never inspected on upload) and in-flight upload
// parts (*.part).
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"telesrv/internal/app/files"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: content-scan <blob dir>")
		os.Exit(2)
	}
	root := os.Args[1]
	counts := map[string]int{}
	var hits []string
	start := time.Now()
	n := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		st, err := f.Stat()
		if err == nil && st.Size() > 0 {
			kind, format, detail := files.InspectContentForScan(f, st.Size())
			key := kind + "|" + format
			counts[key]++
			if kind != "" {
				hits = append(hits, fmt.Sprintf("%s\t%s\t%s\t%d\t%s", filepath.Base(path), kind, format, st.Size(), detail))
			}
		}
		f.Close()
		n++
		return nil
	})
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("scanned %d files in %s\n", n, time.Since(start).Round(time.Second))
	for _, k := range keys {
		fmt.Printf("%8d  %s\n", counts[k], k)
	}
	fmt.Println("--- non-ok")
	for _, h := range hits {
		fmt.Println(h)
	}
}
