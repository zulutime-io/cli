package hint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/gitx"
)

const throttle = 10 * time.Minute

type stateFile struct {
	LastHint map[string]time.Time `json:"last_hint"`
}

// Run prints a short booking tip. Always returns nil (safe for git hooks).
func Run(client *api.Client, cfg *config.Config) error {
	if os.Getenv("ZTIME_HINT") == "0" || os.Getenv("ZTIME_HINT") == "off" {
		return nil
	}
	force := os.Getenv("ZTIME_HINT") == "force"

	cwd, _ := os.Getwd()
	info, err := gitx.Detect(cwd)
	if err != nil {
		return nil
	}

	if !force && throttled(info.Root) {
		return nil
	}

	if client == nil {
		fmt.Println("⏱  ztime · not logged in → ztime login")
		markHint(info.Root)
		return nil
	}
	if _, err := client.Me(); err != nil {
		fmt.Println("⏱  ztime · not logged in → ztime login")
		markHint(info.Root)
		return nil
	}

	unbooked := gitx.SuggestCommits(cwd, 20)
	today := time.Now().Format("2006-01-02")
	to := time.Now()
	from := to.AddDate(0, 0, -14)
	recent, _ := client.ListTimeEntries(from.Format("2006-01-02"), to.Format("2006-01-02"), "")
	shas, subjects := api.CollectBookedIndexes(recent)
	unbooked = gitx.FilterUnbooked(unbooked, shas, subjects)

	if len(unbooked) == 0 {
		return nil
	}

	last := gitx.LastCommit(cwd)
	subject := unbooked[0].Subject
	if last != nil && last.Subject != "" {
		subject = last.Subject
	}

	rememberedProject := ""
	if info.Remote != "" {
		rememberedProject = cfg.ProjectByRemote[info.Remote]
	}

	var draftsSameProject int
	var anyDraft bool
	for _, e := range recent {
		if e.EntryDate != today {
			continue
		}
		if e.Status != "draft" && e.Status != "rejected" {
			continue
		}
		anyDraft = true
		if rememberedProject != "" && e.ProjectID == rememberedProject {
			draftsSameProject++
		}
	}

	fmt.Printf("⏱  ztime · %s", info.Repo)
	if info.Branch != "" {
		fmt.Printf(" (%s)", info.Branch)
	}
	fmt.Println()
	fmt.Printf("    %q\n", subject)
	if len(unbooked) > 1 {
		fmt.Printf("    + %d other unbooked commit(s)\n", len(unbooked)-1)
	}

	switch {
	case draftsSameProject >= 2:
		fmt.Println("    → ztime amend     or     ztime squash")
	case anyDraft:
		fmt.Println("    → ztime amend     (add commits to draft)")
	default:
		fmt.Println("    → ztime book")
	}

	markHint(info.Root)
	return nil
}

func statePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hint-state.json"), nil
}

func loadState() stateFile {
	p, err := statePath()
	if err != nil {
		return stateFile{LastHint: map[string]time.Time{}}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return stateFile{LastHint: map[string]time.Time{}}
	}
	var s stateFile
	if json.Unmarshal(data, &s) != nil || s.LastHint == nil {
		return stateFile{LastHint: map[string]time.Time{}}
	}
	return s
}

func saveState(s stateFile) {
	p, err := statePath()
	if err != nil {
		return
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(p, data, 0o600)
}

func throttled(repoRoot string) bool {
	s := loadState()
	t, ok := s.LastHint[repoRoot]
	if !ok {
		return false
	}
	return time.Since(t) < throttle
}

func markHint(repoRoot string) {
	s := loadState()
	if s.LastHint == nil {
		s.LastHint = map[string]time.Time{}
	}
	s.LastHint[repoRoot] = time.Now()
	saveState(s)
}
