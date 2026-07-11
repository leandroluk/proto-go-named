// Command protoc-gen-go-named wraps the standard protoc-gen-go, letting
// individual proto fields override the Go identifier generated for them via
// the (golang.field_name) extension, e.g.:
//
//	string user_id = 1 [(golang.field_name) = "UserID"];
//
// It works by running the real protoc-gen-go unmodified and then rewriting
// the resulting Go source with go/ast: the wire format, JSON name, and proto
// reflection are all untouched, only the Go struct field and its getter are
// renamed.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	golangpb "github.com/leandroluk/proto-go-named/gen/golang"
)

// realPlugin is the protoc-gen-go binary this wrapper delegates the actual
// code generation to. Override with PROTOC_GEN_GO_NAMED_REAL for testing or
// to pin a specific binary.
const realPluginEnv = "PROTOC_GEN_GO_NAMED_REAL"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "protoc-gen-go-named:", err)
		os.Exit(1)
	}
}

func run() error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}

	req := &pluginpb.CodeGeneratorRequest{}
	if err := proto.Unmarshal(input, req); err != nil {
		return fmt.Errorf("unmarshal request: %w", err)
	}

	renames, err := collectRenames(req)
	if err != nil {
		return fmt.Errorf("collect renames: %w", err)
	}

	respBytes, err := runRealPlugin(input)
	if err != nil {
		return fmt.Errorf("run real protoc-gen-go: %w", err)
	}

	resp := &pluginpb.CodeGeneratorResponse{}
	if err := proto.Unmarshal(respBytes, resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	for _, f := range resp.File {
		byStruct, ok := renames[f.GetName()]
		if !ok {
			continue
		}
		renamed, err := renameFields(f.GetContent(), byStruct)
		if err != nil {
			return fmt.Errorf("rename fields in %s: %w", f.GetName(), err)
		}
		f.Content = proto.String(renamed)
	}

	out, err := proto.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	_, err = os.Stdout.Write(out)
	return err
}

// structRenames maps old Go field identifier -> desired Go field identifier,
// for every field of one message that carries the extension.
type structRenames map[string]string

// collectRenames inspects the request with protogen (the same library
// protoc-gen-go itself uses for naming) to learn, per generated file and
// struct, which fields want a custom Go name.
func collectRenames(req *pluginpb.CodeGeneratorRequest) (map[string]map[string]structRenames, error) {
	plugin, err := protogen.Options{}.New(req)
	if err != nil {
		return nil, err
	}

	out := map[string]map[string]structRenames{}
	for _, file := range plugin.Files {
		if !file.Generate {
			continue
		}
		var walk func(messages []*protogen.Message)
		walk = func(messages []*protogen.Message) {
			for _, msg := range messages {
				structName := msg.GoIdent.GoName
				for _, field := range msg.Fields {
					opts, ok := field.Desc.Options().(*descriptorpb.FieldOptions)
					if !ok || opts == nil {
						continue
					}
					newName := proto.GetExtension(opts, golangpb.E_FieldName).(string)
					if newName == "" || newName == field.GoName {
						continue
					}
					filename := file.GeneratedFilenamePrefix + ".pb.go"
					if out[filename] == nil {
						out[filename] = map[string]structRenames{}
					}
					if out[filename][structName] == nil {
						out[filename][structName] = structRenames{}
					}
					out[filename][structName][field.GoName] = newName
				}
				walk(msg.Messages) // nested messages
			}
		}
		walk(file.Messages)
	}
	return out, nil
}

func runRealPlugin(request []byte) ([]byte, error) {
	bin := os.Getenv(realPluginEnv)
	if bin == "" {
		bin = "protoc-gen-go"
	}
	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", bin, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// renameFields rewrites struct field declarations and their "Get<Field>"
// accessors from old to new Go identifiers, scoped to the owning struct so
// that two different messages reusing the same field name never collide.
func renameFields(src string, byStruct map[string]structRenames) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return "", err
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			renames, ok := byStruct[ts.Name.Name]
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for i, name := range field.Names {
					if newName, ok := renames[name.Name]; ok {
						field.Names[i].Name = newName
					}
				}
			}
		}
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		structName, recvName := receiverInfo(fn.Recv.List[0])
		renames, ok := byStruct[structName]
		if !ok {
			continue
		}
		for old, new := range renames {
			if fn.Name.Name == "Get"+old {
				fn.Name.Name = "Get" + new
			}
		}
		if recvName == "" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != recvName {
				return true
			}
			if newName, ok := renames[sel.Sel.Name]; ok {
				sel.Sel.Name = newName
			}
			return true
		})
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func receiverInfo(field *ast.Field) (structName, recvName string) {
	if len(field.Names) == 1 {
		recvName = field.Names[0].Name
	}
	expr := field.Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		structName = id.Name
	}
	return structName, recvName
}
