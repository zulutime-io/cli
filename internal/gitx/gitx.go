package gitx

import (
	"bytes"
	"os/exec"
	"path"
	"strings"
)

type Info struct {
	Root   string
	Remote string
	Branch string
	Repo   string // short name derived from remote or folder
}

type Commit struct {
	SHA     string
	Subject string
}

func Detect(cwd string) (*Info, error) {
	root, err := run(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	info := &Info{Root: root, Repo: path.Base(root)}
	if remote, err := run(cwd, "remote", "get-url", "origin"); err == nil {
		info.Remote = remote
		if name := repoNameFromRemote(remote); name != "" {
			info.Repo = name
		}
	}
	if branch, err := run(cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = branch
	}
	return info, nil
}

func repoNameFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	if i := strings.LastIndex(remote, "/"); i >= 0 {
		return remote[i+1:]
	}
	if i := strings.LastIndex(remote, ":"); i >= 0 {
		return remote[i+1:]
	}
	return path.Base(remote)
}

// SuggestCommits returns today's author commits (sha+subject), or recent commits as fallback.
func SuggestCommits(cwd string, limit int) []Commit {
	if limit <= 0 {
		limit = 12
	}
	email, _ := run(cwd, "config", "user.email")
	args := []string{"log", "--since=midnight", "--pretty=%h%x00%s"}
	if email != "" {
		args = append(args, "--author="+email)
	}
	out, err := run(cwd, args...)
	commits := parseCommits(out)
	if err != nil || len(commits) == 0 {
		out, err = run(cwd, "log", "-n", "8", "--pretty=%h%x00%s")
		if err != nil {
			return nil
		}
		commits = parseCommits(out)
	}
	if len(commits) > limit {
		commits = commits[:limit]
	}
	return commits
}

// LastCommit returns HEAD subject/sha or empty.
func LastCommit(cwd string) *Commit {
	out, err := run(cwd, "log", "-1", "--pretty=%h%x00%s")
	if err != nil {
		return nil
	}
	list := parseCommits(out)
	if len(list) == 0 {
		return nil
	}
	return &list[0]
}

func parseCommits(out string) []Commit {
	seen := map[string]bool{}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sha, subject, ok := strings.Cut(line, "\x00")
		if !ok {
			// fallback: first token sha
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 0 {
				continue
			}
			sha = parts[0]
			subject = ""
			if len(parts) > 1 {
				subject = parts[1]
			}
		}
		sha = strings.TrimSpace(sha)
		subject = strings.TrimSpace(subject)
		key := sha
		if key == "" {
			key = subject
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		commits = append(commits, Commit{SHA: sha, Subject: subject})
	}
	return commits
}

// FilterUnbooked removes commits whose SHA (or subject for legacy) is already booked.
func FilterUnbooked(candidates []Commit, bookedSHAs, bookedSubjects map[string]bool) []Commit {
	var out []Commit
	for _, c := range candidates {
		if c.SHA != "" && bookedSHAs[c.SHA] {
			continue
		}
		// also match prefix: booked may store short or longer
		if c.SHA != "" && shaBooked(c.SHA, bookedSHAs) {
			continue
		}
		if c.SHA == "" && c.Subject != "" && bookedSubjects[c.Subject] {
			continue
		}
		out = append(out, c)
	}
	return out
}

func shaBooked(sha string, booked map[string]bool) bool {
	if booked[sha] {
		return true
	}
	for b := range booked {
		if b == "" {
			continue
		}
		if strings.HasPrefix(sha, b) || strings.HasPrefix(b, sha) {
			return true
		}
	}
	return false
}

func run(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
