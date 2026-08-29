package git

import "testing"

func TestNormalizeCommitSHA(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty means branch head", in: "", want: ""},
		{name: "blank is treated as empty", in: "   ", want: ""},
		{name: "abbreviation", in: "8ce16504d4", want: "8ce16504d4"},
		{name: "shortest accepted abbreviation", in: "8ce1650", want: "8ce1650"},
		{name: "full object name", in: "8ce16504d48b6d3ed1e61ed6819320c2c910e413", want: "8ce16504d48b6d3ed1e61ed6819320c2c910e413"},
		{name: "uppercase is lowercased", in: "8CE16504D4", want: "8ce16504d4"},
		{name: "surrounding space is trimmed", in: "  8ce16504d4\n", want: "8ce16504d4"},
		{name: "too short to be unambiguous", in: "8ce165", wantErr: true},
		{name: "longer than a SHA-1", in: "8ce16504d48b6d3ed1e61ed6819320c2c910e4133", wantErr: true},
		{name: "not hexadecimal", in: "8ce16504zz", wantErr: true},
		// A branch name is the most likely thing to get pasted into the wrong
		// field; it must not slip through as a commit pin.
		{name: "branch name", in: "feature/rollback", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeCommitSHA(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeCommitSHA(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeCommitSHA(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeCommitSHA(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
