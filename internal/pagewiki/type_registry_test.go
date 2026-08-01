package pagewiki_test

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/pagewiki"
	"github.com/stretchr/testify/suite"
)

type TypeRegistrySuite struct {
	suite.Suite
}

func TestTypeRegistrySuite(t *testing.T) {
	suite.Run(t, new(TypeRegistrySuite))
}

func (s *TypeRegistrySuite) TestNormalizeFallsBackForUnknownValues() {
	registry := pagewiki.NewTypeRegistry(pagewiki.SeedTypeRegistryEntries())
	s.Equal(pagewiki.EntityTypeSystem, registry.NormalizeEntity(" System "))
	s.Equal(pagewiki.EntityTypeConcept, registry.NormalizeEntity("galaxy"))
	s.Equal(pagewiki.EntityTypeConcept, registry.NormalizeEntity(""))
	s.Equal(pagewiki.RelationTypeOwns, registry.NormalizeRelation("owns"))
	s.Equal(pagewiki.RelationTypeRelatesTo, registry.NormalizeRelation("bogus"))
}

func (s *TypeRegistrySuite) TestRetiredTypesFallBack() {
	entries := append(pagewiki.SeedTypeRegistryEntries(), pagewiki.TypeRegistryEntry{
		Kind: pagewiki.TypeKindEntity, Name: "incident", Status: pagewiki.TypeStatusRetired,
	})
	registry := pagewiki.NewTypeRegistry(entries)
	s.Equal(pagewiki.EntityTypeConcept, registry.NormalizeEntity("incident"))
}
