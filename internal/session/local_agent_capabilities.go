package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalAgentCapabilities is the provider-scoped loadout visible to one local
// agent session. Tools are MCP server names; Skills are filesystem skills from
// that provider's native project and user directories.
type LocalAgentCapabilities struct {
	Tools  []string
	Skills []string
}

// GetLocalAgentCapabilities reads the effective local MCP and skill loadout
// for this instance. Keeping the lookup on Instance ensures Claude, Cursor and
// Codex never accidentally report another provider's config directory.
func (i *Instance) GetLocalAgentCapabilities() LocalAgentCapabilities {
	if i == nil || i.isRemoteSession() {
		return LocalAgentCapabilities{}
	}
	tool := i.GetToolThreadSafe()

	var result LocalAgentCapabilities
	if info := i.GetMCPInfo(); info != nil {
		result.Tools = info.AllNames()
	}
	var providerConfigDir string
	switch {
	case IsClaudeCompatible(tool):
		providerConfigDir = GetClaudeConfigDirForInstance(i)
	case tool == "cursor":
		providerConfigDir = GetCursorConfigDir()
	case IsCodexCompatible(tool):
		providerConfigDir = i.getCodexHomeDir()
	}
	result.Skills = ListLocalAgentSkills(i.ProjectPath, tool, providerConfigDir)
	return result
}

// ListLocalAgentSkills returns the skills installed in a provider's native
// local directories. Cursor intentionally has its own .cursor/skills target;
// this prevents attaching a Cursor skill from mutating Claude's loadout.
//
// providerConfigDir is accepted explicitly so account/profile-specific Claude
// and Codex homes are represented accurately. Pass an empty value to use the
// provider's normal user-level config directory.
func ListLocalAgentSkills(projectPath, tool, providerConfigDir string) []string {
	roots := localAgentSkillRoots(projectPath, tool, providerConfigDir)
	seen := make(map[string]struct{})
	for _, root := range roots {
		collectSkillNames(root, seen)
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func localAgentSkillRoots(projectPath, tool, providerConfigDir string) []string {
	var roots []string
	addProjectRoot := func(relative string) {
		if strings.TrimSpace(projectPath) != "" {
			roots = append(roots, filepath.Join(projectPath, filepath.FromSlash(relative)))
		}
	}

	switch {
	case IsClaudeCompatible(tool):
		addProjectRoot(projectClaudeSkillsDir)
		configDir := strings.TrimSpace(providerConfigDir)
		if configDir == "" {
			configDir = strings.TrimSpace(GetClaudeConfigDir())
		}
		if configDir != "" {
			roots = append(roots, filepath.Join(configDir, "skills"))
		}
	case tool == "cursor":
		// Cursor documents both namespaces as native skill roots. Keep Claude's
		// compatibility directory out of this list so the two provider bars stay
		// distinct and actionable.
		addProjectRoot(projectCursorSkillsDir)
		addProjectRoot(projectAgentsSkillsDir)
		configDir := strings.TrimSpace(providerConfigDir)
		if configDir == "" {
			configDir = strings.TrimSpace(GetCursorConfigDir())
		}
		if configDir != "" {
			roots = append(roots, filepath.Join(configDir, "skills"))
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			roots = append(roots, filepath.Join(home, ".agents", "skills"))
		}
	case IsCodexCompatible(tool):
		addProjectRoot(projectAgentsSkillsDir)
		addProjectRoot(".codex/skills")
		if strings.TrimSpace(providerConfigDir) != "" {
			roots = append(roots, filepath.Join(providerConfigDir, "skills"))
		}
	}
	return roots
}

// collectSkillNames recursively discovers SKILL.md packages. A small explicit
// traversal is used instead of filepath.WalkDir because Agent Deck materializes
// managed project skills as directory symlinks, which WalkDir does not follow.
func collectSkillNames(root string, names map[string]struct{}) {
	const (
		maxDepth   = 16
		maxEntries = 10_000
	)
	visited := make(map[string]struct{})
	entriesSeen := 0

	var walk func(string, int)
	walk = func(dir string, depth int) {
		if depth > maxDepth || entriesSeen >= maxEntries {
			return
		}
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return
		}
		realDir, err = filepath.Abs(realDir)
		if err != nil {
			return
		}
		if _, ok := visited[realDir]; ok {
			return
		}
		visited[realDir] = struct{}{}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			entriesSeen++
			if entriesSeen > maxEntries {
				return
			}
			path := filepath.Join(dir, entry.Name())
			if strings.EqualFold(entry.Name(), "SKILL.md") {
				name, _ := parseSkillMetadata(path, filepath.Base(dir))
				if name = strings.TrimSpace(name); name != "" {
					names[name] = struct{}{}
				}
				continue
			}
			info, statErr := os.Stat(path) // follows managed skill symlinks
			if statErr == nil && info.IsDir() {
				walk(path, depth+1)
			}
		}
	}

	if strings.TrimSpace(root) != "" {
		walk(root, 0)
	}
}
