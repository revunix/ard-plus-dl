// Package tui implements the interactive Bubble Tea front end.
package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
	"github.com/revunix/ard-plus-dl/internal/media"
	"github.com/revunix/ard-plus-dl/internal/secret"
	"github.com/revunix/ard-plus-dl/internal/store"
)

// Screen identifies the visible view.
type Screen int

// Screens.
const (
	ScreenStart Screen = iota
	ScreenLoading
	ScreenSeries
	ScreenTatort
	ScreenQueue
	ScreenSummary
	ScreenLibrary
	ScreenDetails
	ScreenSeasonEpisodes
)

// Messages exchanged with the app through the program loop.

// formPurpose records what the start screen was opened for.
type formPurpose int

const (
	formStart formPurpose = iota
	formSettings
	formAddURL
)

// StartRequest asks the model to collect the missing inputs.
type StartRequest struct{ Reply chan error }

// SeasonsRequest asks the user to pick seasons.
type SeasonsRequest struct {
	Show    string
	Seasons []app.Season
	Reply   chan []int
	Done    chan struct{}
}

// CategoriesRequest asks which Tatort categories to process.
type CategoriesRequest struct {
	Title      string
	Categories []app.Category
	Reply      chan []string
	Done       chan struct{}
}

// SkipRequest asks how many Tatort episodes to skip.
type SkipRequest struct {
	City  string
	Count int
	Reply chan int
	Done  chan struct{}
}

// EventMsg forwards one app event.
type EventMsg struct{ Event app.Event }

// RunDoneMsg reports the end of the download run.
type RunDoneMsg struct{ Err error }

type libraryMsg struct {
	entries []store.Series
	err     error
}

type checkDoneMsg struct {
	results []app.CheckResult
	err     error
}

type downloadNewDoneMsg struct {
	entry store.Series
	err   error
}
type detailsRefreshDoneMsg struct {
	entryID int64
	err     error
}

type job struct {
	label  string
	path   string
	done   int
	total  int
	status string
}

type detailSeasonSummary struct {
	number int
	total  int
	open   int
}

// Options wires the model to the application.
type Options struct {
	Context          context.Context
	Config           *config.Options
	StartScreen      bool
	AutoStart        bool
	NeedsLogin       bool
	Store            *store.Store
	Secrets          secret.Store
	Run              func(context.Context) error
	Check            func(context.Context) ([]app.CheckResult, error)
	DownloadNew      func(context.Context, store.Series) error
	DownloadEpisodes func(context.Context, store.Series, []string) error
	RefreshEntry     func(context.Context, store.Series) error
}

// Model is the TUI state.
type Model struct {
	opts       Options
	cfg        *config.Options
	rootCtx    context.Context
	ctx        context.Context
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
	busy       bool
	needsLogin bool
	store      *store.Store
	secrets    secret.Store

	screen      Screen
	width       int
	height      int
	focus       int
	fields      []textinput.Model
	helpVisible bool

	seasons         []app.Season
	selection       map[int]bool
	cursor          int
	seriesOffset    int
	showName        string
	categoryMode    bool
	categories      []app.Category
	categoriesReply chan []string
	skipField       textinput.Model
	tatortCity      string
	tatortCount     int
	skipErr         string

	library        []store.Series
	libCursor      int
	libOffset      int
	libMessage     string
	checking       bool
	pendingRemoval int64

	details              store.Series
	episodes             []store.KnownEpisode
	detailSeasons        []detailSeasonSummary
	detailSeasonCursor   int
	activeDetailSeason   int
	detailSeasonEpisodes []store.KnownEpisode
	detailCursor         int
	detailOffset         int
	detailChoice         map[int]bool
	detailChoiceExplicit bool
	detailMessage        string

	downloadReturnScreen Screen
	downloadReturnSeason int
	detailReturnSeason   int

	jobs      []job
	log       []string
	startErr  error
	doneErr   error
	exitCode  int
	cancelled bool
	running   bool

	formPurpose     formPurpose
	pendingDownload *store.Series

	startReply     chan error
	seasonsReply   chan []int
	skipReply      chan int
	seasonsDone    chan struct{}
	categoriesDone chan struct{}
	skipDone       chan struct{}
}

var (
	titleStyle    = theme.Title
	statusStyle   = theme.Muted
	errStyle      = theme.Danger
	okStyle       = theme.Success
	cursorStyle   = theme.Selected
	selectedStyle = theme.Selected
	badgeStyle    = theme.Badge
)

// styleInput applies the design-system styles to one text input.
func styleInput(input *textinput.Model) {
	input.PromptStyle = theme.InputPrompt
	input.TextStyle = theme.InputText
	input.PlaceholderStyle = theme.InputPlaceholder
	input.Cursor.Style = theme.InputCursor
}

// errQuit tells the app that the TUI closed while it waited for input.
var errQuit = errors.New("quit")

func newModel(opts Options) *Model {
	rootCtx := opts.Context
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(rootCtx)
	cfg := opts.Config
	model := &Model{
		opts:         opts,
		cfg:          cfg,
		rootCtx:      rootCtx,
		ctx:          ctx,
		cancel:       cancel,
		wg:           &sync.WaitGroup{},
		needsLogin:   opts.NeedsLogin,
		store:        opts.Store,
		secrets:      opts.Secrets,
		selection:    map[int]bool{},
		detailChoice: map[int]bool{},
		screen:       ScreenLoading,
	}
	model.loadLibrary()
	switch {
	case opts.StartScreen:
		model.screen = ScreenStart
	case opts.AutoStart:
		model.screen = ScreenLoading
	case len(model.library) > 0:
		// The library is the home screen; the form is one keystroke away.
		model.screen = ScreenLibrary
	default:
		// Without a configured URL there is nothing to run yet. The prefilled
		// last URL only populates the form.
		model.screen = ScreenStart
	}

	url := textinput.New()
	url.Prompt = ""
	url.Placeholder = "https://www.ardplus.de/details/..."
	url.SetValue(cfg.URL)
	url.Width = 70
	styleInput(&url)

	user := textinput.New()
	user.Prompt = ""
	user.Placeholder = "ARD Plus username"
	user.SetValue(cfg.Username)
	user.Width = 40
	styleInput(&user)

	password := textinput.New()
	password.Prompt = ""
	password.Placeholder = "ARD Plus password"
	password.EchoMode = textinput.EchoPassword
	password.EchoCharacter = '•'
	password.Width = 40
	styleInput(&password)

	tag := textinput.New()
	tag.Prompt = ""
	tag.Width = 24
	tag.Placeholder = "Datei-Tag (z. B. example)"
	tag.SetValue(cfg.FileTag)
	styleInput(&tag)

	model.fields = []textinput.Model{url, user, password, tag}
	model.skipField = textinput.New()
	model.skipField.Prompt = ""
	model.skipField.SetValue("0")
	model.skipField.Width = 8
	styleInput(&model.skipField)
	model.focusField(0)
	return model
}

func (m *Model) loadLibrary() {
	if m.store == nil {
		return
	}
	entries, err := m.store.SeriesList()
	if err != nil {
		m.libMessage = "Bibliothek: " + err.Error()
		return
	}
	m.library = entries
	m.clampLibOffset()
}

func (m *Model) focusField(index int) {
	for i := range m.fields {
		if i == index {
			m.fields[i].Focus()
		} else {
			m.fields[i].Blur()
		}
	}
	m.focus = index
}

// Init starts the run or the input screen.
func (m Model) Init() tea.Cmd {
	if m.screen == ScreenStart {
		return textinput.Blink
	}
	if m.screen == ScreenLibrary {
		return nil
	}
	return m.startRun()
}

func (m *Model) startRun() tea.Cmd {
	if m.running {
		return nil
	}
	m.running = true
	m.jobs = nil
	m.log = nil
	m.cancelled = false
	m.screen = ScreenLoading
	run := m.opts.Run
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return RunDoneMsg{Err: run(ctx)}
	})
}

// track keeps the wait group balanced so Close can wait for running commands.
func (m *Model) track(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	wg := m.wg
	wg.Add(1)
	return func() tea.Msg {
		defer wg.Done()
		return cmd()
	}
}

// release cancels the run and wakes up pending UI requests.
func (m *Model) release() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.seasonsDone != nil {
		close(m.seasonsDone)
		m.seasonsDone = nil
	}
	if m.categoriesDone != nil {
		close(m.categoriesDone)
		m.categoriesDone = nil
	}
	if m.skipDone != nil {
		close(m.skipDone)
		m.skipDone = nil
	}
}

// Close cancels the run and waits up to five seconds for pending commands.
func (m Model) Close() {
	m.release()
	if m.wg != nil {
		done := make(chan struct{})
		go func() {
			m.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// ExitCode is the process exit code for the finished run.
func (m Model) ExitCode() int { return m.exitCode }

// quit releases pending UI requests and ends the program.
func (m Model) quit(code int) (tea.Model, tea.Cmd) {
	if code != 0 {
		m.exitCode = code
	}
	m.release()
	if m.startReply != nil {
		select {
		case m.startReply <- errQuit:
		default:
		}
		m.startReply = nil
	}
	m.seasonsReply = nil
	m.categoriesReply = nil
	m.skipReply = nil
	return m, tea.Quit
}

func (m *Model) checkLibrary() tea.Cmd {
	if m.opts.Check == nil {
		return nil
	}
	if m.busy {
		return nil
	}
	m.busy = true
	m.checking = true
	m.libMessage = "Prüfe gespeicherte Serien…"
	check := m.opts.Check
	ctx := m.ctx
	return m.track(func() tea.Msg {
		results, err := check(ctx)
		return checkDoneMsg{results: results, err: err}
	})
}

func (m *Model) downloadNew(entry store.Series) tea.Cmd {
	if m.opts.DownloadNew == nil {
		return nil
	}
	if m.busy {
		return nil
	}
	m.busy = true
	m.libMessage = "Lade neue Folgen von " + entryTitle(entry) + "…"
	m.beginDownload()
	downloadNew := m.opts.DownloadNew
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return downloadNewDoneMsg{entry: entry, err: downloadNew(ctx, entry)}
	})
}

// beginDownload shows the progress pane and remembers the screen to return to.
func (m *Model) beginDownload() {
	m.cancelled = false
	m.jobs = nil
	m.log = nil
	if m.ctx.Err() != nil {
		m.ctx, m.cancel = context.WithCancel(m.rootCtx)
	}
	m.downloadReturnScreen = m.screen
	m.downloadReturnSeason = 0
	if m.screen == ScreenSeasonEpisodes {
		m.downloadReturnSeason = m.activeDetailSeason
	}
	m.screen = ScreenQueue
}

// Update handles one message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = typed.Width, typed.Height
		m.clampLibOffset()
		m.clampSeriesOffset()
		m.clampDetailOffset()
		m.clampSeasonOffset()
		m.resizeFields()
		return m, nil

	case StartRequest:
		m.startReply = typed.Reply
		m.formPurpose = formStart
		m.pendingDownload = nil
		m.screen = ScreenStart
		return m, nil

	case SeasonsRequest:
		m.categoryMode = false
		m.categoriesReply = nil
		m.showName = typed.Show
		m.seasons = typed.Seasons
		m.cursor = 0
		m.seriesOffset = 0
		m.selection = map[int]bool{}
		for _, season := range typed.Seasons {
			m.selection[season.Number] = true
		}
		m.seasonsReply = typed.Reply
		m.seasonsDone = typed.Done
		m.screen = ScreenSeries
		return m, nil

	case CategoriesRequest:
		m.showName = typed.Title
		m.categories = typed.Categories
		m.seasons = make([]app.Season, 0, len(typed.Categories))
		for i, category := range typed.Categories {
			m.seasons = append(m.seasons, app.Season{Number: i + 1, Title: category.Title, ID: category.Slug})
		}
		m.selection = map[int]bool{} // nothing preselected: the archive is large
		m.cursor = 0
		m.seriesOffset = 0
		m.categoryMode = true
		m.categoriesReply = typed.Reply
		m.categoriesDone = typed.Done
		m.screen = ScreenSeries
		return m, nil

	case SkipRequest:
		m.tatortCity = typed.City
		m.tatortCount = typed.Count
		m.skipErr = ""
		m.skipField.SetValue("0")
		m.skipField.Focus()
		m.skipReply = typed.Reply
		m.skipDone = typed.Done
		m.screen = ScreenTatort
		return m, textinput.Blink

	case EventMsg:
		m.applyEvent(typed.Event)
		return m, nil

	case libraryMsg:
		return m.handleLibraryMsg(typed)

	case pendingCountMsg:
		return m.handlePendingCountMsg(typed)

	case storeActionMsg:
		return m.handleStoreActionMsg(typed)

	case detailsLoadedMsg:
		return m.handleDetailsLoadedMsg(typed)

	case checkDoneMsg:
		m.busy = false
		m.checking = false
		if typed.err != nil {
			m.exitCode = 1
			m.libMessage = "Prüfung fehlgeschlagen: " + typed.err.Error()
			return m, m.loadLibraryCmd()
		}
		newEpisodes := 0
		failed := 0
		firstFailure := ""
		notes := 0
		firstNote := ""
		for _, result := range typed.results {
			newEpisodes += len(result.New)
			if result.Err != nil {
				failed++
				if firstFailure == "" {
					firstFailure = result.Err.Error()
				}
			}
			if result.Note != "" {
				notes++
				if firstNote == "" {
					firstNote = result.Note
				}
			}
		}
		m.libMessage = fmt.Sprintf("%d Serie(n) geprüft, %d neue Folge(n).", len(typed.results), newEpisodes)
		if failed > 0 {
			m.exitCode = 1
			m.libMessage += fmt.Sprintf(" %d fehlgeschlagen: %s", failed, firstFailure)
		}
		if notes > 0 {
			label := "Hinweise"
			if notes == 1 {
				label = "Hinweis"
			}
			m.libMessage += fmt.Sprintf(" %d %s: %s", notes, label, firstNote)
		}
		return m, m.loadLibraryCmd()

	case downloadNewDoneMsg:
		m.busy = false
		if typed.err != nil {
			m.exitCode = 1
		}
		returnScreen := m.downloadReturnScreen
		returnSeason := m.downloadReturnSeason
		m.downloadReturnScreen = ScreenStart
		m.downloadReturnSeason = 0
		m.cancelled = false
		switch returnScreen {
		case ScreenLibrary:
			m.screen = ScreenLibrary
		case ScreenStart:
			// Downloads from the URL flow stay on the queue/summary screens.
		default:
			cmd := m.openDetails(m.details)
			if returnSeason > 0 {
				m.detailReturnSeason = returnSeason
			}
			if typed.err != nil {
				m.detailMessage = typed.err.Error()
			} else {
				m.detailMessage = "Fertig."
			}
			return m, tea.Batch(cmd, m.loadLibraryCmd())
		}
		if typed.err != nil {
			m.libMessage = entryTitle(typed.entry) + ": " + typed.err.Error()
		} else if returnScreen == ScreenLibrary {
			m.libMessage = entryTitle(typed.entry) + ": Folgen geladen."
		}
		return m, m.loadLibraryCmd()

	case detailsRefreshDoneMsg:
		m.busy = false
		if typed.err != nil {
			m.exitCode = 1
		}
		if (m.screen == ScreenDetails || m.screen == ScreenSeasonEpisodes) && typed.entryID == m.details.ID {
			returnToSeason := m.screen == ScreenSeasonEpisodes
			seasonNumber := m.activeDetailSeason
			cmd := m.openDetails(m.details)
			if returnToSeason {
				m.detailReturnSeason = seasonNumber
			}
			if typed.err != nil {
				m.detailMessage = "Folgenliste konnte nicht aktualisiert werden: " + typed.err.Error()
			} else {
				m.detailMessage = ""
			}
			return m, tea.Batch(cmd, m.loadLibraryCmd())
		}
		return m, m.loadLibraryCmd()

	case RunDoneMsg:
		m.running = false
		m.busy = false
		m.doneErr = typed.Err
		if typed.Err != nil {
			m.exitCode = 1
		}
		m.screen = ScreenSummary
		if m.cancelled {
			return m.quit(130)
		}
		return m, m.loadLibraryCmd()

	case tea.KeyMsg:
		return m.handleKey(typed)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		switch m.screen {
		case ScreenQueue, ScreenLoading:
			if m.cancelled {
				return m.quit(130)
			}
			m.cancelled = true
			m.cancel()
			return m, nil
		default:
			return m.quit(130)
		}
	}

	if key == "?" {
		m.helpVisible = !m.helpVisible
		return m, nil
	}
	if m.helpVisible {
		m.helpVisible = false
		return m, nil
	}

	switch m.screen {
	case ScreenLibrary:
		return m.handleLibraryKey(key)

	case ScreenDetails:
		return m.handleDetailsKey(key)

	case ScreenSeasonEpisodes:
		return m.handleSeasonEpisodesKey(key)
	case ScreenStart:
		switch key {
		case "tab", "down":
			m.focusField((m.focus + 1) % len(m.fields))
			return m, nil
		case "shift+tab", "up":
			m.focusField((m.focus + len(m.fields) - 1) % len(m.fields))
			return m, nil
		case "esc":
			m.pendingDownload = nil
			m.formPurpose = formStart
			if len(m.library) > 0 {
				m.screen = ScreenLibrary
				return m, nil
			}
		case "enter":
			if m.busy {
				m.startErr = errors.New("Eine Aktion läuft noch. Bitte warten.")
				return m, nil
			}
			if err := m.applyStartInput(); err != nil {
				m.startErr = err
				return m, nil
			}
			m.startErr = nil
			m.persistStart()
			if m.startReply != nil {
				reply := m.startReply
				m.startReply = nil
				reply <- nil
				return m, m.startRun()
			}
			if m.pendingDownload != nil {
				entry := *m.pendingDownload
				m.pendingDownload = nil
				return m, m.downloadNew(entry)
			}
			if m.formPurpose == formSettings || (m.cfg.URL == "" && len(m.library) > 0) {
				// Settings-only edit: nothing to download.
				m.loadLibrary()
				m.screen = ScreenLibrary
				m.libMessage = "Gespeichert."
				return m, nil
			}
			return m, m.startRun()
		}
		updated, cmd := m.fields[m.focus].Update(msg)
		m.fields[m.focus] = updated
		return m, cmd

	case ScreenSeries:
		switch key {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.clampSeriesOffset()
		case "down", "j":
			if m.cursor < len(m.seasons)-1 {
				m.cursor++
			}
			m.clampSeriesOffset()
		case "pgup":
			m.cursor -= max(1, m.seriesVisible()-1)
			m.clampSeriesOffset()
		case "pgdown":
			m.cursor += max(1, m.seriesVisible()-1)
			m.clampSeriesOffset()
		case "home":
			m.cursor = 0
			m.clampSeriesOffset()
		case "end":
			m.cursor = len(m.seasons) - 1
			m.clampSeriesOffset()
		case " ":
			m.toggleSeason(m.cursor)
		case "a":
			allSelected := true
			for _, season := range m.seasons {
				if !m.selection[season.Number] {
					allSelected = false
					break
				}
			}
			for _, season := range m.seasons {
				m.selection[season.Number] = !allSelected
			}
		case "enter":
			if m.categoryMode {
				slugs := m.selectedCategories()
				if len(slugs) == 0 {
					return m, nil
				}
				if m.categoriesReply != nil {
					reply := m.categoriesReply
					m.categoriesReply = nil
					reply <- slugs
				}
				m.categoriesDone = nil
				m.screen = ScreenLoading
				return m, nil
			}
			selected := m.selectedSeasons()
			if len(selected) == 0 {
				return m, nil
			}
			if m.seasonsReply != nil {
				reply := m.seasonsReply
				m.seasonsReply = nil
				reply <- selected
			}
			m.seasonsDone = nil
			m.screen = ScreenLoading
			return m, nil
		}
		return m, nil

	case ScreenTatort:
		if key == "enter" {
			count, err := strconv.Atoi(strings.TrimSpace(m.skipField.Value()))
			if err != nil || count < 0 || count > m.tatortCount {
				m.skipErr = fmt.Sprintf("Bitte eine Zahl zwischen 0 und %d eingeben.", m.tatortCount)
				return m, nil
			}
			if m.skipReply != nil {
				reply := m.skipReply
				m.skipReply = nil
				reply <- count
			}
			m.skipDone = nil
			m.skipErr = ""
			m.screen = ScreenLoading
			return m, nil
		}
		m.skipErr = ""
		updated, cmd := m.skipField.Update(msg)
		m.skipField = updated
		return m, cmd

	case ScreenQueue, ScreenLoading:
		if key == "q" {
			if m.cancelled {
				return m.quit(130)
			}
			m.cancelled = true
			m.cancel()
		}
		return m, nil

	case ScreenSummary:
		switch key {
		case "l":
			m.loadLibrary()
			m.screen = ScreenLibrary
			return m, nil
		case "q", "enter":
			return m.quit(0)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleLibraryKey(key string) (tea.Model, tea.Cmd) {
	if m.busy {
		switch key {
		case "n", "c", "d", "enter", "o", " ", "x":
			m.libMessage = "Bitte warten, eine Aktion läuft noch."
			return m, nil
		}
	}
	if key != "x" {
		m.pendingRemoval = 0
	}
	switch key {
	case "q":
		return m.quit(0)
	case "up", "k":
		if m.libCursor > 0 {
			m.libCursor--
		}
		m.clampLibOffset()
	case "down", "j":
		if m.libCursor < len(m.library)-1 {
			m.libCursor++
		}
		m.clampLibOffset()
	case "pgup":
		m.libCursor -= max(1, m.libVisible()-1)
		m.clampLibOffset()
	case "pgdown":
		m.libCursor += max(1, m.libVisible()-1)
		m.clampLibOffset()
	case "home":
		m.libCursor = 0
		m.clampLibOffset()
	case "end":
		m.libCursor = len(m.library) - 1
		m.clampLibOffset()
	case "n", "c":
		return m, m.checkLibrary()
	case "s":
		m.formPurpose = formSettings
		m.pendingDownload = nil
		m.fields[0].SetValue(m.cfg.URL)
		m.fields[3].SetValue(m.cfg.FileTag)
		m.focusField(3)
		m.startErr = nil
		m.screen = ScreenStart
		return m, nil
	case "a":
		m.formPurpose = formAddURL
		m.pendingDownload = nil
		m.fields[0].SetValue("")
		m.focusField(0)
		m.startErr = nil
		m.screen = ScreenStart
		return m, nil
	case "enter", "o":
		if len(m.library) == 0 {
			return m, nil
		}
		entry := m.library[m.libCursor]
		cmd := m.openDetails(entry)
		return m, tea.Batch(cmd, m.refreshDetails(entry))
	case "d":
		if len(m.library) == 0 {
			return m, nil
		}
		return m, m.pendingCountCmd(m.library[m.libCursor])
	case " ":
		if len(m.library) == 0 || m.store == nil {
			return m, nil
		}
		entry := m.library[m.libCursor]
		return m, m.setEnabledCmd(entry, !entry.Enabled)
	case "x":
		if len(m.library) == 0 || m.store == nil {
			return m, nil
		}
		entry := m.library[m.libCursor]
		if m.pendingRemoval != entry.ID {
			m.pendingRemoval = entry.ID
			m.libMessage = entryTitle(entry) + ": x erneut drücken zum Entfernen."
			return m, nil
		}
		return m, m.removeSeriesCmd(entry)
	}
	return m, nil
}

// readyForDownload reports whether the settings needed for a download are
// complete; otherwise the user is sent to the form.
func (m *Model) readyForDownload() error {
	if err := config.ValidateFileTag(m.cfg.FileTag); err != nil {
		return err
	}
	if m.needsLogin && (m.cfg.Username == "" || m.cfg.Password == "") {
		return fmt.Errorf("Credentials missing! Set ARD_PLUS_USER/ARD_PLUS_PASSWORD or enter them in the form.")
	}
	return nil
}

func (m *Model) rebuildDetailSeasons() {
	m.detailSeasons = nil
	indices := make(map[int]int)
	for _, episode := range m.episodes {
		if episode.Season <= 0 {
			continue
		}
		index, ok := indices[episode.Season]
		if !ok {
			index = len(m.detailSeasons)
			indices[episode.Season] = index
			m.detailSeasons = append(m.detailSeasons, detailSeasonSummary{number: episode.Season})
		}
		season := &m.detailSeasons[index]
		season.total++
		if episode.DownloadedAt == nil {
			season.open++
		}
	}
	sort.Slice(m.detailSeasons, func(i, j int) bool {
		return m.detailSeasons[i].number < m.detailSeasons[j].number
	})
}

func (m Model) hasDetailSeason(number int) bool {
	for _, season := range m.detailSeasons {
		if season.number == number {
			return true
		}
	}
	return false
}

func (m *Model) openSeasonEpisodes(seasonNumber int) {
	m.activeDetailSeason = seasonNumber
	m.detailSeasonEpisodes = nil
	for _, episode := range m.episodes {
		if episode.Season == seasonNumber {
			m.detailSeasonEpisodes = append(m.detailSeasonEpisodes, episode)
		}
	}
	for index, season := range m.detailSeasons {
		if season.number == seasonNumber {
			m.detailSeasonCursor = index
			break
		}
	}
	m.detailCursor = 0
	m.detailOffset = 0
	m.detailChoice = map[int]bool{}
	m.detailChoiceExplicit = false
	m.detailMessage = ""
	m.screen = ScreenSeasonEpisodes
}

func (m *Model) refreshDetails(entry store.Series) tea.Cmd {
	if m.opts.RefreshEntry == nil || entry.Kind == store.KindMovie {
		return nil
	}
	if m.busy {
		return nil
	}
	m.busy = true
	m.detailMessage = "Folgenliste wird aktualisiert…"
	refresh := m.opts.RefreshEntry
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return detailsRefreshDoneMsg{entryID: entry.ID, err: refresh(ctx, entry)}
	})
}

// libVisible is the number of library rows that fit on the screen.
func (m Model) libVisible() int {
	if m.height <= 0 {
		return 20
	}
	return max(1, m.height-10)
}

// seriesVisible is the number of season rows that fit on the selection screen.
func (m Model) seriesVisible() int {
	if m.height <= 0 {
		return 20
	}
	return max(1, m.height-8)
}

// detailVisible is the number of rows that fit in the details lists.
func (m Model) detailVisible() int {
	if m.height <= 0 {
		return 12
	}
	return max(1, m.height-11)
}

func (m *Model) clampLibOffset() {
	if len(m.library) == 0 {
		m.libCursor = 0
		m.libOffset = 0
		return
	}
	if m.libCursor < 0 {
		m.libCursor = 0
	}
	if m.libCursor >= len(m.library) {
		m.libCursor = len(m.library) - 1
	}
	visible := m.libVisible()
	if m.libCursor < m.libOffset {
		m.libOffset = m.libCursor
	}
	if m.libCursor >= m.libOffset+visible {
		m.libOffset = m.libCursor - visible + 1
	}
	if m.libOffset < 0 {
		m.libOffset = 0
	}
	if limit := len(m.library) - visible; m.libOffset > limit {
		m.libOffset = max(0, limit)
	}
}

func (m *Model) clampSeriesOffset() {
	if len(m.seasons) == 0 {
		m.cursor = 0
		m.seriesOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.seasons) {
		m.cursor = len(m.seasons) - 1
	}
	visible := m.seriesVisible()
	if m.cursor < m.seriesOffset {
		m.seriesOffset = m.cursor
	}
	if m.cursor >= m.seriesOffset+visible {
		m.seriesOffset = m.cursor - visible + 1
	}
	if m.seriesOffset < 0 {
		m.seriesOffset = 0
	}
	if limit := len(m.seasons) - visible; m.seriesOffset > limit {
		m.seriesOffset = max(0, limit)
	}
}

func (m *Model) clampDetailOffset() {
	episodes := m.selectedEpisodeList()
	if len(episodes) == 0 {
		m.detailCursor = 0
		m.detailOffset = 0
		return
	}
	if m.detailCursor < 0 {
		m.detailCursor = 0
	}
	if m.detailCursor >= len(episodes) {
		m.detailCursor = len(episodes) - 1
	}
	visible := m.detailVisible()
	if m.detailCursor < m.detailOffset {
		m.detailOffset = m.detailCursor
	}
	if m.detailCursor >= m.detailOffset+visible {
		m.detailOffset = m.detailCursor - visible + 1
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
	if limit := len(episodes) - visible; m.detailOffset > limit {
		m.detailOffset = max(0, limit)
	}
}

func (m *Model) clampSeasonOffset() {
	// The season cursor is not shown on the season episode list; the offset
	// there belongs to the episode list and is clamped by clampDetailOffset.
	if m.screen == ScreenSeasonEpisodes {
		return
	}
	if len(m.detailSeasons) == 0 {
		m.detailSeasonCursor = 0
		// A flat episode list shares detailOffset and keeps its own position.
		if len(m.episodes) == 0 {
			m.detailOffset = 0
		}
		return
	}
	if m.detailSeasonCursor < 0 {
		m.detailSeasonCursor = 0
	}
	if m.detailSeasonCursor >= len(m.detailSeasons) {
		m.detailSeasonCursor = len(m.detailSeasons) - 1
	}
	visible := m.detailVisible()
	if m.detailSeasonCursor < m.detailOffset {
		m.detailOffset = m.detailSeasonCursor
	}
	if m.detailSeasonCursor >= m.detailOffset+visible {
		m.detailOffset = m.detailSeasonCursor - visible + 1
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
	if limit := len(m.detailSeasons) - visible; m.detailOffset > limit {
		m.detailOffset = max(0, limit)
	}
}

// resizeFields keeps the input widths within the terminal.
func (m *Model) resizeFields() {
	if len(m.fields) != 4 {
		return
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	base := width - 20
	if base < 10 {
		base = 10
	}
	m.fields[0].Width = min(base, 100)
	m.fields[1].Width = min(base, 60)
	m.fields[2].Width = min(base, 60)
	m.fields[3].Width = max(8, min(base, 40))
}

func (m Model) selectedEpisodeList() []store.KnownEpisode {
	if m.screen == ScreenSeasonEpisodes {
		return m.detailSeasonEpisodes
	}
	return m.episodes
}

// selectedEpisodes returns the chosen IDs, or every open episode in the
// current list when nothing was explicitly selected.
func (m Model) selectedEpisodes() []string {
	episodes := m.selectedEpisodeList()
	var ids []string
	for index, episode := range episodes {
		if m.detailChoice[index] {
			ids = append(ids, episode.ID)
		}
	}
	if m.detailChoiceExplicit {
		return ids
	}
	if len(ids) > 0 {
		return ids
	}
	for _, episode := range episodes {
		if episode.DownloadedAt == nil {
			ids = append(ids, episode.ID)
		}
	}
	return ids
}

func (m *Model) downloadSeason(seasonNumber int) tea.Cmd {
	var ids []string
	for _, episode := range m.episodes {
		if episode.Season == seasonNumber && episode.DownloadedAt == nil {
			ids = append(ids, episode.ID)
		}
	}
	return m.downloadEpisodeIDs(ids, fmt.Sprintf("Keine offenen Folgen in Staffel %d.", seasonNumber))
}

func (m *Model) downloadDetails() tea.Cmd {
	ids := m.selectedEpisodes()
	if len(ids) == 0 && m.detailChoiceExplicit {
		m.detailMessage = "Keine Folge ausgewählt."
		return nil
	}
	return m.downloadEpisodeIDs(ids, "Keine offenen Folgen.")
}

func (m *Model) downloadEpisodeIDs(ids []string, emptyMessage string) tea.Cmd {
	if m.opts.DownloadEpisodes == nil {
		return nil
	}
	if m.busy {
		return nil
	}
	if len(ids) == 0 {
		m.detailMessage = emptyMessage
		return nil
	}
	if err := m.readyForDownload(); err != nil {
		m.fields[3].SetValue(m.cfg.FileTag)
		m.focusField(3)
		m.startErr = err
		m.screen = ScreenStart
		return nil
	}
	m.busy = true
	m.beginDownload()
	m.detailMessage = fmt.Sprintf("Lade %d Folge(n)…", len(ids))
	download := m.opts.DownloadEpisodes
	entry := m.details
	ctx := m.ctx
	return m.track(func() tea.Msg {
		return downloadNewDoneMsg{entry: entry, err: download(ctx, entry, ids)}
	})
}

func (m Model) handleDetailsKey(key string) (tea.Model, tea.Cmd) {
	if m.details.Kind == store.KindSeries && len(m.detailSeasons) > 0 {
		switch key {
		case "q", "esc", "backspace":
			m.screen = ScreenLibrary
			return m, nil
		case "up", "k":
			if m.detailSeasonCursor > 0 {
				m.detailSeasonCursor--
			}
			m.clampSeasonOffset()
		case "down", "j":
			if m.detailSeasonCursor < len(m.detailSeasons)-1 {
				m.detailSeasonCursor++
			}
			m.clampSeasonOffset()
		case "pgup":
			m.detailSeasonCursor -= max(1, m.detailVisible()-1)
			m.clampSeasonOffset()
		case "pgdown":
			m.detailSeasonCursor += max(1, m.detailVisible()-1)
			m.clampSeasonOffset()
		case "home":
			m.detailSeasonCursor = 0
			m.clampSeasonOffset()
		case "end":
			m.detailSeasonCursor = len(m.detailSeasons) - 1
			m.clampSeasonOffset()
		case "enter", "o":
			season := m.detailSeasons[m.detailSeasonCursor].number
			m.openSeasonEpisodes(season)
			return m, nil
		case "d":
			season := m.detailSeasons[m.detailSeasonCursor].number
			return m, m.downloadSeason(season)
		}
		return m, nil
	}
	return m.handleEpisodeListKey(key, false)
}

func (m Model) handleSeasonEpisodesKey(key string) (tea.Model, tea.Cmd) {
	return m.handleEpisodeListKey(key, true)
}

func (m Model) handleEpisodeListKey(key string, inSeason bool) (tea.Model, tea.Cmd) {
	episodes := m.episodes
	if inSeason {
		episodes = m.detailSeasonEpisodes
	}
	switch key {
	case "q":
		m.screen = ScreenLibrary
		return m, nil
	case "esc", "backspace":
		if inSeason {
			m.screen = ScreenDetails
			m.detailSeasonEpisodes = nil
			m.detailChoice = map[int]bool{}
			m.detailChoiceExplicit = false
			m.detailMessage = ""
			m.clampSeasonOffset()
		} else {
			m.screen = ScreenLibrary
		}
		return m, nil
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
		m.clampDetailOffset()
	case "down", "j":
		if m.detailCursor < len(episodes)-1 {
			m.detailCursor++
		}
		m.clampDetailOffset()
	case "pgup":
		m.detailCursor -= max(1, m.detailVisible()-1)
		m.clampDetailOffset()
	case "pgdown":
		m.detailCursor += max(1, m.detailVisible()-1)
		m.clampDetailOffset()
	case "home":
		m.detailCursor = 0
		m.clampDetailOffset()
	case "end":
		m.detailCursor = len(episodes) - 1
		m.clampDetailOffset()
	case " ":
		if m.detailCursor < len(episodes) {
			m.detailChoice[m.detailCursor] = !m.detailChoice[m.detailCursor]
			m.detailChoiceExplicit = true
		}
	case "a":
		selected := 0
		for _, chosen := range m.detailChoice {
			if chosen {
				selected++
			}
		}
		all := selected == len(episodes)
		m.detailChoice = map[int]bool{}
		if !all {
			for index := range episodes {
				m.detailChoice[index] = true
			}
		}
		m.detailChoiceExplicit = true
	case "d", "enter":
		return m, m.downloadDetails()
	}
	return m, nil
}

func (m *Model) toggleSeason(index int) {
	if index < 0 || index >= len(m.seasons) {
		return
	}
	number := m.seasons[index].Number
	m.selection[number] = !m.selection[number]
}

func (m Model) selectedCategories() []string {
	var slugs []string
	for _, season := range m.seasons {
		if m.selection[season.Number] {
			slugs = append(slugs, season.ID)
		}
	}
	return slugs
}

func (m Model) selectedSeasons() []int {
	var selected []int
	for _, season := range m.seasons {
		if m.selection[season.Number] {
			selected = append(selected, season.Number)
		}
	}
	sort.Ints(selected)
	return selected
}

func (m *Model) applyStartInput() error {
	url := strings.TrimSpace(m.fields[0].Value())
	if url != "" {
		url = media.NormalizeURL(url)
		if err := config.ValidateContentURL(url); err != nil {
			return fmt.Errorf("Invalid URL: %s", err)
		}
	}
	m.cfg.URL = url
	switch m.formPurpose {
	case formAddURL:
		if url == "" {
			return errors.New("Bitte eine URL eingeben.")
		}
	case formStart:
		if url == "" && len(m.library) == 0 {
			return config.ErrNoURL
		}
	}

	m.cfg.Username = strings.TrimSpace(m.fields[1].Value())
	m.cfg.Password = m.fields[2].Value()
	m.cfg.FileTag = strings.TrimSpace(m.fields[3].Value())

	if m.needsLogin && (m.cfg.Username == "" || m.cfg.Password == "") {
		return fmt.Errorf("Credentials missing! Set ARD_PLUS_USER/ARD_PLUS_PASSWORD or enter them above.")
	}
	if err := config.ValidateFileTag(m.cfg.FileTag); err != nil {
		return err
	}
	return nil
}

// persistStart keeps what the user typed, so the next start has it.
func (m *Model) persistStart() {
	if m.store != nil {
		_ = m.store.SetSetting(store.SettingUsername, m.cfg.Username)
		_ = m.store.SetSetting(store.SettingFileTag, m.cfg.FileTag)
		_ = m.store.SetSetting(store.SettingLastURL, m.cfg.URL)
	}
	if m.secrets != nil && m.cfg.Password != "" {
		_ = m.secrets.Set(m.cfg.Password)
	}
}

func entryTitle(entry store.Series) string {
	if entry.Title != "" {
		return entry.Title
	}
	return entry.URL
}
