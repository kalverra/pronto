package gendocs

import (
	"fmt"
	"strings"
)

// renderModel renders docs/model.md from the internal/model package: the JSON
// shapes served by queue.snapshot, pr.get, and pronto list --json.
func renderModel(pkgDir string) (string, error) {
	info, err := parsePackage(pkgDir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(generatedHeader + "\n\n")
	b.WriteString("# pronto model reference\n\n")
	if info.Doc != "" {
		b.WriteString(info.Doc + "\n\n")
	}
	b.WriteString("These are the JSON shapes served by `queue.snapshot`, `pr.get`, and\n")
	b.WriteString("`pronto list --json`.\n\n")

	for _, s := range info.Structs {
		writeStructHeading(&b, 2, s)
	}

	// Enumerated string values, grouped by named type in first-seen order.
	var order []string
	byType := map[string][]constDef{}
	for _, c := range info.Consts {
		if c.Type == "" {
			continue
		}
		if _, seen := byType[c.Type]; !seen {
			order = append(order, c.Type)
		}
		byType[c.Type] = append(byType[c.Type], c)
	}
	if len(order) > 0 {
		b.WriteString("## Enumerated values\n\n")
		for _, typeName := range order {
			fmt.Fprintf(&b, "### %s\n\n", typeName)
			b.WriteString("| Value | Description |\n")
			b.WriteString("| ----- | ----------- |\n")
			for _, c := range byType[typeName] {
				fmt.Fprintf(&b, "| `%s` | %s |\n", c.Value, c.Doc)
			}
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}
