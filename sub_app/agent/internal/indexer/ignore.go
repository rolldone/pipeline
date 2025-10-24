package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// SimpleIgnoreCache: a compact, deterministic ignore matcher used by the agent.
// It supports cascading .sync_ignore files and basic negation with last-match-wins
// semantics.
type SimpleIgnoreCache struct {
	Root           string
	raw            map[string][]string // directory -> preprocessed lines
	authoritative  bool
	manualTransfer []string
}

func NewSimpleIgnoreCache(root string) *SimpleIgnoreCache {
	c := &SimpleIgnoreCache{Root: root, raw: map[string][]string{}}
	cfgPath := filepath.Join(root, ".sync_temp", "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var try1 struct {
			Devsync struct {
				Ignores        []string `json:"ignores"`
				ManualTransfer []string `json:"manual_transfer"`
			} `json:"devsync"`
		}
		var try2 struct {
			Pipeline struct {
				Ignores        []string `json:"ignores"`
				ManualTransfer []string `json:"manual_transfer"`
			} `json:"pipeline"`
		}
		if jerr := json.Unmarshal(data, &try2); jerr == nil && len(try2.Pipeline.Ignores) > 0 {
			c.raw[root] = append(c.raw[root], try2.Pipeline.Ignores...)
			c.authoritative = true
			c.manualTransfer = try2.Pipeline.ManualTransfer
		} else if jerr := json.Unmarshal(data, &try1); jerr == nil && len(try1.Devsync.Ignores) > 0 {
			c.raw[root] = append(c.raw[root], try1.Devsync.Ignores...)
			c.authoritative = true
			c.manualTransfer = try1.Devsync.ManualTransfer
		}
	}
	return c
}

func (c *SimpleIgnoreCache) Match(path string, isDir bool) bool {
	defaults := []string{".sync_temp", "pipeline.yaml", ".sync_ignore", ".sync_collections"}
	base := filepath.Base(path)
	for _, d := range defaults {
		if strings.EqualFold(d, base) {
			return true
		}
	}

	dir := path
	if !isDir {
		dir = filepath.Dir(path)
	}

	var ancestors []string
	cur := dir
	for {
		ancestors = append(ancestors, cur)
		if cur == c.Root || cur == string(os.PathSeparator) {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
		ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
	}

	cumulative := []string{}
	if c.authoritative {
		if lines, ok := c.raw[c.Root]; ok {
			cumulative = append(cumulative, lines...)
		}
	} else {
		for _, a := range ancestors {
			if lines, ok := c.raw[a]; ok {
				cumulative = append(cumulative, lines...)
				continue
			}
			syncPath := filepath.Join(a, ".sync_ignore")
			if _, err := os.Stat(syncPath); err == nil {
				data, rerr := os.ReadFile(syncPath)
				if rerr == nil {
					raw := strings.Split(string(data), "\n")
					lines := []string{}
					for _, ln := range raw {
						l := strings.TrimSpace(ln)
						if l == "" || strings.HasPrefix(l, "#") {
							continue
						}
						neg := false
						if strings.HasPrefix(l, "!") {
							neg = true
							l = strings.TrimPrefix(l, "!")
						}
						l = filepath.ToSlash(l)
						if strings.Contains(l, "/") || strings.Contains(l, "**") {
							if neg {
								lines = append(lines, "!"+l)
							} else {
								lines = append(lines, l)
							}
							continue
						}
						if neg {
							lines = append(lines, "!"+l)
							lines = append(lines, "!**/"+l)
						} else {
							lines = append(lines, l)
							lines = append(lines, "**/"+l)
						}
					}
					c.raw[a] = lines
					cumulative = append(cumulative, lines...)
					continue
				}
			}
			c.raw[a] = nil
		}
	}

	if len(cumulative) == 0 {
		return false
	}

	relToRoot, err := filepath.Rel(c.Root, path)
	if err != nil {
		relToRoot = path
	}
	relToRoot = filepath.ToSlash(relToRoot)
	baseName := filepath.ToSlash(filepath.Base(path))

	// Use go-gitignore to evaluate the cumulative rules (supports negation)
	gi := gitignore.CompileIgnoreLines(cumulative...)
	if gi == nil {
		return false
	}
	if gi.MatchesPath(relToRoot) || gi.MatchesPath(baseName) {
		return true
	}
	return false
}

func (c *SimpleIgnoreCache) MatchWithManualTransfer(path string, isDir bool) bool {
	for _, endpoint := range c.manualTransfer {
		endpoint = filepath.ToSlash(endpoint)
		pathRel, err := filepath.Rel(c.Root, path)
		if err != nil {
			pathRel = path
		}
		pathRel = filepath.ToSlash(pathRel)
		if pathRel == endpoint || strings.HasPrefix(pathRel, endpoint+"/") {
			return false
		}
	}
	return c.Match(path, isDir)
}

func matchedGlob(pattern, target string) bool {
	p := filepath.ToSlash(pattern)
	t := filepath.ToSlash(target)
	m, _ := filepath.Match(p, t)
	if m {
		return true
	}
	if strings.HasPrefix(p, "**/") {
		m2, _ := filepath.Match(strings.TrimPrefix(p, "**/"), t)
		if m2 {
			return true
		}
	}
	return false
}
