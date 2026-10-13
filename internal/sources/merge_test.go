package sources

import (
	"context"
	"testing"

	"github.com/fullerzz/herdr-plugin-sesh/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticSource struct {
	name     string
	sessions []model.Session
}

func (s staticSource) Name() string { return s.name }
func (s staticSource) List(context.Context) (model.Sessions, error) {
	out := model.NewSessions()
	for _, x := range s.sessions {
		out.Add(x)
	}
	return out, nil
}
func TestMergeOrderBlacklistDedupe(t *testing.T) {
	srcs := []Source{staticSource{"zoxide", []model.Session{{Source: "zoxide", Name: "api", Path: "/z"}}}, staticSource{"config", []model.Session{{Source: "config", Name: "api", Path: "/c"}, {Source: "config", Name: "scratch", Path: "/s"}}}}
	got, err := Merge(context.Background(), srcs, []string{"config", "zoxide"}, []string{"scratch"}, false, true)
	require.NoError(t, err)
	os := got.Ordered()
	require.Len(t, os, 1)
	require.Equal(t, "config", os[0].Source)
	assert.Equal(t, "api", os[0].Name)
}
func TestParseZoxideLine(t *testing.T) {
	s, ok := ParseZoxideLine("42.5 /tmp/my app")
	require.True(t, ok)
	require.InDelta(t, 42.5, s.Score, 0)
	require.Equal(t, "/tmp/my app", s.Path)
	assert.Equal(t, "my app", s.Name)
}

// Directories from different sources that share a folder name must not collapse:
// zoxide names entries after their last path component, so name-based dedupe
// silently dropped every same-named directory but the highest ranked one.
func TestMergeDedupesDiscoveredSessionsByPath(t *testing.T) {
	srcs := []Source{staticSource{"zoxide", []model.Session{
		{Source: "zoxide", Name: "mnemex", Path: "/a/mnemex", Score: 9},
		{Source: "zoxide", Name: "mnemex", Path: "/b/mnemex", Score: 3},
	}}}
	got, err := Merge(context.Background(), srcs, []string{"zoxide"}, nil, false, true)
	require.NoError(t, err)
	require.Len(t, got.Ordered(), 2)
}

// A herdr workspace label that happens to equal a discovered folder name must
// not hide an unrelated directory.
func TestMergeHerdrLabelDoesNotHideOtherPath(t *testing.T) {
	srcs := []Source{
		staticSource{"herdr", []model.Session{{Source: "herdr", Name: "vault", Path: "/ws/vault", WorkspaceID: "w1"}}},
		staticSource{"zoxide", []model.Session{{Source: "zoxide", Name: "vault", Path: "/other/vault", Score: 5}}},
	}
	got, err := Merge(context.Background(), srcs, []string{"herdr", "zoxide"}, nil, false, true)
	require.NoError(t, err)
	require.Len(t, got.Ordered(), 2)
}

// The same path from two sources is still a duplicate, and the first source in
// order wins so a live herdr workspace shadows its zoxide entry.
func TestMergeDedupesSamePathAcrossSources(t *testing.T) {
	srcs := []Source{
		staticSource{"herdr", []model.Session{{Source: "herdr", Name: "api", Path: "/p", WorkspaceID: "w1"}}},
		staticSource{"zoxide", []model.Session{{Source: "zoxide", Name: "api", Path: "/p", Score: 5}}},
	}
	got, err := Merge(context.Background(), srcs, []string{"herdr", "zoxide"}, nil, false, true)
	require.NoError(t, err)
	require.Len(t, got.Ordered(), 1)
	require.Equal(t, "herdr", got.Ordered()[0].Source)
}

// A config session carries startup, preview, and window settings, so it is
// matched by name only and never dropped in favour of a discovered entry that
// happens to hold the same path under a different name.
func TestMergeKeepsConfigSessionAtDiscoveredPath(t *testing.T) {
	srcs := []Source{
		staticSource{"herdr", []model.Session{{Source: "herdr", Name: "repo:branch", Path: "/p", WorkspaceID: "w1"}}},
		staticSource{"config", []model.Session{{Source: "config", Name: "vault", Path: "/p"}}},
	}
	got, err := Merge(context.Background(), srcs, []string{"herdr", "config"}, nil, false, true)
	require.NoError(t, err)
	require.Len(t, got.Ordered(), 2)
}
