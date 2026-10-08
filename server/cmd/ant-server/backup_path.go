package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func backupSourcePath(dsn string) (string, error) {
	if !strings.HasPrefix(dsn, "file:") {
		return dsn, nil
	}
	path := strings.TrimPrefix(dsn, "file:")
	path = strings.SplitN(path, "?", 2)[0]
	if strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "///") {
		host, rest, found := strings.Cut(strings.TrimPrefix(path, "//"), "/")
		if !found || (host != "" && host != "localhost") {
			return "", errors.New("backup source must use a local SQLite file path")
		}
		path = "/" + rest
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", fmt.Errorf("decode backup source database path: %w", err)
	}
	if decoded == "" || decoded == ":memory:" {
		return "", errors.New("backup source must be a file-backed SQLite database")
	}
	return decoded, nil
}
