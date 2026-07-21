package desk

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/zulutime-io/cli/internal/api"
	"github.com/zulutime-io/cli/internal/config"
	"github.com/zulutime-io/cli/internal/timer"
)

type panel int

const (
	panelAssigned panel = iota
	panelTimer
	panelEntries
	panelRequests
)

const panelCount = 4

type deskData struct {
	Assigned    []api.ClientRequest
	Requests    []api.ClientRequest
	Entries     []api.TimeEntry
	Timer       *timer.State
	CanRequests bool
}

type deskOutcome struct {
	Action      string
	RequestID   string
	EntryID     string
	Label       string
	Focus       panel
	AssignedIdx int
	RequestIdx  int
	EntryIdx    int
}

type model struct {
	client *api.Client
	cfg    *config.Config
	myID   string
	data   *deskData

	focus       panel
	assignedIdx int
	requestIdx  int
	entryIdx    int

	width  int
	height int
	status string
	out    deskOutcome
	tick   int
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	panelOn     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Padding(0, 1)
	panelOff    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	rowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	refStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	timerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	draftStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("228"))
	rejectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	submitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
)

type tickMsg time.Time

func runTUI(client *api.Client, cfg *config.Config, data *deskData, myID string, focus panel, aIdx, rIdx, eIdx int) (deskOutcome, error) {
	m := model{
		client:      client,
		cfg:         cfg,
		myID:        myID,
		data:        data,
		focus:       focus,
		assignedIdx: clamp(aIdx, 0, max(0, len(data.Assigned)-1)),
		requestIdx:  clamp(rIdx, 0, max(0, len(data.Requests)-1)),
		entryIdx:    clamp(eIdx, 0, max(0, len(data.Entries)-1)),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return deskOutcome{}, err
	}
	fm, ok := final.(model)
	if !ok {
		return deskOutcome{Action: "quit"}, nil
	}
	if fm.out.Action == "" {
		fm.out.Action = "quit"
	}
	fm.out.Focus = fm.focus
	fm.out.AssignedIdx = fm.assignedIdx
	fm.out.RequestIdx = fm.requestIdx
	fm.out.EntryIdx = fm.entryIdx
	return fm.out, nil
}

func (m model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tickMsg:
		m.tick++
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.out = deskOutcome{Action: "quit"}
		return m, tea.Quit
	case "r":
		m.out = deskOutcome{Action: "reload"}
		return m, tea.Quit
	case "left", "h", "shift+tab":
		m.focus = prevPanel(m.focus)
		return m, nil
	case "right", "l", "tab":
		m.focus = nextPanel(m.focus)
		return m, nil
	case "up", "k":
		m.move(-1)
		return m, nil
	case "down", "j":
		m.move(1)
		return m, nil
	case "enter":
		return m.enterAction()
	case "b":
		return m.bookAction()
	case "a":
		return m.assignAction()
	case "s":
		if m.focus == panelEntries && len(m.data.Entries) > 0 {
			e := m.data.Entries[m.entryIdx]
			if e.Status == "draft" || e.Status == "rejected" {
				m.out = deskOutcome{Action: "submit-entry", EntryID: e.ID}
				return m, tea.Quit
			}
			m.status = "Only draft/rejected can be submitted"
			return m, nil
		}
		if m.focus == panelTimer && m.data.Timer != nil {
			m.out = deskOutcome{Action: "book-timer"}
			return m, tea.Quit
		}
		return m, nil
	case "S":
		m.out = deskOutcome{Action: "submit-all"}
		return m, tea.Quit
	case "e":
		if m.focus == panelEntries && len(m.data.Entries) > 0 {
			e := m.data.Entries[m.entryIdx]
			if e.Status == "draft" || e.Status == "rejected" {
				m.out = deskOutcome{Action: "edit-entry", EntryID: e.ID}
				return m, tea.Quit
			}
			m.status = "Only draft/rejected can be edited"
			return m, nil
		}
		return m, nil
	case "t":
		if m.data.Timer != nil {
			m.out = deskOutcome{Action: "book-timer"}
		} else {
			label := ""
			if m.focus == panelAssigned && len(m.data.Assigned) > 0 {
				label = m.data.Assigned[m.assignedIdx].Title
			} else if m.focus == panelRequests && len(m.data.Requests) > 0 {
				label = m.data.Requests[m.requestIdx].Title
			}
			m.out = deskOutcome{Action: "start-timer", Label: label}
		}
		return m, tea.Quit
	case "x":
		if m.data.Timer != nil {
			m.out = deskOutcome{Action: "cancel-timer"}
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

func (m *model) move(delta int) {
	switch m.focus {
	case panelAssigned:
		if len(m.data.Assigned) == 0 {
			return
		}
		m.assignedIdx = clamp(m.assignedIdx+delta, 0, len(m.data.Assigned)-1)
	case panelRequests:
		if len(m.data.Requests) == 0 {
			return
		}
		m.requestIdx = clamp(m.requestIdx+delta, 0, len(m.data.Requests)-1)
	case panelEntries:
		if len(m.data.Entries) == 0 {
			return
		}
		m.entryIdx = clamp(m.entryIdx+delta, 0, len(m.data.Entries)-1)
	}
}

func (m model) assignAction() (tea.Model, tea.Cmd) {
	if !m.data.CanRequests {
		m.status = api.MsgRequestsLocked
		return m, nil
	}
	var r *api.ClientRequest
	switch m.focus {
	case panelAssigned:
		if len(m.data.Assigned) == 0 {
			m.status = "No assigned requests"
			return m, nil
		}
		r = &m.data.Assigned[m.assignedIdx]
	case panelRequests:
		if len(m.data.Requests) == 0 {
			m.status = "No open requests"
			return m, nil
		}
		r = &m.data.Requests[m.requestIdx]
	default:
		return m, nil
	}
	action := "assign-request"
	if isAssigned(*r, m.myID) {
		action = "unassign-request"
	}
	m.out = deskOutcome{Action: action, RequestID: r.ID}
	return m, tea.Quit
}

func (m model) enterAction() (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelAssigned:
		if !m.data.CanRequests {
			m.status = api.MsgRequestsLocked
			return m, nil
		}
		if len(m.data.Assigned) == 0 {
			m.status = "No assigned requests"
			return m, nil
		}
		m.out = deskOutcome{Action: "request-detail", RequestID: m.data.Assigned[m.assignedIdx].ID}
		return m, tea.Quit
	case panelRequests:
		if !m.data.CanRequests {
			m.status = api.MsgRequestsLocked
			return m, nil
		}
		if len(m.data.Requests) == 0 {
			m.status = "No open requests"
			return m, nil
		}
		m.out = deskOutcome{Action: "request-detail", RequestID: m.data.Requests[m.requestIdx].ID}
		return m, tea.Quit
	case panelTimer:
		return m.timerPrimary()
	case panelEntries:
		// enter does nothing on Recent — use e to edit, s to submit
		return m, nil
	}
	return m, nil
}

func (m model) bookAction() (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelAssigned:
		if m.data.CanRequests && len(m.data.Assigned) > 0 {
			m.out = deskOutcome{Action: "book-request", RequestID: m.data.Assigned[m.assignedIdx].ID}
			return m, tea.Quit
		}
		// No request selected → same as normal hours booking
		m.out = deskOutcome{Action: "book-hours"}
		return m, tea.Quit
	case panelRequests:
		if m.data.CanRequests && len(m.data.Requests) > 0 {
			m.out = deskOutcome{Action: "book-request", RequestID: m.data.Requests[m.requestIdx].ID}
			return m, tea.Quit
		}
		m.out = deskOutcome{Action: "book-hours"}
		return m, tea.Quit
	case panelTimer:
		if m.data.Timer != nil {
			return m.timerPrimary()
		}
		m.out = deskOutcome{Action: "book-hours"}
		return m, tea.Quit
	case panelEntries:
		m.out = deskOutcome{Action: "book-hours"}
		return m, tea.Quit
	}
	m.out = deskOutcome{Action: "book-hours"}
	return m, tea.Quit
}

func (m model) timerPrimary() (tea.Model, tea.Cmd) {
	if m.data.Timer != nil {
		m.out = deskOutcome{Action: "book-timer"}
	} else {
		label := ""
		if len(m.data.Assigned) > 0 {
			label = m.data.Assigned[clamp(m.assignedIdx, 0, len(m.data.Assigned)-1)].Title
		}
		m.out = deskOutcome{Action: "start-timer", Label: label}
	}
	return m, tea.Quit
}

func (m model) View() string {
	w := m.width
	if w <= 0 {
		w = 88
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("ztime desk"))
	b.WriteString(mutedStyle.Render("  ·  personal workspace"))
	b.WriteString("\n\n")

	// Wide terminals: 2×2 dashboard. Narrow: one focused panel (tabs).
	if w >= 76 {
		b.WriteString(m.renderDashboard(w))
	} else {
		b.WriteString(m.renderTabs())
		b.WriteString("\n\n")
		switch m.focus {
		case panelAssigned:
			b.WriteString(m.renderAssigned(w))
		case panelTimer:
			b.WriteString(m.renderTimer(w))
		case panelEntries:
			b.WriteString(m.renderEntries(w))
		case panelRequests:
			b.WriteString(m.renderRequests(w))
		}
	}

	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(warnStyle.Render(m.status) + "\n")
	}
	b.WriteString(helpStyle.Render(m.helpLine()))
	return b.String()
}

func (m model) renderDashboard(w int) string {
	gap := 1
	colW := (w - gap) / 2
	if colW < 34 {
		colW = 34
	}
	innerW := colW - 4
	if innerW < 24 {
		innerW = 24
	}
	bodyLines := 8
	if m.height >= 28 {
		bodyLines = 10
	}
	if m.height >= 36 {
		bodyLines = 12
	}

	assignedTitle := fmt.Sprintf("Assigned (%d)", len(m.data.Assigned))
	if !m.data.CanRequests {
		assignedTitle = "Assigned (Team)"
	}
	requestsTitle := fmt.Sprintf("Requests (%d)", len(m.data.Requests))
	if !m.data.CanRequests {
		requestsTitle = "Requests (Team)"
	}

	top := lipgloss.JoinHorizontal(lipgloss.Top,
		m.framePanel(panelAssigned, assignedTitle, m.compactAssigned(innerW, bodyLines), colW, bodyLines+2),
		strings.Repeat(" ", gap),
		m.framePanel(panelTimer, "Timer", m.compactTimer(innerW, bodyLines), colW, bodyLines+2),
	)
	bottom := lipgloss.JoinHorizontal(lipgloss.Top,
		m.framePanel(panelEntries, fmt.Sprintf("Recent (%d)", len(m.data.Entries)), m.compactEntries(innerW, bodyLines), colW, bodyLines+2),
		strings.Repeat(" ", gap),
		m.framePanel(panelRequests, requestsTitle, m.compactRequests(innerW, bodyLines), colW, bodyLines+2),
	)
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
}

func (m model) framePanel(p panel, title, body string, width, height int) string {
	border := lipgloss.Color("240")
	titleStyleLocal := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	if p == m.focus {
		border = lipgloss.Color("81")
		titleStyleLocal = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true).Background(lipgloss.Color("57")).Padding(0, 1)
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Width(width - 2).
		Height(height).
		Padding(0, 1)
	header := titleStyleLocal.Render(title)
	inner := strings.TrimRight(body, "\n")
	return box.Render(header + "\n" + inner)
}

func (m model) compactAssigned(w, maxLines int) string {
	if !m.data.CanRequests {
		return mutedStyle.Render("Team plan required")
	}
	if len(m.data.Assigned) == 0 {
		return mutedStyle.Render("Nothing assigned.\nOpen Requests · a to assign")
	}
	return m.compactRequestList(w, maxLines, m.data.Assigned, m.assignedIdx, m.focus == panelAssigned)
}

func (m model) compactRequests(w, maxLines int) string {
	if !m.data.CanRequests {
		return mutedStyle.Render("Team plan required")
	}
	if len(m.data.Requests) == 0 {
		return mutedStyle.Render("No open requests")
	}
	return m.compactRequestList(w, maxLines, m.data.Requests, m.requestIdx, m.focus == panelRequests)
}

func (m model) compactRequestList(w, maxLines int, list []api.ClientRequest, idx int, showSel bool) string {
	var b strings.Builder
	limit := min(len(list), maxLines)
	start := 0
	if showSel && idx >= limit {
		start = idx - limit + 1
	}
	end := min(len(list), start+limit)
	for i := start; i < end; i++ {
		r := list[i]
		ref := r.Ref
		if ref == "" {
			ref = trunc(r.ID, 6)
		}
		mine := ""
		if isAssigned(r, m.myID) {
			mine = "★ "
		}
		line := trunc(fmt.Sprintf("%s%s · %s", mine, ref, r.Title), w)
		if showSel && i == idx {
			b.WriteString(selStyle.Render("› "+line) + "\n")
		} else {
			b.WriteString(rowStyle.Render("  "+line) + "\n")
		}
	}
	if len(list) > end {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  … +%d more", len(list)-end)) + "\n")
	}
	return b.String()
}

func (m model) compactTimer(w, maxLines int) string {
	_ = maxLines
	s := m.data.Timer
	if s == nil {
		return mutedStyle.Render("Stopped\nenter/t to start")
	}
	label := s.Label
	if label == "" {
		label = "work"
	}
	var b strings.Builder
	b.WriteString(timerStyle.Render("⏱  "+timer.FormatElapsed(s.Elapsed())) + "\n")
	b.WriteString(titleStyle.Render(trunc(label, w)) + "\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("~%.2fh · enter stop&book", timer.HoursRounded(s.Elapsed()))) + "\n")
	return b.String()
}

func (m model) compactEntries(w, maxLines int) string {
	if len(m.data.Entries) == 0 {
		return mutedStyle.Render("No recent drafts")
	}
	var b strings.Builder
	limit := min(len(m.data.Entries), maxLines)
	start := 0
	if m.focus == panelEntries && m.entryIdx >= limit {
		start = m.entryIdx - limit + 1
	}
	end := min(len(m.data.Entries), start+limit)
	for i := start; i < end; i++ {
		e := m.data.Entries[i]
		line := trunc(fmt.Sprintf("%.1fh %s · %s", float64(e.DurationMinutes)/60, e.Status, e.Description), w)
		if e.Description == "" {
			line = trunc(fmt.Sprintf("%.1fh %s · %s", float64(e.DurationMinutes)/60, e.Status, e.ProjectName), w)
		}
		if m.focus == panelEntries && i == m.entryIdx {
			b.WriteString(selStyle.Render("› "+line) + "\n")
		} else {
			b.WriteString(rowStyle.Render("  "+line) + "\n")
		}
	}
	return b.String()
}

func (m model) renderTabs() string {
	tabs := []struct {
		p panel
		t string
	}{
		{panelAssigned, fmt.Sprintf("Assigned (%d)", len(m.data.Assigned))},
		{panelTimer, "Timer"},
		{panelEntries, fmt.Sprintf("Recent (%d)", len(m.data.Entries))},
		{panelRequests, fmt.Sprintf("Requests (%d)", len(m.data.Requests))},
	}
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		label := t.t
		if !m.data.CanRequests {
			switch t.p {
			case panelAssigned:
				label = "Assigned (Team)"
			case panelRequests:
				label = "Requests (Team)"
			}
		}
		if t.p == m.focus {
			parts = append(parts, panelOn.Render(label))
		} else {
			parts = append(parts, panelOff.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

func (m model) renderLockedRequests() string {
	var b strings.Builder
	b.WriteString(warnStyle.Render(api.MsgRequestsLocked))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("Assigned & Requests need Team. Timer and Recent stay free."))
	b.WriteString("\n")
	return b.String()
}

func (m model) renderAssigned(w int) string {
	if !m.data.CanRequests {
		return m.renderLockedRequests()
	}
	var b strings.Builder
	b.WriteString(mutedStyle.Render("←/→ panels   ↑/↓ select   enter detail   b book hours   a assign/unassign"))
	b.WriteString("\n\n")
	if len(m.data.Assigned) == 0 {
		b.WriteString(mutedStyle.Render("Nothing assigned to you. Open Requests panel and press a to assign. Or press b to book hours."))
		b.WriteString("\n")
		return b.String()
	}
	return m.renderRequestList(w, m.data.Assigned, m.assignedIdx)
}

func (m model) renderRequests(w int) string {
	if !m.data.CanRequests {
		return m.renderLockedRequests()
	}
	var b strings.Builder
	b.WriteString(mutedStyle.Render("←/→ panels   ↑/↓ select   enter detail   b book hours   a assign/unassign"))
	b.WriteString("\n\n")
	if len(m.data.Requests) == 0 {
		b.WriteString(mutedStyle.Render("No open/in-progress requests in the org inbox. Press b to book hours."))
		b.WriteString("\n")
		return b.String()
	}
	return m.renderRequestList(w, m.data.Requests, m.requestIdx)
}

func (m model) renderRequestList(w int, list []api.ClientRequest, idx int) string {
	var b strings.Builder
	start := max(0, idx-6)
	end := min(len(list), start+14)
	if end-start < 14 {
		start = max(0, end-14)
	}
	for i := start; i < end; i++ {
		r := list[i]
		ref := r.Ref
		if ref == "" {
			ref = trunc(r.ID, 8)
		}
		mine := ""
		if isAssigned(r, m.myID) {
			mine = "★ "
		}
		line := fmt.Sprintf("%s%-8s  %-12s  %-16s  %5.1fh  %s",
			mine, ref, r.Status, trunc(r.ClientName, 16), float64(r.MinutesLogged)/60, trunc(r.Title, max(12, w-55)))
		if i == idx {
			b.WriteString(selStyle.Render("› "+line) + "\n")
		} else {
			b.WriteString(rowStyle.Render("  "+line) + "\n")
		}
	}
	if idx >= 0 && idx < len(list) {
		r := list[idx]
		b.WriteString("\n")
		b.WriteString(refStyle.Render(nullRef(r)) + "  " + titleStyle.Render(r.Title) + "\n")
		desc := strings.TrimSpace(r.Description)
		if desc != "" {
			b.WriteString(rowStyle.Render(trunc(desc, max(40, w-4))) + "\n")
		}
		proj := r.ProjectName
		if proj == "" {
			proj = "no project"
		}
		assignHint := "a to assign"
		if isAssigned(r, m.myID) {
			assignHint = "a to unassign"
		}
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%s · %s · enter detail · b book · %s", r.ClientName, proj, assignHint)) + "\n")
	}
	return b.String()
}

func (m model) renderTimer(w int) string {
	var b strings.Builder
	b.WriteString(mutedStyle.Render("enter/t book&stop   x cancel   t start when idle"))
	b.WriteString("\n\n")
	s := m.data.Timer
	if s == nil {
		b.WriteString(mutedStyle.Render("No timer running."))
		b.WriteString("\n\n")
		b.WriteString(okStyle.Render("Press enter or t to start a timer"))
		if len(m.data.Assigned) > 0 {
			b.WriteString(mutedStyle.Render("\n(label from selected assigned request if any)"))
		}
		b.WriteString("\n")
		return b.String()
	}
	elapsed := timer.FormatElapsed(s.Elapsed())
	hours := timer.HoursRounded(s.Elapsed())
	label := s.Label
	if label == "" {
		label = "work"
	}
	b.WriteString(timerStyle.Render(fmt.Sprintf("⏱  %s", elapsed)))
	b.WriteString(titleStyle.Render("  ·  "+label) + "\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("since %s  ·  ~%.2fh when booked", s.StartedAt.Local().Format("15:04"), hours)) + "\n")
	if s.GitRepo != "" {
		repo := s.GitRepo
		if s.GitBranch != "" {
			repo += " (" + s.GitBranch + ")"
		}
		b.WriteString(mutedStyle.Render(repo) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(okStyle.Render("enter → stop & book") + "\n")
	return b.String()
}

func (m model) renderEntries(w int) string {
	var b strings.Builder
	b.WriteString(mutedStyle.Render("s submit (confirm)   e edit (draft/rejected)   S submit all   b book hours"))
	b.WriteString("\n\n")
	if len(m.data.Entries) == 0 {
		b.WriteString(mutedStyle.Render("No draft/rejected/submitted entries in the last 14 days."))
		b.WriteString("\n")
		return b.String()
	}
	start := max(0, m.entryIdx-6)
	end := min(len(m.data.Entries), start+14)
	if end-start < 14 {
		start = max(0, end-14)
	}
	for i := start; i < end; i++ {
		e := m.data.Entries[i]
		st := statusColored(e.Status)
		line := fmt.Sprintf("%s  %5.1fh  %-12s  %-14s  %s",
			e.EntryDate, float64(e.DurationMinutes)/60, trunc(e.ClientName, 12), trunc(e.ProjectName, 14), trunc(e.Description, max(10, w-52)))
		prefix := "  "
		body := rowStyle.Render(line)
		if i == m.entryIdx {
			prefix = "› "
			body = selStyle.Render(line)
		}
		b.WriteString(prefix + st + "  " + body + "\n")
	}
	return b.String()
}

func statusColored(status string) string {
	switch status {
	case "draft":
		return draftStyle.Render(fmt.Sprintf("%-9s", status))
	case "rejected":
		return rejectStyle.Render(fmt.Sprintf("%-9s", status))
	case "submitted":
		return submitStyle.Render(fmt.Sprintf("%-9s", status))
	default:
		return mutedStyle.Render(fmt.Sprintf("%-9s", status))
	}
}

func (m model) helpLine() string {
	base := "←/→ panel  ↑/↓  b book hours  r reload  q quit"
	switch m.focus {
	case panelAssigned, panelRequests:
		return base + "  ·  enter detail  a assign"
	case panelTimer:
		return base + "  ·  enter/t start/stop  x cancel"
	case panelEntries:
		return base + "  ·  s submit  e edit  S submit-all"
	}
	return base
}

func nullRef(r api.ClientRequest) string {
	if r.Ref != "" {
		return r.Ref
	}
	return trunc(r.ID, 8)
}

func nextPanel(p panel) panel {
	return panel((int(p) + 1) % panelCount)
}

func prevPanel(p panel) panel {
	return panel((int(p) + panelCount - 1) % panelCount)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
