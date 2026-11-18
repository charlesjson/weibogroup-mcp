package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var httpClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
var apiErrorCode = regexp.MustCompile("^[A-Z][A-Z0-9_]{0,127}$")

func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing data")
	}
	return nil
}
func readLimit(r io.Reader, n int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, n+1))
	if err != nil {
		return nil, errors.New("upstream read failed")
	}
	if int64(len(data)) > n {
		return nil, errors.New("upstream response exceeds byte limit")
	}
	return data, nil
}
func (c connection) request(ctx context.Context, method, route string, q url.Values, body []byte, accept string) (*http.Response, error) {
	target := c.BaseURL + route
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("archive API unreachable")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var info struct{ Code string }
		data, _ := readLimit(resp.Body, 4096)
		_ = json.Unmarshal(data, &info)
		if !apiErrorCode.MatchString(info.Code) {
			info.Code = "UPSTREAM_HTTP_ERROR"
		}
		return nil, fmt.Errorf("%s (HTTP %d)", info.Code, resp.StatusCode)
	}
	return resp, nil
}

func (c connection) execute(ctx context.Context, op *operation, args object) (object, error) {
	route := op.Route
	query := url.Values{}
	body := object{}
	for _, b := range op.Bindings {
		v, ok := args[b.Name]
		if !ok {
			continue
		}
		switch b.Location {
		case "path":
			part := fmt.Sprint(v)
			if part == "." || part == ".." {
				return nil, errors.New("invalid path segment")
			}
			route = strings.ReplaceAll(route, "{"+b.Name+"}", url.PathEscape(part))
		case "query":
			if list, ok := v.([]any); ok {
				ss := make([]string, len(list))
				for i, x := range list {
					ss[i] = fmt.Sprint(x)
				}
				if b.Explode != nil && !*b.Explode {
					query.Set(b.Name, strings.Join(ss, ","))
				} else {
					query[b.Name] = ss
				}
			} else {
				query.Set(b.Name, fmt.Sprint(v))
			}
		case "body":
			body[b.Name] = v
		}
	}
	var payload []byte
	if op.Body {
		payload, _ = json.Marshal(body)
	}
	resp, err := c.request(ctx, op.Method, route, query, payload, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := readLimit(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}
	var value object
	if decode(data, &value) != nil || value == nil {
		return nil, errors.New("invalid upstream JSON object")
	}
	if op.output != nil && op.output.Validate(value) != nil {
		return nil, errors.New("upstream response does not match archive schema")
	}
	return value, nil
}
