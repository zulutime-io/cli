package requests

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/book"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/tz"
)

type Options struct {
	ClientID string
	Status   string
}

func Run(client *api.Client, cfg *config.Config, o Options) error {
	me, err := client.Me()
	if err != nil {
		return err
	}
	if !me.HasClientRequests() {
		return errors.New(api.MsgRequestsLocked)
	}

	statusFilter := o.Status
	if statusFilter == "" {
		statusFilter = "active"
		if err := pickStatusFilter(&statusFilter); err != nil {
			return err
		}
	}

	myID := me.User.ID
	loc := tz.Load(me.Timezone)
	selectedID := ""

	for {
		list, err := loadList(client, o.ClientID, statusFilter)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Printf("\nNo requests for filter %q.\n", statusFilter)
			change := true
			if err := huh.NewConfirm().
				Title("Change filter?").
				Affirmative("Yes").
				Negative("Quit").
				Value(&change).
				Run(); err != nil {
				return err
			}
			if !change {
				return nil
			}
			if err := pickStatusFilter(&statusFilter); err != nil {
				return err
			}
			continue
		}

		out, err := runBrowser(client, cfg, list, myID, o.ClientID, statusFilter, selectedID, false, loc)
		if err != nil {
			return err
		}
		switch out.Action {
		case "quit", "":
			return nil
		case "filter":
			if err := pickStatusFilter(&statusFilter); err != nil {
				return err
			}
			selectedID = ""
		case "reload":
			selectedID = out.RequestID
		case "status":
			r, err := client.GetRequest(out.RequestID)
			if err != nil {
				return err
			}
			if err := changeStatus(client, r); err != nil {
				return err
			}
			selectedID = out.RequestID
		case "book":
			r, err := client.GetRequest(out.RequestID)
			if err != nil {
				return err
			}
			if err := bookOnRequest(client, cfg, r); err != nil {
				return err
			}
			selectedID = out.RequestID
		default:
			return nil
		}
	}
}

// RunDetail opens the requests browser focused on one request's detail view.
func RunDetail(client *api.Client, cfg *config.Config, requestID string) error {
	me, err := client.Me()
	if err != nil {
		return err
	}
	if !me.HasClientRequests() {
		return errors.New(api.MsgRequestsLocked)
	}
	if strings.TrimSpace(requestID) == "" {
		return errors.New("request id required")
	}

	myID := me.User.ID
	loc := tz.Load(me.Timezone)
	statusFilter := "active"
	selectedID := requestID
	openDetail := true

	for {
		list, err := loadList(client, "", statusFilter)
		if err != nil {
			return err
		}
		if findInList(list, selectedID) == nil {
			// Ensure the target is present even if outside the active filter.
			if r, err := client.GetRequest(selectedID); err == nil {
				list = append([]api.ClientRequest{*r}, list...)
			}
		}
		out, err := runBrowser(client, cfg, list, myID, "", statusFilter, selectedID, openDetail, loc)
		if err != nil {
			return err
		}
		openDetail = false
		switch out.Action {
		case "quit", "":
			return nil
		case "filter":
			if err := pickStatusFilter(&statusFilter); err != nil {
				return err
			}
			selectedID = ""
		case "reload":
			selectedID = out.RequestID
			openDetail = selectedID != ""
		case "status":
			r, err := client.GetRequest(out.RequestID)
			if err != nil {
				return err
			}
			if err := changeStatus(client, r); err != nil {
				return err
			}
			selectedID = out.RequestID
			openDetail = true
		case "book":
			r, err := client.GetRequest(out.RequestID)
			if err != nil {
				return err
			}
			if err := bookOnRequest(client, cfg, r); err != nil {
				return err
			}
			selectedID = out.RequestID
			openDetail = true
		default:
			return nil
		}
	}
}

func findInList(list []api.ClientRequest, id string) *api.ClientRequest {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func pickStatusFilter(statusFilter *string) error {
	if *statusFilter == "" {
		*statusFilter = "active"
	}
	h := 8
	fmt.Println()
	return huh.NewSelect[string]().
		Title("Which requests?").
		Options(
			huh.NewOption("Open + in progress", "active"),
			huh.NewOption("Open", "open"),
			huh.NewOption("In progress", "in_progress"),
			huh.NewOption("Assigned to me", "mine"),
			huh.NewOption("Done", "done"),
			huh.NewOption("Cancelled", "cancelled"),
			huh.NewOption("All", "all"),
		).
		Height(h).
		Value(statusFilter).
		Run()
}

func loadList(client *api.Client, clientID, statusFilter string) ([]api.ClientRequest, error) {
	switch statusFilter {
	case "all":
		return client.ListRequests(clientID, "")
	case "active":
		open, err := client.ListRequests(clientID, "open")
		if err != nil {
			return nil, err
		}
		prog, err := client.ListRequests(clientID, "in_progress")
		if err != nil {
			return nil, err
		}
		return append(open, prog...), nil
	case "mine":
		all, err := client.ListRequests(clientID, "")
		if err != nil {
			return nil, err
		}
		me, err := client.Me()
		if err != nil {
			return nil, err
		}
		out := make([]api.ClientRequest, 0)
		for _, r := range all {
			if r.Status != "open" && r.Status != "in_progress" {
				continue
			}
			if isAssigned(r, me.User.ID) {
				out = append(out, r)
			}
		}
		return out, nil
	default:
		return client.ListRequests(clientID, statusFilter)
	}
}

func isAssigned(r api.ClientRequest, userID string) bool {
	for _, a := range r.Assignees {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

func assigneeNames(r api.ClientRequest) string {
	if len(r.Assignees) == 0 {
		return ""
	}
	names := make([]string, 0, len(r.Assignees))
	for _, a := range r.Assignees {
		n := a.UserName
		if n == "" {
			n = a.UserEmail
		}
		if n == "" {
			n = trunc(a.UserID, 8)
		}
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

func changeStatus(client *api.Client, r *api.ClientRequest) error {
	status := r.Status
	fmt.Println()
	if err := huh.NewSelect[string]().
		Title("New status").
		Options(
			huh.NewOption("Open", "open"),
			huh.NewOption("In progress", "in_progress"),
			huh.NewOption("Done", "done"),
			huh.NewOption("Cancelled", "cancelled"),
		).
		Height(5).
		Value(&status).
		Run(); err != nil {
		return err
	}
	if status == r.Status {
		return nil
	}
	updated, err := client.UpdateRequestStatus(r.ID, status)
	if err != nil {
		return err
	}
	fmt.Printf("✓ Status → %s\n", updated.Status)
	return nil
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

func parseTime(s string, loc *time.Location) (time.Time, error) {
	return tz.Parse(s, loc)
}

func formatTime(s string, loc *time.Location) string {
	t, err := tz.Parse(s, loc)
	if err != nil {
		return s
	}
	return tz.Format(t, loc)
}
