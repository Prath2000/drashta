package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// usageError is a mistake in how the command was called. Run prints it and exits with 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{fmt.Sprintf(format, a...)} }

// helpRequested is returned when -h or --help was given. Run prints the help and exits with 0.
type helpRequested struct{ topic string }

func (helpRequested) Error() string { return "help requested" }

// flagDef describes one option of a command.
type flagDef struct {
	value bool   // takes a value (--feature F-3) or is a switch (-q)
	short string // one letter alias, without the dash
}

// commonFlags are accepted by every command.
var commonFlags = map[string]flagDef{
	"project": {value: true, short: "C"},
	"author":  {value: true},
	"help":    {short: "h"},
}

// parsed is the result of parseArgs.
type parsed struct {
	pos  []string
	vals map[string][]string
	on   map[string]bool
}

// parseArgs reads options in any position, so "task new Title --feature F-3" works. "--" ends
// the options. Both "--name value" and "--name=value" are accepted.
func parseArgs(args []string, spec map[string]flagDef) (*parsed, error) {
	all := map[string]flagDef{}
	for k, v := range commonFlags {
		all[k] = v
	}
	for k, v := range spec {
		all[k] = v
	}
	byShort := map[string]string{}
	for name, d := range all {
		if d.short != "" {
			byShort[d.short] = name
		}
	}
	p := &parsed{vals: map[string][]string{}, on: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.pos = append(p.pos, args[i+1:]...)
			break
		}
		var name, val string
		hasVal := false
		switch {
		case strings.HasPrefix(a, "--") && len(a) > 2:
			name = a[2:]
			if k, v, ok := strings.Cut(name, "="); ok {
				name, val, hasVal = k, v, true
			}
		case strings.HasPrefix(a, "-") && len(a) > 1 && !isNumber(a) && !strings.ContainsAny(a, " \t\n"):
			short := a[1:]
			if k, v, ok := strings.Cut(short, "="); ok {
				short, val, hasVal = k, v, true
			}
			n, ok := byShort[short]
			if !ok {
				return nil, usagef("unknown option -%s", short)
			}
			name = n
		default:
			p.pos = append(p.pos, a)
			continue
		}
		d, ok := all[name]
		if !ok {
			return nil, usagef("unknown option --%s", name)
		}
		if !d.value {
			if hasVal {
				return nil, usagef("option --%s does not take a value", name)
			}
			p.on[name] = true
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, usagef("option --%s needs a value", name)
			}
			i++
			val = args[i]
		}
		p.vals[name] = append(p.vals[name], val)
	}
	if p.on["help"] {
		return nil, helpRequested{}
	}
	return p, nil
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// str returns the last value of an option, or "".
func (p *parsed) str(name string) string {
	v := p.vals[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

func (p *parsed) all(name string) []string { return p.vals[name] }
func (p *parsed) has(name string) bool     { return p.on[name] || len(p.vals[name]) > 0 }

func (p *parsed) float(name string) (*float64, error) {
	s := p.str(name)
	if s == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, usagef("option --%s needs a number, got %q", name, s)
	}
	return &f, nil
}

func (p *parsed) int(name string) (*int, error) {
	s := p.str(name)
	if s == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, usagef("option --%s needs a whole number, got %q", name, s)
	}
	return &n, nil
}

// need checks the number of positional arguments.
func (p *parsed) need(min, max int, usage string) error {
	if len(p.pos) < min || (max >= 0 && len(p.pos) > max) {
		return usagef("usage: %s", usage)
	}
	return nil
}
