// Command catalog-check validates an app catalogue against the manifest parser
// the appliance actually uses, so a broken entry is caught before it is
// published rather than being silently skipped in somebody's store.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/danyx67800/homeos/backend/internal/appstore"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: catalog-check <catalogue-dir>")
		os.Exit(2)
	}
	appsDir := filepath.Join(os.Args[1], "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", appsDir, err)
		os.Exit(1)
	}

	bad := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(appsDir, e.Name(), "homeos-app.yml")
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("  MISSING  %s/homeos-app.yml\n", e.Name())
			bad++
			continue
		}
		m, err := appstore.Parse(raw)
		if err != nil {
			fmt.Printf("  INVALID  %-16s %v\n", e.Name(), err)
			bad++
			continue
		}
		if m.ID != e.Name() {
			fmt.Printf("  INVALID  %-16s id is %q but the directory is %q\n", e.Name(), m.ID, e.Name())
			bad++
			continue
		}
		fmt.Printf("  ok       %-16s %-14s v%-10s %v\n", m.ID, m.Category, m.Version, m.Architectures)
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "\n%d manifest(s) failed\n", bad)
		os.Exit(1)
	}
	fmt.Printf("\n%d manifests valid\n", len(entries))
}
