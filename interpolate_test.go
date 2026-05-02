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

func TestInterpolateAmplificationCapped(t *testing.T) {
	props := map[string]string{
		"bomb": "${bomb}${bomb}${bomb}${bomb}${bomb}",
	}
	result := interpolate("${bomb}", props)
	// Without the cap, 10 passes of 5x self-reference would produce ~68 MiB.
	if len(result) > maxInterpolatedLength {
		t.Fatalf("interpolated length %d exceeds cap %d", len(result), maxInterpolatedLength)
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
