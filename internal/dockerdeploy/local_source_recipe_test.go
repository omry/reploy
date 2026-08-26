package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPythonLocalSourceRecipeV1AcceptsOmegaConfLegacyBuild(t *testing.T) {
	dir := t.TempDir()
	writeLocalRecipeTestFile(t, dir, "setup.py", "from setuptools import setup\n")
	writeLocalRecipeTestFile(t, dir, "pyproject.toml", "[tool.ruff]\nline-length = 88\n")
	writeLocalRecipeTestFile(t, dir, LocalSourceRecipeFilename, `schema: 1
project: omegaconf
type: python
build: setuptools-legacy
requires:
  - tool:java
`)
	recipe, err := ReadPythonLocalSourceRecipeV1(dir, "omegaconf")
	if err != nil {
		t.Fatal(err)
	}
	if !recipe.Found || recipe.Project != "omegaconf" ||
		recipe.Build != PythonBuildTypeSetuptoolsLegacy ||
		len(recipe.Tools) != 1 || recipe.Tools[0] != "java" ||
		len(recipe.Requirements) != 1 || recipe.Requirements[0].Scope != "source-builder:omegaconf" ||
		recipe.Requirements[0].Context != "build" || recipe.Requirements[0].Tool != "java" {
		t.Fatalf("recipe = %#v", recipe)
	}
	if err := recipe.Digest.Validate(); err != nil {
		t.Fatalf("recipe digest: %v", err)
	}
}

func TestReadPythonLocalSourceRecipeV1AcceptsPEP517Build(t *testing.T) {
	dir := t.TempDir()
	writeLocalRecipeTestFile(t, dir, "pyproject.toml", `[build-system]
requires = ["setuptools"]
build-backend = "setuptools.build_meta"
`)
	writeLocalRecipeTestFile(t, dir, LocalSourceRecipeFilename, `schema: 1
project: demo
type: python
build: pep517
requires: []
`)
	recipe, err := ReadPythonLocalSourceRecipeV1(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !recipe.Found || recipe.Build != PythonBuildTypePEP517 || len(recipe.Tools) != 0 || len(recipe.Requirements) != 0 {
		t.Fatalf("recipe = %#v", recipe)
	}
}

func TestReadPythonLocalSourceRecipeV1DoesNotRequireRecipe(t *testing.T) {
	recipe, err := ReadPythonLocalSourceRecipeV1(t.TempDir(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Found || recipe.Tools == nil || recipe.Requirements == nil {
		t.Fatalf("missing recipe = %#v", recipe)
	}
}

func TestReadPythonLocalSourceRecipeV1RejectsInvalidContracts(t *testing.T) {
	for _, test := range []struct {
		name      string
		recipe    string
		pyproject string
		setupPy   string
		want      string
	}{
		{
			name:      "unknown field",
			recipe:    "schema: 1\nproject: demo\ntype: python\nbuild: pep517\nrequires: []\ncommand: ./build\n",
			pyproject: "[build-system]\nrequires=[]\n",
			want:      "field command not found",
		},
		{
			name:      "wrong project",
			recipe:    "schema: 1\nproject: other\ntype: python\nbuild: pep517\nrequires: []\n",
			pyproject: "[build-system]\nrequires=[]\n",
			want:      "does not match selected package",
		},
		{
			name:      "invalid tool",
			recipe:    "schema: 1\nproject: demo\ntype: python\nbuild: pep517\nrequires: [tool:Make]\n",
			pyproject: "[build-system]\nrequires=[]\n",
			want:      "tool name",
		},
		{
			name:      "pep517 missing build system",
			recipe:    "schema: 1\nproject: demo\ntype: python\nbuild: pep517\nrequires: []\n",
			pyproject: "[tool.ruff]\n",
			want:      "requires pyproject.toml with [build-system]",
		},
		{
			name:      "legacy missing setup",
			recipe:    "schema: 1\nproject: demo\ntype: python\nbuild: setuptools-legacy\nrequires: []\n",
			pyproject: "[tool.ruff]\n",
			want:      "requires a regular setup.py",
		},
		{
			name:      "legacy contradicts build system",
			recipe:    "schema: 1\nproject: demo\ntype: python\nbuild: setuptools-legacy\nrequires: []\n",
			pyproject: "[build-system]\nrequires=[]\n",
			setupPy:   "from setuptools import setup\n",
			want:      "rejects pyproject.toml with [build-system]",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeLocalRecipeTestFile(t, dir, LocalSourceRecipeFilename, test.recipe)
			if test.pyproject != "" {
				writeLocalRecipeTestFile(t, dir, "pyproject.toml", test.pyproject)
			}
			if test.setupPy != "" {
				writeLocalRecipeTestFile(t, dir, "setup.py", test.setupPy)
			}
			_, err := ReadPythonLocalSourceRecipeV1(dir, "demo")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadPythonLocalSourceRecipeV1CanonicalizesStructuredToolRequests(t *testing.T) {
	dir := t.TempDir()
	writeLocalRecipeTestFile(t, dir, "pyproject.toml", "[build-system]\nrequires=[]\n")
	writeLocalRecipeTestFile(t, dir, LocalSourceRecipeFilename, `schema: 1
project: demo
type: python
build: pep517
requires:
  - tool: playwright
    version: ">=1.60"
    binding: python
    select: {browser: [webkit, chromium]}
  - tool: playwright
    version: "<2"
    binding: "*"
`)
	recipe, err := ReadPythonLocalSourceRecipeV1(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(recipe.Requirements) != 1 || len(recipe.Tools) != 1 || recipe.Tools[0] != "playwright" {
		t.Fatalf("recipe = %#v", recipe)
	}
	requirement := recipe.Requirements[0]
	if requirement.Scope != "source-builder:demo" || requirement.Context != "build" ||
		strings.Join(requirement.VersionConstraints, ",") != "<2,>=1.60" || !requirement.Binding.All ||
		strings.Join(requirement.Selections["browser"], ",") != "chromium,webkit" {
		t.Fatalf("requirement = %#v", requirement)
	}
}

func writeLocalRecipeTestFile(t *testing.T, dir string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
