// Package facts extracts syntax facts (function metrics, import edges) and applies the core metric
// and architecture rules to them.
package facts

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"example.com/pistos/internal/finding"
)

// Algorithm identifies the metric definitions in docs/metrics.md; changing a definition bumps it.
const Algorithm = "metrics-v1"

// skipDirs mirrors what language tooling ignores; facts never descend into dependencies or outputs.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, ".venv": true, "venv": true, "__pycache__": true,
	"bin": true, "obj": true, "dist": true, "build": true, "testdata": true,
}

// GoFacts walks a Go module rooted at dir. Paths in the result are prefixed with repoRoot (the
// project's repository-relative root) so they are repository-relative.
func GoFacts(dir, repoRoot string) (*finding.Facts, []string, error) {
	module := goModulePath(filepath.Join(dir, "go.mod"))
	out := &finding.Facts{Algorithm: Algorithm, Nodes: finding.NodesDir}
	var limits []string
	parseErrors := 0
	pkgDirs := map[string]bool{}
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			if p != dir {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			files = append(files, p)
			rel, _ := filepath.Rel(dir, filepath.Dir(p))
			pkgDirs[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		repoPath := joinRel(repoRoot, filepath.ToSlash(rel))
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			parseErrors++
			if file == nil {
				continue
			}
		}
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
				out.Functions = append(out.Functions, goFunction(fset, fd, repoPath))
			}
		}
		if strings.HasSuffix(f, "_test.go") || module == "" {
			continue
		}
		for _, imp := range file.Imports {
			spec := strings.Trim(imp.Path.Value, "\"`")
			if spec != module && !strings.HasPrefix(spec, module+"/") {
				continue
			}
			target := strings.TrimPrefix(strings.TrimPrefix(spec, module), "/")
			if target == "" {
				target = "."
			}
			e := finding.ImportFact{From: repoPath, Spec: spec, Line: fset.Position(imp.Pos()).Line, Provenance: finding.ProvenanceResolved}
			if pkgDirs[target] {
				e.To = joinRel(repoRoot, target)
			} else {
				e.Provenance = finding.ProvenanceUnknown
			}
			out.Imports = append(out.Imports, e)
		}
	}
	if module == "" {
		limits = append(limits, "go.mod has no module path; import edges were not extracted")
	}
	if parseErrors > 0 {
		limits = append(limits, "files with syntax errors were measured only up to the first error")
	}
	limits = append(limits, "build constraints are not evaluated: every non-test file contributes import edges")
	return out, limits, nil
}

func goModulePath(gomod string) string {
	f, err := os.Open(gomod)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), "\"")
		}
	}
	return ""
}

func goFunction(fset *token.FileSet, fd *ast.FuncDecl, path string) finding.FunctionFact {
	name := fd.Name.Name
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		name = recvName(fd.Recv.List[0].Type) + "." + name
	}
	f := finding.FunctionFact{
		Path: path, Symbol: name,
		Line: fset.Position(fd.Pos()).Line, EndLine: fset.Position(fd.End()).Line,
		Cyclomatic: 1,
	}
	for _, field := range fd.Type.Params.List {
		f.Params += max(1, len(field.Names))
	}
	// Header parts (if/for/switch init and post statements) are walked for decisions but are not
	// statements of their own, matching the other languages' definition.
	inHeader := false
	var walk func(n ast.Node, depth int)
	header := func(n ast.Node, depth int) {
		saved := inHeader
		inHeader = true
		walk(n, depth)
		inHeader = saved
	}
	walk = func(n ast.Node, depth int) {
		ast.Inspect(n, func(c ast.Node) bool {
			if c == nil {
				return true
			}
			if s, ok := c.(ast.Stmt); ok && countsAsStatement(s) && !inHeader {
				f.Statements++
			}
			switch x := c.(type) {
			case *ast.IfStmt:
				f.Cyclomatic++
				f.Nesting = max(f.Nesting, depth+1)
				if x.Init != nil {
					header(x.Init, depth)
				}
				walk(x.Cond, depth)
				walk(x.Body, depth+1)
				if x.Else != nil {
					if elif, ok := x.Else.(*ast.IfStmt); ok {
						// An else-if continues the chain at the same depth.
						f.Statements++
						walkIf(elif, depth, &f, walk, header)
					} else {
						walk(x.Else, depth+1)
					}
				}
				return false
			case *ast.ForStmt, *ast.RangeStmt:
				f.Cyclomatic++
				f.Nesting = max(f.Nesting, depth+1)
				walk(bodyOf(x), depth+1)
				walkHeader(x, depth, header)
				return false
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				f.Nesting = max(f.Nesting, depth+1)
				walk(bodyOf(x), depth+1)
				walkHeader(x, depth, header)
				return false
			case *ast.CaseClause:
				if x.List != nil {
					f.Cyclomatic++
				}
			case *ast.CommClause:
				if x.Comm != nil {
					f.Cyclomatic++
				}
			case *ast.BinaryExpr:
				if x.Op == token.LAND || x.Op == token.LOR {
					f.Cyclomatic++
				}
			}
			return true
		})
	}
	walk(fd.Body, 0)
	return f
}

func walkIf(x *ast.IfStmt, depth int, f *finding.FunctionFact, walk, header func(ast.Node, int)) {
	f.Cyclomatic++
	if x.Init != nil {
		header(x.Init, depth)
	}
	walk(x.Cond, depth)
	walk(x.Body, depth+1)
	if x.Else != nil {
		if elif, ok := x.Else.(*ast.IfStmt); ok {
			f.Statements++
			walkIf(elif, depth, f, walk, header)
		} else {
			walk(x.Else, depth+1)
		}
	}
}

func bodyOf(n ast.Node) ast.Node {
	switch x := n.(type) {
	case *ast.ForStmt:
		return x.Body
	case *ast.RangeStmt:
		return x.Body
	case *ast.SwitchStmt:
		return x.Body
	case *ast.TypeSwitchStmt:
		return x.Body
	case *ast.SelectStmt:
		return x.Body
	}
	return nil
}

// walkHeader visits init/condition/post expressions, which can hold && / || decisions.
func walkHeader(n ast.Node, depth int, walk func(ast.Node, int)) {
	var parts []ast.Node
	switch x := n.(type) {
	case *ast.ForStmt:
		parts = []ast.Node{x.Init, x.Cond, x.Post}
	case *ast.RangeStmt:
		parts = []ast.Node{x.X}
	case *ast.SwitchStmt:
		parts = []ast.Node{x.Init, x.Tag}
	case *ast.TypeSwitchStmt:
		parts = []ast.Node{x.Init, x.Assign}
	}
	for _, p := range parts {
		if p != nil {
			walk(p, depth)
		}
	}
}

// countsAsStatement excludes structural nodes so the count is formatting-invariant and stable.
func countsAsStatement(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.BlockStmt, *ast.EmptyStmt, *ast.LabeledStmt, *ast.CaseClause, *ast.CommClause:
		return false
	}
	return true
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func joinRel(root, p string) string {
	if root == "." || root == "" {
		return p
	}
	if p == "." {
		return root
	}
	return path.Join(root, p)
}
