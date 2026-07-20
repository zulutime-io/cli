package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/authlogin"
	"github.com/zulutime-io/cli/internal/book"
	"github.com/zulutime-io/cli/internal/cmdutil"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/desk"
	"github.com/zulutime-io/cli/internal/devicekey"
	"github.com/zulutime-io/cli/internal/edit"
	"github.com/zulutime-io/cli/internal/hint"
	"github.com/zulutime-io/cli/internal/hook"
	"github.com/zulutime-io/cli/internal/requests"
	"github.com/zulutime-io/cli/internal/squash"
	"github.com/zulutime-io/cli/internal/timer"
	"github.com/zulutime-io/cli/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:           "ztime",
		Short:         "ZuluTime CLI — book hours from the terminal",
		Long:          "Developer CLI for ZuluTime. Login, book hours interactively, with git suggestions from your current repo.\n\nRun `ztime` or `ztime desk` for your personal workspace (Assigned, Timer, Recent, Requests).",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runDesk,
	}

	root.AddCommand(
		loginCmd(), logoutCmd(), whoamiCmd(),
		bookCmd(), editCmd(), amendCmd(), squashCmd(),
		statusCmd(), submitCmd(),
		deskCmd(),
		requestsCmd(),
		timerCmd(),
		hintCmd(), hookCmd(),
		versionCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err.Error())
		os.Exit(1)
	}
}

func loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Log in to ZuluTime via browser",
		Long:  "Opens a browser to authorize the CLI. A personal access token is stored locally after approval.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}

			pkce, err := authlogin.NewPKCE()
			if err != nil {
				return err
			}
			keys, err := devicekey.Generate()
			if err != nil {
				return err
			}
			cb, err := authlogin.StartCallbackServer(pkce.State)
			if err != nil {
				return err
			}

			start, err := client.StartCLILogin(map[string]any{
				"redirect_uri":        cb.RedirectURI,
				"state":               pkce.State,
				"code_challenge":      pkce.Challenge,
				"user_code_challenge": authlogin.UserCodeChallenge(pkce.UserCode),
				"device_name":         authlogin.DefaultDeviceName(),
				"device_public_key":   keys.PublicKeyB64,
				"key_alg":             keys.Alg,
			})
			if err != nil {
				return err
			}

			authURL := authlogin.AuthorizeURL(cfg.WebOrigin(), start.LoginID)
			fmt.Println("Opening browser to authorize ZuluTime CLI…")
			fmt.Printf("\n  Confirmation code: %s\n\n", pkce.UserCode)
			fmt.Println("Enter this code in the browser, then grant access.")
			fmt.Println(authURL)
			if err := authlogin.OpenBrowser(authURL); err != nil {
				fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\nOpen the URL above manually.\n", err)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			res, err := cb.Wait(ctx)
			if err != nil {
				return err
			}
			if _, err := client.ExchangeCLICode(res.Code, pkce.Verifier, cb.RedirectURI, keys.PrivateKeyB64, keys.Alg); err != nil {
				return err
			}

			// Persist effective API/web URLs so later commands without env vars hit the same host.
			if err := cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save API URL to config: %v\n", err)
			}

			me, err := client.Me()
			if err != nil {
				fmt.Println("✓ Logged in")
				fmt.Printf("  API: %s\n", cfg.APIURL)
				return nil
			}
			fmt.Printf("✓ Logged in as %s (%s) — %s\n", me.User.Name, me.User.Email, me.Organization.Name)
			fmt.Printf("  API: %s\n", cfg.APIURL)
			return nil
		},
	}
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			_ = client.Logout()
			if err := config.ClearCredentials(); err != nil {
				return err
			}
			fmt.Println("✓ Logged out")
			return nil
		},
	}
}

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show current user",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			me, err := client.Me()
			if err != nil {
				return err
			}
			fmt.Printf("%s <%s>\n", me.User.Name, me.User.Email)
			fmt.Printf("Organization: %s (%s)\n", me.Organization.Name, me.Role)
			fmt.Printf("API: %s\n", cfg.APIURL)
			return nil
		},
	}
}

func bookCmd() *cobra.Command {
	var (
		clientID  string
		projectID string
		hours     float64
		date      string
		desc      string
		submit    bool
		noGit     bool
	)
	cmd := &cobra.Command{
		Use:   "book",
		Short: "Book hours (interactive)",
		Long:  "Wizard: client → project → date/hours/description. Suggestions from git commits in the current directory.\n\nWith --project alone, the client is derived from the project (no client prompt).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			return book.Run(client, cfg, book.Options{
				ClientID:  clientID,
				ProjectID: projectID,
				Hours:     hours,
				Date:      date,
				Desc:      desc,
				Submit:    submit,
				NoGit:     noGit,
			})
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "", "Client ID (skip selection)")
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID (skip selection; also derives client)")
	cmd.Flags().Float64Var(&hours, "hours", 0, "Hours")
	cmd.Flags().StringVar(&date, "date", "", "Date YYYY-MM-DD")
	cmd.Flags().StringVar(&desc, "desc", "", "Description")
	cmd.Flags().BoolVar(&submit, "submit", false, "Default submit confirm to Yes (still asks)")
	cmd.Flags().BoolVar(&noGit, "no-git", false, "Skip git suggestions")
	return cmd
}

func editCmd() *cobra.Command {
	var hours float64
	var desc string
	var noGit bool
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit latest draft hours",
		Long:  "Edit a recent draft or rejected time entry (hours, description, git commits).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			return edit.RunEdit(client, edit.Options{Hours: hours, Desc: desc, NoGit: noGit})
		},
	}
	cmd.Flags().Float64Var(&hours, "hours", 0, "New hours")
	cmd.Flags().StringVar(&desc, "desc", "", "New description")
	cmd.Flags().BoolVar(&noGit, "no-git", false, "Don't update git commits")
	return cmd
}

func amendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "amend",
		Short: "Add git commits to latest time entry",
		Long:  "Amend source_meta/commits on a recent draft, rejected, or submitted entry (not approved).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			return edit.RunAmend(client, edit.Options{})
		},
	}
}

func statusCmd() *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show today's hours (or --date)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			if date == "" {
				date = time.Now().Format("2006-01-02")
			}
			list, err := client.ListTimeEntries(date, date, "")
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Printf("No hours on %s\n", date)
				return nil
			}
			var total int
			for _, e := range list {
				total += e.DurationMinutes
				fmt.Printf("%-10s  %5.1fh  %-12s  %s · %s\n",
					e.Status,
					float64(e.DurationMinutes)/60,
					trunc(e.ClientName, 14),
					e.ProjectName,
					e.Description,
				)
			}
			fmt.Printf("\nTotal: %.1fh (%d entries)\n", float64(total)/60, len(list))
			return nil
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "Date YYYY-MM-DD (default today)")
	return cmd
}

func submitCmd() *cobra.Command {
	var from, to string
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit draft hours (batch)",
		Long:  "Submits all draft/rejected hours for the period (default: today).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			today := time.Now().Format("2006-01-02")
			if from == "" {
				from = today
			}
			if to == "" {
				to = today
			}
			ok := false
			if err := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Submit all draft/rejected hours (%s → %s)?", from, to)).
					Affirmative("Submit").
					Negative("Cancel").
					Value(&ok),
			)).Run(); err != nil {
				return err
			}
			if !ok {
				return nil
			}
			n, err := client.SubmitBatch(from, to)
			if err != nil {
				return err
			}
			fmt.Printf("✓ %d time entry(ies) submitted (%s to %s)\n", n, from, to)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "From date YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "To date YYYY-MM-DD")
	return cmd
}

func squashCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "squash",
		Short: "Merge draft time entries",
		Long:  "Merge multiple draft/rejected entries into one (sum hours, unique commits by sha). Other entries are deleted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			return squash.Run(client, squash.Options{Force: force})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Allow different projects (keep target)")
	return cmd
}

func runDesk(cmd *cobra.Command, args []string) error {
	cfg, err := cmdutil.LoadConfig()
	if err != nil {
		return err
	}
	client, err := cmdutil.NewAPI(cfg)
	if err != nil {
		return err
	}
	if err := cmdutil.RequireAuth(client); err != nil {
		return err
	}
	return desk.Run(client, cfg)
}

func deskCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "desk",
		Aliases: []string{"home", "today"},
		Short:   "Personal workspace — assigned, timer, recent, requests",
		Long:    "Interactive desk with 4 panels: Assigned (yours), Timer, Recent entries, Requests (org inbox).\n\nKeys: ←/→ panels, enter request detail, b book hours (on a request when selected), a assign, t timer, s submit, e edit, q quit.\nRequests require Team.",
		RunE:    runDesk,
	}
}

func requestsCmd() *cobra.Command {
	var clientID, status string
	cmd := &cobra.Command{
		Use:   "requests",
		Short: "Browse client requests interactively",
		Long:  "Interactively browse verzoeken with preview + hours/status history. Use list/show for non-interactive output.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			return requests.Run(client, cfg, requests.Options{ClientID: clientID, Status: status})
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "", "Filter by client ID")
	cmd.Flags().StringVar(&status, "status", "", "Filter: open|in_progress|active|done|cancelled|all")

	list := &cobra.Command{
		Use:   "list",
		Short: "List requests (non-interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			if err := requireClientRequests(client); err != nil {
				return err
			}
			apiStatus := status
			if apiStatus == "all" || apiStatus == "active" {
				apiStatus = ""
			}
			list, err := client.ListRequests(clientID, apiStatus)
			if err != nil {
				return err
			}
			if status == "active" {
				filtered := list[:0]
				for _, r := range list {
					if r.Status == "open" || r.Status == "in_progress" {
						filtered = append(filtered, r)
					}
				}
				list = filtered
			}
			if len(list) == 0 {
				fmt.Println("No requests")
				return nil
			}
			for _, r := range list {
				hours := float64(r.MinutesLogged) / 60
				proj := r.ProjectName
				if proj == "" {
					proj = "-"
				}
				ref := r.Ref
				if ref == "" {
					ref = r.ID
				}
				fmt.Printf("%-8s  %-12s  %-18s  %-20s  %5.1fh  %s\n",
					ref,
					r.Status,
					trunc(r.ClientName, 18),
					trunc(proj, 20),
					hours,
					r.Title,
				)
			}
			return nil
		},
	}
	list.Flags().StringVar(&clientID, "client", "", "Filter by client ID")
	list.Flags().StringVar(&status, "status", "", "Filter by status")

	show := &cobra.Command{
		Use:   "show <ref>",
		Short: "Show one request (non-interactive)",
		Long:  "Accepts short ref (V-42) or UUID.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireAuth(client); err != nil {
				return err
			}
			if err := requireClientRequests(client); err != nil {
				return err
			}
			r, err := client.GetRequest(args[0])
			if err != nil {
				return err
			}
			if r.Ref != "" {
				fmt.Printf("Ref:      %s\n", r.Ref)
			}
			fmt.Printf("Status:   %s\n", r.Status)
			fmt.Printf("Client:   %s\n", r.ClientName)
			if r.ProjectName != "" {
				fmt.Printf("Project:  %s\n", r.ProjectName)
			}
			if r.TypeName != "" {
				fmt.Printf("Type:     %s\n", r.TypeName)
			}
			fmt.Printf("Title:    %s\n", r.Title)
			if r.Description != "" {
				fmt.Printf("Desc:     %s\n", r.Description)
			}
			if r.CreatedByName != "" {
				fmt.Printf("Created:  %s (%s)\n", r.CreatedByName, r.CreatedAt)
			} else {
				fmt.Printf("Created:  %s\n", r.CreatedAt)
			}
			fmt.Printf("Logged:   %.1fh\n", float64(r.MinutesLogged)/60)
			hours, err := client.ListRequestHours(args[0])
			if err == nil && len(hours) > 0 {
				fmt.Println("\nHours:")
				for _, e := range hours {
					who := e.UserName
					if who == "" {
						who = "-"
					}
					fmt.Printf("  %s  %5.1fh  %-12s  %s · %s\n",
						e.EntryDate, float64(e.DurationMinutes)/60, e.Status, who, e.Description)
				}
			}
			return nil
		},
	}

	cmd.AddCommand(list, show)
	return cmd
}

func hintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hint",
		Short: "Short tip to book hours (for git hooks)",
		Long:  "Prints 1–3 lines when there are unbooked commits. Always exit 0. Throttle 10 min/repo. ZTIME_HINT=0 off, ZTIME_HINT=force on.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return nil
			}
			client, _ := cmdutil.NewAPI(cfg)
			_ = hint.Run(client, cfg)
			return nil
		},
	}
}

func hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Manage git post-commit hook",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install ztime hint in .git/hooks/post-commit",
			RunE: func(cmd *cobra.Command, args []string) error {
				return hook.Install()
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove ztime block from post-commit hook",
			RunE: func(cmd *cobra.Command, args []string) error {
				return hook.Uninstall()
			},
		},
	)
	return cmd
}

func timerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timer",
		Short: "Work timer (local, visible in Starship)",
		Long:  "Start/stop a local timer. No API needed until you book. Works with Starship prompt.",
	}

	var force bool
	start := &cobra.Command{
		Use:   "start [label...]",
		Short: "Start the timer",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return timer.Start(strings.Join(args, " "), force)
		},
	}
	start.Flags().BoolVar(&force, "force", false, "Replace running timer")

	var bookFlag, noBook bool
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop the timer (optionally book)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return timer.Stop(timer.StopOptions{Book: bookFlag, NoBook: noBook})
		},
	}
	stop.Flags().BoolVar(&bookFlag, "book", false, "Start ztime book immediately")
	stop.Flags().BoolVar(&noBook, "no-book", false, "Don't ask to book")

	cmd.AddCommand(
		start,
		stop,
		&cobra.Command{
			Use:   "status",
			Short: "Show running timer",
			RunE: func(cmd *cobra.Command, args []string) error {
				return timer.Status()
			},
		},
		&cobra.Command{
			Use:   "cancel",
			Short: "Cancel timer without booking",
			RunE: func(cmd *cobra.Command, args []string) error {
				return timer.Cancel()
			},
		},
		&cobra.Command{
			Use:    "prompt",
			Short:  "Compact output for Starship (exit 1 if off)",
			Hidden: true,
			Run: func(cmd *cobra.Command, args []string) {
				if err := timer.Prompt(); err != nil {
					os.Exit(1)
				}
			},
		},
		&cobra.Command{
			Use:    "running",
			Short:  "Exit 0 if timer is running (Starship when)",
			Hidden: true,
			Run: func(cmd *cobra.Command, args []string) {
				if err := timer.Running(); err != nil {
					os.Exit(1)
				}
			},
		},
	)
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print ztime version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.Version)
		},
	}
}

func requireClientRequests(client *api.Client) error {
	me, err := client.Me()
	if err != nil {
		return err
	}
	if !me.HasClientRequests() {
		return errors.New(api.MsgRequestsLocked)
	}
	return nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
