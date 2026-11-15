// Command tagcheck fails when a //go:build constraint in the repo names a
// custom tag that .golangci.yml run.build-tags does not list, or when a
// constraint cannot hold under the tags it does list.
//
// Why: golangci-lint only loads files whose constraints hold under the
// configured tags, so such a file is skipped without a word. Constraints are
// parsed as expressions, so "!integration" and "integration && linux" both
// count as a use of "integration" — and "!integration", with integration
// listed, is a file the linter never loads.
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
// as a false alarm naming the tag, extend the lists then.
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
	exprs, err := fileConstraints(name, src)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, e := range exprs {
		tags = append(tags, customTags(e)...)
	}
	return tags, nil
}

// fileConstraints returns the //go:build expressions in src's header.
func fileConstraints(name string, src []byte) ([]constraint.Expr, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var exprs []constraint.Expr
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
			exprs = append(exprs, expr)
		}
	}
	return exprs, nil
}

// maxFreeTags bounds the toolchain tags satisfiable tries every value of. A
// constraint naming more is assumed to hold rather than searched exhaustively;
// real ones name two or three.
const maxFreeTags = 12

// satisfiable reports whether e can hold when every custom tag is set exactly
// as listed says. Toolchain tags are free: which GOOS the linter runs on is not
// this check's business, so a file for another platform is not an alarm. GOOS
// values are tried as independent, which can call an impossible constraint
// such as "linux && darwin" satisfiable — a miss, never a false alarm.
func satisfiable(e constraint.Expr, listed map[string]bool) bool {
	var free []string
	seen := map[string]bool{}
	var collect func(constraint.Expr)
	collect = func(e constraint.Expr) {
		switch e := e.(type) {
		case *constraint.TagExpr:
			if isToolchainTag(e.Tag) && !seen[e.Tag] {
				seen[e.Tag] = true
				free = append(free, e.Tag)
			}
		case *constraint.NotExpr:
			collect(e.X)
		case *constraint.AndExpr:
			collect(e.X)
			collect(e.Y)
		case *constraint.OrExpr:
			collect(e.X)
			collect(e.Y)
		}
	}
	collect(e)
	if len(free) > maxFreeTags {
		return true
	}
	for mask := range 1 << len(free) {
		on := map[string]bool{}
		for i, t := range free {
			on[t] = mask&(1<<i) != 0
		}
		if e.Eval(func(tag string) bool {
			if isToolchainTag(tag) {
				return on[tag]
			}
			return listed[tag]
		}) {
			return true
		}
	}
	return false
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

// missing maps each unlisted tag to the files that use it, and lists the
// files whose constraint cannot hold under the listed tags: the linter skips
// both.
func missing(fsys fs.FS, listed map[string]bool) (map[string][]string, []string, error) {
	out := map[string][]string{}
	var excluded []string
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
		exprs, err := fileConstraints(p, src)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		unlisted := false
		for _, e := range exprs {
			for _, t := range customTags(e) {
				if !listed[t] {
					out[t] = append(out[t], p)
					unlisted = true
				}
			}
		}
		// A file behind an unlisted tag is already reported by that tag.
		if !unlisted {
			for _, e := range exprs {
				if !satisfiable(e, listed) {
					excluded = append(excluded, p)
					break
				}
			}
		}
		return nil
	})
	return out, excluded, err
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
	miss, excluded, err := missing(root.FS(), listed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tagcheck:", err)
		return 2
	}
	if len(miss) == 0 && len(excluded) == 0 {
		return 0
	}
	if len(excluded) > 0 {
		fmt.Fprintf(os.Stderr, "these files' build constraints cannot hold under run.build-tags of %s; they are not linted:\n", cfgPath)
		for _, f := range excluded {
			fmt.Fprintln(os.Stderr, "  ", f)
		}
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
