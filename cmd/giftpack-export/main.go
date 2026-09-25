// Command giftpack-export renders the gift packs authored in this repo
// (internal/seed/giftpacks) into the .zip archives an operator uploads in
// the admin panel.
//
// Packs are not compiled into the running server: it starts with an empty
// shelf, and a pack only exists once someone uploads it. This is how the
// repo's own packs become uploadable files.
//
//	go run ./cmd/giftpack-export                 # every pack into dist/packs
//	go run ./cmd/giftpack-export -o /tmp energy-pack
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"telesrv/internal/seed/giftpacks"
)

func main() {
	out := flag.String("o", filepath.Join("dist", "packs"), "directory to write the archives into")
	flag.Parse()

	ids := flag.Args()
	if len(ids) == 0 {
		ids = giftpacks.IDs()
	}
	if err := run(*out, ids); err != nil {
		fmt.Fprintln(os.Stderr, "giftpack-export:", err)
		os.Exit(1)
	}
}

func run(dir string, ids []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, id := range ids {
		data, err := giftpacks.Archive(id)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, id+".zip")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		fmt.Printf("%s\t%d KiB\n", path, len(data)/1024)
	}
	return nil
}
