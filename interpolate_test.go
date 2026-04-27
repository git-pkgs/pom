package pom

import "testing"

func TestInterpolate(t *testing.T) {
	props := map[string]string{
		"a":               "1",
		"b":               "${a}",
		"c":               "${b}.${a}",
		"project.version": "2.0",
		"project.groupId": "org.example",
		"self":            "${self}",
	}
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"${a}", "1"},
		{"${b}", "1"},
		{"${c}", "1.1"},
		{"v${a}-final", "v1-final"},
		{"${missing}", "${missing}"},
		{"${a}.${missing}", "1.${missing}"},
		{"${pom.version}", "2.0"},
		{"${version}", "2.0"},
		{"${groupId}", "org.example"},
		{"${env.PATH}", "${env.PATH}"},
		{"${self}", "${self}"},
	}
	for _, tt := range tests {
		if got := interpolate(tt.in, props); got != tt.want {
			t.Errorf("interpolate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFirstExpr(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"no expr", ""},
		{"${a}", "a"},
		{"x${foo.bar}y${baz}", "foo.bar"},
	}
	for _, tt := range tests {
		if got := firstExpr(tt.in); got != tt.want {
			t.Errorf("firstExpr(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
