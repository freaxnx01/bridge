package nav

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/launcher"
	"github.com/freaxnx01/bridge/internal/overview"
)

type Model struct {
	cfg           Config
	width, height int
	spin          spinner.Model

	screen      screen
	pickerFocus focus
	showLegend  bool // ? toggles the status-glyph legend overlay (picker/dash only)

	filter      textinput.Model
	sessions    []sessionRow
	localRepos  []repoRow
	remoteRepos []repoRow
	remoteState loadState
	pickerSel   int
	sessionSel  int
	forgeFilter string   // active forge subfilter key ("" = All); session-local, ctrl+f cycles it
	mruPaths    []string // raw MRU order (from recentMsg); resolved lazily by recentRepos
	recentSel   int

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
	repoModal   *newRepoModal
	details     map[string]*worktreeDetails // per-worktree panel cache, keyed by path

	overview      overview.Snapshot
	overviewState loadState
	ovFocus       ovPane // which overview pane has focus
	ovRankedSel   int
	ovInboxSel    int

	agents            []agentRow
	agentsSel         int
	agentsState       loadState
	agentsUnavailable bool

	status string
}

func initialModel(cfg Config) Model {
	if cfg.Backend == nil {
		cfg.Backend = launcher.NewBackend()
	}
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
	cmds := []tea.Cmd{
		m.spin.Tick,
		loadLocalReposCmd(m.cfg.ReposRoots),
		loadSessionsCmd(m.cfg.Backend, m.cfg.SlotsPath),
		loadRemoteCmd(m.cfg.RemoteCache),
	}
	if m.cfg.RecentPath != "" {
		cmds = append(cmds, loadRecentCmd(m.cfg.RecentPath))
	}
	return tea.Batch(cmds...)
}
