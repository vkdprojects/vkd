// Package mcp serves vkd as a Model Context Protocol server over stdio: newline-delimited JSON-RPC 2.0.
// Protocol version 2025-06-18; older clients that ask for another version get the one we speak and decide.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/vkdprojects/vkd/internal/answers"
	"github.com/vkdprojects/vkd/internal/engine"
	"github.com/vkdprojects/vkd/internal/schema"
)

// ProtocolVersion is the MCP revision implemented.
const ProtocolVersion = "2025-06-18"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
)

type server struct {
	eng     *engine.Engine
	version string
	mu      sync.Mutex
	out     io.Writer
}

// Serve reads requests from in and writes responses to out until in is closed or ctx is done.
func Serve(ctx context.Context, in io.Reader, out io.Writer, eng *engine.Engine, version string) error {
	s := &server{eng: eng, version: version, out: out}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		s.handle(line)
	}
	return sc.Err()
}

func (s *server) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
}

func (s *server) fail(id json.RawMessage, code int, msg string) {
	if id == nil {
		id = json.RawMessage("null")
	}
	s.send(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *server) handle(line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.fail(nil, errParse, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.fail(req.ID, errInvalidRequest, "not a JSON-RPC 2.0 request")
		return
	}
	// A request without an id is a notification: never answer it.
	isNotification := req.ID == nil
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "vkd", "version": s.version},
			"instructions": "vkd scaffolds and updates projects. Workflow: vkd_list (blueprints and their questions), " +
				"vkd_schema (input shape), vkd_plan (dry run: the files that would be written), then vkd_create. " +
				"Pass every answer explicitly; vkd never prompts here.",
		}})
	case "ping":
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.tools()}})
	case "tools/call":
		s.call(req)
	default:
		if isNotification {
			return // notifications/initialized, notifications/cancelled, ...
		}
		s.fail(req.ID, errMethodNotFound, "method not found: "+req.Method)
	}
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *server) tools() []tool {
	createInput := schema.Create(s.eng.Registry)
	dirProp := map[string]any{"type": "string", "description": "Project directory"}
	return []tool{
		{"vkd_list", "List blueprints with kind, summary, requirements and questions.", obj(nil, nil)},
		{"vkd_schema", "JSON Schema of the create input (answers).", obj(nil, nil)},
		{"vkd_plan", "Dry run: resolve blueprints and list the files vkd_create would write. Writes nothing. Input is the create answers (see vkd_schema).", createInput},
		{"vkd_create", "Create a project in `dir` (default: ./<name>). Input is the create answers plus optional dir. Never overwrites a non-empty directory.",
			withProps(createInput, map[string]any{"dir": map[string]any{"type": "string", "description": "Target directory (default ./<name>)"}})},
		{"vkd_update", "Re-render a vkd project with the current blueprints and versions and three-way merge it. Optional add: blueprint names to add first.",
			obj(map[string]any{
				"dir":     dirProp,
				"add":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Blueprints to add"},
				"dry_run": map[string]any{"type": "boolean", "description": "Report what would change, write nothing"},
			}, nil)},
	}
}

func obj(props map[string]any, required []any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if required != nil {
		m["required"] = required
	}
	return m
}

func withProps(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	props := map[string]any{}
	for k, v := range base["properties"].(map[string]any) {
		props[k] = v
	}
	for k, v := range extra {
		props[k] = v
	}
	out["properties"] = props
	return out
}

func (s *server) call(req request) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		s.fail(req.ID, errInvalidParams, "tools/call needs params.name")
		return
	}
	text, isErr := s.run(p.Name, p.Arguments)
	if text == "" && isErr {
		s.fail(req.ID, errInvalidParams, "unknown tool: "+p.Name)
		return
	}
	s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isErr,
	}})
}

// Each tool decodes into its own struct, so what a tool accepts is exactly what its inputSchema advertises.
type (
	planArgs   struct{ answers.Answers }
	createArgs struct {
		answers.Answers
		Dir string `json:"dir"`
	}
	updateArgs struct {
		Dir    string   `json:"dir"`
		Add    []string `json:"add"`
		DryRun bool     `json:"dry_run"`
	}
)

func (s *server) run(name string, raw json.RawMessage) (text string, isErr bool) {
	pretty := func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
	switch name {
	case "vkd_list":
		var items []map[string]any
		for _, b := range s.eng.Registry.All() {
			items = append(items, map[string]any{"name": b.Name, "kind": b.Kind, "summary": b.Summary,
				"requires": b.Requires, "conflicts": b.Conflicts, "auto_when": b.When, "questions": b.Questions})
		}
		return pretty(map[string]any{"ok": true, "blueprints": items}), false
	case "vkd_schema":
		return pretty(schema.Create(s.eng.Registry)), false
	case "vkd_plan":
		var a planArgs
		if err := decode(raw, &a); err != nil {
			return pretty(errResult(err)), true
		}
		r := s.eng.PlanResult(&a.Answers)
		return pretty(r), !r.OK
	case "vkd_create":
		var a createArgs
		if err := decode(raw, &a); err != nil {
			return pretty(errResult(err)), true
		}
		dir := a.Dir
		if dir == "" {
			dir = a.Name
		}
		// MCP clients time out on installs, so create never installs and never prompts.
		r := s.eng.Create(dir, &a.Answers, engine.CreateOpts{Git: true})
		return pretty(r), !r.OK
	case "vkd_update":
		var a updateArgs
		if err := decode(raw, &a); err != nil {
			return pretty(errResult(err)), true
		}
		dir := a.Dir
		if dir == "" {
			dir = "."
		}
		r := s.eng.Update(dir, a.Add, a.DryRun)
		return pretty(r), !r.OK
	}
	return "", true
}

func errResult(err error) *engine.Result {
	return engine.Fail(err)
}

// decode parses tool arguments strictly: an unknown field is an error, so a typo does not silently drop an answer.
func decode(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
