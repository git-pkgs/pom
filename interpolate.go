package pom

import (
	"strings"
)

const (
	maxInterpolationPasses = 10
	maxInterpolatedLength  = 1 << 20 // 1 MiB
	expressionStart        = "${"
)

// interpolate substitutes ${name} expressions in s using props. It iterates
// until no further substitutions occur or maxInterpolationPasses is reached,
// so chained references like ${a} -> ${b} -> value resolve correctly.
func interpolate(s string, props map[string]string) string {
	if !strings.Contains(s, expressionStart) {
		return s
	}
	for range maxInterpolationPasses {
		var changed, capped bool
		s, changed, capped = interpolatePass(s, props)
		if capped || !changed || !strings.Contains(s, expressionStart) {
			break
		}
	}
	return s
}

func interpolatePass(s string, props map[string]string) (string, bool, bool) {
	first := strings.Index(s, expressionStart)
	if first < 0 {
		return s, false, false
	}

	// A property reference is commonly the whole value. Returning the map's
	// string directly avoids building an identical intermediate string.
	if v, ok := wholeExpression(s, props); ok {
		if len(v) > maxInterpolatedLength {
			return s, false, true
		}
		return v, v != s, false
	}

	baseLen := len(s)
	growth := 0
	search := 0
	last := 0
	changed := false
	capped := false
	var out strings.Builder
	for {
		open, close, ok := nextExpression(s, search)
		if !ok {
			break
		}
		if close == open+len(expressionStart) {
			search = close + 1
			continue
		}

		replacement, ok := lookup(props, s[open+len(expressionStart):close])
		match := s[open : close+1]
		if ok && !capped {
			growth += len(replacement) - len(match)
			if baseLen+growth > maxInterpolatedLength {
				capped = true
			} else if replacement != match {
				if !changed {
					out.Grow(baseLen)
				}
				out.WriteString(s[last:open])
				out.WriteString(replacement)
				last = close + 1
				changed = true
			}
		}
		search = close + 1
	}
	if !changed {
		return s, false, capped
	}
	out.WriteString(s[last:])
	return out.String(), true, capped
}

func wholeExpression(s string, props map[string]string) (string, bool) {
	if !strings.HasPrefix(s, expressionStart) {
		return "", false
	}
	close := strings.IndexByte(s[len(expressionStart):], '}')
	if close < 0 || close != len(s)-len(expressionStart)-1 || close == 0 {
		return "", false
	}
	return lookup(props, s[len(expressionStart):len(s)-1])
}

func nextExpression(s string, search int) (int, int, bool) {
	relOpen := strings.Index(s[search:], expressionStart)
	if relOpen < 0 {
		return 0, 0, false
	}
	open := search + relOpen
	relClose := strings.IndexByte(s[open+len(expressionStart):], '}')
	if relClose < 0 {
		return 0, 0, false
	}
	return open, open + len(expressionStart) + relClose, true
}

// lookup resolves a single property name, applying the alias rules Maven
// supports for legacy ${pom.*} and bare ${version}/${groupId} references.
func lookup(props map[string]string, name string) (string, bool) {
	if v, ok := props[name]; ok {
		return v, true
	}
	if strings.HasPrefix(name, "pom.") {
		if v, ok := props["project."+name[len("pom."):]]; ok {
			return v, true
		}
	}
	switch name {
	case elementVersion, elementGroupID, elementArtifactID:
		if v, ok := props["project."+name]; ok {
			return v, true
		}
	}
	return "", false
}

// containsExpr reports whether s still contains an unresolved ${...}.
func containsExpr(s string) bool {
	return strings.Contains(s, expressionStart)
}

// firstExpr returns the first ${name} property name in s, or "" if none.
func firstExpr(s string) string {
	for search := 0; search < len(s); {
		open, close, ok := nextExpression(s, search)
		if !ok {
			break
		}
		if close > open+len(expressionStart) {
			return s[open+len(expressionStart) : close]
		}
		search = close + 1
	}
	return ""
}
