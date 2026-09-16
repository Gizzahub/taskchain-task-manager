package boardpolicy

func validModuleName(module string) bool {
	if module == "" || module[0] < 'a' || module[0] > 'z' {
		return false
	}
	for _, c := range module[1:] {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Modules returns a defensive copy of the explicitly declared module roots.
func (p Policy) Modules() []string {
	if p.modules == nil {
		return nil
	}
	modules := make([]string, len(p.modules))
	copy(modules, p.modules)
	return modules
}

func (p Policy) IsModule(name string) bool {
	for _, module := range p.modules {
		if module == name {
			return true
		}
	}
	return false
}
