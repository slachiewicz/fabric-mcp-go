package docs

import (
	"strings"
	"unicode"
)

// Path segments and file names ported from FabricPublicApiService's
// constants. They describe the layout upstream's fabric-rest-api-specs
// mirror uses, which the resources/ tree here reproduces exactly.
const (
	basePath            = "fabric-rest-api-specs/contents/"
	specFileName        = "swagger.json"
	definitionsFileName = "definitions.json"
	definitionsDirName  = "definitions/"
	examplesDirName     = "examples/"
)

// fabricPublicApi is the OpenAPI spec plus any referenced model definitions
// for one Fabric item type or the platform. Field names mirror upstream's
// Fabric.Mcp.Tools.Docs.Models.FabricPublicApi: System.Text.Json serializes
// C# positional record parameters using their declared (already camelCase)
// names, so the JSON keys are ported verbatim.
type fabricPublicApi struct {
	APISpecification    string            `json:"apiSpecification"`
	APIModelDefinitions map[string]string `json:"apiModelDefinitions"`
}

// listItemTypes ports FabricPublicApiService.ListItemTypesAsync: the
// directories under fabric-rest-api-specs/contents, minus "common", which
// holds shared schema fragments rather than an item type of its own.
func listItemTypes() ([]string, error) {
	dirs, err := listDir(basePath, dirResource)
	if err != nil {
		return nil, err
	}
	types := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if !strings.EqualFold(d, "common") {
			types = append(types, d)
		}
	}
	return types, nil
}

// publicAPI ports FabricPublicApiService.GetPublicApis: itemType's
// swagger.json plus any definitions found beside it.
//
// The caller is responsible for the special-cased "common" rejection that
// upstream's GetItemApisCommand performs before ever reaching the service.
// Beyond that, neither GetItemApisCommand nor GetPlatformApisCommand catch
// the *notFoundError a missing swagger.json produces here - upstream only
// catches HttpRequestException, which the embedded provider never raises -
// so an unknown item type propagates to the caller and surfaces through the
// generic exception envelope (see handleExceptionResult in handlers.go),
// not a bespoke "item type not found" message.
func publicAPI(itemType string) (fabricPublicApi, error) {
	spec, err := readResource(basePath + itemType + "/" + specFileName)
	if err != nil {
		return fabricPublicApi{}, err
	}
	defs, err := specDefinitions(itemType)
	if err != nil {
		return fabricPublicApi{}, err
	}
	return fabricPublicApi{APISpecification: spec, APIModelDefinitions: defs}, nil
}

// specDefinitions ports FabricPublicApiService.GetSpecDefinitionsAsync.
//
// Upstream checks the directory listing for a literal "definitions.json"
// file and a literal "definitions/" directory entry. Both the embedded and
// network resource providers list directory entries without a trailing
// slash, so the "definitions/" check can never match in practice; that
// dead branch is ported as-is for behavioral parity rather than "fixed".
func specDefinitions(itemType string) (map[string]string, error) {
	dir := basePath + itemType + "/"
	content, err := listDir(dir, anyResource)
	if err != nil {
		return nil, err
	}
	res := map[string]string{}
	if contains(content, definitionsFileName) {
		v, err := readResource(dir + definitionsFileName)
		if err != nil {
			return nil, err
		}
		res[definitionsFileName] = v
	}
	if contains(content, definitionsDirName) { // unreachable, see doc comment
		names, err := listDir(dir+definitionsDirName, anyResource)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			// Upstream reads from "definitions/<name>" relative to the
			// resource root rather than the item type's own directory;
			// ported as-is alongside the dead branch above.
			v, err := readResource(definitionsDirName + name)
			if err != nil {
				return nil, err
			}
			res[definitionsDirName+name] = v
		}
	}
	return res, nil
}

// examples ports FabricPublicApiService.GetExamplesAsync.
func examples(itemType string) (map[string]string, error) {
	return examplesInDir(basePath + itemType + "/" + examplesDirName)
}

// examplesInDir ports GetExamplesFromDirectoryAsync: every file under dir,
// recursively, keyed by its path relative to dir. Nested keys are the
// upstream subdirectory name concatenated directly with the file name,
// with no separator - a quirk of the original code kept for parity (see
// GetExamplesAsync_WithSubDirectories_ReturnsAllExamples upstream).
func examplesInDir(dir string) (map[string]string, error) {
	res := map[string]string{}
	files, err := listDir(dir, fileResource)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		content, err := readResource(dir + f)
		if err != nil {
			return nil, err
		}
		res[f] = content
	}
	subdirs, err := listDir(dir, dirResource)
	if err != nil {
		return nil, err
	}
	for _, sub := range subdirs {
		nested, err := examplesInDir(dir + sub + "/")
		if err != nil {
			return nil, err
		}
		for f, content := range nested {
			res[sub+f] = content
		}
	}
	return res, nil
}

// itemDefinition ports FabricPublicApiService.GetItemDefinition. Unlike
// publicAPI, GetItemDefinitionCommand does catch the ArgumentException a
// missing match produces and replaces it with its own NotFound message, so
// the caller only needs to know that itemDefinition failed, not why.
func itemDefinition(itemType string) (string, error) {
	return readResource(buildItemDefinitionPattern(itemType))
}

// topicBestPractices ports FabricPublicApiService.GetTopicBestPractices:
// upstream always returns a single-element slice holding the matched
// resource's content. As with itemDefinition, GetBestPracticesCommand
// catches the underlying ArgumentException and supplies its own message.
func topicBestPractices(topic string) ([]string, error) {
	content, err := readResource(topic)
	if err != nil {
		return nil, err
	}
	return []string{content}, nil
}

// buildItemDefinitionPattern ports
// FabricPublicApiService.BuildItemDefinitionPattern.
//
// Item type names from directory listings are camelCase (e.g.
// "kqlDatabase"), while item definition files use kebab-case (e.g.
// "kql-database-definition.md"). The returned regex absorbs three kinds of
// naming mismatch:
//
//  1. camelCase boundaries -> optional hyphens plus extra kebab-case
//     segments (e.g. "mirroredAzureDatabricksCatalog" matches
//     "mirrored-azuredatabricks-unitycatalog")
//  2. all-lowercase item type names -> optional hyphens between every
//     character (e.g. "sparkjob" matches "spark-job")
//  3. a trailing "definition" in the item type name is stripped, since the
//     file suffix already includes "-definition.md" (e.g.
//     "sparkjobdefinition" -> "sparkjob")
func buildItemDefinitionPattern(itemType string) string {
	// Strip a trailing "definition": the file naming convention already
	// includes "-definition.md".
	if strings.HasSuffix(strings.ToLower(itemType), "definition") {
		itemType = itemType[:len(itemType)-len("definition")]
	}

	var b strings.Builder
	b.WriteString("item-definitions/")
	for i, r := range itemType {
		if i > 0 {
			if unicode.IsUpper(r) {
				// At camelCase boundaries, allow extra lowercase
				// characters and hyphens for naming inconsistencies
				// (e.g. "AzureDatabricksCatalog" matching
				// "azuredatabricks-unitycatalog").
				b.WriteString("[-a-z]*?")
			} else {
				// Between consecutive lowercase characters, allow an
				// optional hyphen for kebab-case variants (e.g.
				// "sparkjob" matching "spark-job").
				b.WriteString("-?")
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	b.WriteString(`-definition\.md`)
	return b.String()
}
