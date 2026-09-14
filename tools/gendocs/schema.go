package gendocs

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/server"
)

// RenderSchema builds the socket API JSON Schema document. Enum values are
// derived from events.ValidTypes, events.ValidCodes, and server.Methods, so
// the schema can never drift from the code. Output is deterministic.
func RenderSchema() ([]byte, error) {
	eventTypes := make([]string, 0, len(events.ValidTypes))
	for typ := range maps.Keys(events.ValidTypes) {
		eventTypes = append(eventTypes, string(typ))
	}
	slices.Sort(eventTypes)

	doc := schemaRoot{
		Schema: "https://json-schema.org/draft/2020-12/schema",
		ID:     "https://kalverra.com/pronto/socket-api.schema.json",
		Title:  "pronto socket API",
		Description: "NDJSON wire protocol for the pronto daemon. Requests and responses are " +
			"newline-delimited JSON over a Unix domain socket. Subscription connections stay open " +
			"after the acknowledgement and receive pushed event lines.",
		Type:  "object",
		OneOf: []refObj{{Ref: "#/$defs/request"}, {Ref: "#/$defs/response"}, {Ref: "#/$defs/pushedEvent"}},
		Defs: schemaDefs{
			Request: requestDef{
				Type:     "object",
				Required: []string{"id", "method"},
				Properties: requestProps{
					ID: propDef{
						Type:        "string",
						Description: "Caller-chosen correlation id echoed in the response.",
					},
					Method: enumProp{
						Type:        "string",
						Enum:        slices.Clone(server.Methods),
						Description: "Unsupported methods return an unsupported_method error.",
					},
					Params: paramsDef{
						Type: "object",
						Properties: paramsProps{
							Ref: propDef{Type: "string"},
							Subscriptions: subsProp{
								Type:  "array",
								Items: refObj{Ref: "#/$defs/subscription"},
							},
						},
					},
				},
			},
			Response: responseDef{
				Type:     "object",
				Required: []string{"id"},
				OneOf: []requireObj{
					{Required: []string{"result"}},
					{Required: []string{"error"}},
				},
				Properties: responseProps{
					ID:     propDef{Type: "string"},
					Result: propDef{Type: "object"},
					Error:  refObj{Ref: "#/$defs/error"},
				},
			},
			Error: errorDef{
				Type:     "object",
				Required: []string{"code", "message"},
				Properties: errorProps{
					Code: enumProp{
						Type: "string",
						Enum: slices.Clone(events.ValidCodes),
					},
					Message: propDef{Type: "string"},
				},
			},
			PushedEvent: pushedEventDef{
				Type:        "object",
				Description: "Emitted on subscription connections after the acknowledgement.",
				AllOf:       []refObj{{Ref: "#/$defs/event"}},
			},
			Event: eventDef{
				Type:     "object",
				Required: []string{"seq", "type", "ts"},
				Properties: eventProps{
					Seq: propDef{
						Type:        "integer",
						Description: "Monotonic per server lifetime.",
						Minimum:     new(1),
					},
					Type: enumProp{
						Type: "string",
						Enum: eventTypes,
					},
					TS: propDef{
						Type:   "string",
						Format: "date-time",
					},
					Repo: propDef{
						Type:        "string",
						Description: "owner/name; present on PR-scoped events.",
					},
					PR: propDef{
						Type:        "integer",
						Description: "PR number; present on PR-scoped events.",
					},
					Title: propDef{
						Type:        "string",
						Description: "PR title; present on PR-scoped events.",
					},
					Payload: propDef{
						Type:        "object",
						Description: "Type-specific details (review author/state, refresh outcome).",
					},
				},
			},
			Subscription: subscriptionDef{
				Type:        "object",
				Description: "Filters the event stream. Empty fields match anything; set fields must all match.",
				Properties: subscriptionProps{
					Types: typesProp{
						Type:  "array",
						Items: refObj{Ref: "#/$defs/event/properties/type"},
					},
					Repo: propDef{Type: "string"},
					PR: propDef{
						Type:    "integer",
						Minimum: new(1),
					},
				},
			},
			Snapshot: snapshotDef{
				Type:        "object",
				Description: "Result of the queue.snapshot and queue.refresh methods.",
				Required:    []string{"queue", "fetched_at", "age_seconds", "refreshing", "dropped_events"},
				Properties: snapshotProps{
					Queue: propDef{
						Type:        "object",
						Description: "Current PR review queue.",
					},
					FetchedAt: propDef{
						Type:        "string",
						Format:      "date-time",
						Description: "Timestamp of the last completed fetch.",
					},
					AgeSeconds: propDef{
						Type:        "number",
						Minimum:     new(0),
						Description: "Seconds since the last completed fetch.",
					},
					Refreshing: propDef{
						Type:        "boolean",
						Description: "True when a fetch is in progress.",
					},
					LastError: propDef{
						Type:        "string",
						Description: "Error message from the last failed fetch attempt, if any.",
					},
					DroppedEvents: propDef{
						Type:        "integer",
						Minimum:     new(0),
						Description: "Total events dropped by the event bus due to slow or saturated subscribers.",
					},
				},
			},
		},
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// GenerateSchema regenerates internal/events/schema.json under root.
func GenerateSchema(root string) error {
	raw, err := RenderSchema()
	if err != nil {
		return err
	}
	path := filepath.Join(root, "internal", "events", "schema.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

type refObj struct {
	Ref string `json:"$ref"`
}

type requireObj struct {
	Required []string `json:"required"`
}

type schemaRoot struct {
	Schema      string     `json:"$schema"`
	ID          string     `json:"$id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	OneOf       []refObj   `json:"oneOf"`
	Defs        schemaDefs `json:"$defs"`
}

type schemaDefs struct {
	Request      requestDef      `json:"request"`
	Response     responseDef     `json:"response"`
	Error        errorDef        `json:"error"`
	PushedEvent  pushedEventDef  `json:"pushedEvent"`
	Event        eventDef        `json:"event"`
	Subscription subscriptionDef `json:"subscription"`
	Snapshot     snapshotDef     `json:"snapshot"`
}

type propDef struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
	Minimum     *int   `json:"minimum,omitempty"`
}

type enumProp struct {
	Type        string   `json:"type"`
	Enum        []string `json:"enum"`
	Description string   `json:"description,omitempty"`
}

type requestDef struct {
	Type       string       `json:"type"`
	Required   []string     `json:"required"`
	Properties requestProps `json:"properties"`
}

type requestProps struct {
	ID     propDef   `json:"id"`
	Method enumProp  `json:"method"`
	Params paramsDef `json:"params"`
}

type paramsDef struct {
	Type       string      `json:"type"`
	Properties paramsProps `json:"properties"`
}

type paramsProps struct {
	Ref           propDef  `json:"ref"`
	Subscriptions subsProp `json:"subscriptions"`
}

type subsProp struct {
	Type  string `json:"type"`
	Items refObj `json:"items"`
}

type responseDef struct {
	Type       string        `json:"type"`
	Required   []string      `json:"required"`
	OneOf      []requireObj  `json:"oneOf"`
	Properties responseProps `json:"properties"`
}

type responseProps struct {
	ID     propDef `json:"id"`
	Result propDef `json:"result"`
	Error  refObj  `json:"error"`
}

type errorDef struct {
	Type       string     `json:"type"`
	Required   []string   `json:"required"`
	Properties errorProps `json:"properties"`
}

type errorProps struct {
	Code    enumProp `json:"code"`
	Message propDef  `json:"message"`
}

type pushedEventDef struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	AllOf       []refObj `json:"allOf"`
}

type eventDef struct {
	Type       string     `json:"type"`
	Required   []string   `json:"required"`
	Properties eventProps `json:"properties"`
}

type eventProps struct {
	Seq     propDef  `json:"seq"`
	Type    enumProp `json:"type"`
	TS      propDef  `json:"ts"`
	Repo    propDef  `json:"repo"`
	PR      propDef  `json:"pr"`
	Title   propDef  `json:"title"`
	Payload propDef  `json:"payload"`
}

type subscriptionDef struct {
	Type        string            `json:"type"`
	Description string            `json:"description"`
	Properties  subscriptionProps `json:"properties"`
}

type subscriptionProps struct {
	Types typesProp `json:"types"`
	Repo  propDef   `json:"repo"`
	PR    propDef   `json:"pr"`
}

type typesProp struct {
	Type  string `json:"type"`
	Items refObj `json:"items"`
}

type snapshotDef struct {
	Type        string        `json:"type"`
	Description string        `json:"description"`
	Required    []string      `json:"required"`
	Properties  snapshotProps `json:"properties"`
}

type snapshotProps struct {
	Queue         propDef `json:"queue"`
	FetchedAt     propDef `json:"fetched_at"`
	AgeSeconds    propDef `json:"age_seconds"`
	Refreshing    propDef `json:"refreshing"`
	LastError     propDef `json:"last_error"`
	DroppedEvents propDef `json:"dropped_events"`
}
