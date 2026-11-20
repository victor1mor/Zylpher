package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

type schema struct {
	Ref        string `json:"$ref"`
	Type       string
	Format     string
	Const      json.RawMessage
	Items      *schema
	Properties map[string]schema
	Required   []string
}

type parameter struct {
	Ref      string `json:"$ref"`
	Name     string
	In       string
	Required bool
	Schema   schema
}

type message struct {
	Ref     string `json:"$ref"`
	Content map[string]struct{ Schema schema }
}

type operation struct {
	OperationID string
	Summary     string
	Parameters  []parameter
	RequestBody message
	Responses   map[string]message
}

type document struct {
	Paths      map[string]map[string]json.RawMessage
	Components struct {
		Schemas    map[string]schema
		Parameters map[string]parameter
		Responses  map[string]message
	}
}

type generator struct {
	doc        document
	models     strings.Builder
	operations strings.Builder
	seen       map[string]bool
}

func main() {
	log.SetFlags(0)
	if len(os.Args) != 3 {
		log.Fatal("usage: genclient openapi.json client.gen.go")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	source, err := generate(data)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[2], source, 0o644); err != nil {
		log.Fatal(err)
	}
}

func generate(data []byte) ([]byte, error) {
	g := generator{seen: make(map[string]bool)}
	if err := json.Unmarshal(data, &g.doc); err != nil {
		return nil, err
	}
	for _, path := range slices.Sorted(maps.Keys(g.doc.Paths)) {
		item := g.doc.Paths[path]
		var parameters []parameter
		if raw := item["parameters"]; raw != nil {
			if err := json.Unmarshal(raw, &parameters); err != nil {
				return nil, err
			}
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
			if raw := item[method]; raw != nil {
				var op operation
				if err := json.Unmarshal(raw, &op); err != nil {
					return nil, err
				}
				g.writeOperation(strings.ToUpper(method), path, append(slices.Clone(parameters), op.Parameters...), op)
			}
		}
	}
	header := "// Code generated from openapi.json by genclient. DO NOT EDIT.\n\npackage client\n\n"
	imports := `import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

`
	return format.Source([]byte(header + imports + g.models.String() + g.operations.String()))
}

// ponytail: handle the JSON objects, scalar parameters and single success
// response used by this contract. Extend the mappings if those shapes change.
func (g *generator) writeOperation(method, path string, parameters []parameter, op operation) {
	if op.OperationID == "" {
		log.Fatalf("%s %s: missing operationId", method, path)
	}
	name := goName(op.OperationID)
	args := []string{"ctx context.Context"}
	pathArgs := make(map[string]string)
	var query []parameter
	for _, param := range parameters {
		if param.Ref != "" {
			param = g.doc.Components.Parameters[strings.TrimPrefix(param.Ref, "#/components/parameters/")]
		}
		switch param.In {
		case "path":
			args = append(args, param.Name+" string")
			pathArgs["{"+param.Name+"}"] = "url.PathEscape(" + param.Name + ")"
		case "query":
			query = append(query, param)
		default:
			log.Fatalf("%s: unsupported parameter %q", op.OperationID, param.In)
		}
	}
	pathExpr := strconv.Quote(path)
	if len(pathArgs) > 0 {
		var ordered []string
		for _, segment := range strings.Split(path, "/") {
			if value, ok := pathArgs[segment]; ok {
				ordered = append(ordered, value)
				path = strings.Replace(path, segment, "%s", 1)
			}
		}
		pathExpr = fmt.Sprintf("fmt.Sprintf(%q, %s)", path, strings.Join(ordered, ", "))
	}

	var options []string
	var queryCode strings.Builder
	if len(query) != 0 {
		args = append(args, "params "+name+"Params")
		fmt.Fprintf(&g.models, "type %sParams struct {\n", name)
		queryCode.WriteString("query := url.Values{}\n")
		for _, param := range query {
			field := goName(param.Name)
			kind := g.goType(param.Schema, "")
			fmt.Fprintf(&g.models, "%s %s\n", field, kind)
			value := "params." + field
			condition := value + ` != ""`
			if kind == "bool" {
				condition, value = value, "fmt.Sprint("+value+")"
			} else if kind != "string" {
				log.Fatalf("%s: unsupported query type %q", op.OperationID, kind)
			}
			if !param.Required {
				fmt.Fprintf(&queryCode, "if %s {\n", condition)
			}
			fmt.Fprintf(&queryCode, "query.Set(%q, %s)\n", param.Name, value)
			if !param.Required {
				queryCode.WriteString("}\n")
			}
		}
		g.models.WriteString("}\n\n")
		options = append(options, "query: query")
	}
	input, jsonInput := op.RequestBody.Content["application/json"]
	if jsonInput {
		args = append(args, "body "+g.goType(input.Schema, name+"Request"))
	} else if hasContent(op.RequestBody, "*/*") || hasContent(op.RequestBody, "application/octet-stream") {
		args = append(args, "contentType string", "body io.Reader")
		options = append(options, "contentType: contentType", "body: body")
	} else if len(op.RequestBody.Content) != 0 {
		log.Fatalf("%s: unsupported request body", op.OperationID)
	}

	var status string
	var response message
	for code, entry := range op.Responses {
		if n, err := strconv.Atoi(code); err == nil && n >= 200 && n < 300 {
			if status != "" {
				log.Fatalf("%s: multiple success responses", op.OperationID)
			}
			status, response = code, entry
		}
	}
	if status == "" {
		log.Fatalf("%s: missing success response", op.OperationID)
	}
	if response.Ref != "" {
		response = g.doc.Components.Responses[strings.TrimPrefix(response.Ref, "#/components/responses/")]
	}
	call := fmt.Sprintf("c.request(ctx, %q, %s, %s, requestOptions{%s})", method, pathExpr, status, strings.Join(options, ", "))
	result := "error"
	switch {
	case hasContent(response, "application/json"):
		kind := g.goType(response.Content["application/json"].Schema, name+"Response")
		result = "(" + kind + ", error)"
		if jsonInput {
			call = fmt.Sprintf("writeJSON[%s](ctx, c, %q, %s, %s, body)", kind, method, pathExpr, status)
		} else {
			call = "readJSON[" + kind + "](" + call + ")"
		}
	case hasContent(response, "text/plain"):
		call, result = "readText("+call+")", "(string, error)"
	case hasContent(response, "*/*") || hasContent(response, "application/octet-stream"):
		result = "(*http.Response, error)"
		op.Summary += ". Feche o corpo da resposta após a leitura"
	case status == "204":
		call = "closeResponse(" + call + ")"
	default:
		log.Fatalf("%s: unsupported success response %s", op.OperationID, status)
	}
	fmt.Fprintf(&g.operations, "// %s %s.\nfunc (c *Client) %s(%s) %s {\n%sreturn %s\n}\n\n",
		name, strings.Join(strings.Fields(op.Summary), " "), name, strings.Join(args, ", "), result, queryCode.String(), call)
}

func hasContent(msg message, kind string) bool {
	_, ok := msg.Content[kind]
	return ok
}

func (g *generator) goType(value schema, name string) string {
	if value.Ref != "" {
		name = strings.TrimPrefix(value.Ref, "#/components/schemas/")
		return g.goType(g.doc.Components.Schemas[name], name)
	}
	switch value.Type {
	case "string":
		if value.Format == "date-time" {
			return "time.Time"
		}
		return "string"
	case "integer":
		return "int64"
	case "boolean":
		return "bool"
	case "array":
		return "[]" + g.goType(*value.Items, name)
	case "object":
		if name == "" {
			log.Fatal("unnamed object in client schema")
		}
		if !g.seen[name] {
			g.seen[name] = true
			var fields strings.Builder
			for _, key := range slices.Sorted(maps.Keys(value.Properties)) {
				kind := g.goType(value.Properties[key], name+goName(key))
				tag := key
				if !slices.Contains(value.Required, key) {
					kind, tag = "*"+kind, tag+",omitempty"
				}
				fmt.Fprintf(&fields, "%s %s `json:%q`\n", goName(key), kind, tag)
			}
			fmt.Fprintf(&g.models, "type %s struct {\n%s}\n\n", name, fields.String())
		}
		return name
	default:
		var constant string
		if json.Unmarshal(value.Const, &constant) == nil {
			return "string"
		}
		log.Fatalf("unsupported schema %q for %s", value.Type, name)
		return ""
	}
}

func goName(value string) string {
	var result strings.Builder
	for _, part := range strings.Split(value, "_") {
		switch part {
		case "id":
			result.WriteString("ID")
		default:
			result.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return result.String()
}
