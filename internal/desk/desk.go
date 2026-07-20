package desk

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/book"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/edit"
	"github.com/zulutime-io/cli/internal/requests"
	"github.com/zulutime-io/cli/internal/timer"
)

// Run opens the personal desk TUI: assigned, timer, recent, org requests.
func Run(client *api.Client, cfg *config.Config) error {
	me, err := client.Me()
	if err != nil {
		return err
	}
	myID := me.User.ID
	canRequests := me.HasClientRequests()
	focus := panelAssigned
	assignedIdx, requestIdx, entryIdx := 0, 0, 0

	for {
		data, err := loadDesk(client, myID, canRequests)
		if err != nil {
			return err
		}
		out, err := runTUI(client, cfg, data, myID, focus, assignedIdx, requestIdx, entryIdx)
		if err != nil {
			return err
		}
		focus = out.Focus
		assignedIdx = out.AssignedIdx
		requestIdx = out.RequestIdx
		entryIdx = out.EntryIdx

		switch out.Action {
		case "quit", "":
			return nil
		case "request-detail":
			if !canRequests {
				fmt.Println(api.MsgRequestsLocked)
				continue
			}
			if err := requests.RunDetail(client, cfg, out.RequestID); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "book-request":
			r := findRequest(data.Assigned, out.RequestID)
			if r == nil {
				r = findRequest(data.Requests, out.RequestID)
			}
			if r == nil {
				fmt.Println("error: request not found")
				continue
			}
			if err := bookOnRequest(client, cfg, r); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "assign-request", "unassign-request":
			if !canRequests {
				fmt.Println(api.MsgRequestsLocked)
				continue
			}
			var err error
			if out.Action == "unassign-request" {
				_, err = client.UnassignRequest(out.RequestID, "me")
			} else {
				_, err = client.AssignRequest(out.RequestID, "")
			}
			if err != nil {
				fmt.Println("error:", err.Error())
			}
		case "book-hours":
			if err := bookHours(client, cfg); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "book-timer":
			if err := timer.Stop(timer.StopOptions{Book: true}); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "start-timer":
			label := out.Label
			if label == "" {
				label = "work"
			}
			if err := timer.Start(label, false); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "cancel-timer":
			if err := timer.Cancel(); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "submit-entry":
			e := findEntry(data.Entries, out.EntryID)
			label := "Submit this entry for approval?"
			if e != nil {
				label = fmt.Sprintf("Submit %.1fh · %s · %s?",
					float64(e.DurationMinutes)/60, e.ProjectName, trunc(e.Description, 40))
			}
			ok, err := confirmYes(label)
			if err != nil {
				fmt.Println("error:", err.Error())
				continue
			}
			if !ok {
				continue
			}
			if _, err := client.SubmitTimeEntry(out.EntryID); err != nil {
				fmt.Println("error:", err.Error())
			} else {
				fmt.Println("✓ Submitted")
			}
		case "submit-all":
			ok, err := confirmYes("Submit all draft/rejected entries from the last 14 days?")
			if err != nil {
				fmt.Println("error:", err.Error())
				continue
			}
			if !ok {
				continue
			}
			today := time.Now().Format("2006-01-02")
			from := time.Now().AddDate(0, 0, -14).Format("2006-01-02")
			n, err := client.SubmitBatch(from, today)
			if err != nil {
				fmt.Println("error:", err.Error())
			} else {
				fmt.Printf("✓ %d entr(y/ies) submitted\n", n)
			}
		case "edit-entry":
			if out.EntryID == "" {
				continue
			}
			if err := edit.RunEdit(client, edit.Options{EntryID: out.EntryID}); err != nil {
				fmt.Println("error:", err.Error())
			}
		case "reload":
			continue
		default:
			return nil
		}
	}
}

func loadDesk(client *api.Client, myID string, canRequests bool) (*deskData, error) {
	data := &deskData{CanRequests: canRequests}

	if canRequests {
		all, err := client.ListRequests("", "")
		if err != nil {
			return nil, err
		}
		assigned := make([]api.ClientRequest, 0)
		inbox := make([]api.ClientRequest, 0)
		for _, r := range all {
			if r.Status != "open" && r.Status != "in_progress" {
				continue
			}
			inbox = append(inbox, r)
			if isAssigned(r, myID) {
				assigned = append(assigned, r)
			}
		}
		data.Assigned = assigned
		data.Requests = inbox
	}

	to := time.Now()
	from := to.AddDate(0, 0, -14)
	entries, err := client.ListTimeEntries(from.Format("2006-01-02"), to.Format("2006-01-02"), "")
	if err != nil {
		return nil, err
	}
	recent := make([]api.TimeEntry, 0)
	for i := len(entries) - 1; i >= 0; i-- { // newest first
		e := entries[i]
		switch e.Status {
		case "draft", "rejected", "submitted":
			recent = append(recent, e)
		}
		if len(recent) >= 20 {
			break
		}
	}
	data.Entries = recent

	tstate, _ := timer.Load()
	data.Timer = tstate
	return data, nil
}

func isAssigned(r api.ClientRequest, userID string) bool {
	for _, a := range r.Assignees {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

func findRequest(list []api.ClientRequest, id string) *api.ClientRequest {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func findEntry(list []api.TimeEntry, id string) *api.TimeEntry {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func confirmYes(title string) (bool, error) {
	ok := false
	fmt.Println()
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Affirmative("Yes").
			Negative("Cancel").
			Value(&ok),
	)).Run()
	return ok, err
}

func bookOnRequest(client *api.Client, cfg *config.Config, r *api.ClientRequest) error {
	desc := r.Title
	if strings.TrimSpace(r.Description) != "" {
		desc = r.Title + " — " + trunc(strings.TrimSpace(r.Description), 80)
	}
	opts := book.Options{
		ClientID:  r.ClientID,
		RequestID: r.ID,
		Desc:      desc,
	}
	if r.ProjectID != nil && strings.TrimSpace(*r.ProjectID) != "" {
		opts.ProjectID = strings.TrimSpace(*r.ProjectID)
	}
	fmt.Println()
	return book.Run(client, cfg, opts)
}

// bookHours starts a normal hours booking (same as `ztime book`), picking a project first.
func bookHours(client *api.Client, cfg *config.Config) error {
	projects, err := client.ListProjects()
	if err != nil {
		return err
	}
	active := make([]api.Project, 0)
	for _, p := range projects {
		if p.Active {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return errors.New("no active projects")
	}
	projectID := ""
	choices := make([]huh.Option[string], 0, len(active))
	for _, p := range active {
		label := p.ClientName + " · " + p.Name
		if p.Code != "" {
			label = p.ClientName + " · " + p.Code + " — " + p.Name
		}
		choices = append(choices, huh.NewOption(label, p.ID))
	}
	fmt.Println()
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Book hours — which project?").
			Options(choices...).
			Value(&projectID),
	)).Run(); err != nil {
		return err
	}
	var clientID string
	for _, p := range active {
		if p.ID == projectID {
			clientID = p.ClientID
			break
		}
	}
	return book.Run(client, cfg, book.Options{
		ClientID:  clientID,
		ProjectID: projectID,
	})
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
