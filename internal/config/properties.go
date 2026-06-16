package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

const utf8BOM = "\xef\xbb\xbf"

// Parse reads a simple properties stream and returns key/value pairs.
//
// Grammar:
//   - One entry per line.
//   - Lines whose first non-whitespace character is '#' are comments.
//   - Blank lines are ignored.
//   - The first '=' on a line separates key and value; subsequent '=' stay in value.
//   - Key and value are trimmed of surrounding whitespace.
//   - Empty value is allowed; empty key is an error.
//   - Duplicate keys: last occurrence wins.
//   - A UTF-8 BOM at the start of the stream is stripped.
//   - CR/LF and LF line endings are both supported.
func Parse(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lineNo := 0
	first := true
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, utf8BOM)
			first = false
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		idx := strings.IndexByte(trimmed, '=')
		if idx < 0 {
			return nil, fmt.Errorf("properties: line %d: missing '=' separator: %q", lineNo, line)
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])
		if key == "" {
			return nil, fmt.Errorf("properties: line %d: empty key", lineNo)
		}
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("properties: read error: %w", err)
	}
	return out, nil
}

// ParseFile reads a properties file from disk and returns its key/value map.
func ParseFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("properties: open %s: %w", path, err)
	}
	defer f.Close()
	return Parse(f)
}
