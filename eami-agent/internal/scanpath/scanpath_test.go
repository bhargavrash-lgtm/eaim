package scanpath

import "testing"

// Shape rules hold on every OS (both forms are checked everywhere). Forms
// that only *clean* to a root ("/.", "/..", C:\..\) are not roots by shape:
// they fail IsNormalized first, which callers check before IsRoot.
func TestShapes(t *testing.T) {
	cases := []struct {
		p                                         string
		network, absolute, root, normalized, colon bool
	}{
		{"/", false, true, true, true, false},
		{"/.", false, true, false, false, false},
		{"/..", false, true, false, false, false},
		{"/srv", false, true, false, true, false},
		{`C:\`, false, true, true, true, false},
		{"C:/", false, true, true, true, false},
		{"c:", false, false, true, true, false},
		{`C:\..\`, false, true, false, false, false},
		{`C:\Users`, false, true, false, true, false},
		{`C:\\Users`, false, true, false, true, false}, // the stored legacy form stays normal
		{`C:\Users.`, false, true, false, false, false},
		{`C:\Users `, false, true, false, false, false},
		{`C:\Users::$INDEX_ALLOCATION`, false, true, false, true, true},
		{"/srv/a:b", false, true, false, true, true},
		{`\\nas\m`, true, false, false, true, false},
		{"//nas/m", true, false, false, true, false},
		{`\\?\C:\x`, true, false, false, true, true},
		{"models", false, false, false, true, false},
		{"", false, false, false, true, false},
	}
	for _, c := range cases {
		got := []bool{IsNetwork(c.p), IsAbsolute(c.p), IsRoot(c.p), IsNormalized(c.p), HasStrayColon(c.p)}
		want := []bool{c.network, c.absolute, c.root, c.normalized, c.colon}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q: network/absolute/root/normalized/colon = %v, want %v", c.p, got, want)
				break
			}
		}
	}
}
