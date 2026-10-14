package sources

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseZoxideLine(t *testing.T) {
	s, ok := ParseZoxideLine("42.5 /tmp/my app", 1)
	require.True(t, ok)
	require.InDelta(t, 42.5, s.Score, 0)
	require.Equal(t, "/tmp/my app", s.Path)
	assert.Equal(t, "my app", s.Name)
}

func TestParseZoxideLineDirLength(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		dirLength int
		wantName  string
		wantPath  string
	}{
		{name: "one component by default", line: "9 /home/halderm/mnemex", dirLength: 1, wantName: "mnemex", wantPath: "/home/halderm/mnemex"},
		{name: "two components", line: "9 /home/halderm/mnemex", dirLength: 2, wantName: "halderm/mnemex", wantPath: "/home/halderm/mnemex"},
		{name: "below one falls back to basename", line: "9 /home/halderm/mnemex", dirLength: 0, wantName: "mnemex", wantPath: "/home/halderm/mnemex"},
		{name: "n exceeds path components", line: "1 /mnemex", dirLength: 2, wantName: "mnemex", wantPath: "/mnemex"},
		{name: "spaces in path", line: "3 /x/my dir", dirLength: 2, wantName: "x/my dir", wantPath: "/x/my dir"},
		{name: "no score prefix", line: "/home/a/mnemex", dirLength: 2, wantName: "a/mnemex", wantPath: "/home/a/mnemex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := ParseZoxideLine(tc.line, tc.dirLength)
			require.True(t, ok)
			assert.Equal(t, tc.wantName, s.Name)
			assert.Equal(t, tc.wantPath, s.Path)
		})
	}
}

func TestParseZoxideLineBlank(t *testing.T) {
	_, ok := ParseZoxideLine("   ", 2)
	assert.False(t, ok)
}

func TestZoxideListNamesEntriesWithDirLength(t *testing.T) {
	z := Zoxide{
		Runner: func(context.Context) (string, error) {
			return "9 /home/a/mnemex\n3 /src/b/mnemex\n", nil
		},
		DirLength: 2,
	}
	ss, err := z.List(context.Background())
	require.NoError(t, err)
	got := ss.Ordered()
	require.Len(t, got, 2)
	assert.Equal(t, "a/mnemex", got[0].Name)
	assert.Equal(t, "b/mnemex", got[1].Name)
}
