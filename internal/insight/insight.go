// Package insight adds the readings that need more than a rule/gate pair:
// where the gates are, who owns them, and when a gap opened.
//
// Two features from the design were deliberately cut rather than shipped weak.
// A "refusal audit" and a "provenance audit" were both specified, and neither can
// be done generically without a pile of heuristics that produce confident-looking
// false findings. This tool's whole argument is that a wrong number is worse than
// no number, so they stay unbuilt until there is a real signal to read.
package insight

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

// Tree is the gate density of one top-level directory.
type Tree struct {
	Path  string `json:"path"`
	Gates int    `json:"gates"`
	Files int    `json:"files"`
}

// PathCoverage reports which top-level trees have gates and which have none.
// A repository can score well overall while an entire subsystem is ungoverned.
func PathCoverage(root string, rep model.Report) []Tree {
	gates := map[string]int{}
	count := func(file string) {
		if t := topLevel(file); t != "" {
			gates[t]++
		}
	}
	for _, f := range rep.Findings {
		for _, g := range f.Gates {
			count(g.File)
		}
	}
	for _, g := range rep.Unlogged {
		count(g.File)
	}

	files := map[string]int{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() || skip(e.Name()) {
			continue
		}
		files[e.Name()] = countFiles(filepath.Join(root, e.Name()))
	}

	out := make([]Tree, 0, len(files))
	for dir, n := range files {
		// Ignore trivial directories: a tree with three files does not need its
		// own gate, and listing it as ungoverned is noise.
		if n < 5 {
			continue
		}
		out = append(out, Tree{Path: dir, Gates: gates[dir], Files: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Gates != out[j].Gates {
			return out[i].Gates < out[j].Gates
		}
		return out[i].Files > out[j].Files
	})
	return out
}

func topLevel(file string) string {
	file = filepath.ToSlash(file)
	if i := strings.IndexByte(file, '/'); i > 0 {
		return file[:i]
	}
	return ""
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			if skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		n++
		return nil
	})
	return n
}

func skip(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "dist", "build",
		"target", ".next", "__pycache__", ".terraform":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// Owner is a gate and who is responsible for it.
type Owner struct {
	Gate   string   `json:"gate"`
	File   string   `json:"file"`
	Owners []string `json:"owners"`
}

// BusFactor cross-references gates against CODEOWNERS and returns the gates with
// one owner or none. A gate owned by one person who has left is a gate that will
// not be fixed the day it starts failing.
func BusFactor(root string, rep model.Report) []Owner {
	rules := loadCodeowners(root)
	if rules == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []Owner
	consider := func(g model.Gate) {
		if seen[g.ID] {
			return
		}
		seen[g.ID] = true
		owners := matchOwners(rules, g.File)
		if len(owners) <= 1 {
			out = append(out, Owner{Gate: g.Name, File: g.File, Owners: owners})
		}
	}
	for _, f := range rep.Findings {
		for _, g := range f.Gates {
			consider(g)
		}
	}
	for _, g := range rep.Unlogged {
		consider(g)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Owners) != len(out[j].Owners) {
			return len(out[i].Owners) < len(out[j].Owners)
		}
		return out[i].File < out[j].File
	})
	return out
}

type coRule struct {
	pattern string
	owners  []string
}

// loadCodeowners reads CODEOWNERS from the three locations GitHub honours.
func loadCodeowners(root string) []coRule {
	for _, p := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		fh, err := os.Open(filepath.Join(root, filepath.FromSlash(p))) //nolint:gosec
		if err != nil {
			continue
		}
		defer func() { _ = fh.Close() }()
		var out []coRule
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			out = append(out, coRule{pattern: parts[0], owners: parts[1:]})
		}
		return out
	}
	return nil
}

// matchOwners applies CODEOWNERS precedence: the LAST matching rule wins.
func matchOwners(rules []coRule, file string) []string {
	var owners []string
	for _, r := range rules {
		if coMatch(r.pattern, file) {
			owners = r.owners
		}
	}
	return owners
}

func coMatch(pattern, file string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	file = filepath.ToSlash(file)
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "/"):
		return strings.HasPrefix(file, pattern)
	case strings.HasPrefix(pattern, "*."):
		return strings.HasSuffix(file, strings.TrimPrefix(pattern, "*"))
	}
	if ok, _ := filepath.Match(pattern, file); ok {
		return true
	}
	return file == pattern || strings.HasPrefix(file, pattern+"/")
}
