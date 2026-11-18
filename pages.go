package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const pageBytes = 24000

func pick(v object, keys ...string) object {
	out := object{}
	for _, k := range keys {
		if x, ok := v[k]; ok {
			out[k] = x
		}
	}
	return out
}
func intVal(v object, k string, fallback int) int {
	if n, ok := v[k].(json.Number); ok {
		x, e := n.Float64()
		if e == nil {
			return int(x)
		}
	}
	if n, ok := v[k].(int); ok {
		return n
	}
	return fallback
}
func project(messages []any, full bool) []any {
	result := make([]any, 0, len(messages))
	for _, item := range messages {
		if full {
			result = append(result, item)
			continue
		}
		m := maps.Clone(item.(object))
		for _, key := range []string{"senderAvatarUrl", "senderProfileUrl", "rawJson", "search"} {
			delete(m, key)
		}
		if quote, ok := m["quote"].(object); ok {
			m["quote"] = pick(quote, "messageId", "sender", "text")
		}
		if assets, ok := m["assets"].([]any); ok {
			out := make([]any, 0, len(assets))
			for _, a := range assets {
				out = append(out, pick(a.(object), "accountId", "groupId", "messageId", "assetType", "assetKey", "url", "title"))
			}
			m["assets"] = out
		}
		result = append(result, m)
	}
	return result
}
func digest(v any) string { b, _ := json.Marshal(v); return sha(b)[:24] }

type pageCursor struct {
	Scope, Snapshot string
	Offset          int
}

func messagePage(ctx context.Context, c connection, op *operation, name string, args object) (*mcp.CallToolResult, error) {
	full := args["detail"] == "full" || args["raw"] == "1"
	presentation := object{"maxPageBytes": pageBytes, "detail": "full", "messageTextTruncated": false}
	if !full {
		presentation["detail"] = "compact"
		presentation["omittedFields"] = []string{"senderAvatarUrl", "senderProfileUrl", "rawJson", "search", "assets.metadata", "quote.nested", "quote.quotedContent"}
		presentation["detailTool"] = "getMessagesBatch"
	}
	limit := intVal(args, "limit", 10)
	if args["beforeMessageId"] != nil && args["afterMessageId"] != nil {
		return nil, errors.New("beforeMessageId and afterMessageId are mutually exclusive")
	}
	if name == "getMessageContext" {
		for _, k := range []string{"before", "after"} {
			if args[k] == nil {
				args[k] = 5
			}
		}
		if args["raw"] == nil {
			args["raw"] = "0"
		}
		return contextPage(ctx, c, op, args, full, presentation)
	}
	for {
		args["limit"] = limit
		v, err := c.execute(ctx, op, args)
		if err != nil {
			return nil, err
		}
		messages := v["messages"].([]any)
		v["messages"] = project(messages, full)
		v["presentation"] = presentation
		if name == "searchMessages" {
			p := v["pagination"].(object)
			more := v["nextCursor"] != nil
			p["complete"] = !more && p["complete"] != false && p["truncated"] != true
			p["truncated"] = more || p["truncated"] == true
			p["returnedMessageCount"] = len(messages)
		} else {
			dir, field, index := "older", "beforeMessageId", 0
			if args["afterMessageId"] != nil {
				dir, field, index = "newer", "afterMessageId", len(messages)-1
			}
			p := object{"direction": dir, "returnedMessageCount": len(messages), "hasMore": nil, "continuation": nil}
			if len(messages) < limit {
				p["hasMore"] = false
			}
			if len(messages) > 0 {
				next := pick(args, "accountId", "groupId", "senderUid", "detail", "raw", "limit")
				next[field] = messages[index].(object)["messageId"]
				p["continuation"] = next
			}
			v["pagination"] = p
		}
		r := textResult(v)
		if len(r.Content[0].(*mcp.TextContent).Text) <= pageBytes {
			return r, nil
		}
		if limit == 1 || len(messages) == 0 {
			return nil, tooLarge(messages)
		}
		limit = max(1, limit/2)
	}
}
func tooLarge(page []any) error {
	var key object
	if len(page) > 0 {
		key = pick(page[0].(object), "accountId", "groupId", "messageId")
	}
	b, _ := json.Marshal(object{"code": "MESSAGE_TOO_LARGE", "messageKey": key, "detailTool": "weibo_getMessagesBatch"})
	return errors.New(string(b))
}
func contextPage(ctx context.Context, c connection, op *operation, args object, full bool, presentation object) (*mcp.CallToolResult, error) {
	scope := digest([]any{pick(args, "accountId", "groupId", "messageId", "raw"), strconv.Itoa(intVal(args, "before", 5)), strconv.Itoa(intVal(args, "after", 5)), full})
	cursor := pageCursor{Scope: scope}
	encoded, continuing := args["pageCursor"].(string)
	if continuing {
		b, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.Scope != scope || cursor.Offset <= 0 {
			return nil, errors.New("invalid pageCursor for this window")
		}
	}
	v, err := c.execute(ctx, op, args)
	if err != nil {
		return nil, err
	}
	all := v["messages"].([]any)
	snapshot := digest(all)
	if continuing && (cursor.Snapshot != snapshot || cursor.Offset >= len(all)) {
		return nil, errors.New("CONTEXT_PAGE_STALE: restart at the first page")
	}
	start := cursor.Offset
	for count := min(intVal(args, "pageSize", 10), len(all)-start); count >= 0; count-- {
		end := start + count
		page := all[start:end]
		more := end < len(all)
		included := false
		for _, m := range page {
			if m.(object)["messageId"] == args["messageId"] {
				included = true
			}
		}
		v["messages"] = project(page, full)
		v["presentation"] = presentation
		v["nextPageCursor"] = nil
		if more {
			b, _ := json.Marshal(pageCursor{scope, snapshot, end})
			v["nextPageCursor"] = base64.RawURLEncoding.EncodeToString(b)
		}
		v["pagination"] = object{"scope": "requested-context-window", "complete": !more, "truncated": more, "returnedMessageCount": count, "totalMessageCount": len(all), "targetIncluded": included}
		r := textResult(v)
		if len(r.Content[0].(*mcp.TextContent).Text) <= pageBytes {
			return r, nil
		}
		if count <= 1 {
			return nil, tooLarge(page)
		}
	}
	return nil, fmt.Errorf("invalid context window")
}
