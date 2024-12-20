package mapper

import (
	"fmt"
	"regexp"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

var whitespaceRegex = regexp.MustCompile(`\s`)
var manyWhitespaceRegex = regexp.MustCompile(`\s+`)

func GetCodeMap(root *sitter.Node, filename string, sourceCode []byte) ([]string, error) {
	if root == nil {
		return nil, fmt.Errorf("root node cannot be nil")
	}

	terms := map[string]bool{}

	// var builder strings.Builder
	// builder.WriteString(fmt.Sprintf("## %s\n", filename))

	// Helper function to recursively collect all identifier values
	collectIdentifiers := func(node *sitter.Node) []string {
		var values []string
		var collect func(*sitter.Node)

		collect = func(n *sitter.Node) {
			if n == nil {
				return
			}

			if n.IsNamed() {
				nodeType := n.Kind()
				switch nodeType {
				case "identifier", "field_identifier", "package_identifier":
					text := string(sourceCode[n.StartByte():n.EndByte()])
					if len(text) > 1 && !whitespaceRegex.MatchString(text) {
						values = append(values, text)
					}
				}
			}

			// Recursively process all children
			for i := uint(0); i < n.NamedChildCount(); i++ {
				if child := n.NamedChild(i); child != nil {
					collect(child)
				}
			}
		}

		collect(node)
		return values
	}

	var traverse func(node *sitter.Node)

	traverse = func(node *sitter.Node) {
		if node == nil {
			return
		}

		if node.IsNamed() {
			nodeType := node.Kind()

			switch nodeType {
			// case "identifier", "field_identifier", "package_identifier":
			// 	text := string(sourceCode[node.StartByte():node.EndByte()])
			// 	if len(text) > 1 && !whitespaceRegex.MatchString(text) {
			// 		builder.WriteString(fmt.Sprintf("%s%s\n", indent, text))
			// 	}
			// 	return

			case "function_declaration", "method_declaration", "struct_declaration",
				"interface_declaration", "type_declaration", "identifier", "field_identifier", "package_identifier":
				text := string(sourceCode[node.StartByte():node.EndByte()])
				if len(text) > 1 {
					for _, id := range collectIdentifiers(node) {
						terms[id] = true
					}

				}
				return // Skip further traversal for this branch
			}
		}

		// Process children for non-declaration nodes
		for i := uint(0); i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				traverse(child)
			}
		}
	}

	traverse(root)

	keywords := []string{}
	for t := range terms {
		keywords = append(keywords, t)
	}

	return keywords, nil
}
