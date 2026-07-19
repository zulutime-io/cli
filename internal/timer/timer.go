package timer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/zulutime-io/cli/internal/book"
	"github.com/zulutime-io/cli/internal/cmdutil"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/gitx"
)

const fileName = "timer.json"

type State struct {
	StartedAt time.Time `json:"started_at"`
	Label     string    `json:"label,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	GitRepo   string    `json:"git_repo,omitempty"`
	GitRemote string    `json:"git_remote,omitempty"`
	GitBranch string    `json:"git_branch,omitempty"`
}

func statePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

func Load() (*State, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.StartedAt.IsZero() {
		return nil, nil
	}
	return &s, nil
}

func Save(s *State) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func Clear() error {
	p, err := statePath()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *State) Elapsed() time.Duration {
	if s == nil {
		return 0
	}
	d := time.Since(s.StartedAt)
	if d < 0 {
		return 0
	}
	return d.Round(time.Second)
}

func FormatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, sec)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

func HoursRounded(d time.Duration) float64 {
	mins := d.Minutes()
	if mins < 1 {
		mins = 1
	}
	// round up to nearest 0.25h (15 min)
	quarters := int((mins + 14.999) / 15)
	if quarters < 1 {
		quarters = 1
	}
	return float64(quarters) * 0.25
}

func Start(label string, force bool) error {
	existing, err := Load()
	if err != nil {
		return err
	}
	if existing != nil && !force {
		return fmt.Errorf("timer already running (%s · %s) — stop/cancel first, or --force",
			FormatElapsed(existing.Elapsed()), displayLabel(existing))
	}

	label = strings.TrimSpace(label)
	cwd, _ := os.Getwd()
	s := &State{
		StartedAt: time.Now(),
		Label:     label,
		CWD:       cwd,
	}
	if info, err := gitx.Detect(cwd); err == nil {
		s.GitRepo = info.Repo
		s.GitRemote = info.Remote
		s.GitBranch = info.Branch
		if s.Label == "" {
			if last := gitx.LastCommit(cwd); last != nil && last.Subject != "" {
				s.Label = last.Subject
			} else {
				s.Label = info.Repo
			}
		}
	}
	if s.Label == "" {
		s.Label = "work"
	}

	if err := Save(s); err != nil {
		return err
	}
	fmt.Printf("⏱  timer started · %s\n", s.Label)
	if s.GitRepo != "" {
		fmt.Printf("   %s", s.GitRepo)
		if s.GitBranch != "" {
			fmt.Printf(" (%s)", s.GitBranch)
		}
		fmt.Println()
	}
	fmt.Println("   stop with: ztime timer stop")
	return nil
}

func Status() error {
	s, err := Load()
	if err != nil {
		return err
	}
	if s == nil {
		fmt.Println("No active timer")
		return nil
	}
	fmt.Printf("⏱  %s · %s\n", FormatElapsed(s.Elapsed()), displayLabel(s))
	fmt.Printf("   since %s\n", s.StartedAt.Local().Format("15:04"))
	if s.GitRepo != "" {
		fmt.Printf("   %s", s.GitRepo)
		if s.GitBranch != "" {
			fmt.Printf(" (%s)", s.GitBranch)
		}
		fmt.Println()
	}
	return nil
}

// Prompt prints a compact line for starship. Exit 1 via error if no timer.
func Prompt() error {
	s, err := Load()
	if err != nil {
		return err
	}
	if s == nil {
		return ErrInactive
	}
	label := displayLabel(s)
	if len(label) > 28 {
		label = label[:27] + "…"
	}
	fmt.Printf("⏱ %s %s", FormatElapsed(s.Elapsed()), label)
	return nil
}

// Running is for starship `when`: exit 0 if active.
func Running() error {
	s, err := Load()
	if err != nil {
		return err
	}
	if s == nil {
		return ErrInactive
	}
	return nil
}

// ErrInactive means no timer is running (exit 1 for starship helpers).
var ErrInactive = errors.New("inactive")

func Cancel() error {
	s, err := Load()
	if err != nil {
		return err
	}
	if s == nil {
		fmt.Println("No active timer")
		return nil
	}
	elapsed := s.Elapsed()
	if err := Clear(); err != nil {
		return err
	}
	fmt.Printf("⏱  timer cancelled (%s · %s)\n", FormatElapsed(elapsed), displayLabel(s))
	return nil
}

type StopOptions struct {
	Book   bool // --book: book immediately
	NoBook bool // --no-book: don't ask
}

func Stop(o StopOptions) error {
	s, err := Load()
	if err != nil {
		return err
	}
	if s == nil {
		return errors.New("no active timer")
	}
	elapsed := s.Elapsed()
	hours := HoursRounded(elapsed)
	label := displayLabel(s)

	if err := Clear(); err != nil {
		return err
	}

	fmt.Printf("⏱  stopped · %s (%s → %.2fh)\n", label, FormatElapsed(elapsed), hours)

	doBook := false
	switch {
	case o.NoBook:
		doBook = false
	case o.Book:
		doBook = true
	default:
		ask := true
		if err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Book %.2fh?", hours)).
				Affirmative("ztime book").
				Negative("Later").
				Value(&ask),
		)).Run(); err != nil {
			return err
		}
		doBook = ask
	}

	if !doBook {
		printBookLater(hours, label)
		return nil
	}

	cfg, err := cmdutil.LoadConfig()
	if err != nil {
		printBookLater(hours, label)
		return err
	}
	client, err := cmdutil.NewAPI(cfg)
	if err != nil {
		printBookLater(hours, label)
		return err
	}
	if err := cmdutil.RequireAuth(client); err != nil {
		printBookLater(hours, label)
		return fmt.Errorf("API unreachable or not logged in — book manually later: %w", err)
	}
	if err := book.Run(client, cfg, book.Options{
		Hours: hours,
		Desc:  label,
		Date:  time.Now().Format("2006-01-02"),
	}); err != nil {
		printBookLater(hours, label)
		return err
	}
	return nil
}

func printBookLater(hours float64, label string) {
	fmt.Printf("   later: ztime book --hours %.2f --desc %s\n", hours, shellQuote(label))
}

func displayLabel(s *State) string {
	if s.Label != "" {
		return s.Label
	}
	if s.GitRepo != "" {
		return s.GitRepo
	}
	return "timer"
}

func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
