package raymond

import "github.com/aymerick/raymond/ast"

// ValidateHelpers follows Handlebars.java's compile-time invocation checks,
// including expressions inside branches that will not execute.
func (tpl *Template) ValidateHelpers() error {
	if err := tpl.parse(); err != nil {
		return err
	}
	var walk func(ast.Node, bool) error
	walk = func(raw ast.Node, block bool) error {
		var children []ast.Node
		switch node := raw.(type) {
		case *ast.Program:
			if node == nil {
				return nil
			}
			children = node.Body
		case *ast.BlockStatement:
			if err := walk(node.Expression, true); err != nil {
				return err
			}
			children = []ast.Node{node.Program, node.Inverse}
		case *ast.MustacheStatement:
			children = []ast.Node{node.Expression}
		case *ast.SubExpression:
			children = []ast.Node{node.Expression}
		case *ast.Expression:
			name := node.HelperName()
			if name != "" && (len(node.Params) > 0 || (node.Hash != nil && len(node.Hash.Pairs) > 0)) && tpl.findHelper(name) == zero && findHelper(name) == zero {
				return &MissingHelperError{Name: name, Position: node.Path.Location().Pos, Block: block}
			}
			children = append(children, node.Params...)
			if node.Hash != nil {
				children = append(children, node.Hash)
			}
		case *ast.Hash:
			for _, pair := range node.Pairs {
				children = append(children, pair.Val)
			}
		}
		for _, child := range children {
			if err := walk(child, false); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(tpl.program, false)
}
