// Command tagcheck fails when a //go:build constraint in the repo names a
// custom tag that .golangci.yml run.build-tags does not list.
//
// Why: golangci-lint only loads files whose constraints hold under the
// configured tags, so a file behind an unlisted tag is skipped without a
// word. Constraints are parsed as expressions, so "!integration" and
// "integration && linux" both count as a use of "integration".
package main

import (
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tags the toolchain defines itself; they are never listed in build-tags.
// race, msan, asan and purego are not here: the linter does not set them, so a
// file behind one is unlinted and has to be listed.
// shortcut: hand-kept lists; a GOOS/GOARCH added by a new Go release shows up
// as a false alarm naming the tag, extend the lists then (personal-mue9).
var (
	knownOS   = set("aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos unix")
	knownArch = set("386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm")
	knownMisc = set("cgo gc gccgo ignore")
)

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

func isToolchainTag(tag string) bool {
	return knownOS[tag] || knownArch[tag] || knownMisc[tag] ||
		strings.HasPrefix(tag, "go1.") || strings.HasPrefix(tag, "goexperiment.")
}

// customTags collects the non-toolchain tags of a constraint expression.
func customTags(e constraint.Expr) []string {
	switch e := e.(type) {
	case *constraint.TagExpr:
		if isToolchainTag(e.Tag) {
			return nil
		}
		return []string{e.Tag}
	case *constraint.NotExpr:
		return customTags(e.X)
	case *constraint.AndExpr:
		return append(customTags(e.X), customTags(e.Y)...)
	case *constraint.OrExpr:
		return append(customTags(e.X), customTags(e.Y)...)
	}
	return nil
}

// fileTags returns the custom tags named by the //go:build line in src's
// header, or nil when it has none.
func fileTags(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return nil, err
			}
			tags = append(tags, customTags(expr)...)
		}
	}
	return tags, nil
}

func configuredTags(fsys fs.FS, path string) (map[string]bool, error) {
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Run struct {
			BuildTags []string `yaml:"build-tags"`
		} `yaml:"run"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := map[string]bool{}
	for _, t := range cfg.Run.BuildTags {
		m[t] = true
	}
	return m, nil
}

// missing maps each unlisted tag to the files that use it.
func missing(fsys fs.FS, listed map[string]bool) (map[string][]string, error) {
	out := map[string][]string{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// go tooling skips these too; .git and vendored code are not ours.
			if n := d.Name(); n == ".git" || n == "vendor" || n == "node_modules" || n == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		tags, err := fileTags(p, src)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		for _, t := range tags {
			if !listed[t] {
				out[t] = append(out[t], p)
			}
		}
		return nil
	})
	return out, err
}

func main() { os.Exit(run()) }

// run works from the module root, as make and CI call it. Reads go through an
// os.Root, so a symlink in the tree cannot lead a read outside it.
func run() int {
	const cfgPath = ".golangci.yml"
	root, err := os.OpenRoot(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tagcheck:", err)
		return 2
	}
	defer func() { _ = root.Close() }()
	listed, err := configuredTags(root.FS(), cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tagcheck:", err)
		return 2
	}
	miss, err := missing(root.FS(), listed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tagcheck:", err)
		return 2
	}
	if len(miss) == 0 {
		return 0
	}
	tags := make([]string, 0, len(miss))
	for t := range miss {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		fmt.Fprintf(os.Stderr, "build tag %q is not in run.build-tags of %s; files behind it are not linted:\n", t, cfgPath)
		for _, f := range miss[t] {
			fmt.Fprintln(os.Stderr, "  ", f)
		}
	}
	return 1
}
