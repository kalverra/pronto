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
	b.WriteString(renderExampleTOML())
	b.WriteString("```\n")
	return b.String(), nil
}

func renderExampleTOML() string {
	var b strings.Builder
	b.WriteString("pr_view = \"condensed\"\n")
	b.WriteString("pr_view_command = \"\"\n\n")

	b.WriteString("[notifications]\n")
	b.WriteString("mode = \"native\"\n")
	b.WriteString("popups = true\n")
	b.WriteString("sound = false\n")
	b.WriteString("groups = [\"focus\", \"mine\", \"priority\"]\n\n")

	b.WriteString("[notifications.sounds]\n")
	b.WriteString("# ci_passed = \"Glass\"\n\n")

	b.WriteString("[notifications.images]\n")
	b.WriteString("# ci_passed = \"/path/to/icon.png\"\n\n")

	b.WriteString("[focus]\n")
	b.WriteString("exclude_bots = true\n")
	b.WriteString("# authors = [\"alice\"]\n")
	b.WriteString("# repos = [\"kalverra/pronto\"]\n\n")

	b.WriteString("[[focus.rules]]\n")
	b.WriteString("# repo = \"kalverra/pronto\"\n")
	b.WriteString("# keywords = [\"urgent\", \"security\"]\n")
	b.WriteString("# files = [\"go.mod\"]\n")
	b.WriteString("# directories = [\"internal/notify\"]\n")
	b.WriteString("# regex = ['^migrations/.*\\.sql$']\n")
	b.WriteString("# authors = [\"charlie\"]\n\n")

	b.WriteString("[priority]\n")
	b.WriteString("direct_requests = true\n")
	b.WriteString("exclude_bots = true\n")
	b.WriteString("# authors = [\"alice\"]\n")
	b.WriteString("# repos = [\"org/critical-service\"]\n\n")

	b.WriteString("[[priority.rules]]\n")
	b.WriteString("# repo = \"org/app\"\n")
	b.WriteString("# directories = [\"infra\"]\n")
	b.WriteString("# regex = ['(?i)\\.proto$']\n\n")

	b.WriteString("[server]\n")
	b.WriteString("# poll_interval = \"30s\"\n")
	b.WriteString("pprof_addr = \"\"\n")
	b.WriteString("leak_check_interval = \"1h\"\n")

	return b.String()
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
	case []string:
		if len(v) == 0 {
			return "`[]`"
		}
		items := make([]string, len(v))
		for i, s := range v {
			items[i] = `"` + s + `"`
		}
		return "`[" + strings.Join(items, ", ") + "]`"
	default:
		return fmt.Sprintf("%v", v)
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
