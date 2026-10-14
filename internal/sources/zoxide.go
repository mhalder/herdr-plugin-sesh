package sources

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/fullerzz/herdr-plugin-sesh/internal/model"
	"github.com/fullerzz/herdr-plugin-sesh/internal/namer"
)

// Zoxide lists directories tracked by zoxide. DirLength mirrors the
// [naming] path_components setting so a picker row reads the same as the direct
// path namer would name the workspace; values below 1 fall back to the
// basename.
type Zoxide struct {
	Runner    func(context.Context) (string, error)
	DirLength int
}

func (Zoxide) Name() string { return "zoxide" }
func (z Zoxide) List(ctx context.Context) (model.Sessions, error) {
	out := model.NewSessions()
	var raw string
	var err error
	if z.Runner != nil {
		raw, err = z.Runner(ctx)
	} else {
		b, e := exec.CommandContext(ctx, "zoxide", "query", "-l", "-s").Output()
		raw = string(b)
		err = e
	}
	if err != nil {
		return out, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		if s, ok := ParseZoxideLine(scanner.Text(), z.DirLength); ok {
			out.Add(s)
		}
	}
	return out, nil
}

// ParseZoxideLine parses one `zoxide query -l -s` line into a session. The path
// is named with the trailing dirLength components, matching the direct-path
// namer so a picker row and the created workspace label agree.
func ParseZoxideLine(line string, dirLength int) (model.Session, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return model.Session{}, false
	}
	fields := strings.Fields(line)
	score := 0.0
	path := line
	if len(fields) >= 2 {
		if f, err := strconv.ParseFloat(fields[0], 64); err == nil {
			score = f
			path = strings.Join(fields[1:], " ")
		}
	}
	if dirLength < 1 {
		dirLength = 1
	}
	return model.Session{Source: "zoxide", Name: namer.LastComponents(path, dirLength), Path: path, Score: score}, true
}
func AddPath(ctx context.Context, path string) {
	//nolint:gosec // zoxide is a fixed local integration; path is a single argv value.
	_ = exec.CommandContext(ctx, "zoxide", "add", path).Run()
}
