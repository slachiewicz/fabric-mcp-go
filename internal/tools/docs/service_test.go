package docs

import (
	"sort"
	"strings"
	"testing"
)

// TestBuildItemDefinitionPattern ports
// FabricPublicApiServiceTests.BuildItemDefinitionPattern_ConvertsCorrectly.
func TestBuildItemDefinitionPattern(t *testing.T) {
	cases := []struct {
		itemType string
		want     string
	}{
		{"notebook", `item-definitions/n-?o-?t-?e-?b-?o-?o-?k-definition\.md`},
		{"kqlDatabase", `item-definitions/k-?q-?l[-a-z]*?d-?a-?t-?a-?b-?a-?s-?e-definition\.md`},
		{"cosmosDbDatabase", `item-definitions/c-?o-?s-?m-?o-?s[-a-z]*?d-?b[-a-z]*?d-?a-?t-?a-?b-?a-?s-?e-definition\.md`},
		{"graphQLApi", `item-definitions/g-?r-?a-?p-?h[-a-z]*?q[-a-z]*?l[-a-z]*?a-?p-?i-definition\.md`},
		{"sparkjobdefinition", `item-definitions/s-?p-?a-?r-?k-?j-?o-?b-definition\.md`},
		{
			"mirroredAzureDatabricksCatalog",
			`item-definitions/m-?i-?r-?r-?o-?r-?e-?d[-a-z]*?a-?z-?u-?r-?e[-a-z]*?d-?a-?t-?a-?b-?r-?i-?c-?k-?s[-a-z]*?c-?a-?t-?a-?l-?o-?g-definition\.md`,
		},
	}
	for _, c := range cases {
		t.Run(c.itemType, func(t *testing.T) {
			if got := buildItemDefinitionPattern(c.itemType); got != c.want {
				t.Errorf("buildItemDefinitionPattern(%q) = %q, want %q", c.itemType, got, c.want)
			}
		})
	}
}

// TestItemDefinitionCamelCase ports
// FabricPublicApiServiceTests.GetItemDefinition_WithCamelCaseItemType_ReturnsDefinition:
// every one of these camelCase item types must resolve to some embedded
// item-definition file.
func TestItemDefinitionCamelCase(t *testing.T) {
	itemTypes := []string{
		"notebook",
		"kqlDatabase",
		"cosmosDbDatabase",
		"digitalTwinBuilder",
		"graphQLApi",
		"semanticModel",
		"sparkjobdefinition",
		"mirroredCatalog",
		"mirroredAzureDatabricksCatalog",
	}
	for _, itemType := range itemTypes {
		t.Run(itemType, func(t *testing.T) {
			def, err := itemDefinition(itemType)
			if err != nil {
				t.Fatalf("itemDefinition(%q): %v", itemType, err)
			}
			if def == "" {
				t.Errorf("itemDefinition(%q) returned empty content", itemType)
			}
		})
	}
}

// TestItemDefinitionAmbiguousMatch checks the "shortest match wins" rule
// from EmbeddedResourceHelper.FindEmbeddedResource: "mirroredCatalog" could
// match both "mirrored-catalog-definition.md" and
// "mirrored-azuredatabricks-unitycatalog-definition.md"; the shorter one
// must be chosen.
func TestItemDefinitionAmbiguousMatch(t *testing.T) {
	path, err := findEmbeddedResource(buildItemDefinitionPattern("mirroredCatalog"))
	if err != nil {
		t.Fatalf("findEmbeddedResource: %v", err)
	}
	if want := "item-definitions/mirrored-catalog-definition.md"; path != want {
		t.Errorf("findEmbeddedResource(mirroredCatalog pattern) = %q, want %q", path, want)
	}
}

// TestItemDefinitionUnknown ports the NotFound side of
// GetItemDefinitionCommandTests.GetItemDefinitionCommand_ExecuteAsync_WithInvalidItemType_ReturnsNotFound.
func TestItemDefinitionUnknown(t *testing.T) {
	if _, err := itemDefinition("this-item-type-does-not-exist"); err == nil {
		t.Fatal("itemDefinition(unknown item type) = nil error, want error")
	}
}

// TestTopicBestPractices ports
// FabricPublicApiServiceTests.GetTopicBestPractices_WithValidTopic_ReturnsPractices.
func TestTopicBestPractices(t *testing.T) {
	got, err := topicBestPractices("pagination")
	if err != nil {
		t.Fatalf("topicBestPractices(pagination): %v", err)
	}
	if len(got) != 1 || got[0] == "" {
		t.Errorf("topicBestPractices(pagination) = %v, want a single non-empty entry", got)
	}
}

// TestTopicBestPracticesUnknown ports the NotFound side of
// GetBestPracticesCommandTests.GetBestPracticesCommand_ExecuteAsync_WithInvalidTopic_ReturnsNotFound.
func TestTopicBestPracticesUnknown(t *testing.T) {
	if _, err := topicBestPractices("invalid-topic"); err == nil {
		t.Fatal("topicBestPractices(invalid-topic) = nil error, want error")
	}
}

// TestListItemTypesExcludesCommon ports
// CombinedWorkflowTests.ListItemTypes_DoesNotReturnCommon.
func TestListItemTypesExcludesCommon(t *testing.T) {
	types, err := listItemTypes()
	if err != nil {
		t.Fatalf("listItemTypes: %v", err)
	}
	if len(types) == 0 {
		t.Fatal("listItemTypes returned no item types")
	}
	for _, it := range types {
		if strings.EqualFold(it, "common") {
			t.Fatalf("listItemTypes returned %q, common should be filtered out", it)
		}
	}
}

// TestPublicAPIUnknownItemType ports
// GetItemApisCommandTests.GetApiSpecCommand_ExecuteAsync_WithHttpNotFoundError_ReturnsNotFound,
// adapted to what the embedded provider actually does: a missing
// swagger.json raises the same *notFoundError findEmbeddedResource always
// raises (confirmed against the live v1.4.0 reference binary - see
// handleExceptionEnvelope in handlers.go), not the HttpRequestException
// upstream's HTTP-only NotFound branch expects.
func TestPublicAPIUnknownItemType(t *testing.T) {
	_, err := publicAPI("this-item-type-does-not-exist")
	if err == nil {
		t.Fatal("publicAPI(unknown item type) = nil error, want error")
	}
	const want = "fabric-rest-api-specs/contents/this-item-type-does-not-exist/swagger.json"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("publicAPI(unknown item type) error = %q, want it to mention %q", err.Error(), want)
	}
}

// TestExamplesWithSubdirectories ports
// FabricPublicApiServiceTests.GetExamplesAsync_WithSubDirectories_ReturnsAllExamples
// against the real "lakehouse" examples tree, which is known to be flat
// (no subdirectories) - this asserts the flat case still round-trips, and
// TestListItemTypesThenGetDefinitionsAndExamples below exercises the
// recursive walk across every item type's examples directory.
func TestExamplesForKnownItemType(t *testing.T) {
	ex, err := examples("lakehouse")
	if err != nil {
		t.Fatalf("examples(lakehouse): %v", err)
	}
	if len(ex) == 0 {
		t.Fatal("examples(lakehouse) returned no examples")
	}
	for name, content := range ex {
		if content == "" {
			t.Errorf("examples(lakehouse)[%q] is empty", name)
		}
	}
}

// TestListItemTypesThenGetApisDefinitionsAndExamples ports
// CombinedWorkflowTests, which exercises every listed item type end to end
// against the real embedded resources (no mocks). It also reproduces the
// orphan accounting from
// ListItemTypes_ThenGetDefinitionForEach_ReturnsOrReportsNotFound: some
// item-definition files have no corresponding item type directory in
// fabric-rest-api-specs/contents, and upstream documents exactly which.
func TestListItemTypesThenGetApisDefinitionsAndExamples(t *testing.T) {
	itemTypes, err := listItemTypes()
	if err != nil {
		t.Fatalf("listItemTypes: %v", err)
	}
	if len(itemTypes) == 0 {
		t.Fatal("listItemTypes returned no item types")
	}

	matched := map[string]bool{}
	successCount := 0
	for _, itemType := range itemTypes {
		if _, err := publicAPI(itemType); err != nil {
			t.Errorf("publicAPI(%q): %v", itemType, err)
		}
		if _, err := examples(itemType); err != nil {
			t.Errorf("examples(%q): %v", itemType, err)
		}

		path, err := findEmbeddedResource(buildItemDefinitionPattern(itemType))
		if err == nil {
			matched[path] = true
			successCount++
		}
	}

	allDefinitions, err := allItemDefinitionPaths()
	if err != nil {
		t.Fatalf("allItemDefinitionPaths: %v", err)
	}
	if len(allDefinitions) == 0 {
		t.Fatal("expected embedded item-definition resources to exist")
	}
	if len(matched) != successCount {
		t.Fatalf("matched %d definitions but only %d itemDefinition calls succeeded", len(matched), successCount)
	}

	// dbtjob, hlscohort, orgapp and orgappaudience have no corresponding
	// item type API spec directory, so no item type's pattern can reach
	// them; upstream documents this as a known, accepted gap.
	knownOrphans := map[string]bool{
		"dbtjob-definition.md":         true,
		"hlscohort-definition.md":      true,
		"orgapp-definition.md":         true,
		"orgappaudience-definition.md": true,
	}
	var orphaned []string
	for _, path := range allDefinitions {
		if matched[path] {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if !knownOrphans[name] {
			orphaned = append(orphaned, path)
		}
	}
	sort.Strings(orphaned)
	if len(orphaned) != 0 {
		t.Fatalf("item-definition file(s) with no matching item type: %v", orphaned)
	}
}

// allItemDefinitionPaths lists every embedded item-definitions/*-definition.md
// resource, mirroring the assembly scan CombinedWorkflowTests performs
// directly against GetManifestResourceNames.
func allItemDefinitionPaths() ([]string, error) {
	names, err := listDir("item-definitions", fileResource)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasSuffix(strings.ToLower(n), "-definition.md") {
			paths = append(paths, "item-definitions/"+n)
		}
	}
	return paths, nil
}
