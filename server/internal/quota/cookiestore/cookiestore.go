// Package cookiestore reads browsers' own cookie databases on this machine,
// read-only and without taking the browser's lock. It is the one place on
// the quota side that opens SQLite (scripts/check-deps.py names it): it
// knows the Chromium and Firefox table layouts and nothing about providers,
// sessions or decryption. It never writes and never opens Ant's database.
package cookiestore

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNoCookie means the store holds no matching cookie.
var ErrNoCookie = errors.New("no cookie found")

// Cookie is one row as the browser keeps it. Access is microseconds since
// 1601-01-01 UTC for every browser, so rows of different browsers compare.
// Encrypted is set only for Chromium rows whose Value is empty.
type Cookie struct {
	Host, Name, Value string
	Encrypted         []byte
	Access            int64
}

// unixToWindowsMicros is the distance between 1970 and 1601 in µs.
const unixToWindowsMicros = 11644473600 * 1_000_000

func open(path string) (*sql.DB, error) {
	// immutable: read the file as it is, without taking the browser's lock
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	return sql.Open("sqlite", u.String())
}

func likeSubdomains(domain string) string { return "%." + strings.TrimPrefix(domain, ".") }

// chromiumVersion is the cookie database version; from 24 the decrypted
// value carries a host hash prefix.
func chromiumVersion(ctx context.Context, db *sql.DB) int {
	var raw string
	if db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'version'`).Scan(&raw) != nil {
		return 0
	}
	v, _ := strconv.Atoi(raw)
	return v
}

// ChromiumLatest returns the most recently used cookie called name (any name
// when "") on domain or its subdomains, with the database version.
func ChromiumLatest(ctx context.Context, file, domain, name string) (Cookie, int, error) {
	db, err := open(file)
	if err != nil {
		return Cookie{}, 0, err
	}
	defer db.Close()
	version := chromiumVersion(ctx, db)
	var c Cookie
	err = db.QueryRowContext(ctx, `SELECT host_key, name, value, encrypted_value, last_access_utc FROM cookies
		WHERE (? = '' OR name = ?) AND (host_key = ? OR host_key LIKE ?) ORDER BY last_access_utc DESC LIMIT 1`,
		name, name, domain, likeSubdomains(domain)).Scan(&c.Host, &c.Name, &c.Value, &c.Encrypted, &c.Access)
	if errors.Is(err, sql.ErrNoRows) {
		return Cookie{}, 0, ErrNoCookie
	}
	if err != nil {
		return Cookie{}, 0, err
	}
	// Chromium already counts from 1601.
	return c, version, nil
}

// ChromiumSite returns every cookie on domain or its subdomains, with the
// database version.
func ChromiumSite(ctx context.Context, file, domain string) ([]Cookie, int, error) {
	db, err := open(file)
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	version := chromiumVersion(ctx, db)
	rows, err := db.QueryContext(ctx, `SELECT host_key, name, value, encrypted_value, last_access_utc FROM cookies
		WHERE host_key = ? OR host_key LIKE ? ORDER BY length(host_key), name`, domain, likeSubdomains(domain))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Cookie
	for rows.Next() {
		var c Cookie
		if err := rows.Scan(&c.Host, &c.Name, &c.Value, &c.Encrypted, &c.Access); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, version, rows.Err()
}

// FirefoxLatest returns the most recently used non-empty cookie called name
// (any name when "") on domain or its subdomains.
func FirefoxLatest(ctx context.Context, file, domain, name string) (Cookie, error) {
	db, err := open(file)
	if err != nil {
		return Cookie{}, err
	}
	defer db.Close()
	var c Cookie
	err = db.QueryRowContext(ctx, `SELECT host, name, value, lastAccessed FROM moz_cookies
		WHERE (? = '' OR name = ?) AND value != '' AND (host = ? OR host LIKE ?) ORDER BY lastAccessed DESC LIMIT 1`,
		name, name, domain, likeSubdomains(domain)).Scan(&c.Host, &c.Name, &c.Value, &c.Access)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && c.Value == "") {
		return Cookie{}, ErrNoCookie
	}
	if err != nil {
		return Cookie{}, err
	}
	c.Access += unixToWindowsMicros // Firefox counts from 1970
	return c, nil
}

// FirefoxSite returns every cookie on domain or its subdomains.
func FirefoxSite(ctx context.Context, file, domain string) ([]Cookie, error) {
	db, err := open(file)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT host, name, value, lastAccessed FROM moz_cookies WHERE host = ? OR host LIKE ?`,
		domain, likeSubdomains(domain))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cookie
	for rows.Next() {
		var c Cookie
		if err := rows.Scan(&c.Host, &c.Name, &c.Value, &c.Access); err != nil {
			return nil, err
		}
		c.Access += unixToWindowsMicros
		out = append(out, c)
	}
	return out, rows.Err()
}
