package clipboard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PasteImage saves the first image currently in the system clipboard to a
// temporary PNG file and returns its path. Clipboard image formats are exposed
// by the standard desktop tools on Linux and by pngpaste on macOS.
func PasteImage() (string, error) {
	name := filepath.Join(os.TempDir(), fmt.Sprintf("agent-deck-paste-%d.png", time.Now().UnixNano()))
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pngpaste"); err == nil {
			cmd = exec.Command("pngpaste", name)
		} else if _, err := exec.LookPath("osascript"); err == nil {
			// pngpaste is convenient but optional. macOS's built-in AppleScript
			// bridge can write the PNG clipboard flavor without extra software.
			path := strings.ReplaceAll(name, `\`, `\\`)
			path = strings.ReplaceAll(path, `"`, `\"`)
			script := fmt.Sprintf(`set pngData to the clipboard as «class PNGf»
set f to open for access POSIX file "%s" with write permission
write pngData to f
close access f`, path)
			cmd = exec.Command("osascript", "-e", script)
		} else {
			return "", fmt.Errorf("image paste is unavailable (install pngpaste with: brew install pngpaste)")
		}
	case "linux":
		if _, err := exec.LookPath("wl-paste"); err == nil {
			cmd = exec.Command("wl-paste", "--type", "image/png")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		} else {
			return "", fmt.Errorf("image paste requires wl-paste or xclip")
		}
	default:
		return "", fmt.Errorf("image paste is not supported on %s", runtime.GOOS)
	}

	if cmd.Args[0] == "pngpaste" {
		if err := cmd.Run(); err != nil {
			_ = os.Remove(name)
			return "", fmt.Errorf("could not read image clipboard: %w", err)
		}
	} else {
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("could not read image clipboard: %w", err)
		}
		if err := os.WriteFile(name, out, 0o600); err != nil {
			return "", fmt.Errorf("could not save pasted image: %w", err)
		}
	}
	if info, err := os.Stat(name); err != nil || info.Size() == 0 {
		_ = os.Remove(name)
		return "", fmt.Errorf("clipboard does not contain an image")
	}
	return name, nil
}
