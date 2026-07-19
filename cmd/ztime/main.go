package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/zulutime-io/cli/internal/book"
	"github.com/zulutime-io/cli/internal/cmdutil"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/edit"
	"github.com/zulutime-io/cli/internal/hint"
	"github.com/zulutime-io/cli/internal/hook"
	"github.com/zulutime-io/cli/internal/squash"
	"github.com/zulutime-io/cli/internal/timer"
	"github.com/zulutime-io/cli/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:           "ztime",
		Short:         "ZuluTime CLI — book hours from the terminal",
		Long:          "Developer CLI for ZuluTime. Login, book hours interactively, with git suggestions from your current repo.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		loginCmd(), logoutCmd(), whoamiCmd(),
		bookCmd(), editCmd(), amendCmd(), squashCmd(),
		statusCmd(), submitCmd(),
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
		Short: "Log in to ZuluTime",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cmdutil.LoadConfig()
			if err != nil {
				return err
			}
			client, err := cmdutil.NewAPI(cfg)
			if err != nil {
				return err
			}

			var email, password string
			form := huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Email").Value(&email).Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("email required")
					}
					return nil
				}),
				huh.NewInput().Title("Password").EchoMode(huh.EchoModePassword).Value(&password).Validate(func(s string) error {
					if s == "" {
						return errors.New("password required")
					}
					return nil
				}),
			))
			if err := form.Run(); err != nil {
				return err
			}

			if _, err := client.Login(strings.TrimSpace(email), password); err != nil {
				return err
			}
			me, err := client.Me()
			if err != nil {
				fmt.Println("✓ Logged in")
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
		Long:  "Wizard: client → project → date/hours/description. Suggestions from git commits in the current directory.",
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
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID (skip selection)")
	cmd.Flags().Float64Var(&hours, "hours", 0, "Hours")
	cmd.Flags().StringVar(&date, "date", "", "Date YYYY-MM-DD")
	cmd.Flags().StringVar(&desc, "desc", "", "Description")
	cmd.Flags().BoolVar(&submit, "submit", false, "Submit immediately after saving")
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

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
