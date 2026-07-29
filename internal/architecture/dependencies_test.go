package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const modulePath = "github.com/pax-beehive/pax-nexus/internal/"

type dependencySuite struct {
	suite.Suite
	root string
}

func TestDependencySuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(dependencySuite))
}

func (s *dependencySuite) SetupSuite() {
	_, file, _, ok := runtime.Caller(0)
	s.Require().True(ok)
	s.root = filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

// dependencyRule whitelists the internal imports of one directory subtree.
// A package may always import its own subtree, minus excluded
// subdirectories, which must carry their own rule.
type dependencyRule struct {
	directory    string   // relative to internal/
	allowed      []string // allowed internal import prefixes outside the subtree
	excluded     []string // immediate subdirectories owned by another rule
	unrestricted bool     // may import any internal package (eval only)
}

// Default deny: a package not listed here fails the registration test.
// Grant the minimum set — no headroom. Retire entries with the code.
var dependencyRules = []dependencyRule{
	{directory: "architecture"},
	{directory: "deployment", allowed: []string{"explorer", "operations", "teamnote/runtime"}},
	{directory: "eval", unrestricted: true},
	{directory: "explorer"},
	{directory: "llmwiki", allowed: []string{"platform/llm"}},
	{directory: "operations"},
	{directory: "pagewiki", excluded: []string{"transport"},
		allowed: []string{"platform/llm", "platform/observability", "session"}},
	{directory: "pagewiki/transport",
		allowed: []string{"pagewiki", "teamnote/transport/httpapi/router/pagewiki/api"}},
	{directory: "platform", allowed: []string{"deployment/onprem", "explorer", "operations",
		"pagewiki/sessionconsumer", "session", "teamnote"}},
	{directory: "recall", allowed: []string{"teamnote"}},
	{directory: "session"},
	{directory: "sessionlake", allowed: []string{"session"}},
	{directory: "teamnote", excluded: []string{"transport"},
		allowed: []string{"platform/observability", "session", "sessionlake"}},
	{directory: "teamnote/transport", allowed: []string{"deployment/onprem", "explorer",
		"operations", "pagewiki/sessionconsumer", "pagewiki/transport/httpapi", "recall", "teamnote"}},
}

func (s *dependencySuite) TestEveryInternalPackageIsRegistered() {
	entries, err := os.ReadDir(s.root)
	s.Require().NoError(err)
	registered := make(map[string]struct{}, len(dependencyRules))
	for _, rule := range dependencyRules {
		top, _, _ := strings.Cut(rule.directory, "/")
		registered[top] = struct{}{}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_, ok := registered[entry.Name()]
		s.True(ok, "internal/%s has no dependency whitelist entry; add it to "+
			"dependencyRules with an explicit minimal allowed-import list", entry.Name())
	}
}

func (s *dependencySuite) TestDependencyWhitelist() {
	for _, rule := range dependencyRules {
		if rule.unrestricted {
			continue
		}
		s.Run(rule.directory, func() {
			imports, err := productionImports(filepath.Join(s.root, rule.directory), rule.excluded...)
			s.Require().NoError(err)
			for _, imported := range imports {
				if !strings.HasPrefix(imported, modulePath) {
					continue
				}
				relative := strings.TrimPrefix(imported, modulePath)
				s.True(importAllowed(relative, rule),
					"%s imports %s which is not in its whitelist", rule.directory, imported)
			}
		})
	}
}

func (s *dependencySuite) TestOnlyEvalImportsEval() {
	for _, rule := range dependencyRules {
		if rule.directory == "eval" {
			continue
		}
		imports, err := productionImports(filepath.Join(s.root, rule.directory))
		s.Require().NoError(err)
		for _, imported := range imports {
			s.False(hasPathPrefix(strings.TrimPrefix(imported, modulePath), "eval"),
				"%s imports %s; only eval may import eval", rule.directory, imported)
		}
	}
}

func (s *dependencySuite) TestImportAllowedDefaultsToDeny() {
	rule := dependencyRule{directory: "example", excluded: []string{"transport"},
		allowed: []string{"session"}}
	s.False(importAllowed("platform/postgres", rule), "unlisted import must be denied")
	s.False(importAllowed("example/transport/httpapi", rule), "excluded subtree must be denied")
	s.True(importAllowed("session", rule))
	s.True(importAllowed("session/sub", rule))
	s.True(importAllowed("example/inner", rule))
}

func importAllowed(relative string, rule dependencyRule) bool {
	if hasPathPrefix(relative, rule.directory) {
		for _, excluded := range rule.excluded {
			if hasPathPrefix(relative, rule.directory+"/"+excluded) {
				return false
			}
		}
		return true
	}
	for _, allowed := range rule.allowed {
		if hasPathPrefix(relative, allowed) {
			return true
		}
	}
	return false
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func productionImports(directory string, excluded ...string) ([]string, error) {
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		excludedSet[name] = struct{}{}
	}
	imports := make([]string, 0)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipDirectory(path, directory, entry, excludedSet) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !isProductionGoFile(path) {
			return nil
		}
		fileImports, err := parseImports(path)
		if err != nil {
			return err
		}
		imports = append(imports, fileImports...)
		return nil
	})
	return imports, err
}

func shouldSkipDirectory(path, root string, entry fs.DirEntry, excluded map[string]struct{}) bool {
	if !entry.IsDir() || path == root {
		return false
	}
	_, skip := excluded[entry.Name()]
	return skip
}

func isProductionGoFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

func parseImports(path string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(parsed.Imports))
	for _, importSpec := range parsed.Imports {
		value, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, value)
	}
	return imports, nil
}
