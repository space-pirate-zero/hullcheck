// Package gates finds things in a repository that run and can fail a build.
//
// Workflow files are parsed with a line scanner rather than a YAML library, on
// purpose: hullcheck ships with zero third-party dependencies, which is a claim a
// supply-chain-adjacent tool should be able to make about itself. The scanner reads
// only the shapes it needs — job names, run steps, and triggers — and ignores the
// rest of the document rather than pretending to understand it.
package gates

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/space-pirate-zero/hullcheck/internal/model"
)

var (
	reYAMLKey  = regexp.MustCompile(`^(\s*)(?:-\s+)?([A-Za-z0-9_.-]+)\s*:\s*(.*)$`)
	reListItem = regexp.MustCompile(`^\s*-\s+(.*)$`)
	reMakeTgt  = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:(?:[^=]|$)`)
	reComment  = regexp.MustCompile(`^\s*#`)
)

// checkish are the name fragments that mark a target or script as a gate rather
// than a build step. A "deploy" target runs; it does not enforce.
var checkish = []string{
	"check", "verify", "lint", "test", "audit", "validate", "preflight",
	"guard", "enforce", "policy", "scan", "assert", "conform", "gate",
}

// Discover walks root and returns every gate it can justify.
func Discover(root string) ([]model.Gate, error) {
	var out []model.Gate
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree must not fail the walk
		}
		name := d.Name()
		if d.IsDir() {
			if skipDir(strings.ToLower(name)) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		switch {
		case isWorkflow(rel):
			out = append(out, workflowGates(path, rel)...)
		case name == ".pre-commit-config.yaml", name == ".pre-commit-config.yml":
			out = append(out, preCommitGates(path, rel)...)
		case name == "Makefile", name == "makefile", name == "GNUmakefile":
			out = append(out, makeGates(path, rel)...)
		case name == "project.yml", name == "project.yaml":
			out = append(out, xcodegenGates(path, rel)...)
		case strings.HasSuffix(name, ".rego"):
			out = append(out, model.Gate{
				ID: gid(rel, name), Kind: model.KindOPA, File: rel, Line: 1,
				Name: strings.TrimSuffix(name, ".rego"), Stage: model.Manual,
			})
		case isCheckScript(name):
			out = append(out, model.Gate{
				ID: gid(rel, name), Kind: model.KindCommand, File: rel, Line: 1,
				Name: name, Run: rel, Stage: model.Manual,
			})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "venv", ".venv", "dist", "build",
		"target", ".next", "__pycache__", ".terraform":
		return true
	}
	return false
}

func isWorkflow(rel string) bool {
	if !strings.HasPrefix(rel, ".github/workflows/") {
		return rel == ".gitlab-ci.yml" || rel == ".gitlab-ci.yaml"
	}
	return strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")
}

func isCheckScript(name string) bool {
	l := strings.ToLower(name)
	ext := filepath.Ext(l)
	switch ext {
	case ".sh", ".py", ".bash", ".rb", ".js", ".ts":
	default:
		return false
	}
	stem := strings.TrimSuffix(l, ext)
	for _, k := range checkish {
		if strings.HasPrefix(stem, k) || strings.HasSuffix(stem, k) ||
			strings.Contains(stem, "_"+k) || strings.Contains(stem, k+"_") {
			return true
		}
	}
	return false
}

func gid(rel, name string) string {
	return fmt.Sprintf("%s::%s", rel, name)
}

// workflowGates extracts the run steps of a CI workflow and the stage implied by
// its triggers. Every run step is a potential gate; the stage is the workflow's.
func workflowGates(path, rel string) []model.Gate {
	lines, err := readLines(path)
	if err != nil {
		return nil
	}
	stage := workflowStage(lines)
	var out []model.Gate
	job := ""
	inJobs := false
	for i, line := range lines {
		if reComment.MatchString(line) {
			continue
		}
		if m := reYAMLKey.FindStringSubmatch(line); m != nil {
			indent, key, val := len(m[1]), m[2], strings.TrimSpace(m[3])
			if indent == 0 {
				inJobs = key == "jobs"
				job = ""
			}
			// A two-space-indented key inside `jobs:` is a job id. Without the
			// inJobs guard, `pull_request:` under `on:` reads as a job.
			if inJobs && indent == 2 && val == "" && !reservedKey(key) {
				job = key
			}
			if key == "name" && val != "" && job != "" {
				continue
			}
			if key == "run" {
				cmd := val
				if cmd == "" || cmd == "|" || cmd == ">" {
					cmd = firstBlockLine(lines, i+1)
				}
				if cmd == "" {
					continue
				}
				out = append(out, model.Gate{
					ID:    fmt.Sprintf("%s::%s::%d", rel, orDefault(job, "job"), i+1),
					Kind:  model.KindCIJob,
					File:  rel,
					Line:  i + 1,
					Name:  orDefault(job, "job"),
					Run:   cmd,
					Stage: stage,
				})
			}
		}
	}
	return out
}

func reservedKey(k string) bool {
	switch k {
	case "on", "name", "jobs", "env", "permissions", "defaults", "concurrency", "steps":
		return true
	}
	return false
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// firstBlockLine returns the first non-empty line of a YAML block scalar.
func firstBlockLine(lines []string, from int) string {
	for i := from; i < len(lines) && i < from+20; i++ {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if reYAMLKey.MatchString(lines[i]) && !strings.HasPrefix(lines[i], "  ") {
			return ""
		}
		return s
	}
	return ""
}

// workflowStage reads the `on:` triggers. When a workflow has several triggers we
// take the FASTEST, because that is the soonest a violation is actually caught.
func workflowStage(lines []string) model.Stage {
	best := model.Manual
	inOn := false
	onIndent := 0
	consider := func(s model.Stage) {
		if s.Rank() < best.Rank() {
			best = s
		}
	}
	for _, line := range lines {
		if reComment.MatchString(line) {
			continue
		}
		m := reYAMLKey.FindStringSubmatch(line)
		if m == nil {
			if inOn {
				if it := reListItem.FindStringSubmatch(line); it != nil {
					consider(triggerStage(strings.TrimSpace(it[1])))
				}
			}
			continue
		}
		indent, key, val := len(m[1]), m[2], strings.TrimSpace(m[3])
		if key == "on" && indent == 0 {
			inOn = true
			onIndent = indent
			if val != "" {
				consider(triggerStage(strings.Trim(val, "[]{} ")))
			}
			continue
		}
		if inOn {
			if indent <= onIndent && key != "on" {
				inOn = false
			} else {
				consider(triggerStage(key))
			}
		}
	}
	return best
}

func triggerStage(t string) model.Stage {
	t = strings.ToLower(strings.Trim(t, `"' `))
	switch {
	case strings.HasPrefix(t, "pull_request"), t == "merge_group":
		return model.PullReq
	case t == "push":
		return model.PullReq
	case t == "schedule", t == "cron":
		return model.Nightly
	case t == "release", t == "workflow_dispatch", strings.HasPrefix(t, "tag"):
		return model.Release
	}
	return model.Manual
}

func preCommitGates(path, rel string) []model.Gate {
	lines, err := readLines(path)
	if err != nil {
		return nil
	}
	var out []model.Gate
	for i, line := range lines {
		if m := reYAMLKey.FindStringSubmatch(line); m != nil {
			if m[2] == "id" && strings.TrimSpace(m[3]) != "" {
				id := strings.Trim(strings.TrimSpace(m[3]), `"'`)
				out = append(out, model.Gate{
					ID: fmt.Sprintf("%s::%s", rel, id), Kind: model.KindPreCommit,
					File: rel, Line: i + 1, Name: id, Stage: model.PreCommit,
				})
			}
		}
	}
	return out
}

func makeGates(path, rel string) []model.Gate {
	lines, err := readLines(path)
	if err != nil {
		return nil
	}
	var out []model.Gate
	for i, line := range lines {
		if reComment.MatchString(line) || strings.HasPrefix(line, "\t") {
			continue
		}
		m := reMakeTgt.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		tgt := m[1]
		if !isCheckish(tgt) {
			continue
		}
		out = append(out, model.Gate{
			ID: fmt.Sprintf("%s::%s", rel, tgt), Kind: model.KindMakefile,
			File: rel, Line: i + 1, Name: tgt, Run: "make " + tgt, Stage: model.Manual,
		})
	}
	return out
}

// buildPhases are the xcodegen target keys whose scripts Xcode runs as build
// phases, on every build of the target.
var buildPhases = map[string]bool{
	"preBuildScripts": true, "postCompileScripts": true, "postBuildScripts": true,
}

// xcodegenGates reads the build-phase scripts of an xcodegen spec: under
// targets.<name>, each item of preBuildScripts, postCompileScripts and
// postBuildScripts that has a script: or a path:. Like a CI run step, every
// one is a potential gate; unlike one, it runs on every local build.
//
// A file called project.yml that has no top-level targets: is not an xcodegen
// spec, and yields nothing.
func xcodegenGates(path, rel string) []model.Gate {
	lines, err := readLines(path)
	if err != nil {
		return nil
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	var (
		out                  []model.Gate
		inTargets            bool
		targetIndent         = -1
		target               string
		phaseIndent          = -1
		blockIndent          = -1
		itemLine             int
		itemName, itemScript string
	)
	flush := func() {
		if itemLine > 0 && itemScript != "" {
			out = append(out, model.Gate{
				ID:    fmt.Sprintf("%s::%s::%d", rel, target, itemLine),
				Kind:  model.KindBuildPhase,
				File:  rel,
				Line:  itemLine,
				Name:  orDefault(itemName, target),
				Run:   itemScript,
				Stage: model.Build,
			})
		}
		itemLine, itemName, itemScript = 0, "", ""
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// The body of a block scalar is shell, not keys, however it reads.
		if blockIndent >= 0 && len(line)-len(strings.TrimLeft(line, " ")) > blockIndent {
			continue
		}
		blockIndent = -1
		if reComment.MatchString(line) {
			continue
		}
		m := reYAMLKey.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent, key, val := len(m[1]), m[2], strings.TrimSpace(m[3])
		item := strings.HasPrefix(strings.TrimSpace(line), "-")
		if blockScalar(val) {
			// Measured from the key, not the dash: in "- script: |" the block
			// and the item's next key are both indented past the dash.
			blockIndent = strings.Index(line, key)
		}
		// Leaving a list item, a phase list, a target or targets: closes it.
		if phaseIndent >= 0 && (indent < phaseIndent || indent == phaseIndent && !item) {
			flush()
			phaseIndent = -1
		}
		if indent == 0 {
			flush()
			inTargets, targetIndent, target = key == "targets" && val == "", -1, ""
			continue
		}
		if !inTargets {
			continue
		}
		if targetIndent < 0 {
			targetIndent = indent
		}
		if indent == targetIndent {
			target = key
			continue
		}
		if phaseIndent < 0 {
			if buildPhases[key] && val == "" && target != "" {
				phaseIndent = indent
			}
			continue
		}
		if item {
			flush()
			itemLine = i + 1
		}
		if blockScalar(val) {
			val = firstBlockLine(lines, i+1)
		}
		switch key {
		case "name":
			itemName = unquoteYAML(val)
		case "script":
			itemScript = srcRoot(unquoteYAML(val), dir)
		case "path":
			// A path: phase runs a file, relative to the spec's directory.
			if p := unquoteYAML(val); p != "" && itemScript == "" {
				if !strings.HasPrefix(p, "/") && !strings.Contains(p, "$") {
					p = filepath.ToSlash(filepath.Join(dir, p))
				}
				itemScript = p
			}
		}
	}
	flush()
	return out
}

func blockScalar(val string) bool {
	switch val {
	case "|", ">", "|-", ">-", "|+", ">+":
		return true
	}
	return false
}

// unquoteYAML strips one level of YAML quoting from a scalar.
func unquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		switch {
		case s[0] == '\'' && s[len(s)-1] == '\'':
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
		case s[0] == '"' && s[len(s)-1] == '"':
			return s[1 : len(s)-1]
		}
	}
	return s
}

// srcRoot rewrites Xcode's project-directory variables as the spec's directory
// relative to the repository, so a phase that runs "${SRCROOT}/scripts/x.sh"
// is seen to invoke the gate at scripts/x.sh, and can be run from the root.
func srcRoot(script, dir string) string {
	repl := ""
	if dir != "." {
		repl = dir + "/"
	}
	for _, v := range []string{"SRCROOT", "PROJECT_DIR"} {
		for _, form := range []string{"${" + v + "}/", "$(" + v + ")/", "$" + v + "/"} {
			script = strings.ReplaceAll(script, form, repl)
		}
	}
	return script
}

func isCheckish(s string) bool {
	l := strings.ToLower(s)
	for _, k := range checkish {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

func readLines(path string) ([]string, error) {
	fh, err := os.Open(path) //nolint:gosec // path comes from our own walk of root
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	var out []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out, sc.Err()
}

// Promote raises the stage of manual gates that a scheduled gate invokes. A
// checker script is only as fast as the fastest thing that runs it.
func Promote(gs []model.Gate) []model.Gate {
	for i := range gs {
		if gs[i].Stage != model.Manual {
			continue
		}
		for _, other := range gs {
			if other.Stage == model.Manual || other.Run == "" {
				continue
			}
			if invokes(other.Run, gs[i]) && other.Stage.Rank() < gs[i].Stage.Rank() {
				gs[i].Stage = other.Stage
			}
		}
	}
	return gs
}

func invokes(run string, g model.Gate) bool {
	if g.Kind == model.KindMakefile {
		return strings.Contains(run, "make "+g.Name)
	}
	return strings.Contains(run, g.File) || strings.Contains(run, g.Name)
}
