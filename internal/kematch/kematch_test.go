package kematch

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		key     string
		want    bool
	}{
		{"exact match", "a/b/c", "a/b/c", true},
		{"exact mismatch tail", "a/b/c", "a/b/d", false},
		{"prefix mismatch", "a/b/c", "a/b", false},
		{"longer key no wildcard", "a/b", "a/b/c", false},
		{"single star matches one chunk", "a/*/c", "a/x/c", true},
		{"single star does not match two chunks", "a/*/c", "a/x/y/c", false},
		{"single star does not match empty", "a/*/c", "a//c", false},
		{"double star matches zero chunks", "a/**/c", "a/c", true},
		{"double star matches one chunk", "a/**/c", "a/x/c", true},
		{"double star matches many chunks", "a/**/c", "a/x/y/z/c", true},
		{"leading double star", "**/c", "a/b/c", true},
		{"trailing double star", "a/**", "a/b/c", true},
		{"trailing double star matches self", "a/**", "a", true},
		{"double star only", "**", "", true},
		{"double star only with chunks", "**", "a/b/c", true},
		{"literal mismatch within middle", "a/b/c", "a/x/c", false},
		{"single star at end", "a/b/*", "a/b/c", true},
		{"single star at end mismatch", "a/b/*", "a/b/c/d", false},
		{"multiple stars", "*/*/c", "a/b/c", true},
		{"nested double stars", "a/**/x/**/z", "a/1/2/x/3/4/z", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(tc.pattern, tc.key)
			if got != tc.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tc.pattern, tc.key, got, tc.want)
			}
		})
	}
}
