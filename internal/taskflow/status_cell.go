package taskflow

// RewriteStatusCellText rewrites the first **Status** table cell that sits
// outside a fence. Frontmatter, including a status: field, is left unchanged.
// The bool is false when no cell was found; the returned text is then the input.
func RewriteStatusCellText(content string, status Status) (string, bool) {
	return replaceFirstStatusCellOutsideFences(content, status)
}
