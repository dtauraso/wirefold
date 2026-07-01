// wirekey.go — the single field↔key transform for wire:"data.state".
//
// A struct field named Held is read from / written to NodeData.State under the
// JSON key "held". lowerFirst maps the exported Go field name to its data.state
// key; exportedFieldName is the exact inverse. Both directions live here (rather
// than as locals next to any one caller) so the mapping has ONE discoverable
// home — builders.go (reflectBuild) and validate.go both route through these.
package Wiring

import "strings"

// lowerFirst returns s with its first byte lowercased.
// Used for wire:"data.state" key derivation (field Held → key "held").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// exportedFieldName reconstructs the exported struct field name from a
// data.state key (inverse of lowerFirst): "held" → "Held".
func exportedFieldName(key string) string {
	if key == "" {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}
