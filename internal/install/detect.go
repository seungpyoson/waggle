package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	PlatformClaudeCode = "claude-code"
	PlatformCodex      = "codex"
	PlatformGemini     = "gemini"
	PlatformAuggie     = "auggie"
	PlatformAugment    = "augment"
)

type PlatformDetection struct {
	Name     string
	Found    bool
	Evidence string
}

type InstallResult struct {
	Platform string
	Message  string
}

type LookPathFunc func(string) (string, error)

func SupportedPlatforms() []string {
	return []string{PlatformClaudeCode, PlatformCodex, PlatformGemini, PlatformAuggie, PlatformAugment}
}

func DetectPlatforms(homeDir string, lookPath LookPathFunc) []PlatformDetection {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return []PlatformDetection{
		detectPlatform(homeDir, lookPath, PlatformClaudeCode, []string{".claude"}, []string{"claude", "claude-code"}),
		detectPlatform(homeDir, lookPath, PlatformCodex, []string{".codex"}, []string{"codex"}),
		detectPlatform(homeDir, lookPath, PlatformGemini, []string{".gemini"}, []string{"gemini"}),
		detectPlatform(homeDir, lookPath, PlatformAuggie, []string{".augment/rules"}, []string{"auggie"}),
		detectPlatform(homeDir, lookPath, PlatformAugment, []string{".augment/skills"}, []string{"augment"}),
	}
}

func InstallDetected() ([]InstallResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}
	return InstallDetectedInHome(home, exec.LookPath)
}

func InstallDetectedInHome(homeDir string, lookPath LookPathFunc) ([]InstallResult, error) {
	detections := DetectPlatforms(homeDir, lookPath)
	results := make([]InstallResult, 0, len(detections))
	for _, detection := range detections {
		if !detection.Found {
			continue
		}
		if err := InstallPlatformInHome(homeDir, detection.Name); err != nil {
			return results, err
		}
		results = append(results, InstallResult{
			Platform: detection.Name,
			Message:  installMessage(detection.Name),
		})
	}
	return results, nil
}

func InstallPlatformInHome(homeDir, platform string) error {
	switch platform {
	case PlatformClaudeCode:
		return installClaudeCode(homeDir)
	case PlatformCodex:
		return installCodex(homeDir)
	case PlatformGemini:
		return installGemini(homeDir)
	case PlatformAuggie:
		return installAuggie(homeDir)
	case PlatformAugment:
		return installAugment(homeDir)
	default:
		return fmt.Errorf("unknown platform: %s", platform)
	}
}

func installMessage(platform string) string {
	switch platform {
	case PlatformClaudeCode:
		return "Claude Code integration installed. Restart Claude Code to activate."
	case PlatformCodex:
		return "Codex integration installed. Restart Codex to activate."
	case PlatformGemini:
		return "Gemini integration installed. Restart Gemini to activate."
	case PlatformAuggie:
		return "Auggie integration installed. Restart Auggie to activate."
	case PlatformAugment:
		return "Augment integration installed. Restart Augment to activate."
	default:
		return fmt.Sprintf("%s integration installed", platform)
	}
}

func detectPlatform(homeDir string, lookPath LookPathFunc, name string, dirs []string, binaries []string) PlatformDetection {
	for _, dir := range dirs {
		if pathExists(filepath.Join(homeDir, filepath.FromSlash(dir))) {
			return PlatformDetection{Name: name, Found: true, Evidence: dir}
		}
	}
	for _, binary := range binaries {
		path, err := lookPath(binary)
		if err == nil {
			return PlatformDetection{Name: name, Found: true, Evidence: path}
		}
	}
	return PlatformDetection{Name: name}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
