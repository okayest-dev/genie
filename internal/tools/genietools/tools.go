// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/tools"
)

// RegisterGenieTools registers all Genie self-improvement tools with the registry.
func RegisterGenieTools(reg *tools.Registry, cfg *config.Config, cwd, configDir, pluginDir string, skillDirs []string) {
	// Config tools
	reg.Register(NewGenieConfigReadTool(cwd, configDir, ""))
	reg.Register(NewGenieConfigWriteTool(cwd, configDir, ""))
	reg.Register(NewGenieConfigPatchTool(cwd, configDir, ""))

	// Plugin tools
	reg.Register(NewGeniePluginCreateTool(pluginDir))
	reg.Register(NewGeniePluginInstallTool(pluginDir))
	reg.Register(NewGeniePluginRemoveTool(pluginDir))
	reg.Register(NewGeniePluginListTool(pluginDir))

	// Skill tools
	reg.Register(NewGenieSkillCreateTool(skillDirs))
	reg.Register(NewGenieSkillUpdateTool(skillDirs))
	reg.Register(NewGenieSkillRemoveTool(skillDirs))
	reg.Register(NewGenieSkillListTool(skillDirs))

	// Agent tools
	reg.Register(NewGenieAgentCreateTool(configDir, cwd))
	reg.Register(NewGenieAgentUpdateTool(configDir, cwd))
	reg.Register(NewGenieAgentRemoveTool(configDir, cwd))
	reg.Register(NewGenieAgentListTool(configDir, cwd))

	// Theme tools
	reg.Register(NewGenieThemeCreateTool(configDir, cwd))
	reg.Register(NewGenieThemeUpdateTool(configDir, cwd))
	reg.Register(NewGenieThemeRemoveTool(configDir, cwd))
	reg.Register(NewGenieThemeListTool(configDir, cwd))

	// Permission tools
	reg.Register(NewGeniePermissionGrantTool(cfg))
	reg.Register(NewGeniePermissionRevokeTool(cfg))
	reg.Register(NewGeniePermissionListTool(cfg))

	// Provider tools
	reg.Register(NewGenieProviderAddTool(cfg))
	reg.Register(NewGenieProviderRemoveTool(cfg))
	reg.Register(NewGenieProviderListTool(cfg))

	// Self-update tool
	reg.Register(NewGenieSelfUpdateTool())
}