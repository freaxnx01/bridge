package nav

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
)

type Model struct {
	cfg           Config
	width, height int
	spin          spinner.Model

	screen      screen
	pickerFocus focus

	filter      textinput.Model
	sessions    []sessionRow
	localRepos  []repoRow
	remoteRepos []repoRow
	remoteState loadState
	pickerSel   int
	sessionSel  int

	repo        core.Repo
	dashRows    []dashRow
	dashSel     int
	dashFocus   dashFocus
	issues      []issueRow
	issueSel    int
	issuesState loadState
	notes       []noteFile
	ideasScroll int // top display-line offset of the Ideas pane
	todosScroll int // top display-line offset of the Todos pane
	notesState  loadState
	modal       *newWorktreeModal
	details     map[string]*worktreeDetails // per-worktree panel cache, keyed by path

	status string
}

func initialModel(cfg Config) Model {
	ti := textinput.New()
	ti.Placeholder = "filter…"
	ti.Prompt = "filter: "
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		cfg:         cfg,
		spin:        sp,
		screen:      screenPicker,
		pickerFocus: focusFilter,
		filter:      ti,
		details:     map[string]*worktreeDetails{},
		remoteState: loadPending,
		status:      "ready",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spin.Tick,
		loadLocalReposCmd(m.cfg.ReposRoots),
		loadSessionsCmd(m.cfg.SlotsPath),
		loadRemoteCmd(m.cfg.RemoteCache),
	)
}
