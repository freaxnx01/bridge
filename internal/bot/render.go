package bot

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/navdata"
)

// Button is an inline-keyboard callback button.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Keyboard is rows of buttons.
type Keyboard [][]Button

const (
	maxTextRunes   = 4000 // Telegram's message limit is 4096
	maxButtonRunes = 32
)

// Render returns the message text (plain, no parse mode) and keyboard for s.
// A pending confirm or prompt replaces the keyboard with its own buttons.
func Render(s State) (string, Keyboard) {
	var text string
	var kb Keyboard
	switch s.Screen {
	case ScreenDash:
		text, kb = renderDash(s)
	case ScreenIssues:
		text, kb = renderIssues(s)
	default:
		text, kb = renderPicker(s)
	}
	// The overlay and notices go last and must survive: cut the body, not them.
	var tail string
	if q, okb := renderOverlay(s); q != "" {
		tail, kb = "\n\n"+q, okb
	}
	for _, line := range []string{s.Notice, s.Busy} {
		if line != "" {
			tail += "\n" + line
		}
	}
	if kb == nil {
		kb = Keyboard{}
	}
	room := max(maxTextRunes-utf8.RuneCountInString(tail), 1)
	return navdata.TruncRunes(navdata.TruncRunes(text, room)+tail, maxTextRunes), kb
}

func btn(text string, epoch int, verb string, args ...string) Button {
	return Button{Text: text, Data: data(epoch, verb, args...)}
}

// oneLine collapses whitespace (titles may hold newlines) for a button label.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func renderPicker(s State) (string, Keyboard) {
	var b strings.Builder
	present := navdata.PresentForges(s.Local, s.Remote)
	forgeLabel := "All"
	for _, o := range present {
		if o.Key == s.Forge {
			forgeLabel = o.Label
		}
	}
	fmt.Fprintf(&b, "🗂 bridge · repos · %s", forgeLabel)
	if !s.ReposLoaded {
		b.WriteString("\n⏳ loading repos…")
		return b.String(), Keyboard{{btn("🔄", s.Epoch, "rf")}}
	}
	items := s.PickerItems()
	if s.Filter != "" {
		fmt.Fprintf(&b, "\n🔍 %q", s.Filter)
	}
	pages := pageCount(len(items), PageSize)
	page := clamp(s.Page, 0, pages-1)
	fmt.Fprintf(&b, "\n%d repos · page %d/%d", len(items), page+1, pages)

	var kb Keyboard
	if len(present) > 1 {
		kb = append(kb, []Button{btn(forgeLabel+" ▸", s.Epoch, "fg")})
	}
	var row []Button
	for i := page * PageSize; i < min((page+1)*PageSize, len(items)); i++ {
		row = append(row, btn(pickerButtonText(items, i), s.Epoch, "r", strconv.Itoa(i)))
		if len(row) == 2 {
			kb, row = append(kb, row), nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	if pages > 1 {
		kb = append(kb, []Button{
			btn("◀", s.Epoch, "pg", "-1"),
			btn(fmt.Sprintf("%d/%d", page+1, pages), s.Epoch, "nop"),
			btn("▶", s.Epoch, "pg", "+1"),
		})
	}
	filter := btn("🔍 Filter", s.Epoch, "f")
	if s.Filter != "" {
		filter = btn("✖ Filter", s.Epoch, "fx")
	}
	kb = append(kb, []Button{filter, btn("➕ New repo", s.Epoch, "nr"), btn("🔄", s.Epoch, "rf")})
	return b.String(), kb
}

// pickerButtonText is the repo name, qualified by forge (and owner) only when
// the name is ambiguous in the list, prefixed ⭐ (MRU) or ↓ (remote).
func pickerButtonText(items []PickerItem, i int) string {
	f, owner, _, name := navdata.Parts(items[i].Entry)
	byName, byNameForge := 0, 0
	for _, it := range items {
		f2, _, _, n2 := navdata.Parts(it.Entry)
		if strings.EqualFold(n2, name) {
			byName++
			if f2 == f {
				byNameForge++
			}
		}
	}
	label := name
	switch {
	case byNameForge > 1:
		label += " · " + f + "/" + owner
	case byName > 1:
		label += " · " + f
	}
	switch {
	case items[i].Star:
		label = "⭐ " + label
	case items[i].Entry.Remote != nil:
		label = "↓ " + label
	}
	return navdata.TruncRunes(label, maxButtonRunes)
}

func repoWhere(r core.Repo) string {
	switch {
	case r.Forge != "" && r.Owner != "":
		return r.Forge + "/" + r.Owner
	case r.Forge != "":
		return r.Forge
	}
	return r.Path
}

func renderDash(s State) (string, Keyboard) {
	var b strings.Builder
	fmt.Fprintf(&b, "📁 %s · %s", s.Repo.Name, repoWhere(s.Repo))
	bottom := Keyboard{
		{btn("➕ Worktree", s.Epoch, "nw"), btn("🐞 Issues", s.Epoch, "is")},
		{btn("🔄", s.Epoch, "rf"), btn("◀ Repos", s.Epoch, "bk")},
	}
	if s.Rows == nil {
		b.WriteString("\n⏳ loading…")
		return b.String(), bottom
	}
	var starts, stops []Button
	for i, r := range s.Rows {
		name := navdata.TruncRunes(rowName(r), 24)
		sess := "○"
		if r.HasSession {
			sess = "● " + r.State
		}
		fmt.Fprintf(&b, "\n%s  %s  %s", name, sess, gitCell(s.Status, r.Path))
		starts = append(starts, btn(name+" ▶", s.Epoch, "w", strconv.Itoa(i)))
		if r.HasSession {
			stops = append(stops, btn(name+" ⏹", s.Epoch, "x", strconv.Itoa(i)))
		}
	}
	b.WriteString("\n" + issuesSummary(s))
	var kb Keyboard
	kb = appendGrid(kb, starts, 3)
	kb = appendGrid(kb, stops, 3)
	return b.String(), append(kb, bottom...)
}

func appendGrid(kb Keyboard, buttons []Button, perRow int) Keyboard {
	for len(buttons) > 0 {
		n := min(perRow, len(buttons))
		kb = append(kb, buttons[:n])
		buttons = buttons[n:]
	}
	return kb
}

// gitCell: … pending, ? unreadable, ✎N modified, ∅ no upstream, ↑ahead ↓behind, ✓ clean.
func gitCell(st map[string]RowStatus, path string) string {
	rs, ok := st[path]
	switch {
	case !ok:
		return "…"
	case rs.Err:
		return "?"
	}
	d := rs.Dirty
	var t []string
	if d.Modified > 0 {
		t = append(t, fmt.Sprintf("✎%d", d.Modified))
	}
	if d.NoUpstream {
		t = append(t, "∅")
	} else {
		if d.Ahead > 0 {
			t = append(t, fmt.Sprintf("↑%d", d.Ahead))
		}
		if d.Behind > 0 {
			t = append(t, fmt.Sprintf("↓%d", d.Behind))
		}
	}
	if len(t) == 0 {
		return "✓"
	}
	return strings.Join(t, " ")
}

func issuesSummary(s State) string {
	switch {
	case !hasForgeIDs(s.Repo):
		return "Issues: —"
	case !s.IssuesLoaded:
		return "Issues: loading…"
	case len(s.Issues) == 0:
		return "Issues: none"
	}
	const show = 2
	var parts []string
	for i := 0; i < min(show, len(s.Issues)); i++ {
		parts = append(parts, fmt.Sprintf("#%d %s", s.Issues[i].Number, navdata.TruncRunes(oneLine(s.Issues[i].Title), 24)))
	}
	out := "Issues: " + strings.Join(parts, " · ")
	if len(s.Issues) > show {
		out += fmt.Sprintf(" (+%d)", len(s.Issues)-show)
	}
	return out
}

func renderIssues(s State) (string, Keyboard) {
	var b strings.Builder
	fmt.Fprintf(&b, "🐞 %s · open issues", s.Repo.Name)
	back := []Button{btn("◀ Dashboard", s.Epoch, "bk")}
	switch {
	case !hasForgeIDs(s.Repo):
		b.WriteString("\nno forge issues for this repo")
		return b.String(), Keyboard{back}
	case !s.IssuesLoaded:
		b.WriteString("\n⏳ loading…")
		return b.String(), Keyboard{back}
	case len(s.Issues) == 0:
		b.WriteString("\nnone open")
		return b.String(), Keyboard{back}
	}
	pages := pageCount(len(s.Issues), IssuePageSize)
	page := clamp(s.IssuePage, 0, pages-1)
	fmt.Fprintf(&b, " (%d) · page %d/%d", len(s.Issues), page+1, pages)
	kb := Keyboard{}
	for i := page * IssuePageSize; i < min((page+1)*IssuePageSize, len(s.Issues)); i++ {
		is := s.Issues[i]
		label := navdata.TruncRunes(oneLine(fmt.Sprintf("#%d %s", is.Number, is.Title)), 48)
		kb = append(kb, []Button{btn(label, s.Epoch, "i", strconv.Itoa(i))})
	}
	if pages > 1 {
		kb = append(kb, []Button{
			btn("◀", s.Epoch, "ip", "-1"),
			btn(fmt.Sprintf("%d/%d", page+1, pages), s.Epoch, "nop"),
			btn("▶", s.Epoch, "ip", "+1"),
		})
	}
	return b.String(), append(kb, back)
}

// renderOverlay returns the question and keyboard of a pending confirm or
// prompt, or "" when there is none.
func renderOverlay(s State) (string, Keyboard) {
	cancel := []Button{btn("✖ Cancel", s.Epoch, "no")}
	if c := s.Confirm; c != nil {
		return confirmQuestion(s, *c), Keyboard{{
			btn("✅ Yes", s.Epoch, "ok", string(c.Kind), strconv.Itoa(c.Index)),
			btn("✖ No", s.Epoch, "no"),
		}}
	}
	switch s.Prompt {
	case PromptFilter:
		return "✏ Send the filter text (case-insensitive substring).", Keyboard{cancel}
	case PromptNewRepoName:
		return "✏ Send the new repo name (A-Z a-z 0-9 . _ -, max 40).", Keyboard{cancel}
	case PromptNewWorktree:
		return "✏ Send the new worktree name (A-Z a-z 0-9 . _ -, max 40).", Keyboard{cancel}
	case PromptNewRepoForge:
		kb := Keyboard{}
		for i, ch := range navdata.RepoChoices() {
			kb = append(kb, []Button{btn(ch.Label, s.Epoch, "nc", strconv.Itoa(i))})
		}
		return "Where should " + s.NewRepoName + " live?", append(kb, cancel)
	}
	return "", nil
}

func confirmQuestion(s State, c Confirm) string {
	switch c.Kind {
	case ConfirmClone:
		if items := s.PickerItems(); c.Index < len(items) {
			return "Clone " + strings.TrimPrefix(items[c.Index].Entry.Label, "↓ ") + "?"
		}
	case ConfirmStop:
		if c.Index < len(s.Rows) {
			return "Stop " + s.Rows[c.Index].SlotID + "?"
		}
	case ConfirmIssue:
		if c.Index < len(s.Issues) {
			is := s.Issues[c.Index]
			wt, label := navdata.IssueWorktreeName(is.Number, is.Title)
			return "Start " + label + "?\nworktree: " + wt
		}
	case ConfirmNewRepo:
		if ch := navdata.RepoChoices(); c.Index < len(ch) {
			return "Create " + s.NewRepoName + " on " + ch[c.Index].Label + "?"
		}
	}
	return "Confirm?"
}
