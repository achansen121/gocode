package main

import (
	"go/ast"
	"go/token"
	"fmt"
	"reflect"
	"bytes"
	"strings"
	"io"
)

const (
	DECL_CONST = iota
	DECL_VAR
	DECL_TYPE
	DECL_FUNC
	DECL_MODULE
)

var declClassToString = map[int]string{
	0: "const",
	1: "var",
	2: "type",
	3: "func",
	4: "module",
}

type Decl struct {
	Name string
	Type ast.Expr
	Class int

	// functions for interface type, fields+methods for struct type
	// variables with anonymous struct/interface type may have children too
	Children []*Decl

	// if the type is unknown at AST building time, I'm using these
	Value ast.Expr

	// if it's a multiassignment and the Value is a CallExpr, it is being set
	// to an index into the return value tuple, otherwise it's a -1
	ValueIndex int
}

func astDeclType(d ast.Decl) ast.Expr {
	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.CONST, token.VAR:
			c := t.Specs[0].(*ast.ValueSpec)
			return c.Type
		case token.TYPE:
			t := t.Specs[0].(*ast.TypeSpec)
			return t.Type
		}
	case *ast.FuncDecl:
		return t.Type
	}
	panic("unreachable")
	return nil
}

func astDeclClass(d ast.Decl) int {
	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.VAR:
			return DECL_VAR
		case token.CONST:
			return DECL_CONST
		case token.TYPE:
			return DECL_TYPE
		}
	case *ast.FuncDecl:
		return DECL_FUNC
	}
	panic("unreachable")
	return 0
}

func astDeclConvertable(d ast.Decl) bool {
	switch t := d.(type) {
	case *ast.GenDecl:
		switch t.Tok {
		case token.VAR, token.CONST, token.TYPE:
			return true
		}
	case *ast.FuncDecl:
		return true
	}
	return false
}

func astTypeToChildren(ty ast.Expr) []*Decl {
	switch t := ty.(type) {
	case *ast.StructType:
		return astFieldListToDecls(t.Fields, DECL_VAR)
	case *ast.InterfaceType:
		return astFieldListToDecls(t.Methods, DECL_FUNC)
	}
	return nil
}

func astFieldListToDecls(f *ast.FieldList, class int) []*Decl {
	count := 0
	for _, field := range f.List {
		count += len(field.Names)
	}

	decls := make([]*Decl, count)
	i := 0
	for _, field := range f.List {
		for _, name := range field.Names {
			decls[i] = new(Decl)
			decls[i].Name = name.Name()
			decls[i].Type = field.Type
			decls[i].Class = class
			decls[i].Children = astTypeToChildren(field.Type)
			i++
		}
	}
	return decls
}

func astDeclToDecl(name string, d ast.Decl, value ast.Expr, vindex int) *Decl {
	if !astDeclConvertable(d) {
		return nil
	}
	decl := new(Decl)
	decl.Name = name
	decl.Type = astDeclType(d)
	decl.Class = astDeclClass(d)
	decl.Children = astTypeToChildren(decl.Type)
	decl.Value = value
	decl.ValueIndex = vindex

	return decl
}

func newDecl(name string) *Decl {
	decl := new(Decl)
	decl.Name = name
	return decl
}

func NewDecl(name string, class int) *Decl {
	decl := new(Decl)
	decl.Name = name
	decl.Class = class
	return decl
}

func MethodOf(d ast.Decl) string {
	if t, ok := d.(*ast.FuncDecl); ok {
		if t.Recv != nil {
			switch t := t.Recv.List[0].Type.(type) {
			case *ast.StarExpr:
				return t.X.(*ast.Ident).Name()
			case *ast.Ident:
				return t.Name()
			default:
				panic("unreachable")
			}
		}
	}
	return ""
}

func (d *Decl) ApplyDecl(other *Decl) {
	d.Type = other.Type
	d.Class = other.Class

	if other.Children == nil {
		return
	}

	for _, c := range other.Children {
		d.AddChild(c)
	}
}

func (d *Decl) String() string {
	var ty string
	if d.Type != nil {
		buf := bytes.NewBuffer(make([]byte, 0, 256))
		prettyPrintTypeExpr(buf, d.Type)
		ty = buf.String()
	}
	if d.Value != nil {
		return fmt.Sprintf("%s %s %s (somevalue)", declClassToString[d.Class], d.Name, ty)
	}
	return fmt.Sprintf("%s %s %s", declClassToString[d.Class], d.Name, ty)
}

func (d *Decl) PrettyPrint(out io.Writer, p string) {
	if p != "" && !startsWith(d.Name, p) {
		return
	}

	fmt.Fprintf(out, "%s %s", declClassToString[d.Class], d.Name)
	switch d.Class {
	case DECL_TYPE:
		switch t := d.Type.(type) {
		case *ast.StructType:
			fmt.Fprintf(out, " struct")
		case *ast.InterfaceType:
			fmt.Fprintf(out, " interface")
		default:
			prettyPrintTypeExpr(out, d.Type)
		}
	case DECL_VAR:
		if d.Type != nil {
			fmt.Fprintf(out, " ")
			prettyPrintTypeExpr(out, d.Type)
		}
	case DECL_FUNC:
		fmt.Fprintf(out, "(")
		prettyPrintFuncFieldList(out, d.Type.(*ast.FuncType).Params)
		fmt.Fprintf(out, ")")

		buf := bytes.NewBuffer(make([]byte, 0, 256))
		nresults := prettyPrintFuncFieldList(buf, d.Type.(*ast.FuncType).Results)
		if nresults > 0 {
			results := buf.String()
			if strings.Index(results, " ") != -1 {
				results = "(" + results + ")"
			}
			fmt.Fprintf(out, " %s", results)
		}
	}
	fmt.Fprintf(out, "\n")
}

func (d *Decl) PrettyPrintAutoComplete(out io.Writer, p string) {
	if p != "" && !startsWith(d.Name, p) {
		return
	}

	fmt.Fprintf(out, "%s", d.Name[len(p):])
	if d.Class == DECL_FUNC {
		fmt.Fprintf(out, "(")
	}

	fmt.Fprintf(out, "\n")
}

func (d *Decl) AddChild(cd *Decl) {
	if d.Children == nil {
		d.Children = make([]*Decl, 0, 4)
	}

	if cap(d.Children) < len(d.Children)+1 {
		newcap := cap(d.Children) * 2
		if newcap == 0 {
			newcap = 4
		}

		s := make([]*Decl, len(d.Children), newcap)
		copy(s, d.Children)
		d.Children = s
	}

	i := len(d.Children)
	d.Children = d.Children[0:i+1]
	d.Children[i] = cd
}

func checkForBuiltinFuncs(c *ast.CallExpr) ast.Expr {
	if t, ok := c.Fun.(*ast.Ident); ok {
		switch t.Name() {
		case "new":
			e := new(ast.StarExpr)
			e.X = c.Args[0]
			return e
		case "make":
			return c.Args[0]
		}
	}
	return nil
}

func funcReturnType(f *ast.FuncType, index int) ast.Expr {
	if index == -1 {
		return f.Results.List[0].Type
	}

	i := 0
	for _, field := range f.Results.List {
		if i >= index {
			return field.Type
		}
		if field.Names != nil {
			i += len(field.Names)
		} else {
			i++
		}
	}
	return nil
}

func typePath(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name()
	case *ast.StarExpr:
		return typePath(t.X)
	case *ast.SelectorExpr:
		path := ""
		if ident, ok := t.X.(*ast.Ident); ok {
			path += ident.Name()
		}
		return path + "." + t.Sel.Name()
	}
	return ""
}

// return expr and true if it's a type or false if it's a value
func inferType(v ast.Expr, index int, topLevel *AutoCompleteContext) (ast.Expr, bool) {
	//ty := reflect.Typeof(v)
	//fmt.Println(ty)
	switch t := v.(type) {
	case *ast.CompositeLit:
		return t.Type, true
	case *ast.Ident:
		if d := topLevel.findDecl(t.Name()); d != nil {
			return d.InferType(topLevel), d.Class == DECL_TYPE
		}
		return t, true // probably a builtin
	case *ast.UnaryExpr:
		switch t.Op {
		case token.AND:
			it, _ := inferType(t.X, -1, topLevel)
			if it == nil {
				break
			}

			e := new(ast.StarExpr)
			e.X = it
			return e, false
		// TODO: channel ops
		}
	case *ast.IndexExpr:
		it, isType := inferType(t.X, -1, topLevel)
		if it == nil {
			break
		}
		switch t := it.(type) {
		case *ast.ArrayType:
			return t.Elt, isType
		case *ast.MapType:
			return t.Value, isType
		}
	case *ast.StarExpr:
		it, isType := inferType(t.X, -1, topLevel)
		if it == nil {
			break
		}
		if isType {
			e := new(ast.StarExpr)
			e.X = it
			return e, true
		} else if s, ok := it.(*ast.StarExpr); ok {
			return s.X, false
		}
	case *ast.CallExpr:
		ty := checkForBuiltinFuncs(t)
		if ty != nil {
			return ty, true
		}

		it, _ := inferType(t.Fun, -1, topLevel)
		if it == nil {
			break
		}
		if f, ok := it.(*ast.FuncType); ok {
			return funcReturnType(f, index), true
		} else {
			return it, true
		}
	case *ast.ParenExpr:
		it, isType := inferType(t.X, -1, topLevel)
		if it == nil {
			break
		}
		return it, isType
	case *ast.SelectorExpr:
		it, _ := inferType(t.X, -1, topLevel)
		if it == nil {
			break
		}

		name := typePath(it)
		if d := topLevel.findDeclByPath(name); d != nil {
			c := d.FindChild(t.Sel.Name())
			if c != nil {
				return c.InferType(topLevel), c.Class == DECL_TYPE
			}
		}
	case *ast.TypeAssertExpr:
		return t.Type, true
	// TODO: channels here
	case *ast.ArrayType, *ast.MapType:
		return t, true
	default:
		ty := reflect.Typeof(v)
		fmt.Println(ty)
	}
	return nil, false
}

func (d *Decl) InferType(topLevel *AutoCompleteContext) ast.Expr {
	if d.Class == DECL_TYPE || d.Class == DECL_MODULE {
		// we're the type itself
		return ast.NewIdent(d.Name)
	}

	// shortcut
	if d.Type != nil {
		return d.Type
	}

	d.Type, _ = inferType(d.Value, d.ValueIndex, topLevel)
	return d.Type
}

func (d *Decl) FindChild(name string) *Decl {
	for _, c := range d.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}