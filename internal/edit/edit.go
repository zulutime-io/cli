package edit

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/gitx"
)

type Options struct {
	EntryID string // when set, edit this entry (no picker)
	Hours   float64
	Desc    string
	NoGit   bool
}

func latestInRange(client *api.Client, days int) ([]api.TimeEntry, error) {
	to := time.Now()
	from := to.AddDate(0, 0, -days)
	list, err := client.ListTimeEntries(from.Format("2006-01-02"), to.Format("2006-01-02"), "")
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

func pickEntry(list []api.TimeEntry, allowStatuses map[string]bool) (*api.TimeEntry, error) {
	var candidates []api.TimeEntry
	for _, e := range list {
		if allowStatuses[e.Status] {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("no matching time entry found in the recent period")
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	id := candidates[0].ID
	choices := make([]huh.Option[string], 0, len(candidates))
	for _, e := range candidates {
		label := fmt.Sprintf("%s  %.1fh  %s · %s  [%s]",
			e.EntryDate, float64(e.DurationMinutes)/60, e.ProjectName, trunc(e.Description, 40), e.Status)
		choices = append(choices, huh.NewOption(label, e.ID))
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Which entry?").Options(choices...).Value(&id),
	)).Run(); err != nil {
		return nil, err
	}
	for i := range candidates {
		if candidates[i].ID == id {
			return &candidates[i], nil
		}
	}
	return &candidates[0], nil
}

func RunEdit(client *api.Client, o Options) error {
	list, err := latestInRange(client, 14)
	if err != nil {
		return err
	}
	var entry *api.TimeEntry
	if o.EntryID != "" {
		for i := range list {
			if list[i].ID == o.EntryID {
				entry = &list[i]
				break
			}
		}
		if entry == nil {
			return errors.New("entry not found")
		}
		if entry.Status != "draft" && entry.Status != "rejected" {
			return fmt.Errorf("only draft/rejected can be edited (got %s — use `ztime amend` for commits)", entry.Status)
		}
	} else {
		entry, err = pickEntry(list, map[string]bool{"draft": true, "rejected": true})
		if err != nil {
			return fmt.Errorf("%w (only draft/rejected are fully editable — use `ztime amend` for commits)", err)
		}
	}

	fmt.Printf("Editing: %s · %s · %.1fh · %s\n", entry.EntryDate, entry.ProjectName, float64(entry.DurationMinutes)/60, entry.Status)

	hours := o.Hours
	if hours <= 0 {
		hoursStr := fmt.Sprintf("%.2f", float64(entry.DurationMinutes)/60)
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Hours").Value(&hoursStr).Validate(func(s string) error {
				v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
				if err != nil || v <= 0 {
					return errors.New("positive number required")
				}
				return nil
			}),
		)).Run(); err != nil {
			return err
		}
		hours, _ = strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(hoursStr, ",", ".")), 64)
	}

	desc := o.Desc
	if desc == "" {
		desc = entry.Description
	}

	meta := mergeMeta(entry.SourceMeta, nil)
	if !o.NoGit {
		commits, err := amendCommitsInteractive(client, entry, meta)
		if err != nil {
			return err
		}
		meta.Commits = commits
		if o.Desc == "" {
			if err := huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Description").Value(&desc),
			)).Run(); err != nil {
				return err
			}
		}
	} else if o.Desc == "" {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Description").Value(&desc),
		)).Run(); err != nil {
			return err
		}
	}

	billable := entry.Billable
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Billable?").Affirmative("Yes").Negative("No").Value(&billable),
	)).Run(); err != nil {
		return err
	}

	updated, err := client.UpdateTimeEntry(entry.ID, api.UpdateTimeEntryInput{
		ProjectID:     entry.ProjectID,
		EntryDate:     entry.EntryDate,
		DurationHours: hours,
		Description:   desc,
		Billable:      &billable,
		TripMode:      "none",
		SourceMeta:    meta,
	})
	if err != nil {
		return err
	}
	fmt.Printf("✓ Updated: %s · %.2fh\n", updated.ProjectName, float64(updated.DurationMinutes)/60)
	if len(meta.Commits) > 0 {
		fmt.Printf("  Commits: %s\n", meta.Commits.JoinSubjects(" · "))
	}
	return nil
}

func RunAmend(client *api.Client, o Options) error {
	list, err := latestInRange(client, 14)
	if err != nil {
		return err
	}
	entry, err := pickEntry(list, map[string]bool{"draft": true, "rejected": true, "submitted": true})
	if err != nil {
		return err
	}
	if entry.Status == "approved" {
		return errors.New("approved hours can no longer be amended")
	}

	fmt.Printf("Amend: %s · %s · [%s]\n", entry.EntryDate, entry.ProjectName, entry.Status)

	meta := mergeMeta(entry.SourceMeta, nil)
	commits, err := amendCommitsInteractive(client, entry, meta)
	if err != nil {
		return err
	}
	meta.Commits = commits

	desc := entry.Description
	syncDesc := false
	if len(commits) > 0 {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title("Update description from commits?").
				Affirmative("Yes").
				Negative("No").
				Value(&syncDesc),
		)).Run(); err != nil {
			return err
		}
	}
	var descPtr *string
	if syncDesc {
		desc = commits.JoinSubjects("; ")
		descPtr = &desc
	}

	updated, err := client.PatchSourceMeta(entry.ID, meta, descPtr)
	if err != nil {
		return err
	}
	fmt.Printf("✓ Git info updated (%s)\n", updated.ProjectName)
	if len(meta.Commits) > 0 {
		fmt.Printf("  Commits: %s\n", meta.Commits.JoinSubjects(" · "))
	} else {
		fmt.Println("  No commits linked")
	}
	return nil
}

func amendCommitsInteractive(client *api.Client, entry *api.TimeEntry, meta *api.SourceMeta) (api.CommitList, error) {
	cwd, _ := os.Getwd()
	existing := append(api.CommitList{}, meta.Commits...)
	if info, err := gitx.Detect(cwd); err == nil {
		meta.Source = "cli"
		meta.GitRemote = info.Remote
		meta.GitRepo = info.Repo
		meta.GitBranch = info.Branch
		meta.GitRoot = info.Root
	} else if meta.Source == "" {
		meta.Source = "cli"
	}

	suggestions := gitx.SuggestCommits(cwd, 16)
	// Exclude SHAs booked on OTHER entries
	to := time.Now()
	from := to.AddDate(0, 0, -14)
	if all, err := client.ListTimeEntries(from.Format("2006-01-02"), to.Format("2006-01-02"), ""); err == nil {
		var others []api.TimeEntry
		for _, e := range all {
			if e.ID != entry.ID {
				others = append(others, e)
			}
		}
		shas, subjects := api.CollectBookedIndexes(others)
		suggestions = gitx.FilterUnbooked(suggestions, shas, subjects)
	}

	byKey := map[string]api.CommitRef{}
	var choices []huh.Option[string]
	var selected []string

	for _, c := range existing {
		key := c.SHA
		if key == "" {
			key = "s:" + c.Subject
		}
		byKey[key] = c
		label := c.Subject
		if c.SHA != "" {
			label = "✓ " + c.SHA + " " + c.Subject
		} else {
			label = "✓ " + c.Subject
		}
		choices = append(choices, huh.NewOption(label, key))
		selected = append(selected, key)
	}
	for _, g := range suggestions {
		key := g.SHA
		if key == "" {
			key = "s:" + g.Subject
		}
		if _, ok := byKey[key]; ok {
			continue
		}
		ref := api.CommitRef{SHA: g.SHA, Subject: g.Subject}
		byKey[key] = ref
		label := g.Subject
		if g.SHA != "" {
			label = g.SHA + " " + g.Subject
		}
		choices = append(choices, huh.NewOption(label, key))
	}

	if len(choices) == 0 {
		fmt.Println("(no git commits found)")
		return existing, nil
	}

	fmt.Printf("Current commits on entry: %d\n", len(existing))
	if err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Commits (keep/add; already booked elsewhere are filtered)").
			Options(choices...).
			Value(&selected),
	)).Run(); err != nil {
		return nil, err
	}

	var out api.CommitList
	for _, k := range selected {
		if c, ok := byKey[k]; ok {
			out = append(out, c)
		}
	}
	return api.MergeCommitLists(out), nil
}

func mergeMeta(existing *api.SourceMeta, extra *api.SourceMeta) *api.SourceMeta {
	m := &api.SourceMeta{Source: "cli"}
	if existing != nil {
		*m = *existing
		m.Commits = append(api.CommitList{}, existing.Commits...)
	}
	if extra != nil {
		if extra.GitRemote != "" {
			m.GitRemote = extra.GitRemote
		}
		if extra.GitRepo != "" {
			m.GitRepo = extra.GitRepo
		}
		if extra.GitBranch != "" {
			m.GitBranch = extra.GitBranch
		}
		if extra.GitRoot != "" {
			m.GitRoot = extra.GitRoot
		}
		if len(extra.Commits) > 0 {
			m.Commits = api.MergeCommitLists(m.Commits, extra.Commits)
		}
	}
	if m.Source == "" {
		m.Source = "cli"
	}
	return m
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
