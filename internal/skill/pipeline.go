package skill

// FilteredPool discovers skills from dirs and applies the config-level
// enable/disable filter exactly once. Pipeline, PoolNames, and the startup
// agent-resolution path all build on it, so callers that need both the pool
// and the layer share a single discovery pass.
func FilteredPool(dirs []string, enable, disable []string) ([]ParsedSkill, []Warning, error) {
	pool, discoverWarns, err := Discover(dirs)
	if err != nil {
		return nil, nil, err
	}
	filtered, filterWarns := GlobalFilter(pool, enable, disable)
	return filtered, append(discoverWarns, filterWarns...), nil
}

// Pipeline runs the full skill pipeline: discover → global filter → agent
// bind → build layer. It is the primary entry point for callers that need
// the assembled skill-layer string for the instruction pipeline.
//
// agentSkills is the agent's declared skill list: nil = inherit all,
// non-nil empty = none, non-nil non-empty = exact set. agentName is used
// for error messages when an unknown skill name is encountered.
//
// Warnings are non-fatal issues (invalid SKILL.md, disabled skills).
func Pipeline(dirs []string, enable, disable []string, agentSkills []string, agentName string) (layer string, warns []Warning, err error) {
	filtered, warns, err := FilteredPool(dirs, enable, disable)
	if err != nil {
		return "", warns, err
	}

	bound, err := BindToAgent(filtered, agentSkills, agentName)
	if err != nil {
		return "", warns, err
	}

	return BuildSkillLayer(bound), warns, nil
}

// Names returns the names of a filtered pool, preserving discovery order.
func Names(pool []ParsedSkill) []string {
	names := make([]string, 0, len(pool))
	for _, s := range pool {
		names = append(names, s.Name)
	}
	return names
}

// PoolNames is a convenience for callers that only need the config-filtered
// skill names (agent skills inheritance and explicit-list validation) without
// a layer. Prefer FilteredPool + Names when the same pool feeds the layer, so
// discovery runs once.
func PoolNames(dirs []string, enable, disable []string) ([]string, []Warning, error) {
	filtered, warns, err := FilteredPool(dirs, enable, disable)
	if err != nil {
		return nil, warns, err
	}
	return Names(filtered), warns, nil
}
