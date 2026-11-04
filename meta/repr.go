package meta

import (
	"strconv"
	"strings"
)

// parseRepr reads the compact text form OP.GG's tools answer with:
//
//	class LolGetChampionAnalysis: data
//	class Data: damage_type,core_items,boots
//	class CoreItems: ids,ids_names,play,win,pick_rate
//
//	LolGetChampionAnalysis(Data("AP",CoreItems([6617,3083],["A","B"],7373,3796,0.18),[]))
//
// Each Name(...) becomes a map keyed by the field names its class line lists,
// so the result has the same shape as the JSON form ({"data": {"core_items":
// {...}}}) and the same code can read both. ok is false if s isn't this form.
func parseRepr(s string) (v any, ok bool) {
	classes := map[string][]string{}
	body := ""
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if rest, found := strings.CutPrefix(t, "class "); found {
			name, fields, found := strings.Cut(rest, ":")
			if !found {
				continue
			}
			var fs []string
			for _, f := range strings.Split(fields, ",") {
				if f = strings.TrimSpace(f); f != "" {
					fs = append(fs, f)
				}
			}
			classes[strings.TrimSpace(name)] = fs
			continue
		}
		if t != "" && body == "" {
			body = t
		}
	}
	if len(classes) == 0 || body == "" {
		return nil, false
	}
	p := &reprParser{s: body, classes: classes}
	defer func() {
		if recover() != nil {
			v, ok = nil, false
		}
	}()
	v = p.value()
	if p.err {
		return nil, false
	}
	m, isMap := v.(map[string]any)
	return m, isMap
}

type reprParser struct {
	s       string
	i       int
	classes map[string][]string
	err     bool
}

func (p *reprParser) skip() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\r') {
		p.i++
	}
}

func (p *reprParser) value() any {
	p.skip()
	if p.i >= len(p.s) {
		p.err = true
		return nil
	}
	switch c := p.s[p.i]; {
	case c == '"':
		j := p.i + 1
		for j < len(p.s) && p.s[j] != '"' {
			if p.s[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(p.s) {
			p.err = true
			return nil
		}
		q := p.s[p.i : j+1]
		p.i = j + 1
		if u, err := strconv.Unquote(q); err == nil {
			return u
		}
		return q[1 : len(q)-1]
	case c == '[':
		p.i++
		return p.list(']')
	case c == '-' || c == '.' || (c >= '0' && c <= '9'):
		j := p.i
		for j < len(p.s) && strings.IndexByte("+-.eE0123456789", p.s[j]) >= 0 {
			j++
		}
		f, err := strconv.ParseFloat(p.s[p.i:j], 64)
		if err != nil {
			p.err = true
		}
		p.i = j
		return f
	default:
		j := p.i
		for j < len(p.s) && (p.s[j] == '_' || p.s[j] >= 'a' && p.s[j] <= 'z' || p.s[j] >= 'A' && p.s[j] <= 'Z' || p.s[j] >= '0' && p.s[j] <= '9') {
			j++
		}
		if j == p.i {
			p.err = true
			return nil
		}
		word := p.s[p.i:j]
		p.i = j
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == '(' {
			p.i++
			args := p.list(')')
			fields := p.classes[word]
			if len(fields) == 0 || len(args) > len(fields) {
				return args
			}
			m := map[string]any{}
			for k, a := range args {
				m[fields[k]] = a
			}
			return m
		}
		switch word {
		case "null", "None":
			return nil
		case "true", "True":
			return true
		case "false", "False":
			return false
		}
		return word
	}
}

// list reads comma separated values up to the closing bracket.
func (p *reprParser) list(end byte) []any {
	out := []any{}
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == end {
		p.i++
		return out
	}
	for !p.err {
		out = append(out, p.value())
		p.skip()
		if p.i >= len(p.s) {
			p.err = true
			break
		}
		if p.s[p.i] == end {
			p.i++
			break
		}
		if p.s[p.i] != ',' {
			p.err = true
			break
		}
		p.i++
	}
	return out
}
