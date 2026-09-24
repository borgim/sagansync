// Package domains turns a project/workspace pair into its public hostname.
package domains

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

const maxLabel = 63

// Host returns the hostname a workspace is served on, or "" when it has none.
//
//	production              -> domain
//	other, previewDomain    -> <workspace>-<project>.<previewDomain>
//	other, no previewDomain -> <workspace>.<domain>
func Host(project, workspace, domain, previewDomain string) string {
	switch {
	case workspace == "production":
		return domain
	case previewDomain != "":
		return label(workspace+"-"+project) + "." + previewDomain
	case domain != "":
		return workspace + "." + domain
	}
	return ""
}

// label keeps a DNS label within 63 characters. Longer labels become their
// first 55 characters plus a short hash of the whole label, so the result is
// deterministic and different inputs stay distinct.
func label(s string) string {
	if len(s) <= maxLabel {
		return s
	}
	sum := sha1.Sum([]byte(s))
	return strings.TrimRight(s[:55], "-") + "-" + hex.EncodeToString(sum[:])[:7]
}
