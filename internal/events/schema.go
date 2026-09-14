package events

import (
	_ "embed"
)

//go:generate go run github.com/kalverra/pronto/tools/gendocs/cmd/gendocs

//go:embed schema.json
var schemaJSON string

// SchemaJSON returns the embedded wire protocol JSON Schema document. The
// schema is generated: enums derive from ValidTypes, ValidCodes, and
// server.Methods. Never edit schema.json by hand; run `mise run generate`.
func SchemaJSON() string {
	return schemaJSON
}
