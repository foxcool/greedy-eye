package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// usePlatforms replaces the GOOS/GOARCH names with those of a fake
// `go tool dist list` for one test.
func usePlatforms(t *testing.T, list string) {
	t.Helper()
	m, err := parsePlatforms(list)
	if err != nil {
		t.Fatal(err)
	}
	old := platformTags
	platformTags = m
	t.Cleanup(func() { platformTags = old })
}

const fakeDistList = "linux/amd64\ndarwin/arm64\nwindows/386\nfreebsd/riscv64\n"

func TestParsePlatforms(t *testing.T) {
	got, err := parsePlatforms(fakeDistList)
	if err != nil {
		t.Fatal(err)
	}
	want := set("linux amd64 darwin arm64 windows 386 freebsd riscv64")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"", "linux", "linux/"} {
		if _, err := parsePlatforms(bad); err == nil {
			t.Fatalf("want error for %q", bad)
		}
	}
}

// A platform the list does not name is a custom tag: that is how a GOOS added
// by a new release surfaces, and why the list has to come from the toolchain.
func TestPlatformComesFromTheList(t *testing.T) {
	src := []byte("//go:build newos && linux\n\npackage x\n")
	usePlatforms(t, fakeDistList)
	got, err := fileTags("x.go", src)
	if err != nil || !reflect.DeepEqual(got, []string{"newos"}) {
		t.Fatalf("got %v, %v; want [newos]", got, err)
	}
	usePlatforms(t, fakeDistList+"newos/amd64\n")
	got, err = fileTags("x.go", src)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want none", got, err)
	}
}

func TestFileTags(t *testing.T) {
	usePlatforms(t, fakeDistList)
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
	usePlatforms(t, fakeDistList)
	if _, err := fileTags("x.go", []byte("//go:build integration &&\n\npackage x\n")); err == nil {
		t.Fatal("want error for a malformed constraint")
	}
}

func TestMissing(t *testing.T) {
	usePlatforms(t, fakeDistList)
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

	got, excluded, err := missing(os.DirFS(dir), map[string]bool{"integration": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["newtag"]) != 1 || len(excluded) != 0 {
		t.Fatalf("got %v, excluded %v; want only newtag", got, excluded)
	}

	got, excluded, err = missing(os.DirFS(dir), map[string]bool{"integration": true, "newtag": true})
	if err != nil || len(got) != 0 || len(excluded) != 0 {
		t.Fatalf("got %v, excluded %v, %v; want none", got, excluded, err)
	}
}

// TestMissingReportsANegatedListedTag: "!integration" with integration listed
// is a file the linter never loads, although every tag it names is listed.
func TestMissingReportsANegatedListedTag(t *testing.T) {
	usePlatforms(t, fakeDistList)
	dir := t.TempDir()
	for rel, src := range map[string]string{
		"a/unit.go":      "//go:build !integration\n\npackage a\n",
		"b/platform.go":  "//go:build darwin && !windows\n\npackage b\n",
		"c/either.go":    "//go:build !integration || linux\n\npackage c\n",
		"d/listed_ok.go": "//go:build integration && !smoke || eval\n\npackage d\n",
	} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, excluded, err := missing(os.DirFS(dir), map[string]bool{"integration": true, "smoke": true, "eval": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got unlisted %v, want none", got)
	}
	// platform.go holds on some GOOS and either.go on linux; d/ holds via eval.
	if !reflect.DeepEqual(excluded, []string{"a/unit.go"}) {
		t.Fatalf("excluded %v, want only a/unit.go", excluded)
	}
}
