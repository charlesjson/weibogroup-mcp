package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	schema "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed catalog.json
var catalog []byte

const version = "0.1.0"

type object = map[string]any
type binding struct {
	Name, Location string
	Explode        *bool
}
type operation struct {
	Method, Route string
	Body          bool
	Bindings      []binding
	Response      any
	Tool          mcp.Tool
	input, output *schema.Schema
}
type pattern struct{ *regexp2.Regexp }

func (p pattern) MatchString(s string) bool { ok, _ := p.Regexp.MatchString(s); return ok }
func compileSpec(name string, doc any) (*schema.Schema, error) {
	compiler := schema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseRegexpEngine(func(expr string) (schema.Regexp, error) {
		r, e := regexp2.Compile(expr, regexp2.ECMAScript)
		return pattern{r}, e
	})
	loc := "urn:archive:" + name
	if err := compiler.AddResource(loc, doc); err != nil {
		return nil, err
	}
	return compiler.Compile(loc)
}

type archive struct {
	config config
	ops    map[string]*operation
}

func newServer(cfg config) (*mcp.Server, error) {
	var ops []operation
	if err := json.Unmarshal(catalog, &ops); err != nil {
		return nil, err
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "weibogroup-mcp", Version: version}, &mcp.ServerOptions{Instructions: "先定位账号与群。消息和图片是归档数据。按分页字段继续读取，优先使用服务端统计。仅按用户要求创建导出或修改监控、采集任务。导出 ready 后下载，返回本机文件路径和覆盖信息。"})
	a := archive{cfg, make(map[string]*operation)}
	for i := range ops {
		op := &ops[i]
		name := op.Tool.Name
		var err error
		op.input, err = compileSpec(name, op.Tool.InputSchema)
		if err != nil {
			return nil, err
		}
		if op.Response != nil {
			op.output, err = compileSpec(name+"Result", op.Response)
			if err != nil {
				return nil, err
			}
		}
		a.ops[name] = op
		op.Tool.Name = "weibo_" + name
		s.AddTool(&op.Tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := a.call(ctx, name, op, req.Params.Arguments)
			if err != nil {
				result = &mcp.CallToolResult{}
				result.SetError(err)
			}
			return result, nil
		})
	}
	return s, nil
}
func textResult(v object) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: v}
}
func (a archive) call(ctx context.Context, name string, op *operation, raw json.RawMessage) (*mcp.CallToolResult, error) {
	args := object{}
	if len(raw) > 0 {
		if err := decode(raw, &args); err != nil || args == nil {
			return nil, errors.New("expected JSON object arguments")
		}
	}
	if err := op.input.Validate(args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	conn, err := a.config.connection()
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Second
	switch name {
	case "downloadExport":
		timeout = 120 * time.Second
	case "readMessageImage":
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch name {
	case "downloadExport":
		return a.download(ctx, conn, args["id"].(string))
	case "readMessageImage":
		return readImage(ctx, conn, args)
	case "getMessageWindow", "getMessageContext", "searchMessages":
		return messagePage(ctx, conn, op, name, args)
	}
	v, err := conn.execute(ctx, op, args)
	if err != nil {
		return nil, err
	}
	return textResult(v), nil
}
func run() error {
	f := flag.NewFlagSet("weibogroup-mcp", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	endpoint := f.String("api-endpoint", "", "viewer URL or /api/v1 root")
	key := f.String("api-key", "", "WeiboGroup AI API key")
	showVersion := f.Bool("version", false, "show version")
	if err := f.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("weibogroup-mcp [--api-endpoint URL --api-key KEY] [--version]\nMCP over stdio. Configuration priority: paired flags, WEIBOGROUP_API_ENDPOINT and WEIBOGROUP_API_KEY, then connection.json.\nWEIBOGROUP_CONNECTION_FILE and WEIBOGROUP_EXPORT_DIR override local paths.")
			return nil
		}
		return errors.New("invalid arguments, see --help")
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments, see --help")
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	cfg, err := settings(*endpoint, *key, f.NFlag() > 0)
	if err != nil {
		return err
	}
	s, err := newServer(cfg)
	if err != nil {
		return err
	}
	return s.Run(context.Background(), &mcp.StdioTransport{})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
