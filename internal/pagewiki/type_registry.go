package pagewiki

import (
	"sort"
	"strings"
)

// TypeKind distinguishes the two kinds of rows a TypeRegistry holds: entity
// types (what a Page is about) and relation types (how a PageLink relates
// its source and target).
type TypeKind string

const (
	TypeKindEntity   TypeKind = "entity"
	TypeKindRelation TypeKind = "relation"
)

// TypeStatus tracks a registry entry's lifecycle. Only "seed" and "active"
// entries normalize to their typed value; "candidate" and "retired" entries
// fall back to the untyped catch-all (EntityTypeConcept / RelationTypeRelatesTo).
type TypeStatus string

const (
	TypeStatusSeed      TypeStatus = "seed"
	TypeStatusCandidate TypeStatus = "candidate"
	TypeStatusActive    TypeStatus = "active"
	TypeStatusRetired   TypeStatus = "retired"
)

// TypeRegistryEntry is one row of the type registry: a named entity or
// relation type with a one-line description (fed into the planner prompt so
// it knows when the type applies) and a lifecycle status.
type TypeRegistryEntry struct {
	Kind        TypeKind
	Name        string
	Description string
	Status      TypeStatus
}

// SeedTypeRegistryEntries returns the built-in entity and relation types the
// registry is seeded with: 5 entity rows and 6 relation rows, all with
// Status "seed".
func SeedTypeRegistryEntries() []TypeRegistryEntry {
	return []TypeRegistryEntry{
		{
			Kind:        TypeKindEntity,
			Name:        string(EntityTypePerson),
			Description: "A named individual: a teammate, stakeholder, or external contact.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindEntity,
			Name:        string(EntityTypeSystem),
			Description: "A service, tool, repo, or piece of infrastructure the team builds or depends on.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindEntity,
			Name:        string(EntityTypeDecision),
			Description: "A choice the team made, with its rationale and the alternatives it ruled out.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindEntity,
			Name:        string(EntityTypeConvention),
			Description: "A recurring rule or practice the team follows (coding style, process, naming).",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindEntity,
			Name:        string(EntityTypeConcept),
			Description: "Fallback for anything that doesn't fit person, system, decision, or convention.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindRelation,
			Name:        string(RelationTypeOwns),
			Description: "The source is responsible for or maintains the target.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindRelation,
			Name:        string(RelationTypeDependsOn),
			Description: "The source requires the target to function.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindRelation,
			Name:        string(RelationTypePartOf),
			Description: "The source is a component or subset of the target.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindRelation,
			Name:        string(RelationTypeSupersedes),
			Description: "The source replaces or overrides the target.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindRelation,
			Name:        string(RelationTypeAffects),
			Description: "The source has an impact on the target without owning or replacing it.",
			Status:      TypeStatusSeed,
		},
		{
			Kind:        TypeKindRelation,
			Name:        string(RelationTypeRelatesTo),
			Description: "Fallback for a relation that doesn't fit owns, depends-on, part-of, supersedes, or affects.",
			Status:      TypeStatusSeed,
		},
	}
}

// TypeRegistry normalizes free-form entity/relation strings (as produced by
// an LLM) into typed values, falling back to the untyped catch-all for
// anything unregistered or whose registered status isn't seed/active.
type TypeRegistry struct {
	entities  map[string]TypeRegistryEntry
	relations map[string]TypeRegistryEntry
}

// NewTypeRegistry builds a TypeRegistry from entries, keyed by normalized
// (trimmed, lowercased) name within each kind. A later entry for the same
// (Kind, Name) overwrites an earlier one.
func NewTypeRegistry(entries []TypeRegistryEntry) TypeRegistry {
	registry := TypeRegistry{
		entities:  make(map[string]TypeRegistryEntry),
		relations: make(map[string]TypeRegistryEntry),
	}
	for _, entry := range entries {
		key := normalizeTypeKey(entry.Name)
		switch entry.Kind {
		case TypeKindEntity:
			registry.entities[key] = entry
		case TypeKindRelation:
			registry.relations[key] = entry
		}
	}
	return registry
}

// NormalizeEntity trims and lowercases value, then resolves it against
// registered entity types: a registered entry with status seed or active
// yields its typed EntityType, anything else (unregistered, candidate, or
// retired) falls back to EntityTypeConcept.
func (r TypeRegistry) NormalizeEntity(value string) EntityType {
	entry, found := r.entities[normalizeTypeKey(value)]
	if !found || !typeStatusIsUsable(entry.Status) {
		return EntityTypeConcept
	}
	return EntityType(entry.Name)
}

// NormalizeRelation trims and lowercases value, then resolves it against
// registered relation types: a registered entry with status seed or active
// yields its typed RelationType, anything else falls back to
// RelationTypeRelatesTo.
func (r TypeRegistry) NormalizeRelation(value string) RelationType {
	entry, found := r.relations[normalizeTypeKey(value)]
	if !found || !typeStatusIsUsable(entry.Status) {
		return RelationTypeRelatesTo
	}
	return RelationType(entry.Name)
}

// Entities returns the registered entity types in stable (name) order, for
// feeding the planner prompt.
func (r TypeRegistry) Entities() []TypeRegistryEntry {
	return sortedTypeRegistryEntries(r.entities)
}

// Relations returns the registered relation types in stable (name) order,
// for feeding the planner prompt.
func (r TypeRegistry) Relations() []TypeRegistryEntry {
	return sortedTypeRegistryEntries(r.relations)
}

func sortedTypeRegistryEntries(entries map[string]TypeRegistryEntry) []TypeRegistryEntry {
	sorted := make([]TypeRegistryEntry, 0, len(entries))
	for _, entry := range entries {
		sorted = append(sorted, entry)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted
}

func typeStatusIsUsable(status TypeStatus) bool {
	return status == TypeStatusSeed || status == TypeStatusActive
}

func normalizeTypeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
