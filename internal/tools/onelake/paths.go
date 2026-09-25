package onelake

import (
	"net/url"
	"strings"
)

// validatePathForTraversal rejects ".", ".." and "~" segments, also when
// percent-encoded, ported from ValidatePathForTraversal. param is the
// upstream parameter name, reported the way .NET's ArgumentException does.
func validatePathForTraversal(path, param string) error {
	decoded, err := url.PathUnescape(path)
	if err != nil {
		decoded = path
	}
	for _, seg := range strings.FieldsFunc(decoded, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch strings.TrimSpace(seg) {
		case ".", "..", "~":
			return &argError{msg: "Path cannot contain directory traversal sequences. (Parameter '" + param + "')"}
		}
	}
	return nil
}

// isTopLevelFolder reports whether path starts with the Files or Tables folder.
func isTopLevelFolder(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return strings.EqualFold(first, "Tables") || strings.EqualFold(first, "Files")
}

// resolveDirectoryPath puts a path that isn't under Files or Tables under
// Files, as upstream does for backward compatibility.
func resolveDirectoryPath(path string) string {
	p := strings.TrimLeft(path, "/")
	if isTopLevelFolder(p) {
		return p
	}
	return "Files/" + p
}
