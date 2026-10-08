package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"

	"example.com/pistos/internal/config"
)

// sourceFile caches the lines and symbol spans of one snapshot file during a review.
type sourceFile struct {
	lines   []string
	symbols []symbolSpan
}

type symbolSpan struct {
	name       string
	start, end int
}

type sourceCache struct {
	mu    sync.Mutex
	files map[string]*sourceFile
}

func newSourceCache() *sourceCache { return &sourceCache{files: map[string]*sourceFile{}} }

func (c *sourceCache) get(absPath, ecosystem string, content func() ([]byte, error)) *sourceFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f, ok := c.files[absPath]; ok {
		return f
	}
	f := &sourceFile{}
	if b, err := content(); err == nil {
		f.lines = strings.Split(string(b), "\n")
		f.symbols = symbolsFor(ecosystem, absPath, b, f.lines)
	}
	c.files[absPath] = f
	return f
}

func (f *sourceFile) line(n int) string {
	if n < 1 || n > len(f.lines) {
		return ""
	}
	return f.lines[n-1]
}

// enclosing returns the innermost symbol containing line.
func (f *sourceFile) enclosing(line int) string {
	best := ""
	bestSize := -1
	for _, s := range f.symbols {
		if line >= s.start && line <= s.end && (bestSize < 0 || s.end-s.start < bestSize) {
			best, bestSize = s.name, s.end-s.start
		}
	}
	return best
}

func symbolsFor(ecosystem, path string, src []byte, lines []string) []symbolSpan {
	switch {
	case ecosystem == config.Go && strings.HasSuffix(path, ".go"):
		return goSymbols(src)
	case ecosystem == config.Python && strings.HasSuffix(path, ".py"):
		return pythonSymbols(lines)
	}
	return nil
}

func goSymbols(src []byte) []symbolSpan {
	fset := token.NewFileSet()
	// A partially parsed file still yields spans for the declarations before the syntax error.
	file, _ := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if file == nil {
		return nil
	}
	var spans []symbolSpan
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			name = receiverName(fd.Recv.List[0].Type) + "." + name
		}
		spans = append(spans, symbolSpan{name, fset.Position(fd.Pos()).Line, fset.Position(fd.End()).Line})
	}
	return spans
}

func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// pythonSymbols derives def/class spans from indentation; a qualified name joins enclosing scopes.
func pythonSymbols(lines []string) []symbolSpan {
	type open struct {
		name   string
		indent int
		start  int
	}
	var stack []open
	var spans []symbolSpan
	lastCode := 0
	closeTo := func(indent int) {
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			spans = append(spans, symbolSpan{o.name, o.start, lastCode})
		}
	}
	for i, raw := range lines {
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(trimmed)
		closeTo(indent)
		for _, kw := range []string{"def ", "async def ", "class "} {
			if strings.HasPrefix(trimmed, kw) {
				rest := strings.TrimPrefix(trimmed, kw)
				name := rest
				if j := strings.IndexAny(rest, "(:"); j >= 0 {
					name = rest[:j]
				}
				name = strings.TrimSpace(name)
				if len(stack) > 0 {
					name = stack[len(stack)-1].name + "." + name
				}
				stack = append(stack, open{name, indent, i + 1})
				break
			}
		}
		lastCode = i + 1
	}
	closeTo(0)
	return spans
}
