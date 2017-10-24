package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// See https://golang.org/ref/spec#Qualified_identifiers
func collectTypeImports(collector map[string]struct{}, node ast.Node) {
	ast.Inspect(node, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.SelectorExpr:
			if ident, ok := x.X.(*ast.Ident); ok {
				collector[ident.Name] = struct{}{}
			}
		}
		return true
	})
}

func collectFuncImports(fn *ast.FuncDecl) map[string]struct{} {
	collector := make(map[string]struct{})
	if params := fn.Type.Params; params != nil {
		for _, field := range params.List {
			collectTypeImports(collector, field.Type)
		}
	}
	if results := fn.Type.Results; results != nil {
		for _, result := range results.List {
			collectTypeImports(collector, result.Type)
		}
	}
	return collector
}

func extractFileImports(specs []*ast.ImportSpec) map[string]string {
	result := make(map[string]string)

	for _, spec := range specs {
		var name string
		path := spec.Path.Value
		if path[0] == '"' {
			path, _ = strconv.Unquote(path)
		}
		if spec.Name != nil {
			name = spec.Name.Name
			if name == "." || name == "_" { // skip !
				continue
			}
		} else {
			name = path
			// FIXME find the true rule for converting an import path into
			// the local alias
			// The spec says this is implementation dependent:
			// http://localhost:6060/ref/spec#Import_declarations
			if i := strings.LastIndexByte(name, '/'); i >= 0 {
				name = name[i+1:]
			}
			if i := strings.IndexByte(name, '.'); i >= 0 {
				name = name[:i]
			}
		}
		result[name] = path
	}

	return result
}

type sortFuncsByName []*ast.FuncDecl

func main() {
	var tags string
	var doNothing bool
	var update bool
	flag.StringVar(&tags, `tags`, ``, `build tags`)
	flag.BoolVar(&doNothing, `n`, false, `do not write file`)
	flag.BoolVar(&update, `u`, false, `update: regenerate using existing settings`)
	flag.Parse()

	// TODO add '!test' and '!gomust' build tags
	//ctx := parser.Default
	// ctx.BuildTags = strings.Split(tags, ",", -1)

	set := token.NewFileSet()
	packs, err := parser.ParseDir(set, flag.Arg(0), nil, parser.ParseComments|parser.DeclarationErrors)
	if err != nil {
		fmt.Println("Failed to parse package:", err)
		os.Exit(1)
	}

	type FuncSource struct {
		Func *ast.FuncDecl
		File *ast.File
	}

	allFuncs := make(map[string]*FuncSource)
	for _, pack := range packs {
		if strings.HasSuffix(pack.Name, "_test") {
			continue
		}
		for _, f := range pack.Files {
			//fmt.Println(f.Name.Name)
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

					ast.Print(set, fn.Type)
					//ast.Print(set, results.List[len(results.List)-1].Type)
					typeOfLastResult := results.List[len(results.List)-1].Type
					ident, isIdent := typeOfLastResult.(*ast.Ident)
					if !isIdent || ident.Name != "error" {
						continue
					}

					allFuncs[fn.Name.Name] = &FuncSource{Func: fn, File: f}
				}
			}
		}
	}

	type File struct {
		Source        *FuncSource
		ImportsWanted map[string]struct{}
	}

	fileImports := make(map[string]*File)

	type Func struct {
		Name      string
		Signature string
		Doc       string
	}

	var funcs []*Func

	for name, source := range allFuncs {
		imports := collectFuncImports(source.Func)

		if f, exists := fileImports[source.File.Name.Name]; exists {
			existing := f.ImportsWanted
			for name := range imports { // merge
				existing[name] = struct{}{}
			}
		} else {
			fileImports[source.File.Name.Name] = &File{
				Source:        source,
				ImportsWanted: imports,
			}
		}

		fn := Func{
			Name: name,
		}
		if source.Func.Doc != nil {
			comments := make([]string, 0, len(source.Func.Doc.List)+1)
			for _, comment := range source.Func.Doc.List {
				comments = append(comments, comment.Text)
			}
			comments = append(comments, "")
			// fmt.Printf("%s: %d\n", fn.Name, len(comments))
			fn.Doc = strings.Join(comments, "\n")
		}
		funcs = append(funcs, &fn)
	}

	// fmt.Printf("%#v\n", fileImports)

	type Import struct {
		Source *FuncSource
		Path   string
	}

	var mergedImports map[string]*Import

	var errors int

	for _, wanted := range fileImports {
		var imports map[string]string
		importPathFor := func(alias string) string {
			if imports == nil {
				imports = extractFileImports(wanted.Source.File.Imports)
				//fmt.Printf("imports: %#v\n", imports)
			}
			return imports[alias]
		}
		for alias := range wanted.ImportsWanted {
			imp := mergedImports[alias]
			if imp == nil {
				path := importPathFor(alias)
				if path == "" {
					fmt.Fprintf(os.Stderr,
						"%s(%s): can't find import path for package %q\n",
						wanted.Source.Func.Name.Name,
						wanted.Source.File.Name,
						alias,
					)
					errors++
					continue
				}
				if mergedImports == nil {
					mergedImports = make(map[string]*Import)
				}
				mergedImports[alias] = &Import{
					Source: wanted.Source,
					Path:   path,
				}
				continue
			}
			if imp.Source.File == wanted.Source.File {
				continue // Already resolved from this file
			}
			path := importPathFor(alias)
			if path == "" {
				fmt.Fprintf(os.Stderr,
					"%s(%s): can't find import path for package %q\n",
					wanted.Source.Func.Name.Name,
					wanted.Source.File.Name,
					alias,
				)
				errors++
				continue
			}
			if path != imp.Path {
				fmt.Fprintf(os.Stderr,
					"%s(%s): import conflict for alias %q with %s(%s): %q vs %q\n",
					wanted.Source.Func.Name.Name,
					wanted.Source.File.Name,
					alias,
					imp.Source.Func.Name.Name,
					imp.Source.File.Name,
					path,
					imp.Path,
				)
				errors++
				continue
			}
		}
	}

	if len(mergedImports) > 0 {
		fmt.Println("Imports:")
		for alias, imp := range mergedImports {
			fmt.Printf("  %s %q\n", alias, imp.Path)
		}
	}

	for _, fn := range funcs {
		fmt.Printf("%sfunc (must) %s()\n\n", fn.Doc, fn.Name)
	}

}
