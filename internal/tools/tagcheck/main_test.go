package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileTags(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"plain", "//go:build integration\n\npackage x\n", []string{"integration"}},
		{"negated", "//go:build !integration\n\npackage x\n", []string{"integration"}},
		{"and with GOOS", "//go:build integration && linux\n\npackage x\n", []string{"integration"}},
		{"or and parens", "//go:build (smoke || eval) && !windows\n\npackage x\n", []string{"smoke", "eval"}},
		{"toolchain only", "//go:build linux && amd64 && go1.21 && cgo\n\npackage x\n", nil},
		{"after license comment", "// Copyright\n\n//go:build smoke\n\npackage x\n", []string{"smoke"}},
		{"after package clause is not a constraint", "package x\n\n//go:build smoke\n", nil},
		{"flag-only tag the linter does not set", "//go:build race\n\npackage x\n", []string{"race"}},
		{"no constraint", "package x\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := fileTags("x.go", []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestFileTagsRejectsBadConstraint(t *testing.T) {
	if _, err := fileTags("x.go", []byte("//go:build integration &&\n\npackage x\n")); err == nil {
		t.Fatal("want error for a malformed constraint")
	}
}

func TestMissing(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/ok_test.go", "//go:build integration\n\npackage a\n")
	write("b/new_test.go", "//go:build newtag && linux\n\npackage b\n")
	write("testdata/skip.go", "//go:build ignoredtag\n\npackage c\n")

	got, err := missing(dir, map[string]bool{"integration": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["newtag"]) != 1 {
		t.Fatalf("got %v, want only newtag", got)
	}

	got, err = missing(dir, map[string]bool{"integration": true, "newtag": true})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want none", got, err)
	}
}
