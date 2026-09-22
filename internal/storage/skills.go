package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// SkillsRepoURL is where the procedures pm's own run kinds hand off to are
// published. They are not in this repository: the solo launch types `/solo`
// into a Claude Code session, and `pm finish` tells its worker to invoke
// `batch-finish-auto`. A machine without them starts a session that has
// nothing to follow, and nothing in the launch itself would say so - hence
// MissingSkills, which every launch surface and `pm executor doctor` ask
// before the fact.
const SkillsRepoURL = "https://github.com/mbalazy/claude-skills"

// AgentSkills are the skills a launch from pm invokes by name.
var AgentSkills = []string{"solo", "batch-finish-auto"}

// MissingSkills returns, in the order given, the names that have no
// SKILL.md under <configDir>/skills. A symlink counts (os.Stat follows it),
// so a checkout of the skills repository linked in by its install.sh passes.
func MissingSkills(configDir string, names ...string) []string {
	var missing []string
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(configDir, "skills", name, "SKILL.md")); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

// SkillsInstallHint is the one sentence every "skill missing" finding ends
// with: how to get the skills onto this machine, for this config dir.
func SkillsInstallHint(configDir string) string {
	return fmt.Sprintf("git clone %s and run its install.sh with CLAUDE_CONFIG_DIR=%s - it links every skill into %s/skills", SkillsRepoURL, configDir, configDir)
}
