package authored

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// A field ties one front-matter key to one Go struct field. The same descriptor is used to
// read a file and to patch it, so the two directions cannot drift apart.
type fkind int

const (
	kStr       fkind = iota // text
	kTime                   // text that is a date or a timestamp; written without quotes
	kStrNull                // text; an empty value is written as null
	kInt                    // integer
	kIntNull                // *int; nil is written as null
	kFloatNull              // *float64; nil is written as null
	kBool                   // true | false
	kList                   // list of text
)

type field struct {
	key      string
	kind     fkind
	optional bool // leave the key out of a new file when the value is empty
	flow     bool // write a non-empty list as [a, b]
	s        *string
	i        *int
	ip       **int
	fp       **float64
	b        *bool
	l        *[]string
}

func strF(key string, p *string, optional bool) field {
	return field{key: key, kind: kStr, optional: optional, s: p}
}
func timeF(key string, p *string, optional bool) field {
	return field{key: key, kind: kTime, optional: optional, s: p}
}
func nullF(key string, p *string) field {
	return field{key: key, kind: kStrNull, optional: true, s: p}
}
func intF(key string, p *int) field { return field{key: key, kind: kInt, i: p} }
func intNullF(key string, p **int) field {
	return field{key: key, kind: kIntNull, optional: true, ip: p}
}
func floatNullF(key string, p **float64) field {
	return field{key: key, kind: kFloatNull, optional: true, fp: p}
}
func boolF(key string, p *bool) field { return field{key: key, kind: kBool, b: p} }
func listF(key string, p *[]string, flow bool) field {
	return field{key: key, kind: kList, l: p, flow: flow}
}

// clone returns a field of the same kind that points at fresh, zero values.
func (f field) clone() field {
	c := f
	c.s, c.i, c.ip, c.fp, c.b, c.l = nil, nil, nil, nil, nil, nil
	switch f.kind {
	case kStr, kTime, kStrNull:
		c.s = new(string)
	case kInt:
		c.i = new(int)
	case kIntNull:
		c.ip = new(*int)
	case kFloatNull:
		c.fp = new(*float64)
	case kBool:
		c.b = new(bool)
	case kList:
		c.l = new([]string)
	}
	return c
}

func isNullNode(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// fromNode reads the YAML value into the Go field.
func (f field) fromNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	switch f.kind {
	case kList:
		switch {
		case isNullNode(n):
			*f.l = nil
		case n.Kind == yaml.ScalarNode:
			*f.l = []string{n.Value}
		case n.Kind == yaml.SequenceNode:
			out := make([]string, 0, len(n.Content))
			for _, c := range n.Content {
				if c.Kind != yaml.ScalarNode {
					return fmt.Errorf("%s: list items must be text", f.key)
				}
				if isNullNode(c) {
					continue
				}
				out = append(out, c.Value)
			}
			*f.l = out
		default:
			return fmt.Errorf("%s: want a list", f.key)
		}
		return nil
	}
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s: want a single value", f.key)
	}
	null := isNullNode(n)
	switch f.kind {
	case kStr, kTime, kStrNull:
		if null {
			*f.s = ""
		} else {
			*f.s = n.Value
		}
	case kInt:
		if null {
			*f.i = 0
			return nil
		}
		v, err := strconv.Atoi(strings.TrimSpace(n.Value))
		if err != nil {
			return fmt.Errorf("%s: want a whole number, got %q", f.key, n.Value)
		}
		*f.i = v
	case kIntNull:
		if null || strings.TrimSpace(n.Value) == "" {
			*f.ip = nil
			return nil
		}
		v, err := strconv.Atoi(strings.TrimSpace(n.Value))
		if err != nil {
			return fmt.Errorf("%s: want a whole number, got %q", f.key, n.Value)
		}
		*f.ip = &v
	case kFloatNull:
		if null || strings.TrimSpace(n.Value) == "" {
			*f.fp = nil
			return nil
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(n.Value), 64)
		if err != nil {
			return fmt.Errorf("%s: want a number, got %q", f.key, n.Value)
		}
		*f.fp = &v
	case kBool:
		if null {
			*f.b = false
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(n.Value)) {
		case "true", "yes", "on", "y":
			*f.b = true
		case "false", "no", "off", "n", "":
			*f.b = false
		default:
			return fmt.Errorf("%s: want true or false, got %q", f.key, n.Value)
		}
	}
	return nil
}

// value is a text form of the Go value, used to compare it with a node and to test emptiness.
func (f field) value() string {
	switch f.kind {
	case kStr, kTime, kStrNull:
		return *f.s
	case kInt:
		if *f.i == 0 {
			return ""
		}
		return strconv.Itoa(*f.i)
	case kIntNull:
		if *f.ip == nil {
			return ""
		}
		return strconv.Itoa(**f.ip)
	case kFloatNull:
		if *f.fp == nil {
			return ""
		}
		return strconv.FormatFloat(**f.fp, 'f', -1, 64)
	case kBool:
		if *f.b {
			return "true"
		}
		return ""
	case kList:
		return strings.Join(*f.l, "\x1f")
	}
	return ""
}

func (f field) isEmpty() bool { return f.value() == "" }

// sameAsNode reports whether the node already holds this field's value.
func (f field) sameAsNode(n *yaml.Node) bool {
	c := f.clone()
	if err := c.fromNode(n); err != nil {
		return false
	}
	return c.value() == f.value()
}

var (
	tagStr   = "!!str"
	tagNull  = "!!null"
	tagInt   = "!!int"
	tagFloat = "!!float"
	tagBool  = "!!bool"
	tagTime  = "!!timestamp"
)

func strNode(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: s}
	if strings.Contains(s, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

// isYAMLTimestamp reports whether YAML would read the plain text s as a timestamp.
func isYAMLTimestamp(s string) bool {
	if len(s) < 8 || s[0] < '0' || s[0] > '9' {
		return false
	}
	var v any
	if err := yaml.Unmarshal([]byte(s), &v); err != nil {
		return false
	}
	_, ok := v.(time.Time)
	return ok
}

func timeNode(s string) *yaml.Node {
	if s != "" && isYAMLTimestamp(s) {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagTime, Value: s}
	}
	return strNode(s)
}

func nullNode() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagNull, Value: "null"} }

// toNode builds a fresh YAML node for the current value.
func (f field) toNode() *yaml.Node {
	switch f.kind {
	case kStr:
		return strNode(*f.s)
	case kTime:
		return timeNode(*f.s)
	case kStrNull:
		if *f.s == "" {
			return nullNode()
		}
		return strNode(*f.s)
	case kInt:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagInt, Value: strconv.Itoa(*f.i)}
	case kIntNull:
		if *f.ip == nil {
			return nullNode()
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagInt, Value: strconv.Itoa(**f.ip)}
	case kFloatNull:
		if *f.fp == nil {
			return nullNode()
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagFloat, Value: strconv.FormatFloat(**f.fp, 'f', -1, 64)}
	case kBool:
		v := "false"
		if *f.b {
			v = "true"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: v}
	case kList:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if f.flow || len(*f.l) == 0 {
			n.Style = yaml.FlowStyle
		}
		for _, s := range *f.l {
			n.Content = append(n.Content, strNode(s))
		}
		return n
	}
	return nullNode()
}
