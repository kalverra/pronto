package gendocs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kalverra/pronto/internal/config"
)

// renderConfig renders docs/config.md from the config spec table plus the
// internal/config package docs.
func renderConfig(pkgDir string) (string, error) {
	info, err := parsePackage(pkgDir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(generatedHeader + "\n\n")
	b.WriteString("# pronto configuration\n\n")
	if info.Doc != "" {
		b.WriteString(info.Doc + "\n\n")
	}
	b.WriteString("Configuration is loaded from `pronto.toml` (see resolution order below);\n")
	b.WriteString("environment variables override file values.\n\n")

	b.WriteString("## Keys\n\n")
	b.WriteString("| TOML key | Env | Type | Default | Allowed values | Description |\n")
	b.WriteString("| -------- | --- | ---- | ------- | -------------- | ----------- |\n")
	for _, spec := range config.Specs {
		env, valid := spec.Env, spec.Valid
		if env == "" {
			env = "—"
		}
		if valid == nil {
			valid = []string{"—"}
		}
		quoted := make([]string, len(valid))
		for i, v := range valid {
			quoted[i] = "`" + v + "`"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s | %s |\n",
			spec.Key, env, spec.Type, formatDefault(spec.Default),
			strings.Join(quoted, ", "), spec.Doc)
	}
	b.WriteString("\n")

	if dir := funcDoc(info, "Dir"); dir != "" {
		b.WriteString("## Config file resolution\n\n")
		b.WriteString(dir + "\n\n")
	}

	b.WriteString("## Example\n\n")
	b.WriteString("```toml\n")
	for _, spec := range config.Specs {
		switch spec.Type {
		case "map[trigger]string":
			fmt.Fprintf(&b, "[%s]\n# %s = \"path/to/file\"\n\n", spec.Key, spec.Valid[0])
		default:
			if spec.Default == nil {
				fmt.Fprintf(&b, "# %s = \"30s\"\n", spec.Key)
				continue
			}
			fmt.Fprintf(&b, "%s = %s\n", spec.Key, formatTOMLValue(spec.Default))
		}
	}
	b.WriteString("```\n")
	return b.String(), nil
}

// formatDefault renders a spec default for the key table.
func formatDefault(d any) string {
	switch v := d.(type) {
	case nil:
		return "—"
	case bool:
		return strconv.FormatBool(v)
	case string:
		if v == "" {
			return "`\"\"`"
		}
		return "`\"" + v + "\"`"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatTOMLValue renders a spec default as a TOML value.
func formatTOMLValue(d any) string {
	switch v := d.(type) {
	case bool:
		return strconv.FormatBool(v)
	case string:
		return fmt.Sprintf("%q", v)
	default:
		return fmt.Sprintf("%v", d)
	}
}

// funcDoc returns the doc comment of an exported function, or empty.
func funcDoc(info packageInfo, name string) string {
	for _, f := range info.Funcs {
		if f.Name == name {
			return f.Doc
		}
	}
	return ""
}
