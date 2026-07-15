package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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

func (s *dependencySuite) TestProductDependencyDirection() {
	tests := []struct {
		name      string
		directory string
		forbidden []string
	}{
		{name: "session is product independent", directory: "session", forbidden: []string{"teamnote", "eval", "llmwiki"}},
		{name: "session lake is product independent", directory: "sessionlake", forbidden: []string{"teamnote", "eval", "llmwiki"}},
		{name: "team note is independent", directory: "teamnote", forbidden: []string{"eval", "llmwiki"}},
		{name: "LLM wiki is independent", directory: "llmwiki", forbidden: []string{"teamnote", "eval"}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			imports, err := productionImports(filepath.Join(s.root, test.directory))
			s.Require().NoError(err)
			for _, imported := range imports {
				for _, forbidden := range test.forbidden {
					s.False(hasModulePrefix(imported, modulePath+forbidden), "%s imports forbidden module %s", test.directory, imported)
				}
			}
		})
	}
}

func productionImports(directory string) ([]string, error) {
	imports := make([]string, 0)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Decls {
			declaration, ok := spec.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, item := range declaration.Specs {
				importSpec, ok := item.(*ast.ImportSpec)
				if !ok {
					continue
				}
				value, err := strconv.Unquote(importSpec.Path.Value)
				if err != nil {
					return err
				}
				imports = append(imports, value)
			}
		}
		return nil
	})
	return imports, err
}

func hasModulePrefix(imported, prefix string) bool {
	return imported == prefix || strings.HasPrefix(imported, prefix+"/")
}
