package report

import (
	"html/template"
	"path"
	"strings"
)

// lexer describes just enough of a language to colour comments, strings, numbers and keywords.
// Snippets are short excerpts, so a lexer that starts mid-construct only miscolours a few lines.
type lexer struct {
	name         string
	lineComments []string
	blockComment [2]string
	quotes       string
	// rawQuote never processes escapes (Go and JS template literals, C# verbatim strings use @").
	rawQuote    byte
	tripleQuote bool
	keywords    map[string]bool
	literals    map[string]bool
	types       map[string]bool
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var (
	goLexer = &lexer{
		name: "go", lineComments: []string{"//"}, blockComment: [2]string{"/*", "*/"}, quotes: `"'`, rawQuote: '`',
		keywords: words("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var"),
		literals: words("true false nil iota"),
		types:    words("any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr"),
	}
	pyLexer = &lexer{
		name: "python", lineComments: []string{"#"}, quotes: `"'`, tripleQuote: true,
		keywords: words("and as assert async await break class continue def del elif else except finally for from global if import in is lambda match case nonlocal not or pass raise return try while with yield"),
		literals: words("True False None self cls"),
		types:    words("bool bytes dict float int list object set str tuple"),
	}
	jsLexer = &lexer{
		name: "javascript", lineComments: []string{"//"}, blockComment: [2]string{"/*", "*/"}, quotes: `"'`, rawQuote: '`',
		keywords: words("abstract as async await break case catch class const continue debugger declare default delete do else enum export extends finally for from function if implements import in instanceof interface let new of private protected public readonly return static super switch throw try type typeof var void while with yield"),
		literals: words("true false null undefined this NaN Infinity"),
		types:    words("any bigint boolean never number object string symbol unknown void"),
	}
	csLexer = &lexer{
		name: "csharp", lineComments: []string{"//"}, blockComment: [2]string{"/*", "*/"}, quotes: `"'`,
		keywords: words("abstract as async await base break case catch class const continue default delegate do else enum event explicit extern finally fixed for foreach get goto if implicit in init interface internal is lock namespace new operator out override params private protected public readonly record ref return sealed set sizeof stackalloc static struct switch throw try typeof unchecked unsafe using var virtual void volatile when where while yield bool byte char decimal double float int long object sbyte short string uint ulong ushort"),
		literals: words("true false null this value"),
	}
)

func lexerFor(p string) *lexer {
	switch strings.ToLower(path.Ext(p)) {
	case ".go":
		return goLexer
	case ".py", ".pyi":
		return pyLexer
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return jsLexer
	case ".cs":
		return csLexer
	}
	return nil
}

type token struct {
	class string
	text  string
}

// highlightLines colours src and returns one HTML fragment per source line. Tokens that span lines
// (block comments, raw strings) are closed and reopened per line so every line is valid on its own.
func highlightLines(p string, src []string) []template.HTML {
	out := make([]template.HTML, len(src))
	lx := lexerFor(p)
	if lx == nil {
		for i, l := range src {
			out[i] = template.HTML(template.HTMLEscapeString(l))
		}
		return out
	}
	var b strings.Builder
	line := 0
	for _, t := range lx.tokens(strings.Join(src, "\n")) {
		for i, part := range strings.Split(t.text, "\n") {
			if i > 0 {
				out[line] = template.HTML(b.String())
				b.Reset()
				line++
			}
			if part == "" {
				continue
			}
			if t.class == "" {
				b.WriteString(template.HTMLEscapeString(part))
			} else {
				b.WriteString(`<span class="tk-` + t.class + `">` + template.HTMLEscapeString(part) + `</span>`)
			}
		}
	}
	if line < len(out) {
		out[line] = template.HTML(b.String())
	}
	return out
}

func (lx *lexer) tokens(s string) []token {
	var toks []token
	emit := func(class, text string) {
		if n := len(toks); n > 0 && toks[n-1].class == class {
			toks[n-1].text += text
			return
		}
		toks = append(toks, token{class, text})
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		if lc := lx.lineComment(rest); lc {
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				end = len(rest)
			}
			emit("comment", rest[:end])
			i += end
			continue
		}
		if open := lx.blockComment[0]; open != "" && strings.HasPrefix(rest, open) {
			end := strings.Index(rest[len(open):], lx.blockComment[1])
			n := len(rest)
			if end >= 0 {
				n = len(open) + end + len(lx.blockComment[1])
			}
			emit("comment", rest[:n])
			i += n
			continue
		}
		c := s[i]
		switch {
		case lx.tripleQuote && (strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, `'''`)):
			end := strings.Index(rest[3:], rest[:3])
			n := len(rest)
			if end >= 0 {
				n = 3 + end + 3
			}
			emit("string", rest[:n])
			i += n
		case lx.name == "csharp" && strings.HasPrefix(rest, `@"`):
			n := 2
			for n < len(rest) {
				if rest[n] == '"' {
					if n+1 < len(rest) && rest[n+1] == '"' {
						n += 2
						continue
					}
					n++
					break
				}
				n++
			}
			emit("string", rest[:n])
			i += n
		case lx.rawQuote != 0 && c == lx.rawQuote:
			end := strings.IndexByte(rest[1:], c)
			n := len(rest)
			if end >= 0 {
				n = end + 2
			}
			emit("string", rest[:n])
			i += n
		case strings.IndexByte(lx.quotes, c) >= 0:
			n := 1
			for n < len(rest) && rest[n] != c && rest[n] != '\n' {
				if rest[n] == '\\' {
					n++
				}
				n++
			}
			if n < len(rest) && rest[n] == c {
				n++
			}
			n = min(n, len(rest))
			emit("string", rest[:n])
			i += n
		case isDigit(c) && (i == 0 || !isIdent(s[i-1])):
			n := 1
			for n < len(rest) && (isIdent(rest[n]) || rest[n] == '.') {
				n++
			}
			emit("number", rest[:n])
			i += n
		case isIdent(c):
			n := 1
			for n < len(rest) && isIdent(rest[n]) {
				n++
			}
			w := rest[:n]
			class := ""
			switch {
			case lx.keywords[w]:
				class = "keyword"
			case lx.literals[w]:
				class = "literal"
			case lx.types[w]:
				class = "type"
			case n < len(rest) && rest[n] == '(':
				class = "func"
			case w[0] >= 'A' && w[0] <= 'Z':
				class = "type"
			}
			emit(class, w)
			i += n
		default:
			emit("", rest[:1])
			i++
		}
	}
	return toks
}

func (lx *lexer) lineComment(s string) bool {
	for _, p := range lx.lineComments {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdent(c byte) bool {
	return c == '_' || c == '$' || isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
