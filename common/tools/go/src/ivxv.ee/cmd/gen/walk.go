package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// parse parses the AST of the package.
func parse(pkg *build.Package) (fset *token.FileSet, parsed *ast.Package, ok bool) {
	fset = token.NewFileSet()
	pkgmap, err := parser.ParseDir(fset, pkg.Dir, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not parse %s: %v\n", pkg.ImportPath, err)
		return
	}
	parsed, ok = pkgmap[pkg.Name]
	if !ok {
		fmt.Fprintln(os.Stderr, "error:", rel(pkg.Dir), "does not contain package", pkg.Name)
	}
	return
}

type literal struct {
	Pos    token.Position
	Name   string
	Fields []string
}

func (l literal) String() string {
	return fmt.Sprintf("%s{%s}", l.Name, strings.Join(l.Fields, " "))
}

// walk walks the package AST looking for struct literals with undeclared
// exported local package types and key:value fields.
func walk(path string, fset *token.FileSet, pkg *ast.Package) (literals []literal, ok bool) {
	scope := pkg.Scope
	if scope == nil {
		// Collect all file scopes.
		scope = ast.NewScope(nil)
		for name, file := range pkg.Files {
			// Skip the scope of files that are generated by us.
			// Otherwise when a new literal is added, the old ones
			// generated by us will no longer be included.
			base := filepath.Base(name)
			if base == *namep+".go" || base == *namep+"_test.go" {
				continue
			}
			for _, obj := range file.Scope.Objects {
				// Do not care if the scope already contained
				// an object with this name: it still means
				// that the name is declared.
				scope.Insert(obj)
			}
		}
	}

	ok = true
	seen := make(map[string]token.Position)
	var blocklvl int
	var inspector func(ast.Node) bool // Declare for recursion.
	inspector = func(node ast.Node) (recurse bool) {
		recurse = true
		if node == nil {
			return
		}

		// For block statements, create a new block scope, increase
		// block level, and inspect statements in the block ourselves.
		if block, match := node.(*ast.BlockStmt); match {
			scope = ast.NewScope(scope)
			blocklvl++
			for _, stmt := range block.List {
				ast.Inspect(stmt, inspector)
			}
			blocklvl--
			scope = scope.Outer
			return false // We already recursed.
		}

		// If we are inside a block, add type declarations to the scope.
		if spec, match := node.(*ast.TypeSpec); match && blocklvl > 0 {
			scope.Insert(ast.NewObj(ast.Typ, spec.Name.Name))
			return
		}

		// Return as soon as a condition is not met: avoid deep nesting
		// of control blocks.
		complit, match := node.(*ast.CompositeLit) // Composite literal.
		if !match {
			return
		}
		ident, match := complit.Type.(*ast.Ident) // With simple identifier type.
		if !match {
			return
		}
		if !ident.IsExported() { // That is exported.
			return
		}
		for s := scope; s != nil; s = s.Outer { // And does not exist in the scope.
			if s.Lookup(ident.Name) != nil {
				return
			}
		}

		// Check if we have already seen this name before.
		name := ident.Name
		pos := fset.Position(ident.NamePos)
		if prev, dup := seen[ident.Name]; dup {
			fmt.Fprintf(os.Stderr, "error: %s: duplicate %s, "+
				"previously seen at %s\n", relpos(pos), name, prev)
			ok = false
			return
		}
		seen[name] = relpos(pos)

		// Check the fields of the literal and add it to the result.
		l := literal{Pos: pos, Name: name}
		for _, el := range complit.Elts {
			kv, match := el.(*ast.KeyValueExpr)
			if !match {
				fmt.Fprintln(os.Stderr, fieldError{
					relpos(fset.Position(el.Pos())), ident.Name,
					"must have key:value fields"})
				ok = false
				return
			}
			key, match := kv.Key.(*ast.Ident)
			if !match || !key.IsExported() {
				fmt.Fprintln(os.Stderr, fieldError{
					relpos(fset.Position(el.Pos())), ident.Name,
					"must have exported identifier keys"})
				ok = false
				return
			}
			l.Fields = append(l.Fields, key.Name)
		}
		literals = append(literals, l)

		if *vp {
			var pre string
			if path != "." {
				pre = path + "."
			}
			fmt.Printf("found %s%s at %s\n", pre, l, relpos(l.Pos))
		}
		return
	}
	ast.Inspect(pkg, inspector)
	return
}

// relpos sets the filename in p to rel(p).
func relpos(p token.Position) token.Position {
	p.Filename = rel(p.Filename)
	return p
}

type fieldError struct {
	pos  token.Position
	name string
	err  string
}

func (e fieldError) Error() string {
	return fmt.Sprintf("error: %s: undeclared struct literal %s %s", e.pos, e.name, e.err)
}