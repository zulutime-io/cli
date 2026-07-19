package book

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/gitx"
)

type Options struct {
	ClientID  string
	ProjectID string
	Hours     float64
	Date      string
	Desc      string
	Submit    bool // --submit: skip ask and submit
	NoGit     bool
}

func Run(client *api.Client, cfg *config.Config, o Options) error {
	clients, err := client.ListClients()
	if err != nil {
		return err
	}
	var active []api.ClientRow
	for _, c := range clients {
		if c.Active {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return errors.New("no active clients — create an end client in Admin first")
	}

	projects, err := client.ListProjects()
	if err != nil {
		return err
	}
	var activeProjects []api.Project
	for _, p := range projects {
		if p.Active {
			activeProjects = append(activeProjects, p)
		}
	}
	if len(activeProjects) == 0 {
		return errors.New("no active projects")
	}

	cwd, _ := os.Getwd()
	var gitInfo *gitx.Info
	var gitCommits []gitx.Commit
	if !o.NoGit {
		if info, err := gitx.Detect(cwd); err == nil {
			gitInfo = info
			gitCommits = gitx.SuggestCommits(cwd, 12)
			// Filter already booked commits (last 14 days)
			to := time.Now()
			from := to.AddDate(0, 0, -14)
			if existing, err := client.ListTimeEntries(from.Format("2006-01-02"), to.Format("2006-01-02"), ""); err == nil {
				shas, subjects := api.CollectBookedIndexes(existing)
				gitCommits = gitx.FilterUnbooked(gitCommits, shas, subjects)
			}
		}
	}
	remote := ""
	if gitInfo != nil {
		remote = gitInfo.Remote
	}

	// Defaults from flags / remembered remote / last client — still prompt unless flag set.
	clientID := o.ClientID
	projectID := o.ProjectID
	if projectID == "" && remote != "" {
		if remembered := cfg.ProjectByRemote[remote]; remembered != "" {
			for _, p := range activeProjects {
				if p.ID == remembered {
					if projectID == "" {
						projectID = p.ID
					}
					if clientID == "" {
						clientID = p.ClientID
					}
					break
				}
			}
		}
	}
	if clientID == "" && cfg.LastClientID != "" {
		for _, c := range active {
			if c.ID == cfg.LastClientID {
				clientID = c.ID
				break
			}
		}
	}

	if o.ClientID == "" {
		choices := make([]huh.Option[string], 0, len(active))
		for _, c := range active {
			choices = append(choices, huh.NewOption(c.Name, c.ID))
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Client").Options(choices...).Value(&clientID),
		)).Run(); err != nil {
			return err
		}
	}

	clientProjects := filterProjects(activeProjects, clientID)
	if len(clientProjects) == 0 {
		return fmt.Errorf("no projects for this client")
	}

	if projectID != "" && !projectIn(clientProjects, projectID) {
		projectID = ""
	}
	if o.ProjectID == "" {
		choices := make([]huh.Option[string], 0, len(clientProjects))
		for _, p := range clientProjects {
			label := p.Name
			if p.Code != "" {
				label = p.Code + " — " + p.Name
			}
			choices = append(choices, huh.NewOption(label, p.ID))
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Project").Options(choices...).Value(&projectID),
		)).Run(); err != nil {
			return err
		}
	} else if !projectIn(clientProjects, projectID) {
		return fmt.Errorf("project does not belong to selected client")
	}

	var project api.Project
	for _, p := range clientProjects {
		if p.ID == projectID {
			project = p
			break
		}
	}

	date := o.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
		dateInput := date
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Date (YYYY-MM-DD)").
				Value(&dateInput).
				Validate(func(s string) error {
					if _, err := time.Parse("2006-01-02", strings.TrimSpace(s)); err != nil {
						return errors.New("invalid date")
					}
					return nil
				}),
		)).Run(); err != nil {
			return err
		}
		date = strings.TrimSpace(dateInput)
	}

	hours := o.Hours
	if hours <= 0 {
		hoursStr := "1"
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Hours").
				Value(&hoursStr).
				Validate(func(s string) error {
					v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
					if err != nil || v <= 0 {
						return errors.New("enter a positive number")
					}
					return nil
				}),
		)).Run(); err != nil {
			return err
		}
		hours, _ = strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(hoursStr, ",", ".")), 64)
	}

	desc := o.Desc
	var selectedRefs api.CommitList
	if desc == "" {
		if len(gitCommits) > 0 {
			byKey := map[string]gitx.Commit{}
			choices := make([]huh.Option[string], 0, len(gitCommits))
			selectedKeys := make([]string, 0, len(gitCommits))
			for _, c := range gitCommits {
				key := c.SHA
				if key == "" {
					key = c.Subject
				}
				byKey[key] = c
				label := c.Subject
				if c.SHA != "" {
					label = c.SHA + " " + c.Subject
				}
				choices = append(choices, huh.NewOption(label, key))
				selectedKeys = append(selectedKeys, key) // default: all unbooked
			}
			if err := huh.NewForm(huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Include commits (not yet booked)").
					Options(choices...).
					Value(&selectedKeys),
			)).Run(); err != nil {
				return err
			}
			for _, k := range selectedKeys {
				if c, ok := byKey[k]; ok {
					selectedRefs = append(selectedRefs, api.CommitRef{SHA: c.SHA, Subject: c.Subject})
				}
			}
			desc = selectedRefs.JoinSubjects("; ")
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Description").Value(&desc),
		)).Run(); err != nil {
			return err
		}
	}

	billable := project.BillableDefault
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Billable?").
			Affirmative("Yes").
			Negative("No").
			Value(&billable),
	)).Run(); err != nil {
		return err
	}

	if projectID == "" || date == "" || hours <= 0 {
		return errors.New("project, date, and hours are required")
	}

	meta := &api.SourceMeta{Source: "cli"}
	if gitInfo != nil {
		meta.GitRemote = gitInfo.Remote
		meta.GitRepo = gitInfo.Repo
		meta.GitBranch = gitInfo.Branch
		meta.GitRoot = gitInfo.Root
		meta.Commits = selectedRefs
	}

	entry, err := client.CreateTimeEntry(api.CreateTimeEntryInput{
		ProjectID:     projectID,
		EntryDate:     date,
		DurationHours: hours,
		Description:   desc,
		Billable:      &billable,
		TripMode:      "none",
		SourceMeta:    meta,
	})
	if err != nil {
		return err
	}

	cfg.RememberProject(remote, projectID, clientID)
	_ = cfg.Save()

	fmt.Printf("✓ Draft saved: %s · %s · %.2fh\n", entry.ClientName, entry.ProjectName, float64(entry.DurationMinutes)/60)

	doSubmit := o.Submit
	if !o.Submit {
		ask := true
		if err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title("Submit for approval?").
				Affirmative("Submit").
				Negative("Later").
				Value(&ask),
		)).Run(); err != nil {
			return err
		}
		doSubmit = ask
	}

	if doSubmit {
		updated, err := client.SubmitTimeEntry(entry.ID)
		if err != nil {
			return fmt.Errorf("saved, but submit failed: %w", err)
		}
		fmt.Printf("✓ Submitted (%s)\n", updated.Status)
	}
	return nil
}

func filterProjects(all []api.Project, clientID string) []api.Project {
	var out []api.Project
	for _, p := range all {
		if p.ClientID == clientID {
			out = append(out, p)
		}
	}
	return out
}

func projectIn(list []api.Project, id string) bool {
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}
