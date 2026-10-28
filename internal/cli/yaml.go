package cli

import (
	"math"
	"strconv"
	"strings"

	"github.com/hakastein/kartograph/internal/ladybug"
)

// pair is one key of a mapping. A key that comes from data (a field of a graph value) is quoted; a key kartograph
// names is bare.
type pair struct {
	key      string
	value    any
	fromData bool
}

// mapping is a YAML mapping in key order. A value is nil, bool, int64, uint64, float64, string, []any, mapping or
// []ladybug.Field.
type mapping []pair

// render prints a document: mappings in block style, every item of a non-empty list on its own line in flow style,
// every string double-quoted.
func render(doc mapping) string {
	var b strings.Builder
	writeBlock(&b, doc, "")
	return b.String()
}

func writeBlock(b *strings.Builder, m mapping, indent string) {
	for _, p := range m {
		b.WriteString(indent)
		writeKey(b, p)
		b.WriteByte(':')
		switch v := p.value.(type) {
		case mapping:
			if len(v) > 0 {
				b.WriteByte('\n')
				writeBlock(b, v, indent+"  ")
				continue
			}
		case []any:
			if len(v) > 0 {
				b.WriteByte('\n')
				for _, item := range v {
					b.WriteString(indent + "  - ")
					writeFlow(b, item)
					b.WriteByte('\n')
				}
				continue
			}
		}
		b.WriteByte(' ')
		writeFlow(b, p.value)
		b.WriteByte('\n')
	}
}

func writeKey(b *strings.Builder, p pair) {
	if p.fromData {
		b.WriteString(strconv.Quote(p.key))
		return
	}
	b.WriteString(p.key)
}

func writeFlow(b *strings.Builder, v any) {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case uint64:
		b.WriteString(strconv.FormatUint(v, 10))
	case float64:
		writeFloat(b, v)
	case string:
		b.WriteString(strconv.Quote(v))
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			writeFlow(b, item)
		}
		b.WriteByte(']')
	case []ladybug.Field:
		m := make(mapping, len(v))
		for i, f := range v {
			m[i] = pair{key: f.Name, value: f.Value, fromData: true}
		}
		writeFlow(b, m)
	case mapping:
		b.WriteByte('{')
		for i, p := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			writeKey(b, p)
			b.WriteString(": ")
			writeFlow(b, p.value)
		}
		b.WriteByte('}')
	default:
		panic("render: a value of an unknown type")
	}
}

func writeFloat(b *strings.Builder, f float64) {
	switch {
	case math.IsNaN(f):
		b.WriteString(".nan")
	case math.IsInf(f, 1):
		b.WriteString(".inf")
	case math.IsInf(f, -1):
		b.WriteString("-.inf")
	default:
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
	}
}
