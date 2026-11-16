// Package lintpin keeps the golangci-lint version pinned in the Makefile and
// in the CI workflow equal. It is a test, not a make step, so `make test-unit`
// and the CI unit-test job both fail on a mismatch without a second runner or
// another way to invoke it; a lint run with different rules on each side would
// otherwise pass locally and fail on CI, or the reverse.
package lintpin

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var makeVersion = regexp.MustCompile(`(?m)^GOLANGCI_LINT_VERSION\s*[:?]?=\s*(\S+)\s*$`)

// versionInMakefile returns the value of GOLANGCI_LINT_VERSION.
func versionInMakefile(src []byte) (string, error) {
	m := makeVersion.FindSubmatch(src)
	if m == nil {
		return "", fmt.Errorf("GOLANGCI_LINT_VERSION not found")
	}
	return string(m[1]), nil
}

// versionInWorkflow returns the `version` input of the golangci-lint action
// step, and fails when the workflow has none or more than one.
func versionInWorkflow(src []byte) (string, error) {
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &wf); err != nil {
		return "", err
	}
	var found []string
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "golangci/golangci-lint-action@") {
				found = append(found, step.With["version"])
			}
		}
	}
	if len(found) != 1 || found[0] == "" {
		return "", fmt.Errorf("want exactly one golangci-lint-action step with a version, found %q", found)
	}
	return found[0], nil
}

func TestLintVersionPinsAgree(t *testing.T) {
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	mk, err := versionInMakefile(read("../../../Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	ci, err := versionInWorkflow(read("../../../.github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if mk != ci {
		t.Fatalf("golangci-lint is %s in the Makefile and %s in .github/workflows/ci.yml; they must be equal", mk, ci)
	}
}

const workflow = `
jobs:
  lint:
    steps:
      - uses: actions/checkout@abc # v4
      - uses: golangci/golangci-lint-action@abc # v8
        with:
          version: %s
`

func TestVersionParsing(t *testing.T) {
	mk, err := versionInMakefile([]byte("X = 1\nGOLANGCI_LINT_VERSION = v2.15.0\nGOLANGCI_LINT_DIGEST = sha256:aa\n"))
	if err != nil || mk != "v2.15.0" {
		t.Fatalf("makefile: %q, %v", mk, err)
	}
	ci, err := versionInWorkflow([]byte(fmt.Sprintf(workflow, "v2.16.0")))
	if err != nil || ci != "v2.16.0" {
		t.Fatalf("workflow: %q, %v", ci, err)
	}
	if mk == ci {
		t.Fatal("fake pins differ, parsing must keep them apart")
	}
	if _, err := versionInMakefile([]byte("OTHER = v1\n")); err == nil {
		t.Fatal("want error when the Makefile has no pin")
	}
	if _, err := versionInWorkflow([]byte("jobs: {}\n")); err == nil {
		t.Fatal("want error when the workflow has no lint step")
	}
}
