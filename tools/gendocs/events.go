package gendocs

import (
	"fmt"
	"strings"
)

// renderEvents renders docs/events.md from the internal/events package: the
// event type vocabulary, the Event and payload shapes, and Subscription
// filters.
func renderEvents(pkgDir string) (string, error) {
	info, err := parsePackage(pkgDir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(generatedHeader + "\n\n")
	b.WriteString("# pronto events\n\n")
	if info.Doc != "" {
		b.WriteString(info.Doc + "\n\n")
	}

	b.WriteString("## Event types\n\n")
	b.WriteString("| Type | Description |\n")
	b.WriteString("| ---- | ----------- |\n")
	for _, c := range info.Consts {
		if c.Type != "Type" {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", c.Value, c.Doc)
	}
	b.WriteString("\n")

	for _, s := range info.Structs {
		writeStructHeading(&b, 2, s)
	}

	b.WriteString("Subscription filters AND within one filter and OR across filters.\n")
	b.WriteString("Comma-separated type lists are parsed by `events.ParseTypes` (unknown types error).\n\n")
	b.WriteString("Machine-readable contract: `pronto api schema` (internal/events/schema.json).\n")
	return b.String(), nil
}
