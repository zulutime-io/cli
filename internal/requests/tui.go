package requests

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/tz"
)

type viewMode int

const (
	modeList viewMode = iota
	modeDetail
)

type browseOutcome struct {
	Action    string // quit | filter | book | status | reload
	RequestID string
}

type browseModel struct {
	client       *api.Client
	cfg          *config.Config
	myID         string
	clientID     string
	statusFilter string
	loc          *time.Location

	list          []api.ClientRequest
	table         table.Model
	mode          viewMode
	detail        *api.ClientRequest
	hours         []api.TimeEntry
	events        []api.ActivityEvent
	vp            viewport.Model
	startDetailID string

	width  int
	height int
	err    string
	busy   string
	out    browseOutcome
}

type detailLoadedMsg struct {
	req    *api.ClientRequest
	hours  []api.TimeEntry
	events []api.ActivityEvent
	err    error
}

type assignedMsg struct {
	req *api.ClientRequest
	err error
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))

	refStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	askTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229"))
	askBodyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).MarginTop(1).MarginBottom(1)
	labelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	valueStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	projectStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	historyHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Bold(true)
	hoursLineStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("158"))
	metaLineStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func statusStyle(status string) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	switch status {
	case "open":
		return s.Foreground(lipgloss.Color("228")).Background(lipgloss.Color("58"))
	case "in_progress":
		return s.Foreground(lipgloss.Color("195")).Background(lipgloss.Color("26"))
	case "done":
		return s.Foreground(lipgloss.Color("22")).Background(lipgloss.Color("114"))
	case "cancelled":
		return s.Foreground(lipgloss.Color("254")).Background(lipgloss.Color("240"))
	default:
		return s.Foreground(lipgloss.Color("255")).Background(lipgloss.Color("240"))
	}
}

func statusLabel(status string) string {
	switch status {
	case "open":
		return "open"
	case "in_progress":
		return "in progress"
	case "done":
		return "done"
	case "cancelled":
		return "cancelled"
	default:
		return status
	}
}

func runBrowser(client *api.Client, cfg *config.Config, list []api.ClientRequest, myID, clientID, statusFilter, selectedID string, openDetail bool, loc *time.Location) (browseOutcome, error) {
	m := newBrowseModel(client, cfg, list, myID, clientID, statusFilter, selectedID, loc)
	if openDetail && selectedID != "" {
		m.startDetailID = selectedID
		m.busy = "Loading…"
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return browseOutcome{}, err
	}
	bm, ok := final.(browseModel)
	if !ok {
		return browseOutcome{Action: "quit"}, nil
	}
	if bm.out.Action == "" {
		bm.out.Action = "quit"
	}
	return bm.out, nil
}

func newBrowseModel(client *api.Client, cfg *config.Config, list []api.ClientRequest, myID, clientID, statusFilter, selectedID string, loc *time.Location) browseModel {
	cols := []table.Column{
		{Title: "REF", Width: 8},
		{Title: "STATUS", Width: 12},
		{Title: "CLIENT", Width: 16},
		{Title: "HOURS", Width: 6},
		{Title: "TITLE", Width: 40},
		{Title: "ASSIGNED", Width: 18},
	}
	rows := tableRows(list, myID)
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(minInt(16, maxInt(3, len(rows)+1))),
	)
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	st.Selected = st.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(st)
	if selectedID != "" {
		for i, r := range list {
			if r.ID == selectedID {
				t.SetCursor(i)
				break
			}
		}
	}
	if loc == nil {
		loc = tz.Load("")
	}

	return browseModel{
		client:       client,
		cfg:          cfg,
		myID:         myID,
		clientID:     clientID,
		statusFilter: statusFilter,
		loc:          loc,
		list:         list,
		table:        t,
		mode:         modeList,
		vp:           viewport.New(80, 20),
	}
}

func tableRows(list []api.ClientRequest, myID string) []table.Row {
	rows := make([]table.Row, 0, len(list))
	for _, r := range list {
		ref := r.Ref
		if ref == "" {
			ref = trunc(r.ID, 8)
		}
		mine := ""
		if isAssigned(r, myID) {
			mine = "★ "
		}
		rows = append(rows, table.Row{
			ref,
			r.Status,
			trunc(r.ClientName, 16),
			fmt.Sprintf("%.1fh", float64(r.MinutesLogged)/60),
			trunc(r.Title, 40),
			trunc(mine+assigneeNames(r), 18),
		})
	}
	return rows
}

func (m browseModel) Init() tea.Cmd {
	if m.startDetailID == "" {
		return nil
	}
	id := m.startDetailID
	return loadDetailCmd(m.client, id)
}

func (m browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil

	case detailLoadedMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			m.mode = modeList
			return m, nil
		}
		m.detail = msg.req
		m.hours = msg.hours
		m.events = msg.events
		m.mode = modeDetail
		m.vp.SetContent(m.detailContent())
		m.vp.GotoTop()
		return m, nil

	case assignedMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.detail = msg.req
		m.syncListFromDetail()
		m.vp.SetContent(m.detailContent())
		return m, nil

	case tea.KeyMsg:
		if m.busy != "" {
			return m, nil
		}
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeDetail:
			return m.updateDetail(msg)
		}
	}

	var cmd tea.Cmd
	if m.mode == modeList {
		m.table, cmd = m.table.Update(msg)
	} else {
		m.vp, cmd = m.vp.Update(msg)
	}
	return m, cmd
}

func (m browseModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.out = browseOutcome{Action: "quit"}
		return m, tea.Quit
	case "f":
		m.out = browseOutcome{Action: "filter"}
		return m, tea.Quit
	case "right", "enter", "l":
		idx := m.table.Cursor()
		if idx < 0 || idx >= len(m.list) {
			return m, nil
		}
		id := m.list[idx].ID
		m.err = ""
		m.busy = "Loading…"
		return m, loadDetailCmd(m.client, id)
	case "r":
		idx := m.table.Cursor()
		id := ""
		if idx >= 0 && idx < len(m.list) {
			id = m.list[idx].ID
		}
		m.out = browseOutcome{Action: "reload", RequestID: id}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m browseModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "esc", "h", "backspace":
		m.mode = modeList
		m.detail = nil
		m.err = ""
		return m, nil
	case "q", "ctrl+c":
		m.out = browseOutcome{Action: "quit"}
		return m, tea.Quit
	case "a":
		if m.detail == nil {
			return m, nil
		}
		m.busy = "Updating…"
		m.err = ""
		return m, toggleAssignCmd(m.client, m.detail, m.myID)
	case "s":
		if m.detail == nil {
			return m, nil
		}
		m.out = browseOutcome{Action: "status", RequestID: m.detail.ID}
		return m, tea.Quit
	case "b":
		if m.detail == nil {
			return m, nil
		}
		m.out = browseOutcome{Action: "book", RequestID: m.detail.ID}
		return m, tea.Quit
	case "r":
		if m.detail == nil {
			return m, nil
		}
		m.busy = "Loading…"
		return m, loadDetailCmd(m.client, m.detail.ID)
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func loadDetailCmd(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		req, err := client.GetRequest(id)
		if err != nil {
			return detailLoadedMsg{err: err}
		}
		hours, err := client.ListRequestHours(id)
		if err != nil {
			if errors.Is(err, api.ErrUnauthorized) {
				return detailLoadedMsg{err: err}
			}
			var ae *api.APIError
			if errors.As(err, &ae) && ae.Status != 404 {
				// keep going without hours for other soft failures
			}
			hours = nil
		}
		events, _ := client.ListRequestActivity(id)
		return detailLoadedMsg{req: req, hours: hours, events: events}
	}
}

func toggleAssignCmd(client *api.Client, r *api.ClientRequest, myID string) tea.Cmd {
	return func() tea.Msg {
		var (
			updated *api.ClientRequest
			err     error
		)
		if isAssigned(*r, myID) {
			updated, err = client.UnassignRequest(r.ID, "me")
		} else {
			updated, err = client.AssignRequest(r.ID, "")
		}
		return assignedMsg{req: updated, err: err}
	}
}

func (m *browseModel) syncListFromDetail() {
	if m.detail == nil {
		return
	}
	for i := range m.list {
		if m.list[i].ID == m.detail.ID {
			m.list[i] = *m.detail
			break
		}
	}
	cur := m.table.Cursor()
	m.table.SetRows(tableRows(m.list, m.myID))
	if cur >= 0 && cur < len(m.list) {
		m.table.SetCursor(cur)
	}
}

func (m *browseModel) resize() {
	if m.width <= 0 {
		m.width = 100
	}
	if m.height <= 0 {
		m.height = 24
	}
	tableHeight := maxInt(5, m.height-6)
	m.table.SetHeight(tableHeight)
	m.table.SetWidth(m.width - 2)
	cols := m.table.Columns()
	if len(cols) >= 5 && m.width > 100 {
		used := 8 + 12 + 16 + 6 + 18 + 10
		cols[4].Width = maxInt(24, m.width-used)
		m.table.SetColumns(cols)
	}
	m.vp.Width = maxInt(40, m.width-2)
	m.vp.Height = maxInt(8, m.height-8)
}

func (m browseModel) View() string {
	if m.mode == modeDetail {
		return m.viewDetail()
	}
	return m.viewList()
}

func (m browseModel) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Requests · filter %s", m.statusFilter)))
	b.WriteString("\n\n")
	b.WriteString(m.table.View())
	b.WriteString("\n")
	if m.busy != "" {
		b.WriteString(okStyle.Render(m.busy) + "\n")
	}
	if m.err != "" {
		b.WriteString(errStyle.Render(m.err) + "\n")
	}
	b.WriteString(helpStyle.Render("↑/↓ scroll  →/enter details  f filter  r reload  q quit"))
	return b.String()
}

func (m browseModel) viewDetail() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Request detail"))
	b.WriteString("\n\n")
	b.WriteString(m.vp.View())
	b.WriteString("\n")
	if m.busy != "" {
		b.WriteString(okStyle.Render(m.busy) + "\n")
	}
	if m.err != "" {
		b.WriteString(errStyle.Render(m.err) + "\n")
	}
	b.WriteString(helpStyle.Render("←/esc back  a assign/unassign  s status  b book  r refresh  q quit"))
	return b.String()
}

func (m browseModel) detailContent() string {
	if m.detail == nil {
		return ""
	}
	r := m.detail
	width := maxInt(48, m.vp.Width-2)
	var b strings.Builder

	ref := r.Ref
	if ref == "" {
		ref = trunc(r.ID, 8)
	}

	// Header: ref + status — quick orientation
	b.WriteString(refStyle.Render(ref))
	b.WriteString("  ")
	b.WriteString(statusStyle(r.Status).Render(statusLabel(r.Status)))
	b.WriteString("\n\n")

	// The ask — what the engineer needs to read first
	b.WriteString(labelStyle.Render("ASK"))
	b.WriteString("\n")
	b.WriteString(askTitleStyle.Render(r.Title))
	b.WriteString("\n")
	desc := strings.TrimSpace(r.Description)
	if desc == "" {
		desc = "(no description)"
	}
	b.WriteString(askBodyStyle.Width(width).Render(desc))
	b.WriteString("\n")

	// Booking context — project is the important one
	b.WriteString(labelStyle.Render("BOOK ON"))
	b.WriteString("\n")
	if r.ProjectName != "" {
		b.WriteString(projectStyle.Render(r.ProjectName))
		if r.ClientName != "" {
			b.WriteString(mutedStyle.Render(" · " + r.ClientName))
		}
	} else if r.ClientName != "" {
		b.WriteString(valueStyle.Render(r.ClientName))
		b.WriteString(mutedStyle.Render(" · no project set"))
	} else {
		b.WriteString(mutedStyle.Render("—"))
	}
	b.WriteString("\n")
	if r.TypeName != "" {
		b.WriteString(metaLineStyle.Render("Type  " + r.TypeName))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Ownership / progress
	b.WriteString(labelStyle.Render("TEAM"))
	b.WriteString("\n")
	who := r.CreatedByName
	if who == "" {
		who = "—"
	}
	b.WriteString(metaLineStyle.Render("Asked by  " + who + " · " + formatTime(r.CreatedAt, m.loc)))
	b.WriteString("\n")
	names := assigneeNames(*r)
	if names == "" {
		names = "nobody yet"
	}
	if isAssigned(*r, m.myID) {
		names += " · you"
	}
	b.WriteString(valueStyle.Render("Assigned  " + names))
	b.WriteString("\n")
	b.WriteString(valueStyle.Render(fmt.Sprintf("Logged    %.1fh", float64(r.MinutesLogged)/60)))
	b.WriteString("\n\n")

	b.WriteString(historyHeadStyle.Render("HISTORY"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", minInt(width, 56))))
	b.WriteString("\n")
	b.WriteString(formatHistoryText(r, m.events, m.hours, m.loc))
	return b.String()
}

func formatHistoryText(r *api.ClientRequest, events []api.ActivityEvent, hours []api.TimeEntry, loc *time.Location) string {
	type line struct {
		when  time.Time
		text  string
		hours bool
	}
	var lines []line
	if created, err := parseTime(r.CreatedAt, loc); err == nil {
		who := r.CreatedByName
		if who == "" {
			who = "someone"
		}
		lines = append(lines, line{created, fmt.Sprintf("created by %s", who), false})
	}
	for _, ev := range events {
		if ev.EventType == "client_request.created" {
			continue
		}
		when, err := parseTime(ev.CreatedAt, loc)
		if err != nil {
			continue
		}
		who := ev.ActorName
		if who == "" {
			who = "someone"
		}
		text := formatEvent(ev, who)
		if text != "" {
			lines = append(lines, line{when, text, false})
		}
	}
	for _, e := range hours {
		// Timeline uses created_at (when booked), not entry_date (work day).
		// entry_date alone is date-only and was shown as 02:00 after UTC→local.
		when, err := parseTime(e.CreatedAt, loc)
		if err != nil {
			when, err = parseTime(e.EntryDate, loc)
			if err != nil {
				continue
			}
		}
		who := e.UserName
		if who == "" {
			who = "someone"
		}
		desc := strings.TrimSpace(e.Description)
		if desc == "" {
			desc = e.ProjectName
		}
		text := fmt.Sprintf("%.1fh %s · %s [%s]", float64(e.DurationMinutes)/60, who, trunc(desc, 40), e.Status)
		if e.EntryDate != "" {
			text += " · day " + e.EntryDate
		}
		lines = append(lines, line{when, text, true})
	}
	if len(lines) == 0 {
		return mutedStyle.Render("  (nothing yet)") + "\n"
	}
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			if lines[j].when.Before(lines[i].when) {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}
	var b strings.Builder
	for _, l := range lines {
		ts := mutedStyle.Render(tz.Format(l.when, loc))
		style := metaLineStyle
		if l.hours {
			style = hoursLineStyle
		}
		b.WriteString("  " + ts + "  " + style.Render(l.text) + "\n")
	}
	return b.String()
}

func formatEvent(ev api.ActivityEvent, who string) string {
	switch ev.EventType {
	case "client_request.status_changed":
		var p struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if p.From != "" && p.To != "" {
			return fmt.Sprintf("status %s → %s by %s", p.From, p.To, who)
		}
		return fmt.Sprintf("status updated by %s", who)
	case "client_request.assigned":
		return fmt.Sprintf("assigned by %s", who)
	case "client_request.unassigned":
		return fmt.Sprintf("unassigned by %s", who)
	default:
		return fmt.Sprintf("%s by %s", ev.EventType, who)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
