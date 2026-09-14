// Command gendocs regenerates pronto's published reference docs from source.
//
// Invoked by the go:generate directive in internal/events/schema.go
// (mise run generate). Run from anywhere in the repo.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kalverra/pronto/tools/gendocs"
)

func main() {
	out := flag.String("out", "docs", "output directory, relative to the repo root")
	check := flag.Bool("check", false, "verify committed docs are current; exit 1 if stale, without writing")
	flag.Parse()

	root, err := findRoot(".")
	if err != nil {
		fatal(err)
	}
	outDir := filepath.Join(root, *out)

	if *check {
		if err := checkGenerated(root, outDir); err != nil {
			fatal(err)
		}
		fmt.Println("generated files are current")
		return
	}
	if err := gendocs.Generate(root, outDir); err != nil {
		fatal(err)
	}
	if err := gendocs.GenerateSchema(root); err != nil {
		fatal(err)
	}
}

// findRoot walks up from start until it finds the directory containing go.mod.
func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", start)
		}
		dir = parent
	}
}

// checkGenerated regenerates docs into a temp dir and the schema in memory,
// then reports any that differ from the committed copies.
func checkGenerated(root, outDir string) error {
	tmp, err := os.MkdirTemp("", "gendocs-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := gendocs.Generate(root, tmp); err != nil {
		return err
	}

	stale := false
	for _, name := range gendocs.Files {
		want, err := os.ReadFile(filepath.Join(tmp, name)) // #nosec G304 — generated doc names, not user input.
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(outDir, name)) // #nosec G304 — generated doc names, not user input.
		if os.IsNotExist(err) {
			fmt.Printf("docs/%s: missing\n", name)
			stale = true
			continue
		}
		if err != nil {
			return err
		}
		if string(want) != string(got) {
			fmt.Printf("docs/%s: stale\n", name)
			stale = true
		}
	}

	schemaWant, err := gendocs.RenderSchema()
	if err != nil {
		return err
	}
	schemaGot, err := os.ReadFile(
		filepath.Join(root, "internal", "events", "schema.json"),
	) // #nosec G304 — generated schema path under the repo.
	switch {
	case os.IsNotExist(err):
		fmt.Println("internal/events/schema.json: missing")
		stale = true
	case err != nil:
		return err
	case string(schemaWant) != string(schemaGot):
		fmt.Println("internal/events/schema.json: stale")
		stale = true
	}

	if stale {
		return errors.New("generated files are stale; run: mise run generate")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gendocs:", err)
	os.Exit(1)
}
