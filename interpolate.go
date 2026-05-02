package pom

import (
	"regexp"
	"strings"
)

const (
	maxInterpolationPasses  = 10
	maxInterpolatedLength   = 1 << 20 // 1 MiB
)

var exprRE = regexp.MustCompile(`\$\{([^}]+)\}`)

// interpolate substitutes ${name} expressions in s using props. It iterates
// until no further substitutions occur or maxInterpolationPasses is reached,
// so chained references like ${a} -> ${b} -> value resolve correctly.
func interpolate(s string, props map[string]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	for range maxInterpolationPasses {
		changed := false
		capped := false
		growth := 0
		baseLen := len(s)
		s = exprRE.ReplaceAllStringFunc(s, func(m string) string {
			if capped {
				return m
			}
			name := m[2 : len(m)-1]
			if v, ok := lookup(props, name); ok {
				growth += len(v) - len(m)
				if baseLen+growth > maxInterpolatedLength {
					capped = true
					return m
				}
				changed = true
				return v
			}
			return m
		})
		if capped || !changed || !strings.Contains(s, "${") {
			break
		}
	}
	return s
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
	case "version", "groupId", "artifactId":
		if v, ok := props["project."+name]; ok {
			return v, true
		}
	}
	return "", false
}

// containsExpr reports whether s still contains an unresolved ${...}.
func containsExpr(s string) bool {
	return strings.Contains(s, "${")
}

// firstExpr returns the first ${name} property name in s, or "" if none.
func firstExpr(s string) string {
	m := exprRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}
