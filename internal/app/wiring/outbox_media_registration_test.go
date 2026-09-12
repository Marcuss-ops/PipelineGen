package wiring

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// TestMediaDeliveryEventsRegisteredOnlyOnPostgresOutbox pins the September 2026
// media-cutover contract for Drive delivery events.
//
// A Drive delivery event is a MEDIA fact: its producer is the canonical media
// committer, which writes into the PostgreSQL outbox. Registering the same
// handler on the SQLite outbox therefore creates a second consumer for a fact
// the SQLite plane never emits — dead wiring that makes the ownership of the
// event ambiguous (which plane drains it?) and hides a real duplicate the day
// a SQLite producer is reintroduced.
//
// The assertion is AST-scoped on purpose: a substring scan would be satisfied
// by the comment that documents the removal.
func TestMediaDeliveryEventsRegisteredOnlyOnPostgresOutbox(t *testing.T) {
	const src = "build_outbox_handlers.go"
	if _, err := os.Stat(src); err != nil {
		t.Skipf("wiring source not available: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}

	// mediaDeliveryEvents maps the qualified event expression used as the
	// registration key to the ONLY composition function allowed to register it.
	mediaDeliveryEvents := map[string]string{
		"cliprender.EventClipRenderDriveDeliveryRequested": "registerPostgresMediaOutboxHandlers",
		"imagesapp.EventTypeImageDriveDeliveryRequested":   "registerPostgresMediaOutboxHandlers",
	}

	found := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Register" && sel.Sel.Name != "RegisterHandler") {
				return true
			}
			for _, arg := range call.Args {
				name := qualifiedExprName(arg)
				if _, wanted := mediaDeliveryEvents[name]; !wanted {
					continue
				}
				found[name] = append(found[name], fn.Name.Name+"/"+sel.Sel.Name)
			}
			return true
		})
	}

	for name, wantFn := range mediaDeliveryEvents {
		got := found[name]
		if len(got) != 1 {
			t.Fatalf("%s: want exactly 1 registration, got %d (%v). The event is PostgreSQL-media-owned: the canonical media committer emits it into the PG outbox, so a SQLite registration has no producer and is a parallel consumer.", name, len(got), got)
		}
		if got[0] != wantFn+"/RegisterHandler" {
			t.Fatalf("%s: registered by %q, want %q (PostgreSQL media outbox is the single owner)", name, got[0], wantFn+"/RegisterHandler")
		}
	}
}

// qualifiedExprName renders an AST expression as a dotted name, so a selector
// (`imagesapp.EventTypeX`) and a plain identifier (`TypeX`) are distinguishable
// in diagnostics.
func qualifiedExprName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		prefix := qualifiedExprName(n.X)
		if prefix == "" {
			return n.Sel.Name
		}
		return prefix + "." + n.Sel.Name
	case *ast.ParenExpr:
		return qualifiedExprName(n.X)
	default:
		return ""
	}
}
