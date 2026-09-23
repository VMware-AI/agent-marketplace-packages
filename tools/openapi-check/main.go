// Command openapi-check validates that docs/api/openapi.json is in sync
// with the Go source tree:
//
//  1. docs/api/openapi.json parses as OpenAPI 3.0.x.
//  2. The embed copy internal/server/openapi.json is byte-equal.
//  3. Every path listed in the spec has a matching route registration
//     in internal/server/router.go.
//  4. Every schema referenced by $ref exists as a Go type in
//     internal/apitypes/ or internal/skills/.
//
// Exits 0 on success, 1 on any violation. Designed to run in CI as
// `make openapi-check` before merge.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "openapi-check:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: openapi-check <spec.json> <embed-copy.json> <apitypes-dir> [skills-dir]")
	}
	specPath, embedPath, apitypesDir := args[0], args[1], args[2]
	skillsDir := ""
	if len(args) >= 4 {
		skillsDir = args[3]
	}

	// (1) spec parses
	specData, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", specPath, err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specData, &spec); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", specPath, err)
	}
	if ver, _ := spec["openapi"].(string); !strings.HasPrefix(ver, "3.0") {
		return fmt.Errorf("openapi version: got %q, want 3.0.x", ver)
	}

	// (2) embed copy identical
	embedData, err := os.ReadFile(embedPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", embedPath, err)
	}
	if !bytes.Equal(specData, embedData) {
		return fmt.Errorf("%s and %s differ; run 'make openapi-embed'", specPath, embedPath)
	}

	// (3) every spec path has a corresponding registration in router.go.
	// The router nests everything under r.Route("/api/v1", ...) so the
	// route registrations there use paths like "/agents/{name}", not the
	// full "/api/v1/agents/{name}" we expose externally. We:
	//   - keep `/api/v1/health` as-is (root of the /api/v1 group)
	//   - strip the `/api/v1` prefix from spec paths before lookup
	//   - also accept `/swagger*` paths (root-level, not in /api/v1)
	paths, _ := spec["paths"].(map[string]any)
	routerSrc, err := os.ReadFile(filepath.Join("internal", "server", "router.go"))
	if err != nil {
		return fmt.Errorf("read internal/server/router.go: %w", err)
	}
	specPathList := make([]string, 0, len(paths))
	for p := range paths {
		specPathList = append(specPathList, p)
	}
	sort.Strings(specPathList)
	for _, p := range specPathList {
		// Trim /api/v1 prefix so the search matches router-relative paths
		// like r.Get("/agents/{name}", ...). /api/v1/health stays as
		// /health which also matches the registration.
		needle := strings.TrimPrefix(p, "/api/v1")
		if needle == "" || needle == p {
			// Top-level path (e.g. /swagger) — search as-is.
			needle = p
		}
		if !bytes.Contains(routerSrc, []byte(needle)) {
			return fmt.Errorf("spec path %q (router-relative %q) not registered in router.go", p, needle)
		}
	}

	// (4) every $ref schema exists as a Go type in apitypes/ + skills/
	refs := collectRefs(spec)
	knownTypes, err := collectTypesFromDir(apitypesDir)
	if err != nil {
		return err
	}
	if skillsDir != "" {
		skillTypes, err := collectTypesFromDir(skillsDir)
		if err != nil {
			return err
		}
		for k := range skillTypes {
			knownTypes[k] = true
		}
	}
	for _, ref := range refs {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if name == ref || name == "" {
			continue
		}
		if _, ok := knownTypes[name]; !ok {
			return fmt.Errorf("spec references schema %q but apitypes/skills has no such type", name)
		}
	}

	fmt.Printf("openapi-check OK: %d paths, %d schemas, %d refs\n", len(paths), len(knownTypes), len(refs))
	return nil
}

var refRE = regexp.MustCompile(`"#/components/schemas/([A-Za-z0-9_]+)"`)

func collectRefs(spec map[string]any) []string {
	data, _ := json.Marshal(spec)
	matches := refRE.FindAllStringSubmatch(string(data), -1)
	seen := map[string]bool{}
	out := []string{}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		key := "#/components/schemas/" + m[1]
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func collectTypesFromDir(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				out[ts.Name.Name] = true
			}
		}
	}
	return out, nil
}