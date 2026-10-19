package boardpolicy

// SchemaVersion reports the parsed codec version, not a semantic profile or
// authority revision. A zero Policy has no parsed version.
func (p Policy) SchemaVersion() int { return p.schemaVersion }
