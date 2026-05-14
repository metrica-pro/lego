package cloudruevolution

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRelativeName(t *testing.T) {
	cases := []struct {
		fqdn, zone, want string
	}{
		// apex challenge: _acme-challenge.example.com. on zone example.com.
		{"_acme-challenge.example.com.", "example.com.", "_acme-challenge"},
		// subdomain challenge: _acme-challenge.foo.example.com. on zone example.com.
		{"_acme-challenge.foo.example.com.", "example.com.", "_acme-challenge.foo"},
		// challenge directly on zone apex (rare but legal — e.g. wildcard on apex)
		{"example.com.", "example.com.", ""},
		// trailing dots optional on both sides
		{"_acme-challenge.example.com", "example.com", "_acme-challenge"},
		// mixed case is preserved in the host portion but matched case-insensitively
		{"_acme-challenge.Example.com.", "example.com.", "_acme-challenge"},
		// host does not end with zone (shouldn't normally happen): leave untouched
		{"foo.other.com.", "example.com.", "foo.other.com"},
	}
	for _, tc := range cases {
		t.Run(tc.fqdn+"|"+tc.zone, func(t *testing.T) {
			got := relativeName(tc.fqdn, tc.zone)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFilterOut(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		target  string
		want    []string
		wantLen int
	}{
		{"removes single", []string{"a", "b", "c"}, "b", []string{"a", "c"}, 2},
		{"removes all duplicates", []string{"a", "b", "b", "c"}, "b", []string{"a", "c"}, 2},
		{"no match", []string{"a", "b"}, "z", []string{"a", "b"}, 2},
		{"empty input", []string{}, "x", []string{}, 0},
		{"empties to nothing", []string{"x"}, "x", []string{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterOut(tc.in, tc.target)
			assert.Len(t, got, tc.wantLen)
			assert.Equal(t, tc.want, got)
		})
	}
}
