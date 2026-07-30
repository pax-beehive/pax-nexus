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
		"pagewiki/sessionconsumer", "session", "teamnote", "todoapp"}},
	{directory: "todoapp", excluded: []string{"transport"},
		allowed: []string{"platform/llm", "platform/observability", "session"}},
	// TODO(Task 11): uncomment when todoapp/transport directory is created
	// {directory: "todoapp/transport",
	// 	allowed: []string{"deployment/onprem", "todoapp", "teamnote/transport/httpapi/router/todoapp/api"}},
	{directory: "recall", allowed: []string{"teamnote"}},
	{directory: "session"},
	{directory: "evidencelake", allowed: []string{"session"}},
	{directory: "teamnote", excluded: []string{"transport"},
		allowed: []string{"platform/observability", "session", "evidencelake"}},
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
	carvedOut := dependencyCarveOuts(dependencyRules)
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
				s.True(importAllowed(relative, rule, carvedOut),
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
	s.False(importAllowed("platform/postgres", rule, nil), "unlisted import must be denied")
	s.False(importAllowed("example/transport/httpapi", rule, nil), "excluded subtree must be denied")
	s.True(importAllowed("session", rule, nil))
	s.True(importAllowed("session/sub", rule, nil))
	s.True(importAllowed("example/inner", rule, nil))
}

// TestImportAllowedDeniesOverGrantIntoCarveOut guards against a coarse allowed
// prefix (e.g. "teamnote") silently reaching into a subtree another rule
// carved out and owns (e.g. "teamnote/transport"). An allowed prefix that
// targets the carve-out directly, or a relative import outside it, is
// unaffected.
func (s *dependencySuite) TestImportAllowedDeniesOverGrantIntoCarveOut() {
	carvedOut := []string{"teamnote/transport", "pagewiki/transport"}
	coarseTeamnote := dependencyRule{directory: "platform", allowed: []string{"teamnote"}}
	s.False(importAllowed("teamnote/transport/httpapi/handler", coarseTeamnote, carvedOut),
		"allowed=teamnote must not reach into the carved-out teamnote/transport subtree")
	s.True(importAllowed("teamnote/extractor", coarseTeamnote, carvedOut),
		"allowed=teamnote must still cover teamnote/extractor, which is outside the carve-out")

	targetedTransport := dependencyRule{directory: "teamnote/transport",
		allowed: []string{"pagewiki/transport/httpapi"}}
	s.True(importAllowed("pagewiki/transport/httpapi/model", targetedTransport, carvedOut),
		"an allowed prefix that itself targets the carve-out must still be granted")
}

// dependencyCarveOuts derives the set of subtrees a rule excludes from its own
// directory because another rule owns them (e.g. "teamnote/transport" is
// carved out of the "teamnote" rule). Computed from dependencyRules itself so
// it cannot drift from the exclusions the rules already declare.
func dependencyCarveOuts(rules []dependencyRule) []string {
	carvedOut := make([]string, 0)
	for _, rule := range rules {
		for _, excluded := range rule.excluded {
			carvedOut = append(carvedOut, rule.directory+"/"+excluded)
		}
	}
	return carvedOut
}

func importAllowed(relative string, rule dependencyRule, carvedOut []string) bool {
	if hasPathPrefix(relative, rule.directory) {
		for _, excluded := range rule.excluded {
			if hasPathPrefix(relative, rule.directory+"/"+excluded) {
				return false
			}
		}
		return true
	}
	for _, allowed := range rule.allowed {
		if hasPathPrefix(relative, allowed) && !crossesCarveOut(relative, allowed, carvedOut) {
			return true
		}
	}
	return false
}

// crossesCarveOut reports whether granting the "allowed" prefix would reach
// "relative" only by cutting through a carved-out subtree that "allowed"
// does not itself lie inside — an over-grant into territory another rule
// owns exclusively.
func crossesCarveOut(relative, allowed string, carvedOut []string) bool {
	for _, carveOut := range carvedOut {
		if hasPathPrefix(relative, carveOut) && !hasPathPrefix(allowed, carveOut) {
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
