package enzyme

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"regexp"
	"strings"

	"github.com/mlekudev/dendrite/pkg/axiom"
)

// GoSource is an enzyme that decomposes Go source code into typed AST elements.
//
// Each emitted element carries two pieces of information:
//   - Type tag: the AST node kind ("func", "assign", "return", "if", etc.)
//   - Value: rendered source text, optionally prefixed with parent context
//
// Body-level statements (assign, return, if, for, switch, select, go, send)
// carry their parent function name separated by \x00:
//
//	"main\x00x := foo()"   — assignment inside main()
//	"Run\x00return nil"    — return inside Run()
//
// This allows the emitter to reconstruct function bodies by grouping
// elements that share a parent function.
type GoSource struct{}

// CanDigest returns true if the sample looks like Go source.
func (GoSource) CanDigest(sample []byte) bool {
	for i := 0; i < len(sample)-8 && i < 512; i++ {
		if string(sample[i:i+8]) == "package " {
			return true
		}
	}
	return false
}

// Digest parses Go source and emits typed elements for each declaration,
// statement, identifier, and literal. Body-level statements carry their
// rendered source text and parent function context.
func (GoSource) Digest(r io.Reader) <-chan axiom.Element {
	ch := make(chan axiom.Element, 128)

	go func() {
		defer close(ch)

		src, err := io.ReadAll(r)
		if err != nil {
			return
		}

		// Extract //go: directives before AST parsing (parser strips them).
		emitDirectives(ch, src)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "input.go", src, parser.ParseComments)
		if err != nil {
			return
		}

		// Package name.
		ch <- newHexElement("package", file.Name.Name)

		// Imports.
		for _, imp := range file.Imports {
			if imp.Path != nil {
				ch <- newHexElement("import", imp.Path.Value)
			}
		}

		// Process top-level declarations with scope tracking.
		for _, decl := range file.Decls {
			emitDecl(ch, fset, src, decl)
		}
	}()

	return ch
}

// emitDecl processes a top-level declaration and emits elements.
func emitDecl(ch chan<- axiom.Element, fset *token.FileSet, src []byte, decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		emitFuncDecl(ch, fset, src, d)
	case *ast.GenDecl:
		emitGenDecl(ch, fset, src, d)
	}
}

// emitGenDecl processes type, const, and var declarations.
func emitGenDecl(ch chan<- axiom.Element, fset *token.FileSet, _ []byte, d *ast.GenDecl) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			ch <- newHexElement("type", s.Name.Name)
			ch <- newHexElement("ident:type-name", s.Name.Name)
			switch s.Type.(type) {
			case *ast.StructType:
				ch <- newHexElement("struct", s.Name.Name)
			case *ast.InterfaceType:
				ch <- newHexElement("interface", s.Name.Name)
			}
			// Emit fields for struct types.
			if st, ok := s.Type.(*ast.StructType); ok && st.Fields != nil {
				for _, f := range st.Fields.List {
					for _, name := range f.Names {
						fieldVal := name.Name
						if f.Type != nil {
							fieldVal += " " + renderNode(fset, f.Type)
						}
						ch <- newHexElement("field", s.Name.Name+"\x00"+fieldVal)
						ch <- newHexElement("ident:field-name", name.Name)
					}
				}
			}
			// Emit method signatures for interface types.
			if it, ok := s.Type.(*ast.InterfaceType); ok && it.Methods != nil {
				for _, m := range it.Methods.List {
					for _, name := range m.Names {
						sig := name.Name
						if ft, ok := m.Type.(*ast.FuncType); ok {
							sig += renderFuncSig(fset, ft)
						}
						ch <- newHexElement("method", sig)
						ch <- newHexElement("ident:method-name", name.Name)
					}
				}
			}
		case *ast.ImportSpec:
			// Already handled above.
		case *ast.ValueSpec:
			for _, name := range s.Names {
				val := name.Name
				if s.Type != nil {
					val += " " + renderNode(fset, s.Type)
				}
				ch <- newHexElement("ident:var-name", val)
			}
			// Also emit a full var declaration for top-level reconstruction.
			if d.Tok.String() == "var" || d.Tok.String() == "const" {
				rendered := renderNode(fset, s)
				ch <- newHexElement("var", d.Tok.String()+" "+rendered)
			}
		}
	}
}

// emitFuncDecl processes a function declaration and its body.
func emitFuncDecl(ch chan<- axiom.Element, fset *token.FileSet, src []byte, fn *ast.FuncDecl) {
	name := fn.Name.Name
	if fn.Recv != nil {
		// Method — emit with receiver type and variable name.
		recv := ""
		recvVar := ""
		if len(fn.Recv.List) > 0 {
			recv = renderNode(fset, fn.Recv.List[0].Type)
			if len(fn.Recv.List[0].Names) > 0 {
				recvVar = fn.Recv.List[0].Names[0].Name
			}
		}
		ch <- newHexElement("method", recvVar+"\x00"+recv+"."+name+renderFuncSig(fset, fn.Type))
		ch <- newHexElement("ident:method-name", name)
		if recvVar != "" {
			ch <- newHexElement("ident:receiver", recvVar)
		}
	} else {
		ch <- newHexElement("func", name+renderFuncSig(fset, fn.Type))
		ch <- newHexElement("ident:func-name", name)
	}

	// Emit param and result name idents.
	emitFuncTypeIdents(ch, fn.Type)

	// Emit function body statements with parent context.
	if fn.Body != nil {
		emitBlock(ch, fset, src, name, fn.Body)
	}
}

// emitBlock processes a block statement, emitting each statement with
// its parent function context.
func emitBlock(ch chan<- axiom.Element, fset *token.FileSet, src []byte, parent string, block *ast.BlockStmt) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		emitStmt(ch, fset, src, parent, stmt)
	}
}

// emitStmt processes a single statement, rendering it to source text
// and tagging it with its parent function and source line number.
func emitStmt(ch chan<- axiom.Element, fset *token.FileSet, src []byte, parent string, stmt ast.Stmt) {
	// The value carries parent context and source position:
	// "funcName\x00linenum\x00rendered_source"
	// The line number enables the emitter to reconstruct original statement
	// order when causal (define-use) analysis is ambiguous.
	tag := stmtTag(stmt)
	if tag == "" {
		return
	}

	rendered := renderNode(fset, stmt)
	lineNum := fset.Position(stmt.Pos()).Line
	val := fmt.Sprintf("%s\x00%d\x00%s", parent, lineNum, rendered)

	ch <- newHexElement(tag, val)

	// Recurse into nested blocks so inner statements are also captured.
	switch s := stmt.(type) {
	case *ast.IfStmt:
		emitBlock(ch, fset, src, parent, s.Body)
		if s.Else != nil {
			if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
				emitBlock(ch, fset, src, parent, elseBlock)
			} else if elseIf, ok := s.Else.(*ast.IfStmt); ok {
				emitStmt(ch, fset, src, parent, elseIf)
			}
		}
	case *ast.ForStmt:
		emitBlock(ch, fset, src, parent, s.Body)
	case *ast.RangeStmt:
		emitBlock(ch, fset, src, parent, s.Body)
	case *ast.SwitchStmt:
		emitBlock(ch, fset, src, parent, s.Body)
	case *ast.TypeSwitchStmt:
		emitBlock(ch, fset, src, parent, s.Body)
	case *ast.SelectStmt:
		emitBlock(ch, fset, src, parent, s.Body)
	case *ast.BlockStmt:
		emitBlock(ch, fset, src, parent, s)
	}

}

// stmtTag returns the element type tag for a statement node.
func stmtTag(stmt ast.Stmt) string {
	switch stmt.(type) {
	case *ast.AssignStmt:
		return "assign"
	case *ast.ReturnStmt:
		return "return"
	case *ast.IfStmt:
		return "if"
	case *ast.ForStmt, *ast.RangeStmt:
		return "for"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return "switch"
	case *ast.SelectStmt:
		return "select"
	case *ast.GoStmt:
		return "go"
	case *ast.SendStmt:
		return "send"
	case *ast.ExprStmt:
		return "expr"
	case *ast.DeferStmt:
		return "defer"
	case *ast.DeclStmt:
		return "decl"
	case *ast.IncDecStmt:
		return "assign" // treat i++ as assignment
	case *ast.BranchStmt:
		return "branch"
	case *ast.CaseClause:
		return "case"
	case *ast.CommClause:
		return "comm"
	}
	return ""
}

// renderNode renders an AST node back to Go source text.
func renderNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.RawFormat, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

// renderFuncSig renders a function type's parameter and result lists.
func renderFuncSig(fset *token.FileSet, ft *ast.FuncType) string {
	if ft == nil {
		return "()"
	}
	var buf bytes.Buffer
	buf.WriteByte('(')
	if ft.Params != nil {
		for i, p := range ft.Params.List {
			if i > 0 {
				buf.WriteString(", ")
			}
			for j, name := range p.Names {
				if j > 0 {
					buf.WriteString(", ")
				}
				buf.WriteString(name.Name)
			}
			if p.Type != nil {
				if len(p.Names) > 0 {
					buf.WriteByte(' ')
				}
				buf.WriteString(renderNode(fset, p.Type))
			}
		}
	}
	buf.WriteByte(')')
	if ft.Results != nil && len(ft.Results.List) > 0 {
		buf.WriteByte(' ')
		if len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) == 0 {
			buf.WriteString(renderNode(fset, ft.Results.List[0].Type))
		} else {
			buf.WriteByte('(')
			for i, r := range ft.Results.List {
				if i > 0 {
					buf.WriteString(", ")
				}
				for j, name := range r.Names {
					if j > 0 {
						buf.WriteString(", ")
					}
					buf.WriteString(name.Name)
				}
				if r.Type != nil {
					if len(r.Names) > 0 {
						buf.WriteByte(' ')
					}
					buf.WriteString(renderNode(fset, r.Type))
				}
			}
			buf.WriteByte(')')
		}
	}
	return buf.String()
}

// emitFuncTypeIdents emits subtyped idents for function parameter and
// result names. Called from emitFuncDecl for each function/method.
func emitFuncTypeIdents(ch chan<- axiom.Element, ft *ast.FuncType) {
	if ft == nil {
		return
	}
	if ft.Params != nil {
		for _, p := range ft.Params.List {
			for _, name := range p.Names {
				ch <- newHexElement("ident:param", name.Name)
			}
		}
	}
	if ft.Results != nil {
		for _, r := range ft.Results.List {
			for _, name := range r.Names {
				ch <- newHexElement("ident:result", name.Name)
			}
		}
	}
}

// declNameRe matches Go declaration keywords followed by a name.
var declNameRe = regexp.MustCompile(`(?m)^(?:var|type|func|const)\s+(\w+)`)

// emitDirectives scans raw source for //go: directives and emits them
// as "directive" elements. Each directive is associated with the next
// var/type/func/const declaration that follows it.
func emitDirectives(ch chan<- axiom.Element, src []byte) {
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//go:") {
			continue
		}
		// Find the associated declaration: scan forward for var/type/func/const.
		assocName := ""
		for j := i + 1; j < len(lines); j++ {
			m := declNameRe.FindStringSubmatch(strings.TrimSpace(lines[j]))
			if len(m) > 1 {
				assocName = m[1]
				break
			}
		}
		ch <- newHexElement("directive", assocName+"\x00"+trimmed)
	}
}
