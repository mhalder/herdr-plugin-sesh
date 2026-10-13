package sources

import (
	"context"
	"regexp"
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
	// Config sessions are addressed by name, so a config session shadows any
	// later discovered session with the same name. Every other source is a
	// directory (or a herdr workspace labelled like one) and is deduplicated by
	// path, so unrelated directories that share a folder name no longer collapse.
	// Both sets are keyed by machine, so remote workspaces with the same name or
	// path stay distinct.
	seenConfigName := map[[2]string]bool{}
	seenPath := map[[2]string]bool{}
	bl := compile(blacklist)
	for _, name := range ordered {
		ss := by[name]
		for _, sess := range ss.Ordered() {
			isbl := isBlacklisted(bl, sess.Name)
			if len(bl) > 0 || onlyBlacklisted {
				if isbl != onlyBlacklisted {
					continue
				}
			}
			if dedupe {
				id := func(k string) [2]string { return [2]string{sess.Machine, k} }
				if sess.Source == "config" {
					if sess.Name != "" && seenConfigName[id(sess.Name)] {
						continue
					}
					if sess.Name != "" {
						seenConfigName[id(sess.Name)] = true
					}
					if sess.Path != "" {
						if seenPath[id(sess.Path)] {
							continue
						}
						seenPath[id(sess.Path)] = true
					}
				} else {
					if sess.Name != "" && seenConfigName[id(sess.Name)] {
						continue
					}
					key := sess.Path
					if key == "" {
						key = sess.Name
					}
					if seenPath[id(key)] {
						continue
					}
					seenPath[id(key)] = true
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
