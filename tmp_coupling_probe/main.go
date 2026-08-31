package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type Use struct {
	File   string `json:"file"`
	Offset int    `json:"offset"`
	Name   string `json:"name"`
	Exp    string `json:"exp"`
}

func main() {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps, Tests: true}
	pkgs, err := packages.Load(cfg, "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	var mainPkg, testPkg *packages.Package
	for _, p := range pkgs {
		if p.ID == "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images" {
			mainPkg = p
		}
		if p.Name == "images" && len(p.Errors) == 0 && (strings.HasSuffix(p.ID, ".test") || strings.Contains(p.ID, "[")) {
			testPkg = p
		}
	}
	if mainPkg == nil {
		os.Exit(1)
	}
	moved := map[string]bool{}
	for _, f := range strings.Fields(`capability.go delivery_outbox.go delivery_search.go delivery_types.go drive_port.go dto.go filters.go image_search_resolver.go ingest_metadata.go ingest_utils.go interfaces.go path.go provider_search.go remote_fetch_port.go repository_port.go resolver.go resolver_localpath.go result.go retrieval_types.go search.go search_queries_engines.go search_resolver.go search_result.go search_result_mapper.go search_scoring.go searcher_composite.go searcher_generated.go searcher_retrieved.go semantic_payload.go semantic_port.go service_types.go storage.go storage_file.go types_search.go`) {
		moved[f] = true
	}
	prod, tests := []Use{}, []Use{}
	collect := func(pkg *packages.Package) {
		for node, obj := range pkg.TypesInfo.Uses {
			if obj.Pkg() != pkg.Types {
				continue
			}
			if obj.Parent() == nil || obj.Parent() != pkg.Types.Scope() {
				continue
			}
			useFile := filepath.Base(pkg.Fset.Position(node.Pos()).Filename)
			defFile := filepath.Base(pkg.Fset.Position(obj.Pos()).Filename)
			if !moved[defFile] {
				continue
			}
			if moved[useFile] {
				continue
			}
			exp := "x"
			if obj.Name() != "" && obj.Name()[0] >= 'A' && obj.Name()[0] <= 'Z' {
				exp = "X"
			}
			u := Use{File: useFile, Offset: pkg.Fset.Position(node.Pos()).Offset, Name: obj.Name(), Exp: exp}
			if strings.HasSuffix(useFile, "_test.go") {
				tests = append(tests, u)
			} else {
				prod = append(prod, u)
			}
		}
	}
	collect(mainPkg)
	if testPkg != nil {
		collect(testPkg)
	}
	sort.Slice(prod, func(i, j int) bool { return prod[i].Offset < prod[j].Offset })
	sort.Slice(tests, func(i, j int) bool { return tests[i].Offset < tests[j].Offset })
	out := map[string][]Use{"prod": prod, "tests": tests}
	b, _ := json.MarshalIndent(out, "", " ")
	fmt.Println(string(b))
}
