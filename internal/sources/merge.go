package sources

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/fullerzz/herdr-plugin-sesh/internal/model"
)

func Merge(ctx context.Context, srcs []Source, order []string, blacklist []string, onlyBlacklisted, dedupe bool) (model.Sessions, error) {
	by := map[string]model.Sessions{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var first error
	for _, src := range srcs {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			got, err := s.List(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && first == nil {
				first = err
			}
			by[s.Name()] = got
		}(src)
	}
	wg.Wait()
	if first != nil {
		return model.Sessions{}, first
	}
	ordered := append([]string{}, order...)
	seenOrder := map[string]bool{}
	for _, o := range ordered {
		seenOrder[o] = true
	}
	for _, s := range srcs {
		if !seenOrder[s.Name()] {
			ordered = append(ordered, s.Name())
		}
	}
	out := model.NewSessions()
	// Config sessions are addressed by name: a config session shadows any later
	// discovered session with the same name, whatever its path. Every other
	// source is a directory (or a herdr workspace labelled like one) and is
	// deduplicated by path, so unrelated directories that share a folder name no
	// longer collapse. Config sessions stay out of that path set, so a config
	// session is never dropped in favour of a discovered directory.
	seenConfigName := map[string]bool{}
	seenPath := map[string]bool{}
	bl := compile(blacklist)
	for _, name := range ordered {
		ss := by[name]
		for _, sess := range ss.Ordered() {
			// A name may carry path components (zoxide entries use the trailing
			// path_components), so anchored folder patterns such as ^target$ must
			// also be tested against the last segment.
			isbl := isBlacklisted(bl, sess.Name) || isBlacklisted(bl, baseName(sess.Name))
			if len(bl) > 0 || onlyBlacklisted {
				if isbl != onlyBlacklisted {
					continue
				}
			}
			if dedupe {
				if sess.Source == "config" {
					key := sess.Name
					if key == "" {
						key = sess.Path
					}
					if seenConfigName[key] {
						continue
					}
					seenConfigName[key] = true
				} else {
					if sess.Name != "" && seenConfigName[sess.Name] {
						continue
					}
					key := sess.Path
					if key == "" {
						key = sess.Name
					}
					if seenPath[key] {
						continue
					}
					seenPath[key] = true
				}
			}
			out.Add(sess)
		}
	}
	return out, nil
}
func compile(items []string) []*regexp.Regexp {
	out := []*regexp.Regexp{}
	for _, it := range items {
		if re, err := regexp.Compile(it); err == nil {
			out = append(out, re)
		}
	}
	return out
}
func isBlacklisted(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// baseName returns the final slash-separated segment of a session name.
func baseName(name string) string {
	name = strings.TrimRight(name, "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}
