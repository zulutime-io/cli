package squash

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/zulutime-io/cli/internal/api"
)

type Options struct {
	Force bool // allow different projects
}

func Run(client *api.Client, o Options) error {
	to := time.Now()
	from := to.AddDate(0, 0, -14)
	list, err := client.ListTimeEntries(from.Format("2006-01-02"), to.Format("2006-01-02"), "")
	if err != nil {
		return err
	}
	var drafts []api.TimeEntry
	for _, e := range list {
		if e.Status == "draft" || e.Status == "rejected" {
			drafts = append(drafts, e)
		}
	}
	if len(drafts) < 2 {
		return errors.New("need at least 2 draft/rejected entries to squash")
	}

	// newest first for display
	for i, j := 0, len(drafts)-1; i < j; i, j = i+1, j-1 {
		drafts[i], drafts[j] = drafts[j], drafts[i]
	}

	byID := map[string]api.TimeEntry{}
	choices := make([]huh.Option[string], 0, len(drafts))
	for _, e := range drafts {
		byID[e.ID] = e
		label := fmt.Sprintf("%s  %.1fh  %s · %s  [%s]",
			e.EntryDate, float64(e.DurationMinutes)/60, e.ProjectName, trunc(e.Description, 36), e.Status)
		choices = append(choices, huh.NewOption(label, e.ID))
	}

	var selected []string
	if err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Entries to merge (min. 2)").
			Options(choices...).
			Value(&selected).
			Validate(func(v []string) error {
				if len(v) < 2 {
					return errors.New("pick at least 2 entries")
				}
				return nil
			}),
	)).Run(); err != nil {
		return err
	}

	targetID := selected[0]
	tChoices := make([]huh.Option[string], 0, len(selected))
	for _, id := range selected {
		e := byID[id]
		tChoices = append(tChoices, huh.NewOption(
			fmt.Sprintf("Keep: %s · %.1fh · %s", e.EntryDate, float64(e.DurationMinutes)/60, e.ProjectName),
			id,
		))
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Which entry to keep (target)?").Options(tChoices...).Value(&targetID),
	)).Run(); err != nil {
		return err
	}

	target := byID[targetID]
	var others []api.TimeEntry
	totalMin := 0
	var descs []string
	var commitLists []api.CommitList
	projects := map[string]bool{}

	for _, id := range selected {
		e := byID[id]
		totalMin += e.DurationMinutes
		projects[e.ProjectID] = true
		if e.Description != "" {
			descs = append(descs, e.Description)
		}
		if e.SourceMeta != nil && len(e.SourceMeta.Commits) > 0 {
			commitLists = append(commitLists, e.SourceMeta.Commits)
		}
		if id != targetID {
			others = append(others, e)
		}
	}

	if len(projects) > 1 && !o.Force {
		return errors.New("entries have different projects — use --force to merge onto the target project anyway")
	}

	mergedCommits := api.MergeCommitLists(commitLists...)
	desc := uniqueJoin(descs, "; ")
	if len(mergedCommits) > 0 {
		// Prefer commit subjects if descriptions are just commit joins
		subj := mergedCommits.JoinSubjects("; ")
		if desc == "" {
			desc = subj
		}
	}

	hours := float64(totalMin) / 60
	fmt.Printf("Squash → %.2fh on %s · %s\n", hours, target.ProjectName, target.EntryDate)
	confirm := false
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Merge %d entries and delete %d?", len(selected), len(others))).
			Affirmative("Squash").
			Negative("Cancel").
			Value(&confirm),
	)).Run(); err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	meta := &api.SourceMeta{Source: "cli", Commits: mergedCommits}
	if target.SourceMeta != nil {
		meta.GitRemote = target.SourceMeta.GitRemote
		meta.GitRepo = target.SourceMeta.GitRepo
		meta.GitBranch = target.SourceMeta.GitBranch
		meta.GitRoot = target.SourceMeta.GitRoot
		if meta.Source == "" {
			meta.Source = target.SourceMeta.Source
		}
	}
	billable := target.Billable

	updated, err := client.UpdateTimeEntry(target.ID, api.UpdateTimeEntryInput{
		ProjectID:     target.ProjectID,
		EntryDate:     target.EntryDate,
		DurationHours: hours,
		Description:   desc,
		Billable:      &billable,
		TripMode:      "none",
		SourceMeta:    meta,
	})
	if err != nil {
		return err
	}

	var failed int
	for _, e := range others {
		if err := client.DeleteTimeEntry(e.ID); err != nil {
			failed++
			fmt.Printf("  ! could not delete %s: %v\n", e.ID[:8], err)
		}
	}

	fmt.Printf("✓ Merged: %s · %.2fh · %d commits\n",
		updated.ProjectName, float64(updated.DurationMinutes)/60, len(mergedCommits))
	if failed > 0 {
		return fmt.Errorf("%d source entry(ies) not deleted", failed)
	}
	return nil
}

func uniqueJoin(parts []string, sep string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, sep)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
