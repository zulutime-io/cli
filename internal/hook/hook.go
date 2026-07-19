package hook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zulutime-io/cli/internal/gitx"
)

const (
	beginMarker = "# >>> ztime"
	endMarker   = "# <<< ztime"
)

func Install() error {
	bin, err := resolveBinary()
	if err != nil {
		return err
	}

	root, err := gitRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, ".git", "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(existing)
	updated := strings.Contains(content, beginMarker)
	if updated {
		content = stripBlock(content)
	}

	var out strings.Builder
	trimmed := strings.TrimSpace(content)
	if trimmed != "" {
		out.WriteString(strings.TrimRight(content, "\n"))
		out.WriteString("\n\n")
	} else {
		out.WriteString("#!/bin/sh\n")
	}
	out.WriteString(hookBlock(bin))
	if !strings.HasSuffix(out.String(), "\n") {
		out.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(out.String()), 0o755); err != nil {
		return err
	}
	if updated {
		fmt.Printf("✓ post-commit hook updated → %s\n", bin)
	} else {
		fmt.Printf("✓ post-commit hook installed in %s\n", path)
		fmt.Printf("  binary: %s\n", bin)
	}
	return nil
}

func Uninstall() error {
	root, err := gitRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, ".git", "hooks", "post-commit")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("No post-commit hook found")
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, beginMarker) {
		fmt.Println("No ztime block in post-commit hook")
		return nil
	}
	cleaned := stripBlock(content)
	cleaned = strings.TrimSpace(cleaned) + "\n"
	// If only shebang left, remove file
	trimmed := strings.TrimSpace(cleaned)
	if trimmed == "" || trimmed == "#!/bin/sh" || trimmed == "#!/usr/bin/env sh" {
		if err := os.Remove(path); err != nil {
			return err
		}
		fmt.Println("✓ ztime hook removed")
		return nil
	}
	if err := os.WriteFile(path, []byte(cleaned), 0o755); err != nil {
		return err
	}
	fmt.Println("✓ ztime block removed from post-commit")
	return nil
}

func hookBlock(bin string) string {
	return fmt.Sprintf(`%s
# git hooks have a minimal PATH — use absolute path
%s hint || true
%s
`, beginMarker, shellQuote(bin), endMarker)
}

func resolveBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not find ztime binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("could not resolve ztime binary: %w", err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func stripBlock(content string) string {
	for {
		start := strings.Index(content, beginMarker)
		if start < 0 {
			return content
		}
		end := strings.Index(content[start:], endMarker)
		if end < 0 {
			return content[:start]
		}
		endAbs := start + end + len(endMarker)
		// eat trailing newline
		for endAbs < len(content) && (content[endAbs] == '\n' || content[endAbs] == '\r') {
			endAbs++
		}
		content = content[:start] + content[endAbs:]
	}
}

func gitRoot() (string, error) {
	cwd, _ := os.Getwd()
	info, err := gitx.Detect(cwd)
	if err != nil {
		return "", errors.New("no git repo in this directory")
	}
	return info.Root, nil
}
