package frontmatter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Document struct {
	Fields map[string]any
	Body   []byte
}

func Parse(markdown []byte) (Document, error) {
	if !bytes.HasPrefix(markdown, []byte("---\n")) {
		return Document{Body: append([]byte(nil), markdown...)}, nil
	}
	rest := markdown[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---\n"))
	bodyStart := end + len("\n---\n")
	if end < 0 {
		if bytes.HasSuffix(rest, []byte("\n---")) {
			end = len(rest) - len("\n---")
			bodyStart = len(rest)
		} else {
			return Document{}, fmt.Errorf("frontmatter close delimiter not found")
		}
	}
	meta := rest[:end]
	body := rest[bodyStart:]
	fields := map[string]any{}
	if len(meta) > 0 {
		if err := yaml.Unmarshal(meta, &fields); err != nil {
			return Document{}, err
		}
	}
	return Document{Fields: fields, Body: append([]byte(nil), body...)}, nil
}

func Render(doc Document) ([]byte, error) {
	if len(doc.Fields) == 0 {
		return append([]byte(nil), doc.Body...), nil
	}
	keys := make([]string, 0, len(doc.Fields))
	for k := range doc.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b bytes.Buffer
	b.WriteString("---\n")
	for i, key := range keys {
		if i > 0 {
			// no-op; keys are written one per line.
		}
		if err := writeYAMLField(&b, key, doc.Fields[key]); err != nil {
			return nil, err
		}
	}
	b.WriteString("---\n")
	b.Write(doc.Body)
	return b.Bytes(), nil
}

func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func WithTimestamp(doc Document, now time.Time) Document {
	clone := Document{Body: append([]byte(nil), doc.Body...)}
	if len(doc.Fields) > 0 {
		clone.Fields = make(map[string]any, len(doc.Fields)+1)
		for k, v := range doc.Fields {
			clone.Fields[k] = v
		}
	} else {
		clone.Fields = make(map[string]any, 1)
	}
	clone.Fields["timestamp"] = now.UTC().Format(time.RFC3339Nano)
	return clone
}

func writeYAMLField(b *bytes.Buffer, key string, value any) error {
	rendered, err := renderYAMLValue(value)
	if err != nil {
		return fmt.Errorf("render %q: %w", key, err)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) == 1 && !isCompositeValue(value) {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(lines[0])
		b.WriteByte('\n')
		return nil
	}
	b.WriteString(key)
	b.WriteString(":\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return nil
}

func isCompositeValue(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Array:
		return true
	case reflect.Slice, reflect.Map:
		return !rv.IsNil()
	default:
		return false
	}
}

func renderYAMLValue(value any) (string, error) {
	switch v := value.(type) {
	case string, bool:
		return marshalScalar(v)
	case int, int8, int16, int32, int64:
		return strconv.FormatInt(asInt64(v), 10), nil
	case uint, uint8, uint16, uint32, uint64:
		return strconv.FormatUint(asUint64(v), 10), nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case []string:
		return marshalScalar(v)
	case []any:
		return marshalScalar(v)
	default:
		return marshalScalar(v)
	}
}

func marshalScalar(value any) (string, error) {
	out, err := yaml.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int8:
		return int64(n)
	case int16:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func asUint64(v any) uint64 {
	switch n := v.(type) {
	case uint:
		return uint64(n)
	case uint8:
		return uint64(n)
	case uint16:
		return uint64(n)
	case uint32:
		return uint64(n)
	case uint64:
		return n
	}
	return 0
}
