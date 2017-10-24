package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

type sortFuncsByName []*ast.FuncDecl

func main() {
	var tags string
	var doNothing bool
	flag.StringVar(&tags, `tags`, ``, `build tags`)
	flag.BoolVar(&doNothing, `n`, false, `do nothing`)
	flag.Parse()

	// TODO add '!test' and '!gomust' build tags
	//ctx := parser.Default
	// ctx.BuildTags = strings.Split(tags, ",", -1)

	set := token.NewFileSet()
	packs, err := parser.ParseDir(set, flag.Arg(0), nil, parser.ParseComments)
	if err != nil {
		fmt.Println("Failed to parse package:", err)
		os.Exit(1)
	}

	allFuncs := make(map[string]*ast.FuncDecl)
	for _, pack := range packs {
		if strings.HasSuffix(pack.Name, "_test") {
			continue
		}
		for _, f := range pack.Files {
			if strings.HasSuffix(f.Name.Name, "_test.go") {
				continue
			}
			for _, d := range f.Decls {
				if fn, isFn := d.(*ast.FuncDecl); isFn {
					if fn.Recv != nil {
						continue
					}
					if !fn.Name.IsExported() {
						continue
					}
					results := fn.Type.Results
					if results == nil || results.List == nil {
						continue
					}
					if strings.HasPrefix(fn.Name.Name, "Must") {
						letterAfterMust, _ := utf8.DecodeRuneInString(fn.Name.Name[4:])
						if unicode.IsUpper(letterAfterMust) {
							continue
						}
					}

					// Keep function only if have an 'error' as type of last result

					//ast.Print(set, results.List[len(results.List)-1].Type)
					typeOfLastResult := results.List[len(results.List)-1].Type
					ident, isIdent := typeOfLastResult.(*ast.Ident)
					if !isIdent || ident.Name != "error" {
						continue
					}

					allFuncs[fn.Name.Name] = fn
				}
			}
		}
	}

	type Func struct {
		Name      string
		Signature string
		Doc       string
	}

	var funcs []*Func

	for name, decl := range allFuncs {
		fn := Func{
			Name: name,
		}
		if decl.Doc != nil {
			comments := make([]string, 0, len(decl.Doc.List)+1)
			for _, comment := range decl.Doc.List {
				comments = append(comments, comment.Text)
			}
			comments = append(comments, "")
			// fmt.Printf("%s: %d\n", fn.Name, len(comments))
			fn.Doc = strings.Join(comments, "\n")
		}
		funcs = append(funcs, &fn)
	}

	for _, fn := range funcs {
		fmt.Printf("%sfunc (must) %s()\n\n", fn.Doc, fn.Name)
	}

}
