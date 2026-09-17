package skill

import "fmt"

// GlobalFilter applies the config-level enable/disable lists to a skill pool.
// When enable is non-empty, only skills named in it pass. When disable is
// non-empty, skills named in it are removed. Disable wins over enable.
// Both empty is a no-op (pass-through).
func GlobalFilter(pool []ParsedSkill, enable, disable []string) ([]ParsedSkill, []Warning) {
	if len(enable) == 0 && len(disable) == 0 {
		return pool, nil
	}

	var result []ParsedSkill
	var warns []Warning

	if len(enable) > 0 {
		allowed := make(map[string]bool, len(enable))
		for _, name := range enable {
			allowed[name] = true
		}
		for _, s := range pool {
			if allowed[s.Name] {
				result = append(result, s)
			}
		}
	} else {
		result = append(result, pool...)
	}

	if len(disable) > 0 {
		denied := make(map[string]bool, len(disable))
		for _, name := range disable {
			denied[name] = true
		}
		filtered := result[:0]
		for _, s := range result {
			if denied[s.Name] {
				warns = append(warns, Warning{
					Message: fmt.Sprintf("skill: filter: %q disabled by config", s.Name),
				})
				continue
			}
			filtered = append(filtered, s)
		}
		result = filtered
	}

	return result, warns
}

// BindToAgent selects skills from the pool that an agent explicitly binds.
// When agentSkills is nil, the agent inherits all pool skills. When
// agentSkills is non-nil but empty, no skills are bound. When non-empty,
// only named skills from the pool are selected — a name not in the pool
// is a hard error.
func BindToAgent(pool []ParsedSkill, agentSkills []string, agentName string) ([]ParsedSkill, error) {
	if agentSkills == nil {
		return pool, nil
	}
	if len(agentSkills) == 0 {
		return nil, nil
	}

	poolByName := make(map[string]ParsedSkill, len(pool))
	for _, s := range pool {
		poolByName[s.Name] = s
	}

	var bound []ParsedSkill
	for _, name := range agentSkills {
		s, ok := poolByName[name]
		if !ok {
			return nil, fmt.Errorf("agent %q: unknown skill %q", agentName, name)
		}
		bound = append(bound, s)
	}
	return bound, nil
}
