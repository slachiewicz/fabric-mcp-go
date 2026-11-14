package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports upstream's item definition support: the definition
// envelope validator, inline and saved-file input, local export, and the
// get-item-definition and update-item-definition commands.

// Definition budgets: FabricItemDefinitionFileLimits defaults.
const (
	maxParts            = 1024
	maxPartBytes        = 16 * 1024 * 1024
	maxTotalBytes       = 32 * 1024 * 1024
	maxDefinitionJSON   = 64 * 1024 * 1024 // FabricCoreService.MaxDefinitionJsonBytes
	maxDefinitionInput  = 4*((maxTotalBytes+2*maxParts)/3) + 16384*maxParts + 1024
	maxEncodedPart      = 4 * ((maxPartBytes + 2) / 3)
	definitionJSONDepth = 8
)

type definitionPart struct {
	Path        *string `json:"path"`
	Payload     *string `json:"payload"`
	PayloadType *string `json:"payloadType"`
}

// definition ports FabricItemDefinition.
type definition struct {
	Parts  []*definitionPart `json:"parts"`
	Format *string           `json:"format,omitempty"`
}

// argMsg formats a .NET ArgumentException message with its parameter name.
func argMsg(msg, param string) string { return msg + " (Parameter '" + param + "')" }

func nullOrWhiteSpace(s *string, param string) error {
	switch {
	case s == nil:
		return message(argMsg("Value cannot be null.", param))
	case isBlank(*s):
		return message(argMsg("The value cannot be an empty string or composed entirely of whitespace.", param))
	}
	return nil
}

// message is an error whose text is an upstream exception message.
type message string

func (m message) Error() string { return string(m) }

// errBudget is FabricItemDefinitionValidator's InvalidDataException.
type errBudget struct{ msg string }

func (e errBudget) Error() string { return e.msg }

func validateCount(n int) error {
	if n == 0 || n > maxParts {
		return message("A definition must have a nonempty part list within the configured part-count budget.")
	}
	return nil
}

func validateFormat(format *string) error {
	if format != nil && isBlank(*format) {
		return message(argMsg("The value cannot be an empty string or composed entirely of whitespace.", "format"))
	}
	return nil
}

var deviceNames = []string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$"}

func isDeviceName(name string) bool {
	for _, d := range deviceNames {
		if strings.EqualFold(name, d) {
			return true
		}
	}
	r := []rune(name)
	if len(r) == 4 && (strings.EqualFold(string(r[:3]), "COM") || strings.EqualFold(string(r[:3]), "LPT")) {
		return r[3] >= '1' && r[3] <= '9' || r[3] == '¹' || r[3] == '²' || r[3] == '³'
	}
	return false
}

// validatePart ports FabricItemDefinitionPath.ValidatePart.
func validatePart(path string) error {
	if err := nullOrWhiteSpace(&path, "path"); err != nil {
		return err
	}
	if utf16Len(path) > 1024 {
		return message(argMsg("A definition path cannot exceed 1024 characters.", "path"))
	}
	bad := message(argMsg("Definition paths must be portable relative file paths with '/' separators and no traversal.", "path"))
	for _, seg := range strings.Split(path, "/") {
		stem := strings.TrimRight(strings.SplitN(seg, ".", 2)[0], " ")
		if seg == "" || utf16Len(seg) > 255 || seg == "." || seg == ".." ||
			strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, ".") || isDeviceName(stem) {
			return bad
		}
		for _, c := range seg {
			if unicode.IsControl(c) || strings.ContainsRune(`\:<>"|?*`, c) {
				return bad
			}
		}
	}
	return nil
}

// validatePaths ports FabricItemDefinitionValidator.ValidatePaths.
func validatePaths(paths []*string) error {
	files, dirs := map[string]bool{}, map[string]bool{}
	for _, p := range paths {
		if err := nullOrWhiteSpace(p, "path"); err != nil {
			return err
		}
		if err := validatePart(*p); err != nil {
			return err
		}
		key := strings.ToLower(*p)
		if files[key] || dirs[key] {
			return message("Definition part paths must be unique and cannot collide with directories.")
		}
		files[key] = true
		for i := strings.IndexByte(key, '/'); i >= 0; i = nextSlash(key, i) {
			parent := key[:i]
			if files[parent] {
				return message("A definition part cannot also be the parent directory of another part.")
			}
			dirs[parent] = true
		}
	}
	return nil
}

func nextSlash(s string, i int) int {
	j := strings.IndexByte(s[i+1:], '/')
	if j < 0 {
		return -1
	}
	return i + 1 + j
}

// decodeBase64 ports Base64.IsValid plus decoding: whitespace is ignored.
func decodeBase64(s string) ([]byte, bool) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	b, err := base64.StdEncoding.DecodeString(s)
	return b, err == nil
}

// validateDefinition ports FabricItemDefinitionValidator.Validate.
func validateDefinition(d *definition) error {
	if d.Parts == nil {
		return message(argMsg("Value cannot be null.", "definition.Parts"))
	}
	if err := validateCount(len(d.Parts)); err != nil {
		return err
	}
	if err := validateFormat(d.Format); err != nil {
		return err
	}
	paths := make([]*string, len(d.Parts))
	for i, p := range d.Parts {
		if p != nil {
			paths[i] = p.Path
		}
	}
	if err := validatePaths(paths); err != nil {
		return err
	}
	var total int64
	for _, p := range d.Parts {
		if p.PayloadType == nil || *p.PayloadType != "InlineBase64" || p.Payload == nil {
			return message("Every definition part must have an InlineBase64 payload.")
		}
		if utf16Len(*p.Payload) > maxEncodedPart {
			return errBudget{"A definition part exceeds the encoded payload budget."}
		}
		b, ok := decodeBase64(*p.Payload)
		if !ok {
			return message("A definition part contains invalid Base64.")
		}
		var err error
		if total, err = addBytes(total, int64(len(b))); err != nil {
			return err
		}
	}
	return nil
}

func addBytes(total, part int64) (int64, error) {
	if part > maxPartBytes || total+part > maxTotalBytes {
		return 0, errBudget{"The definition exceeds the per-part or total decoded byte budget."}
	}
	return total + part, nil
}

// --- strict input JSON ---

// strictObject decodes a JSON object with exactly-named, non-duplicate
// keys, as upstream's input JSON contexts do (UnmappedMemberHandling
// Disallow, RespectRequiredConstructorParameters, AllowDuplicateProperties
// false). It returns nil for a JSON null.
func strictObject(raw json.RawMessage, allowed, required []string) (map[string]json.RawMessage, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, message("not an object")
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := k.(string)
		if _, dup := out[key]; dup || !contains(allowed, key) {
			return nil, message("unexpected property")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out[key] = v
	}
	for _, r := range required {
		if _, ok := out[r]; !ok {
			return nil, message("missing property")
		}
	}
	return out, nil
}

// optString decodes a JSON string or null.
func optString(raw json.RawMessage) (*string, error) {
	if raw == nil || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// depthOK reports whether JSON nests at most max levels.
func depthOK(b []byte, max int) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			if depth++; depth > max {
				return false
			}
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
}

// parseDefinitionInput ports deserializing --definition with
// FabricItemDefinitionInputJsonContext.
func parseDefinitionInput(s string) (*definition, error) {
	b := []byte(s)
	if !json.Valid(b) || !depthOK(b, definitionJSONDepth) {
		return nil, message("invalid")
	}
	obj, err := strictObject(b, []string{"parts", "format"}, []string{"parts"})
	if err != nil || obj == nil {
		return nil, err
	}
	d := &definition{}
	if d.Format, err = optString(obj["format"]); err != nil {
		return nil, err
	}
	if string(obj["parts"]) == "null" {
		return d, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(obj["parts"], &parts); err != nil {
		return nil, err
	}
	d.Parts = []*definitionPart{}
	for _, raw := range parts {
		po, err := strictObject(raw, []string{"path", "payload", "payloadType"}, []string{"path", "payload", "payloadType"})
		if err != nil {
			return nil, err
		}
		if po == nil {
			d.Parts = append(d.Parts, nil)
			continue
		}
		p := &definitionPart{}
		if p.Path, err = optString(po["path"]); err != nil {
			return nil, err
		}
		if p.Payload, err = optString(po["payload"]); err != nil {
			return nil, err
		}
		if p.PayloadType, err = optString(po["payloadType"]); err != nil {
			return nil, err
		}
		d.Parts = append(d.Parts, p)
	}
	return d, nil
}

type definitionFile struct {
	PartPath   *string
	SourcePath *string
}

// parseDefinitionFiles ports deserializing --definition-files.
func parseDefinitionFiles(s string) ([]*definitionFile, bool, error) {
	b := []byte(s)
	if !json.Valid(b) || !depthOK(b, definitionJSONDepth) {
		return nil, false, message("invalid")
	}
	if strings.TrimSpace(s) == "null" {
		return nil, true, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, false, err
	}
	out := []*definitionFile{}
	for _, raw := range items {
		o, err := strictObject(raw, []string{"partPath", "sourcePath"}, []string{"partPath"})
		if err != nil {
			return nil, false, err
		}
		if o == nil {
			out = append(out, nil)
			continue
		}
		f := &definitionFile{}
		if f.PartPath, err = optString(o["partPath"]); err != nil {
			return nil, false, err
		}
		if f.SourcePath, err = optString(o["sourcePath"]); err != nil {
			return nil, false, err
		}
		out = append(out, f)
	}
	return out, false, nil
}

// definitionInput is DefinitionInputOptions.
type definitionInput struct {
	definition, directory, files, format *string
}

func (d definitionInput) hasInput() bool {
	return d.definition != nil || d.directory != nil || d.files != nil || d.format != nil
}

// inputError is FabricItemDefinitionInputException.
func inputError(msg string) *failure { return &failure{kind: kInput, cls: "definitionInput", msg: msg} }

// itemInputError is FabricItemInputException.
func itemInputError(msg string) *failure { return &failure{kind: kInput, cls: "itemInput", msg: msg} }

// readDefinitionInput ports FabricItemDefinitionInputService.ReadAsync.
func (a *Area) readDefinitionInput(in definitionInput) (*definition, error) {
	local := in.directory != nil || in.files != nil || in.format != nil
	if in.definition != nil && local {
		return nil, inputError("Choose either --definition or --definition-directory with --definition-files and optional --definition-format, not both.")
	}
	if local && !a.localFiles {
		return nil, inputError("Local definition files are unavailable on this host. Supply inline --definition JSON instead.")
	}
	if local && (in.directory == nil || isBlank(*in.directory) || in.files == nil || isBlank(*in.files)) {
		return nil, inputError("Saved-file input requires both an absolute --definition-directory and an explicit --definition-files JSON array.")
	}
	if in.definition == nil && !local {
		return nil, nil
	}
	var d *definition
	if in.definition != nil {
		const guidance = "--definition must be a JSON object with parts and optional format, not a file path or a request envelope."
		if utf16Len(*in.definition) > maxDefinitionInput {
			return nil, inputError("Definition input JSON exceeds the configured packaging budget.")
		}
		var err error
		if d, err = parseDefinitionInput(*in.definition); err != nil {
			return nil, inputError(guidance + " Required fields must be present; unknown and duplicate fields are rejected.")
		}
		if d == nil {
			return nil, inputError(guidance)
		}
	} else {
		const guidance = "--definition-files must be a JSON array of objects with partPath and optional sourcePath."
		if utf16Len(*in.files) > maxDefinitionInput {
			return nil, inputError("Definition input JSON exceeds the configured packaging budget.")
		}
		files, null, err := parseDefinitionFiles(*in.files)
		if err != nil {
			return nil, inputError(guidance + " Required fields must be present; unknown and duplicate fields are rejected.")
		}
		if null {
			return nil, inputError(guidance)
		}
		paths := make([]*string, len(files))
		for i, f := range files {
			if f != nil {
				paths[i] = f.PartPath
			}
		}
		if err := validateCount(len(files)); err != nil {
			return nil, inputError(err.Error())
		}
		if err := validateFormat(in.format); err != nil {
			return nil, inputError(err.Error())
		}
		if err := validatePaths(paths); err != nil {
			return nil, inputError(err.Error())
		}
		if d, err = readDefinitionFiles(*in.directory, files, in.format); err != nil {
			return nil, inputError("Cannot package the selected definition files. Use an existing absolute local directory and portable relative file paths, " +
				"without links, traversal, UNC/device paths, or duplicates; save the files and keep them within the configured size limits.")
		}
	}
	if err := validateDefinition(d); err != nil {
		return nil, inputError(err.Error())
	}
	return d, nil
}

// --- local files ---

// localDirectory ports FabricItemDefinitionPath.GetDirectory.
func localDirectory(path string) (string, error) {
	if isBlank(path) || !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || strings.ContainsRune(path, 0) {
		return "", message("Select an absolute local directory, not a relative, UNC, or device path.")
	}
	dir := filepath.Clean(path)
	if strings.HasPrefix(dir, "//") {
		return "", message("The normalized directory must be a local path, not a UNC or device path.")
	}
	root := filepath.VolumeName(dir) + string(filepath.Separator)
	if dir == root || len(dir) <= len(root) {
		return "", message("Select an item directory rather than a filesystem root.")
	}
	if err := validatePart(filepath.ToSlash(dir[len(root):])); err != nil {
		return "", err
	}
	return dir, nil
}

// ensureNoLinks ports FabricItemDefinitionPath.EnsureNoLinks: no
// component of path, including its ancestors, may be a link or device.
func ensureNoLinks(path string) error {
	for cur := path; ; {
		fi, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeDevice|os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
			return message("Symbolic links, junctions, and device paths are not supported for definition files.")
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil
		}
		cur = parent
	}
}

func ensureDirectory(path string) error {
	if err := ensureNoLinks(path); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return message("The selected path must be a directory.")
	}
	return nil
}

func ensureNewDestination(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return message("The output destination already exists. Select a new directory; existing files are never overwritten.")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// localFile ports FabricItemDefinitionPath.GetFile.
func localFile(dir, rel string) (string, error) {
	if err := validatePart(rel); err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// readDefinitionFiles ports FabricItemDefinitionFileService.ReadAsync.
func readDefinitionFiles(directory string, files []*definitionFile, format *string) (*definition, error) {
	dir, err := localDirectory(directory)
	if err != nil {
		return nil, err
	}
	if err := ensureDirectory(dir); err != nil {
		return nil, err
	}
	type snapshot struct {
		path string
		fi   os.FileInfo
	}
	var snaps []snapshot
	d := &definition{Format: format}
	var total int64
	for _, f := range files {
		src := f.PartPath
		if f.SourcePath != nil {
			src = f.SourcePath
		}
		path, err := localFile(dir, strings.ReplaceAll(*src, `\`, "/"))
		if err != nil {
			return nil, err
		}
		if err := ensureNoLinks(path); err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		fi, err := file.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			_ = file.Close()
			return nil, message("not a regular file")
		}
		if total, err = addBytes(total, fi.Size()); err != nil {
			_ = file.Close()
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(file, fi.Size()+1))
		_ = file.Close()
		if err != nil || int64(len(b)) != fi.Size() {
			return nil, message("A selected definition file changed while it was being read. Save the files and try again.")
		}
		payload, typ := base64.StdEncoding.EncodeToString(b), "InlineBase64"
		d.Parts = append(d.Parts, &definitionPart{Path: f.PartPath, Payload: &payload, PayloadType: &typ})
		snaps = append(snaps, snapshot{path, fi})
	}
	for _, s := range snaps {
		if err := ensureNoLinks(s.path); err != nil {
			return nil, err
		}
		fi, err := os.Stat(s.path)
		if err != nil || fi.Size() != s.fi.Size() || !fi.ModTime().Equal(s.fi.ModTime()) {
			return nil, message("A selected definition file changed during packaging. Save the files and try again.")
		}
	}
	return d, nil
}

// exportError is FabricItemDefinitionExportException.
func exportError(msg string, status int) *failure {
	return &failure{kind: kOther, cls: "export", status: status, msg: msg}
}

// exportDestination ports FabricItemDefinitionExportService.ValidateDestination.
func (a *Area) exportDestination(dir *string) (string, error) {
	if dir == nil {
		return "", nil
	}
	if !a.localFiles {
		return "", exportError("Local definition export is unavailable on this host. Omit --output-directory to retrieve inline content.", http.StatusBadRequest)
	}
	dest, err := localDirectory(*dir)
	if err == nil {
		if err = ensureDirectory(filepath.Dir(dest)); err == nil {
			err = ensureNewDestination(dest)
		}
	}
	if err != nil {
		return "", exportError("Invalid definition export destination. Select a new absolute local directory below an existing parent, "+
			"using portable paths without links, UNC/device paths, or an existing destination. No files were saved.", http.StatusBadRequest)
	}
	return dest, nil
}

// exportDefinition ports FabricItemDefinitionFileService.WriteAsync behind
// the export service's error mapping.
func exportDefinition(dest string, d *definition) (string, error) {
	out, err := writeDefinition(dest, d)
	if err != nil {
		return "", exportError("The definition was retrieved, but local export failed. No completed directory was published. "+
			"Check local permissions, free space, path and size limits, and destination collisions before retrying.", http.StatusInternalServerError)
	}
	return out, nil
}

func writeDefinition(dest string, d *definition) (string, error) {
	if err := validateDefinition(d); err != nil {
		return "", err
	}
	dir, err := localDirectory(dest)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(dir)
	if err := ensureDirectory(parent); err != nil {
		return "", err
	}
	if err := ensureNewDestination(dir); err != nil {
		return "", err
	}
	decoded := make([][]byte, len(d.Parts))
	for i, p := range d.Parts {
		b, ok := decodeBase64(*p.Payload)
		if !ok {
			return "", message("invalid Base64")
		}
		decoded[i] = b
	}
	staging := filepath.Join(parent, ".fabric-definition-"+strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err := ensureNewDestination(staging); err != nil {
		return "", err
	}
	if err := os.Mkdir(staging, 0o755); err != nil {
		return "", err
	}
	publish := func() error {
		for i, p := range d.Parts {
			out, err := localFile(staging, *p.Path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := ensureDirectory(filepath.Dir(out)); err != nil {
				return err
			}
			f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			_, werr := f.Write(decoded[i])
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return werr
			}
		}
		if err := ensureDirectory(staging); err != nil {
			return err
		}
		if err := ensureNewDestination(dir); err != nil {
			return err
		}
		return os.Rename(staging, dir)
	}
	if err := publish(); err != nil {
		if ensureDirectory(staging) == nil {
			_ = os.RemoveAll(staging)
		}
		return "", err
	}
	return dir, nil
}

// --- get-item-definition ---

const invalidDefinitionMessage = "Fabric returned an invalid item definition. Expected a definition envelope with nonempty parts, " +
	"portable unique paths, InlineBase64 payloads, and content within the configured byte/count limits."

// readItemDefinition ports FabricCoreService.ReadItemDefinition.
func readItemDefinition(v json.RawMessage) (*definition, error) {
	bad := opFailure("InvalidItemDefinition", invalidDefinitionMessage, 0, nil)
	if !bytes.HasPrefix(bytes.TrimSpace(v), []byte("{")) || noDuplicateKeys(v) != nil {
		return nil, bad
	}
	var resp struct {
		Definition *definition `json:"definition"`
	}
	if json.Unmarshal(v, &resp) != nil || resp.Definition == nil || validateDefinition(resp.Definition) != nil {
		return nil, bad
	}
	return resp.Definition, nil
}

// noDuplicateKeys rejects JSON objects with repeated property names.
func noDuplicateKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				if seen[k.(string)] {
					return message("duplicate property")
				}
				seen[k.(string)] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case json.Delim('['):
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	return walk()
}

// definitionReceipt ports FabricCoreService.DefinitionReceipt.
func definitionReceipt(r *receipt) *receipt {
	if r == nil || r.Issue == nil {
		return r
	}
	var msg string
	switch r.Issue.Code {
	case "MissingOperationId":
		msg = "Fabric accepted definition retrieval but returned no usable operation ID. Completion is unconfirmed."
	case "OperationFailed":
		msg = "Fabric reports that definition retrieval failed. Inspect the safe error code and request ID."
	case "WaitBudgetExpired":
		if r.Status == opResultUnavailable {
			msg = "The operation succeeded, but its definition result was not retrieved within the local wait budget."
		} else {
			msg = "The local wait budget expired. Definition retrieval may still be running; the Fabric operation was not canceled."
		}
	case "CallerCanceled":
		msg = "The caller canceled the local wait, not the Fabric operation."
	case "ReadTimedOut":
		msg = "The operation read timed out; the Fabric operation was not canceled."
	case "InvalidItemDefinition":
		msg = invalidDefinitionMessage
	case "InvalidJson":
		msg = "Fabric returned malformed operation JSON. No content was fabricated or truncated."
	case "ResultTooLarge":
		if r.Status == opTrackingStopped {
			msg = "Operation state JSON exceeds the 1 MiB (1,048,576 byte) limit. No content was truncated."
		} else {
			msg = "Definition JSON exceeds the 64 MiB (67,108,864 byte) limit. No content was truncated."
		}
	case "UnsupportedResultMediaType":
		msg = "Fabric operation reads require application/json. Binary and other media types are not downloaded."
	case "MissingResult":
		msg = "Fabric reported completion but did not return the required definition JSON."
	case "InvalidOperationId":
		msg = "Fabric returned inconsistent operation identification. Use only the original operation ID."
	case "InvalidOperationState":
		msg = "Fabric returned an invalid operation state. Completion is unconfirmed."
	default:
		msg = "Definition retrieval or tracking could not be completed. Check Fabric availability, operation state, and read/write item permissions."
	}
	msg += " No definition was returned or saved."
	if r.OperationID != nil {
		msg += " Resume with get-operation-state; when Succeeded, use get-item-definition with --operation-id instead of workspace/item IDs to read or export the result without another POST."
	}
	out := *r
	issue := *r.Issue
	issue.Message = msg
	out.Issue = &issue
	return &out
}

const invalidWaitMessage = "The maximum wait must be finite, greater than zero, and at most 600 seconds. (Parameter 'MaxWaitSeconds')"

// waitFrom applies upstream's option defaults.
func waitFrom(sync *bool, maxWait *float64, early *bool) waitOptions {
	w := waitOptions{maxWait: defaultMaxWait, early: true}
	if sync != nil {
		w.sync = *sync
	}
	if maxWait != nil {
		w.maxWait = *maxWait
	}
	if early != nil {
		w.early = *early
	}
	return w
}

// definitionGetIDs ports ItemDefinitionGetInput.Validate.
func definitionGetIDs(in getItemDefinitionInput, w waitOptions) (ws, item, op *uuid.UUID, err error) {
	if !validBudget(w.maxWait) {
		return nil, nil, nil, message(invalidWaitMessage)
	}
	if in.OperationID != nil {
		id, ok := parseID(*in.OperationID)
		if !ok {
			return nil, nil, nil, message("--operation-id must be a nonempty UUID from an earlier definition retrieval.")
		}
		if in.WorkspaceID != nil || in.ItemID != nil || in.Format != nil || w.sync {
			return nil, nil, nil, message("--operation-id reads a completed definition result once and cannot be combined with --workspace-id, --item-id, --format, or --sync true.")
		}
		return nil, nil, &id, nil
	}
	var w1, i1 uuid.UUID
	var ok bool
	if in.WorkspaceID == nil {
		ok = false
	} else {
		w1, ok = parseID(*in.WorkspaceID)
	}
	if !ok {
		return nil, nil, nil, message("Provide a nonempty workspace UUID using --workspace-id; names are not resolved.")
	}
	if in.ItemID == nil {
		ok = false
	} else {
		i1, ok = parseID(*in.ItemID)
	}
	if !ok {
		return nil, nil, nil, message("Provide a nonempty item UUID using --item-id; names are not resolved.")
	}
	if f := in.Format; f != nil {
		bad := isBlank(*f) || utf16Len(*f) > 128
		for _, r := range *f {
			bad = bad || unicode.IsControl(r)
		}
		if bad {
			return nil, nil, nil, message("--format must not be empty or whitespace, must be at most 128 characters, and contain no control characters.")
		}
	}
	return &w1, &i1, nil, nil
}

type definitionGetResult struct {
	Definition      *definition `json:"definition,omitempty"`
	Operation       *receipt    `json:"operation,omitempty"`
	OutputDirectory *string     `json:"outputDirectory,omitempty"`
}

// receiptStatus ports the response status the LRO commands derive from
// their receipt.
func receiptStatus(r *receipt) int {
	switch {
	case r == nil:
		return http.StatusOK
	case r.Status == opFailed:
		return http.StatusBadGateway
	case (r.Status == opTrackingStopped || r.Status == opResultUnavailable) && r.Issue != nil && r.Issue.HTTPStatus != nil:
		return *r.Issue.HTTPStatus
	case r.Status != opSucceeded:
		return http.StatusAccepted
	}
	return http.StatusOK
}

func (a *Area) getItemDefinition(ctx context.Context, _ *mcp.CallToolRequest, in getItemDefinitionInput) (*mcp.CallToolResult, any, error) {
	w := waitFrom(in.Sync, in.MaxWaitSeconds, in.EarlyPoll)
	ws, item, op, err := definitionGetIDs(in, w)
	if err != nil {
		return invalid([]string{err.Error()}), nil, nil
	}
	var res *definitionGetResult
	run := func() error {
		dest, err := a.exportDestination(in.OutputDirectory)
		if err != nil {
			return err
		}
		if op != nil {
			r, err := a.getOperationResult(ctx, *op, maxDefinitionJSON)
			if err != nil {
				return err
			}
			if !r.HasBody {
				return opFailure("InvalidItemDefinition", invalidDefinitionMessage, 0, nil)
			}
			d, err := readItemDefinition(r.Value)
			if err != nil {
				return err
			}
			res = &definitionGetResult{Definition: d, Operation: &receipt{Status: opSucceeded, OperationID: op}}
		} else {
			path := "/workspaces/" + ws.String() + "/items/" + item.String() + "/getDefinition"
			if in.Format != nil {
				path += "?format=" + escapeData(*in.Format)
			}
			resp, _, err := a.send(ctx, http.MethodPost, path, nil)
			if err != nil {
				return err
			}
			d, rc, err := runOperation(ctx, a, resp, opContract[*definition]{
				completed: map[int]bool{http.StatusOK: true}, read: readItemDefinition, max: maxDefinitionJSON,
			}, w)
			if err != nil {
				if f := asFailure(err); f.op != nil {
					f.op = definitionReceipt(f.op)
				}
				return err
			}
			res = &definitionGetResult{Operation: definitionReceipt(rc)}
			if d != nil {
				res.Definition = *d
			}
		}
		if dest != "" && res.Definition != nil {
			out, err := exportDefinition(dest, res.Definition)
			if err != nil {
				return err
			}
			res.OutputDirectory = &out
		}
		return nil
	}
	if err := run(); err != nil {
		f := asFailure(err)
		status := baseStatus(f)
		switch {
		case f.cls == "export":
			status = f.status
		case f.kind == kCanceled:
			status = http.StatusRequestTimeout
		}
		var msg string
		switch {
		case f.cls == "export":
			msg = f.msg
		case f.cls == "operation" && f.code == "UnexpectedResponse" && f.status == http.StatusBadGateway:
			msg = "Fabric returned an undocumented success status for definition retrieval. No definition was returned or saved"
		case f.cls == "operation" && f.status == http.StatusBadGateway || f.cls == "throttled":
			msg = f.msg
		case f.kind == kCanceled:
			msg = "Definition retrieval or export was canceled. No completed export was published; the Fabric operation was not canceled. Resume using a known operation ID"
		default:
			msg = messages{
				http.StatusBadRequest:      "Check workspace-id and item-id UUIDs, the optional format, wait options, and operation-id. A resumed result must already be complete",
				http.StatusUnauthorized:    "Authentication failed while retrieving the definition. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. Definition retrieval requires read AND write item permissions, the applicable delegated ReadWrite.All scope, and any encrypted-label usage rights",
				http.StatusNotFound:        "The Fabric item or operation result was not found or is not accessible",
				http.StatusTooManyRequests: "Fabric throttled definition retrieval. Wait before retrying",
				0:                          "Unable to retrieve or export the Fabric item definition. No completed export was published. Check Fabric availability and local export settings; resume using a known operation ID",
			}.get(status)
		}
		if f.op != nil {
			m := msg
			if f.op.Issue != nil {
				m = f.op.Issue.Message
			}
			return response.Status(status, m, &definitionGetResult{Operation: f.op}), nil, nil
		}
		var rc *receipt
		if res != nil {
			rc = res.Operation
		}
		if rc == nil && op != nil {
			rc = &receipt{Status: opTrackingStopped, OperationID: op}
		}
		if rc != nil {
			code := "DefinitionReadFailed"
			if res != nil && res.Definition != nil {
				code = "DefinitionExportFailed"
			}
			r := *rc
			r.Issue = &opIssue{Code: code, Message: msg, HTTPStatus: intPtr(status)}
			return response.Status(status, msg+response.Troubleshooting, &definitionGetResult{Operation: &r}), nil, nil
		}
		return fail(status, msg), nil, nil
	}
	status := receiptStatus(res.Operation)
	msg := "Success"
	switch {
	case res.Operation != nil && res.Operation.Issue != nil:
		msg = res.Operation.Issue.Message
	case res.Definition == nil:
		msg = "Definition retrieval is pending. No files were saved. Check get-operation-state, then use get-item-definition with --operation-id when Succeeded."
	}
	return response.Status(status, msg, res), nil, nil
}

// --- update-item-definition ---

func (a *Area) updateItemDefinition(ctx context.Context, _ *mcp.CallToolRequest, in updateItemDefinitionInput) (*mcp.CallToolResult, any, error) {
	w := waitFrom(in.Sync, in.MaxWaitSeconds, in.EarlyPoll)
	src := definitionInput{in.Definition, in.DefinitionDirectory, in.DefinitionFiles, in.DefinitionFormat}
	ws, wsOK := parseID(in.WorkspaceID)
	item, itemOK := parseID(in.ItemID)
	switch {
	case !wsOK || !itemOK:
		return invalid([]string{"--workspace-id and --item-id must be nonempty UUIDs."}), nil, nil
	case !validBudget(w.maxWait):
		return invalid([]string{"--max-wait-seconds must be finite, greater than zero, and at most 600 seconds."}), nil, nil
	case !src.hasInput():
		return invalid([]string{"Provide a complete replacement definition using exactly one inline or saved-file source."}), nil, nil
	}
	rc, err := func() (*receipt, error) {
		opts, err := parseJSONObject(in.Options, "--options", maxJSONBytes)
		if err != nil {
			return nil, err
		}
		d, err := a.readDefinitionInput(src)
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, itemInputError("Provide a complete replacement definition using exactly one inline or saved-file source.")
		}
		body := map[string]any{"definition": d}
		if opts != nil {
			body["options"] = opts
		}
		resp, _, err := a.send(ctx, http.MethodPost, "/workspaces/"+ws.String()+"/items/"+item.String()+"/updateDefinition"+
			query("updateMetadata", optBool(in.UpdateMetadata)), body)
		if err != nil {
			return nil, err
		}
		id := operationID(resp.Header)
		_, rc, err := runOperation(ctx, a, resp, opContract[struct{}]{completed: map[int]bool{http.StatusOK: true}}, w)
		if err != nil {
			return nil, err
		}
		if rc == nil {
			rc = &receipt{Status: opSucceeded, OperationID: id}
		}
		return rc, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := baseStatus(f)
		switch {
		case f.kind == kInput:
			status = http.StatusBadRequest
		case f.op != nil:
			status = http.StatusRequestTimeout
		}
		var msg string
		switch {
		case f.kind == kInput:
			msg = f.msg
		case f.kind == kAuth:
			msg = "Fabric authentication failed. Authenticate the configured identity and check its access to the item"
		case f.cls == "operation" && f.status == http.StatusBadGateway || f.cls == "throttled":
			msg = f.msg
		case f.kind == kCanceled || f.kind == kTimeout:
			msg = "The definition update timed out or was canceled. Check the item's state before retrying; Fabric may still complete the update"
		default:
			msg = messages{
				http.StatusBadRequest:      "Fabric rejected the definition update. Check the workspace and item UUIDs, complete definition, format, and item-specific options",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity and its access to the item",
				http.StatusForbidden:       "Fabric denied the definition update. Read and write item permissions are required; delegated callers need Item.ReadWrite.All or the item-specific scope. Service-principal and managed-identity support depends on item type",
				http.StatusNotFound:        "The Fabric workspace or item was not found, or the caller does not have access",
				http.StatusConflict:        "The definition update conflicts with the item's current state. Check the item before retrying",
				http.StatusTooManyRequests: "Fabric throttled the definition update. No automatic mutation retry was attempted",
				0:                          "The Fabric definition update could not be confirmed. Check the item's state before retrying; do not automatically resubmit",
			}.get(status)
		}
		if f.op != nil {
			return response.Status(status, f.msg, map[string]any{"operation": f.op}), nil, nil
		}
		return fail(status, msg), nil, nil
	}
	msg := "Success"
	if rc.Issue != nil {
		msg = rc.Issue.Message
	}
	return response.Status(receiptStatus(rc), msg, map[string]any{"operation": rc}), nil, nil
}

// parseJSONObject ports FabricItemJsonInput.ParseObject.
func parseJSONObject(s *string, option string, max int) (json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	if utf16Len(*s) > max || len(*s) > max {
		return nil, itemInputError(fmt.Sprintf("%s must not exceed %d UTF-8 bytes.", option, max))
	}
	b := []byte(*s)
	if !utf8.Valid(b) || !json.Valid(b) || !depthOK(b, 32) || noDuplicateKeys(b) != nil {
		return nil, itemInputError(fmt.Sprintf("%s must contain valid JSON with no duplicate properties and at most 32 nesting levels.", option))
	}
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return nil, itemInputError(option + " must be a JSON object, not null, an array, or a scalar.")
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, b)
	return compact.Bytes(), nil
}

// parseJSONArray ports FabricItemJsonInput.ParseArray.
func parseJSONArray(s *string, option string, max int) (json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	if utf16Len(*s) > max || len(*s) > max {
		return nil, itemInputError(fmt.Sprintf("%s must not exceed %d UTF-8 bytes.", option, max))
	}
	b := []byte(*s)
	if !utf8.Valid(b) || !json.Valid(b) || !depthOK(b, 32) || noDuplicateKeys(b) != nil {
		return nil, itemInputError(fmt.Sprintf("%s must contain valid JSON with no duplicate properties and at most 32 nesting levels.", option))
	}
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
		return nil, itemInputError(option + " must be a JSON array, not null, an object, or a scalar.")
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, b)
	return compact.Bytes(), nil
}
