package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func sha(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func imageBinding(values []any) string {
	// The archive hashes ECMAScript JSON.stringify bytes; preserve HTML and Unicode line separators.
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		if s, ok := v.(string); ok {
			b.WriteByte('"')
			for _, r := range s {
				if r == '"' || r == '\\' {
					b.WriteByte('\\')
				}
				b.WriteRune(r)
			}
			b.WriteByte('"')
		} else {
			fmt.Fprint(&b, v)
		}
	}
	b.WriteByte(']')
	return sha(b.Bytes())
}
func readImage(ctx context.Context, c connection, args object) (*mcp.CallToolResult, error) {
	q := url.Values{}
	var parts []any
	for _, k := range []string{"accountId", "groupId", "messageId", "assetType", "assetKey"} {
		q.Set(k, args[k].(string))
		parts = append(parts, args[k])
	}
	index := intVal(args, "imageIndex", 0)
	q.Set("imageIndex", strconv.Itoa(index))
	resp, err := c.request(ctx, "GET", "/api/v1/messages/image", q, nil, "image/png,image/jpeg,image/webp,image/gif")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Weibogroup-Image-Binding") != imageBinding(append(parts, index)) {
		return nil, errors.New("IMAGE_RESOURCE_MISMATCH")
	}
	data, err := readLimit(resp.Body, 20<<20)
	if err != nil {
		return nil, err
	}
	media := http.DetectContentType(data)
	switch media {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return nil, errors.New("IMAGE_UNSUPPORTED_FORMAT")
	}
	digest := sha(data)
	if media != strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])) || resp.Header.Get("X-Weibogroup-Image-Sha256") != digest {
		return nil, errors.New("IMAGE_DIGEST_OR_TYPE_MISMATCH")
	}
	n, err := strconv.Atoi(resp.Header.Get("X-Weibogroup-Image-Count"))
	preview := resp.Header.Get("X-Weibogroup-Image-Preview")
	if err != nil || n <= index || n > 256 || (preview != "0" && preview != "1") {
		return nil, errors.New("IMAGE_INVALID_METADATA")
	}
	args["bytes"] = len(data)
	args["preview"] = preview == "1"
	args["imageIndex"] = index
	args["imageCount"] = n
	args["mediaType"] = media
	args["sourceSha256"] = digest
	result := textResult(args)
	result.Content = append(result.Content, &mcp.ImageContent{MIMEType: media, Data: data})
	return result, nil
}

func (a archive) download(ctx context.Context, c connection, id string) (*mcp.CallToolResult, error) {
	value, err := c.execute(ctx, a.ops["getExportJob"], object{"id": id})
	if err != nil {
		return nil, err
	}
	job, _ := value["job"].(object)
	if job["id"] != id {
		return nil, errors.New("EXPORT_JOB_MISMATCH")
	}
	if job["status"] != "ready" {
		return nil, fmt.Errorf("export status %v, check weibo_getExportJob", job["status"])
	}
	number, ok := job["size"].(json.Number)
	size, err := number.Int64()
	name, _ := job["filename"].(string)
	media, _ := job["contentType"].(string)
	if !ok || err != nil || size < 0 || size > 64<<20 || name == "" || media == "" {
		return nil, errors.New("invalid export metadata or size exceeds 64 MiB")
	}
	resp, err := c.request(ctx, "GET", "/api/v1/exports/"+url.PathEscape(id)+"/download", nil, nil, media)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	contentType := func(s string) string { return strings.ToLower(strings.TrimSpace(strings.Split(s, ";")[0])) }
	actual := contentType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != 200 || (actual != "application/octet-stream" && actual != contentType(media)) || (resp.ContentLength >= 0 && resp.ContentLength != size) {
		return nil, errors.New("export response type or size mismatch")
	}
	parent, err := filepath.Abs(a.config.exportDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "export-")
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(dir)
		}
	}()
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".csv", ".json", ".pdf", ".xlsx", ".zip":
	default:
		ext = ".bin"
	}
	path := filepath.Join(dir, "export"+ext)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, size+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || written != size {
		return nil, errors.New("export incomplete: byte count or write failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	done = true
	var coverage any
	if summary, ok := job["result"].(object); ok {
		coverage = pick(summary, "messageCount", "truncated", "failedAssetCount")
	}
	return textResult(object{"path": path, "filename": name, "contentType": media, "bytes": written, "coverage": coverage}), nil
}
