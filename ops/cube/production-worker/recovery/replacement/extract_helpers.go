//go:build ignore

// Copy only reviewed shared helpers; exclude original crash/create/commit entrypoints.
package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strconv"
)

func main() {
	source := flag.String("source", "", "reviewed crash coordinator")
	out := flag.String("out", "", "new helper file")
	flag.Parse()
	fs := token.NewFileSet()
	f, e := parser.ParseFile(fs, *source, nil, 0)
	if e != nil {
		panic(e)
	}
	keep := map[string]bool{}
	for _, n := range []string{"must", "nonce", "durable", "save", "privateJSON", "worker", "bootID", "record", "detach", "attach", "remote", "ownership", "request", "ready", "validateEvidence", "probe", "holdWorkerLease"} {
		keep[n] = true
	}
	var decls []ast.Decl
	var imports []*ast.ImportSpec
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if keep[x.Name.Name] {
				decls = append(decls, d)
			}
		case *ast.GenDecl:
			if x.Tok == token.IMPORT {
				for _, s := range x.Specs {
					imports = append(imports, s.(*ast.ImportSpec))
				}
			} else {
				decls = append(decls, d)
			}
		}
	}
	used := map[string]bool{}
	for _, d := range decls {
		ast.Inspect(d, func(n ast.Node) bool {
			if x, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := x.X.(*ast.Ident); ok {
					used[id.Name] = true
				}
			}
			return true
		})
	}
	imp := &ast.GenDecl{Tok: token.IMPORT}
	for _, i := range imports {
		name := ""
		if i.Name != nil {
			name = i.Name.Name
		} else {
			v, _ := strconv.Unquote(i.Path.Value)
			for j := len(v) - 1; j >= 0; j-- {
				if v[j] == '/' {
					name = v[j+1:]
					break
				}
			}
			if name == "" {
				name = v
			}
		}
		if used[name] {
			imp.Specs = append(imp.Specs, i)
		}
	}
	f.Decls = append([]ast.Decl{imp}, decls...)
	f.Comments = nil
	fd, e := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		panic(e)
	}
	defer fd.Close()
	if e = printer.Fprint(fd, fs, f); e != nil {
		panic(e)
	}
}
