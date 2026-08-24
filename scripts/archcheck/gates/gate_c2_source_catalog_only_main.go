//go:build c2_source_catalog_only

// Command gate_c2_source_catalog_only enforces the Source Catalog dispatch
// boundary and a code-derived monotonic ratchet. The current count is scanned
// from source; the previous ceiling is scanned from HEAD^, so a manually copied
// baseline can no longer hide growth.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var canonicalStringKinds = map[string]bool{
	"text": true, "clips": true, "catalog": true, "search": true,
	"curate": true, "artlist": true, "youtube": true, "stock": true,
	"youtube_clip": true, "clip_drive": true, "image": true,
	"generated": true, "sound_effect": true,
}

var canonicalIdentSuffixes = map[string]bool{
	"Text": true, "Clips": true, "Catalog": true, "Search": true,
	"Curate": true, "Stock": true, "Artlist": true, "YoutubeClip": true,
	"ClipDrive": true, "Image": true, "Generated": true, "SoundEffect": true,
}

var allowlist = map[string]bool{
	"internal/application/assets/artifacts/source_resolver.go": true,
	"internal/capabilities/scripts/adapters/source_registry.go": true,
}

type violationRow struct {
	file, nodeKind, match string
	line, col             int
}

type milestone struct {
	due time.Time
	cap int
}

func main() {
	var legacyBaseline int
	flag.IntVar(&legacyBaseline, "baseline", 0, "legacy bootstrap ceiling; used only when HEAD^ is unavailable")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	current, err := scanWorkingTree(root)
	if err != nil {
		fatal2("scan current tree", err)
	}
	parentCount, parentOK, err := scanGitRef(root, "HEAD^")
	if err != nil {
		fatal2("scan parent tree", err)
	}

	ceiling := legacyBaseline
	ceilingSource := "legacy bootstrap"
	if parentOK {
		ceiling = parentCount
		ceilingSource = "HEAD^ code scan"
	}
	if dueCap, ok := activeMilestoneCap(time.Now()); ok && (ceiling == 0 || dueCap < ceiling) {
		ceiling = dueCap
		ceilingSource += "+dated milestone"
	}

	sort.Slice(current, func(i, j int) bool {
		if current[i].file != current[j].file {
			return current[i].file < current[j].file
		}
		if current[i].line != current[j].line {
			return current[i].line < current[j].line
		}
		return current[i].col < current[j].col
	})

	if len(current) > ceiling {
		for _, v := range current {
			fmt.Printf("%s:%d:%d: %s matches canonical source kind (%q) outside Source Catalog allowlist\n",
				v.file, v.line, v.col, v.nodeKind, v.match)
		}
		fmt.Fprintf(os.Stderr, "C2-C gate: current=%d exceeds ceiling=%d (%s); parent=%d. Counts come from code, and growth is forbidden.\n",
			len(current), ceiling, ceilingSource, parentCount)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "C2-C gate: current=%d ceiling=%d (%s) parent=%d target=0\n",
		len(current), ceiling, ceilingSource, parentCount)
}

func activeMilestoneCap(now time.Time) (int, bool) {
	utc := now.UTC()
	milestones := []milestone{
		{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), 36},
		{time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), 24},
		{time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 12},
		{time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), 0},
	}
	cap := 0
	active := false
	for _, m := range milestones {
		if !utc.Before(m.due) {
			cap, active = m.cap, true
		}
	}
	return cap, active
}

func scanWorkingTree(root string) ([]violationRow, error) {
	var files []string
	for _, subtree := range []string{"internal/application", "internal/api"} {
		err := filepath.WalkDir(filepath.Join(root, subtree), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "generated" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(files)
	var out []violationRow
	for _, path := range files {
		rel := relPath(root, path)
		if allowlist[rel] {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rows, err := scanSource(rel, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: C2-C parse error in %s: %v\n", rel, err)
			continue
		}
		out = append(out, rows...)
	}
	return out, nil
}

func scanGitRef(root, ref string) (int, bool, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", ref)
	if err := cmd.Run(); err != nil {
		return 0, false, nil
	}
	cmd = exec.Command("git", "-C", root, "ls-tree", "-r", "--name-only", ref, "--", "internal/application", "internal/api")
	listing, err := cmd.Output()
	if err != nil {
		return 0, false, err
	}
	count := 0
	for _, raw := range bytes.Split(listing, []byte{'\n'}) {
		path := strings.TrimSpace(string(raw))
		if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/generated/") || allowlist[path] {
			continue
		}
		data, err := exec.Command("git", "-C", root, "show", ref+":"+path).Output()
		if err != nil {
			return 0, false, err
		}
		rows, err := scanSource(path, data)
		if err != nil {
			continue
		}
		count += len(rows)
	}
	return count, true, nil
}

func scanSource(path string, data []byte) ([]violationRow, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		return nil, err
	}
	var out []violationRow
	ast.Inspect(file, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.SwitchStmt:
			inspectSwitch(fset, path, stmt, &out)
		case *ast.IfStmt:
			// Preserve the historical gate's recursive else-if accounting so
			// the first code-derived run compares like-for-like with its parent.
			inspectIfChain(fset, path, stmt, &out)
		}
		return true
	})
	return out, nil
}

func inspectSwitch(fset *token.FileSet, path string, sw *ast.SwitchStmt, out *[]violationRow) {
	for _, clause := range sw.Body.List {
		cc, ok := clause.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expr := range cc.List {
			if match, ok := matchSourceKindExpr(expr); ok {
				pos := fset.Position(expr.Pos())
				*out = append(*out, violationRow{path, "switch case", match, pos.Line, pos.Column})
			}
		}
	}
}

func inspectIfChain(fset *token.FileSet, path string, stmt *ast.IfStmt, out *[]violationRow) {
	if match, ok := matchSourceKindCondition(stmt.Cond); ok {
		pos := fset.Position(stmt.Cond.Pos())
		*out = append(*out, violationRow{path, "if condition", match, pos.Line, pos.Column})
	}
	if nested, ok := stmt.Else.(*ast.IfStmt); ok {
		inspectIfChain(fset, path, nested, out)
	}
}

func matchSourceKindCondition(expr ast.Expr) (string, bool) {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
		return "", false
	}
	if match, ok := matchSourceKindExpr(bin.X); ok {
		return match, true
	}
	return matchSourceKindExpr(bin.Y)
}

func matchSourceKindExpr(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil && canonicalStringKinds[strings.ToLower(value)]
	case *ast.Ident:
		return matchSourceIdent(e.Name)
	case *ast.SelectorExpr:
		if value, ok := matchSourceIdent(e.Sel.Name); ok {
			return selectorRootName(e) + "." + value, true
		}
	}
	return "", false
}

func matchSourceIdent(name string) (string, bool) {
	if !strings.HasPrefix(name, "Source") {
		return "", false
	}
	return name, canonicalIdentSuffixes[strings.TrimPrefix(name, "Source")]
}

func selectorRootName(e *ast.SelectorExpr) string {
	var parts []string
	var cur ast.Expr = e.X
	for {
		switch n := cur.(type) {
		case *ast.Ident:
			parts = append(parts, n.Name)
			for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
				parts[i], parts[j] = parts[j], parts[i]
			}
			return strings.Join(parts, ".")
		case *ast.SelectorExpr:
			parts = append(parts, n.Sel.Name)
			cur = n.X
		default:
			return ""
		}
	}
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func fatal2(label string, err error) {
	fmt.Fprintf(os.Stderr, "C2-C gate: %s: %v\n", label, err)
	os.Exit(2)
}
