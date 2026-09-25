package docs

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

// resources embeds the upstream Fabric.Mcp.Tools.Docs Resources tree
// unchanged: OpenAPI specs and examples under fabric-rest-api-specs/contents,
// item definition schemas under item-definitions, and best-practice topics
// as loose .md files at the root. Paths below "resources" match the
// upstream logical resource names exactly (e.g.
// "fabric-rest-api-specs/contents/lakehouse/swagger.json"), so the lookup
// logic in this package can be ported from
// Fabric.Mcp.Tools.Docs.Services.EmbeddedResourceProviderService verbatim.
//
//go:embed resources
var embeddedFS embed.FS

var resources = subFS(embeddedFS, "resources")

func subFS(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err) // unreachable: dir is embedded immediately above
	}
	return sub
}

// resourceKind filters listDir to files, directories, or both. It mirrors
// the upstream ResourceType enum (File, Directory, or unset for both).
type resourceKind int

const (
	anyResource resourceKind = iota
	fileResource
	dirResource
)

// listDir lists the direct children of dir, matching
// EmbeddedResourceProviderService.ListResourcesInPath. Because "resources"
// is a real directory tree rather than a flat assembly manifest, this is a
// plain fs.ReadDir instead of upstream's prefix scan over resource names;
// the observable behavior is the same. A directory that doesn't exist
// yields an empty list rather than an error, since upstream's prefix scan
// never errors on an unmatched path.
func listDir(dir string, kind resourceKind) ([]string, error) {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	}
	entries, err := fs.ReadDir(resources, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		switch kind {
		case fileResource:
			if e.IsDir() {
				continue
			}
		case dirResource:
			if !e.IsDir() {
				continue
			}
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// notFoundError reproduces the .NET ArgumentException that
// Microsoft.Mcp.Core.Helpers.EmbeddedResourceHelper.FindEmbeddedResource
// throws when no embedded resource matches a pattern, including .NET's
// automatic "(Parameter '<paramName>')" suffix on ArgumentException.Message.
// Callers that don't catch this specifically (item-api-spec,
// platform-api-spec) let it surface through the same generic-exception
// envelope upstream falls back to; see handleExceptionResult in
// handlers.go. Callers that do catch it (item-definitions, best-practices)
// discard this text for their own NotFound message instead.
type notFoundError struct {
	pattern string
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("No resources match pattern '%s'. (Parameter 'resourcePattern')", e.pattern)
}

// findEmbeddedResource ports
// Microsoft.Mcp.Core.Helpers.EmbeddedResourceHelper.FindEmbeddedResource:
// pattern is an unanchored regular expression matched against every
// embedded resource's logical path. When several match, the shortest
// (most specific) path wins, exactly as upstream orders by name length.
func findEmbeddedResource(pattern string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid resource pattern %q: %w", pattern, err)
	}
	var matches []string
	err = fs.WalkDir(resources, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && re.MatchString(path) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", &notFoundError{pattern: pattern}
	}
	sort.SliceStable(matches, func(i, j int) bool { return len(matches[i]) < len(matches[j]) })
	return matches[0], nil
}

// readResource ports EmbeddedResourceProviderService.GetResource /
// GetEmbeddedResource: resourceName is matched as a pattern via
// findEmbeddedResource and the winning resource is read. This is upstream's
// actual behavior for every single-resource read, not just the
// item-definitions/best-practices lookups that are visibly pattern-based:
// even a literal path like
// "fabric-rest-api-specs/contents/lakehouse/swagger.json" goes through the
// same FindEmbeddedResource regex match (it just happens to be specific
// enough to match only itself).
func readResource(resourceName string) (string, error) {
	path, err := findEmbeddedResource(resourceName)
	if err != nil {
		return "", err
	}
	b, err := fs.ReadFile(resources, path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	return string(b), nil
}

// contains reports whether names holds target, matching the C#
// IEnumerable.Contains ordinal comparison used in
// FabricPublicApiService.GetSpecDefinitionsAsync.
func contains(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}
