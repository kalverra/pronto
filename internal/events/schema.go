package events

import (
	_ "embed"
)

//go:generate go run github.com/kalverra/pronto/tools/gendocs/cmd/gendocs

//go:embed schema.json
var schemaJSON string

// SchemaJSON returns the embedded wire protocol JSON Schema document.
func SchemaJSON() string {
	return schemaJSON
}
