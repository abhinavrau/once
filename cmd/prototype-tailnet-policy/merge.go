package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tailscale/hujson"
)

// The sections Once automates once a credential exists (ADR-0017): grants,
// autoApprovers, nodeAttrs. tagOwners stays manual, phase one's job.
var (
	onceGrant     = map[string]any{"src": []any{"autogroup:member"}, "dst": []any{"tag:once"}, "ip": []any{"443"}}
	onceApprovers = map[string]any{"tag:once": []any{"tag:once"}}
	onceNodeAttr  = map[string]any{"target": []any{"tag:once"}, "attr": []any{"funnel"}}
)

// mergePolicy returns the user's policy formatted, the Merged Policy (the same
// document with Once's missing sections added, comments intact), and the names
// of the sections that were added. Both documents come back hujson-formatted
// so the diff between them shows only the additions.
// ponytail: presence checks are structural equality on the parsed doc, not the
// ACL-test equivalence ADR-0017 specifies; the real flow validates via the API.
func mergePolicy(policy string) (original, merged string, added []string, err error) {
	v, err := hujson.Parse([]byte(policy))
	if err != nil {
		return "", "", nil, err
	}
	v.Format()
	original = v.String()

	var doc map[string]any
	if err := json.Unmarshal(pack(v), &doc); err != nil {
		return "", "", nil, err
	}

	var ops []string
	op := func(path string, value any) {
		b, _ := json.Marshal(value)
		ops = append(ops, fmt.Sprintf(`{"op":"add","path":%q,"value":%s}`, path, b))
	}

	if !hasEntry(doc["grants"], onceGrant) {
		if _, ok := doc["grants"]; ok {
			op("/grants/-", onceGrant)
		} else {
			op("/grants", []any{onceGrant})
		}
		added = append(added, "grants")
	}

	if !hasApprovers(doc) {
		switch approvers := doc["autoApprovers"].(type) {
		case map[string]any:
			if _, ok := approvers["services"]; ok {
				op("/autoApprovers/services/tag:once", onceApprovers["tag:once"])
			} else {
				op("/autoApprovers/services", onceApprovers)
			}
		default:
			op("/autoApprovers", map[string]any{"services": onceApprovers})
		}
		added = append(added, "autoApprovers.services")
	}

	if !hasEntry(doc["nodeAttrs"], onceNodeAttr) {
		if _, ok := doc["nodeAttrs"]; ok {
			op("/nodeAttrs/-", onceNodeAttr)
		} else {
			op("/nodeAttrs", []any{onceNodeAttr})
		}
		added = append(added, "nodeAttrs")
	}

	if len(ops) > 0 {
		patch := "[" + strings.Join(ops, ",") + "]"
		if err := v.Patch([]byte(patch)); err != nil {
			return "", "", nil, err
		}
		v.Format()
	}
	return original, v.String(), added, nil
}

type diffLine struct {
	text  string
	added bool
}

// diffLines is a plain LCS line diff. The merge only ever adds, so every old
// line survives and nothing renders as removed; the LCS keeps the alignment
// honest even where hujson's formatter shifts an insertion point.
func diffLines(oldText, newText string) []diffLine {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	var out []diffLine
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			out = append(out, diffLine{text: oldLines[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			// A dropped old line: cannot happen from an add-only merge, but
			// render it as context rather than lying about a removal.
			out = append(out, diffLine{text: oldLines[i]})
			i++
		default:
			out = append(out, diffLine{text: newLines[j], added: true})
			j++
		}
	}
	for ; j < len(newLines); j++ {
		out = append(out, diffLine{text: newLines[j], added: true})
	}
	for ; i < len(oldLines); i++ {
		out = append(out, diffLine{text: oldLines[i]})
	}
	return out
}

// Helpers

func pack(v hujson.Value) []byte {
	std := v.Clone()
	std.Standardize()
	return std.Pack()
}

func hasEntry(section any, want map[string]any) bool {
	list, ok := section.([]any)
	if !ok {
		return false
	}
	wantJSON, _ := json.Marshal(want)
	for _, entry := range list {
		got, _ := json.Marshal(entry)
		if string(got) == string(wantJSON) {
			return true
		}
	}
	return false
}

func hasApprovers(doc map[string]any) bool {
	approvers, ok := doc["autoApprovers"].(map[string]any)
	if !ok {
		return false
	}
	services, ok := approvers["services"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = services["tag:once"]
	return ok
}
