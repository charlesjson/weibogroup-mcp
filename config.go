package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type connection struct {
	BaseURL string `json:"baseUrl"`
	Key     string `json:"apiToken"`
}

type config struct {
	direct          *connection
	file, exportDir string
}

var tokenPattern = regexp.MustCompile("^wg_ai_[A-Za-z0-9_-]{43}$")

func (c connection) validated() (connection, error) {
	if !tokenPattern.MatchString(c.Key) {
		return connection{}, errors.New("invalid WeiboGroup AI API key")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return connection{}, errors.New("invalid archive endpoint")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return connection{}, errors.New("use HTTPS for remote archive endpoints")
	}
	c.BaseURL = strings.TrimSuffix(strings.TrimRight(u.String(), "/"), "/api/v1")
	return c, nil
}

func settings(endpoint, key string, flagsSet bool) (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, errors.New("user home directory unavailable")
	}
	c := config{file: os.Getenv("WEIBOGROUP_CONNECTION_FILE"), exportDir: os.Getenv("WEIBOGROUP_EXPORT_DIR")}
	if c.file == "" {
		c.file = filepath.Join(home, ".config", "dsh-weibogroup", "connection.json")
	}
	if c.exportDir == "" {
		c.exportDir = filepath.Join(home, "Downloads", "weibogroup")
	}
	if !flagsSet {
		var hasURL, hasKey bool
		endpoint, hasURL = os.LookupEnv("WEIBOGROUP_API_ENDPOINT")
		key, hasKey = os.LookupEnv("WEIBOGROUP_API_KEY")
		if !hasURL && !hasKey {
			return c, nil
		}
	}
	if endpoint == "" || key == "" {
		return c, errors.New("supply --api-endpoint and --api-key together, or set both WEIBOGROUP_API_ENDPOINT and WEIBOGROUP_API_KEY")
	}
	direct, err := (connection{endpoint, key}).validated()
	c.direct = &direct
	return c, err
}

func (c config) connection() (connection, error) {
	if c.direct != nil {
		return *c.direct, nil
	}
	f, err := os.Open(c.file)
	if err != nil {
		return connection{}, errors.New("cannot open connection.json")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return connection{}, errors.New("connection.json must be a private regular file (chmod 600)")
	}
	var conn connection
	if info.Size() > 65536 || json.NewDecoder(f).Decode(&conn) != nil {
		return conn, errors.New("invalid connection.json")
	}
	return conn.validated()
}
